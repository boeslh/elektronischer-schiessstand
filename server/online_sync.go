// ============================================================================
// online_sync.go – Uebertragung an die Online-Anzeige im Internet (online/).
//
// Die Verbindung geht immer von hier aus: Fuer jede eingetragene
// Veroeffentlichung (online_publications) wird im eingestellten Takt eine
// Momentaufnahme gebaut (online_snapshot.go) und per HTTPS an
//
//	PUT <online_url>/api/v1/publish/<kind>/<source_id>
//
// geschickt, angemeldet mit dem API-Schluessel des Vereins
// ("Authorization: Bearer ssk_..."). Nur HTTPS ist erlaubt; das
// Server-Zertifikat wird geprueft (gegen die System-Zertifikate oder ein in
// den Einstellungen hinterlegtes eigenes CA-Zertifikat).
//
// Takt: Rundenwettkampf 3 s (Anzeige mit weniger als 10 s Verzoegerung),
// Preisschiessen 1-5 Minuten. Unveraenderte Daten werden nicht erneut
// geschickt (nur ein seltenes Lebenszeichen, solange etwas laeuft).
//
// Entfernen einer Veroeffentlichung hier beendet nur die Uebertragung - die
// Daten bleiben auf dem Online-Server (Historie), geloescht wird dort.
// ============================================================================
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type OnlineSettings struct {
	URL     string `json:"url"`
	APIKey  string `json:"-"`
	CAPEM   string `json:"ca_pem"`
	Enabled bool   `json:"enabled"`
}

func (s *Store) GetOnlineSettings(ctx context.Context) (OnlineSettings, error) {
	var o OnlineSettings
	err := s.pool.QueryRow(ctx, `SELECT online_url, online_api_key, online_ca_pem, online_enabled FROM app_settings WHERE id=1`).
		Scan(&o.URL, &o.APIKey, &o.CAPEM, &o.Enabled)
	return o, err
}

// onlineClient: HTTPS-Client mit Zertifikatspruefung (System + eigenes CA).
func onlineClient(o OnlineSettings) (*http.Client, error) {
	u, err := url.Parse(o.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("Adresse muss mit https:// beginnen")
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if strings.TrimSpace(o.CAPEM) != "" && !pool.AppendCertsFromPEM([]byte(o.CAPEM)) {
		return nil, errors.New("CA-Zertifikat ist kein gültiges PEM")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: 20 * time.Second, Transport: tr}, nil
}

func onlineRequest(ctx context.Context, o OnlineSettings, method, path string, body []byte) (map[string]any, error) {
	cl, err := onlineClient(o)
	if err != nil {
		return nil, err
	}
	if o.APIKey == "" {
		return nil, errors.New("kein API-Schlüssel eingetragen")
	}
	var rd io.Reader
	if body != nil {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		gz.Write(body)
		gz.Close()
		rd = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(o.URL, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		msg, _ := out["error"].(string)
		if msg == "" {
			msg = resp.Status
		}
		return out, fmt.Errorf("Online-Server: %s", msg)
	}
	return out, nil
}

// ── Hintergrund-Uebertragung ─────────────────────────────────────────────

type onlinePub struct {
	ID, Kind, SourceID, LastHash, LastError string
	Interval                                int
	LastSuccess                             *time.Time
}

type OnlineSync struct {
	store    *Store
	mu       sync.Mutex
	inflight map[string]bool
}

func NewOnlineSync(store *Store) *OnlineSync {
	return &OnlineSync{store: store, inflight: map[string]bool{}}
}

func (o *OnlineSync) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.tick(ctx)
		}
	}
}

func (o *OnlineSync) tick(ctx context.Context) {
	set, err := o.store.GetOnlineSettings(ctx)
	if err != nil || !set.Enabled || set.URL == "" || set.APIKey == "" {
		return
	}
	// faellig: Takt abgelaufen; nach einem Fehler fruehestens nach 30 s erneut
	rows, err := o.store.pool.Query(ctx, `
		SELECT id::text, kind, source_id::text, interval_s, last_hash, last_error, last_success_at
		FROM online_publications
		WHERE enabled AND (last_attempt_at IS NULL OR last_attempt_at <
		      now() - make_interval(secs => CASE WHEN last_error <> '' THEN GREATEST(interval_s, 30) ELSE interval_s END))`)
	if err != nil {
		return
	}
	var due []onlinePub
	for rows.Next() {
		var p onlinePub
		if rows.Scan(&p.ID, &p.Kind, &p.SourceID, &p.Interval, &p.LastHash, &p.LastError, &p.LastSuccess) == nil {
			due = append(due, p)
		}
	}
	rows.Close()
	for _, p := range due {
		o.mu.Lock()
		busy := o.inflight[p.ID]
		o.inflight[p.ID] = true
		o.mu.Unlock()
		if busy {
			continue
		}
		go func(p onlinePub) {
			defer func() { o.mu.Lock(); delete(o.inflight, p.ID); o.mu.Unlock() }()
			o.syncOne(ctx, set, p)
		}(p)
	}
}

// snapshot: Daten und ob noch Aenderungen zu erwarten sind (laufend).
func (o *OnlineSync) snapshot(ctx context.Context, kind, sourceID string) (any, bool, error) {
	if kind == "rwk" {
		s, err := o.store.BuildOnlineRWK(ctx, sourceID)
		if err != nil {
			return nil, false, err
		}
		return s, s.Status == "running" || s.Status == "planned", nil
	}
	s, err := o.store.BuildOnlinePS(ctx, sourceID)
	if err != nil {
		return nil, false, err
	}
	return s, !s.Finished, nil
}

func (o *OnlineSync) syncOne(ctx context.Context, set OnlineSettings, p onlinePub) {
	o.store.pool.Exec(ctx, `UPDATE online_publications SET last_attempt_at=now() WHERE id=$1::uuid`, p.ID)
	snap, running, err := o.snapshot(ctx, p.Kind, p.SourceID)
	if err != nil {
		o.fail(ctx, p, err)
		return
	}
	body, err := json.Marshal(snap)
	if err != nil {
		o.fail(ctx, p, err)
		return
	}
	// Hash ohne Erzeugungszeitpunkt: unveraenderte Daten nicht erneut senden
	hashInput := body
	switch v := snap.(type) {
	case *onlineRWK:
		c := *v
		c.GeneratedAt = time.Time{}
		hashInput, _ = json.Marshal(c)
	case *onlinePS:
		c := *v
		c.GeneratedAt = time.Time{}
		hashInput, _ = json.Marshal(c)
	}
	sum := sha256.Sum256(hashInput)
	hash := hex.EncodeToString(sum[:])
	heartbeat := 15 * time.Minute
	if p.Kind == "rwk" {
		heartbeat = time.Minute
	}
	if hash == p.LastHash && p.LastError == "" && p.LastSuccess != nil &&
		(!running || time.Since(*p.LastSuccess) < heartbeat) {
		return
	}
	res, err := onlineRequest(ctx, set, http.MethodPut, "/api/v1/publish/"+p.Kind+"/"+p.SourceID, body)
	if err != nil {
		o.fail(ctx, p, err)
		return
	}
	path, _ := res["url"].(string)
	o.store.pool.Exec(ctx, `UPDATE online_publications SET last_success_at=now(), last_error='', last_hash=$2,
		online_path=CASE WHEN $3 <> '' THEN $3 ELSE online_path END WHERE id=$1::uuid`, p.ID, hash, path)
}

func (o *OnlineSync) fail(ctx context.Context, p onlinePub, err error) {
	if p.LastError != err.Error() {
		log.Printf("Online-Veröffentlichung %s %s: %v", p.Kind, p.SourceID, err)
	}
	o.store.pool.Exec(ctx, `UPDATE online_publications SET last_error=$2 WHERE id=$1::uuid`, p.ID, err.Error())
}

// ── API fuer die Einstellungen (nur Admin) ───────────────────────────────

func (a *APIServer) getOnlineSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	o, err := a.store.GetOnlineSettings(r.Context())
	if err != nil {
		return nil, err
	}
	hint := ""
	if len(o.APIKey) > 12 {
		hint = o.APIKey[:16] + "…" // nur Kennung, nie das Geheimnis
	}
	return map[string]any{"url": o.URL, "enabled": o.Enabled, "ca_pem": o.CAPEM, "has_key": o.APIKey != "", "key_hint": hint}, nil
}

func (a *APIServer) putOnlineSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		URL     string `json:"url"`
		APIKey  string `json:"api_key"` // leer = unveraendert
		CAPEM   string `json:"ca_pem"`
		Enabled bool   `json:"enabled"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body")
	}
	body.URL = strings.TrimRight(strings.TrimSpace(body.URL), "/")
	if body.URL != "" {
		if _, err := onlineClient(OnlineSettings{URL: body.URL, CAPEM: body.CAPEM}); err != nil {
			return nil, errBadRequest(err.Error())
		}
	}
	key := strings.TrimSpace(body.APIKey)
	if key != "" && !strings.HasPrefix(key, "ssk_") {
		return nil, errBadRequest("API-Schlüssel beginnt mit ssk_")
	}
	_, err = a.store.pool.Exec(r.Context(), `UPDATE app_settings SET online_url=$1, online_ca_pem=$2, online_enabled=$3,
		online_api_key = CASE WHEN $4 <> '' THEN $4 ELSE online_api_key END WHERE id=1`,
		body.URL, body.CAPEM, body.Enabled, key)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) postOnlineTest(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	o, err := a.store.GetOnlineSettings(r.Context())
	if err != nil {
		return nil, err
	}
	res, err := onlineRequest(r.Context(), o, http.MethodGet, "/api/v1/ping", nil)
	if err != nil {
		return nil, errBadRequest(err.Error())
	}
	return map[string]any{"ok": true, "verein": res["verein"], "url": strings.TrimRight(o.URL, "/") + fmt.Sprint(res["url"])}, nil
}

type onlinePubView struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	SourceID    string     `json:"source_id"`
	Title       string     `json:"title"`
	Datum       string     `json:"datum"`
	Enabled     bool       `json:"enabled"`
	Interval    int        `json:"interval_s"`
	LastAttempt *time.Time `json:"last_attempt_at"`
	LastSuccess *time.Time `json:"last_success_at"`
	LastError   string     `json:"last_error"`
	OnlineURL   string     `json:"online_url"`
}

func (a *APIServer) listOnlinePublications(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	o, _ := a.store.GetOnlineSettings(r.Context())
	rows, err := a.store.pool.Query(r.Context(), `
		SELECT op.id::text, op.kind, op.source_id::text,
		       COALESCE(ps.name, ev.name, '(gelöscht)'), COALESCE(to_char(COALESCE(ps.starts_on, ev.starts_on),'DD.MM.YYYY'),''),
		       op.enabled, op.interval_s, op.last_attempt_at, op.last_success_at, op.last_error, op.online_path
		FROM online_publications op
		LEFT JOIN preisschiessen ps ON op.kind='preisschiessen' AND ps.id=op.source_id
		LEFT JOIN events ev ON op.kind='rwk' AND ev.id=op.source_id
		ORDER BY op.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []onlinePubView{}
	for rows.Next() {
		var v onlinePubView
		var path string
		if err := rows.Scan(&v.ID, &v.Kind, &v.SourceID, &v.Title, &v.Datum, &v.Enabled, &v.Interval,
			&v.LastAttempt, &v.LastSuccess, &v.LastError, &path); err != nil {
			return nil, err
		}
		if path != "" && o.URL != "" {
			v.OnlineURL = strings.TrimRight(o.URL, "/") + path
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Auswahlliste: Preisschiessen und Rundenwettkaempfe, die noch nicht eingetragen sind.
func (a *APIServer) listOnlineCandidates(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	type cand struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	out := []cand{}
	rows, err := a.store.pool.Query(r.Context(), `
		SELECT 'preisschiessen', id::text, name || COALESCE(' (' || to_char(starts_on,'DD.MM.YYYY') || ')','')
		FROM preisschiessen p WHERE NOT EXISTS (SELECT 1 FROM online_publications o WHERE o.kind='preisschiessen' AND o.source_id=p.id)
		UNION ALL
		SELECT 'rwk', id::text, name || COALESCE(' (' || to_char(starts_on,'DD.MM.YYYY') || ')','')
		FROM events e WHERE type='runde' AND status <> 'archived'
		  AND NOT EXISTS (SELECT 1 FROM online_publications o WHERE o.kind='rwk' AND o.source_id=e.id)
		ORDER BY 1, 3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c cand
		if rows.Scan(&c.Kind, &c.ID, &c.Title) == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func clampInterval(kind string, s int) int {
	if kind == "rwk" {
		if s < 3 || s > 9 {
			return 3
		}
		return s
	}
	if s < 60 {
		return 60
	}
	if s > 300 {
		return 300
	}
	return s
}

func (a *APIServer) postOnlinePublication(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Kind     string `json:"kind"`
		SourceID string `json:"source_id"`
		Interval int    `json:"interval_s"`
	}](r)
	if err != nil || (body.Kind != "preisschiessen" && body.Kind != "rwk") || body.SourceID == "" {
		return nil, errBadRequest("Art und Auswahl angeben")
	}
	_, err = a.store.pool.Exec(r.Context(), `INSERT INTO online_publications (kind, source_id, interval_s)
		VALUES ($1, $2::uuid, $3) ON CONFLICT (kind, source_id) DO UPDATE SET enabled=TRUE`,
		body.Kind, body.SourceID, clampInterval(body.Kind, body.Interval))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) putOnlinePublication(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Enabled  bool `json:"enabled"`
		Interval int  `json:"interval_s"`
		Now      bool `json:"now"` // sofort uebertragen
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body")
	}
	var kind string
	if err := a.store.pool.QueryRow(r.Context(), `SELECT kind FROM online_publications WHERE id=$1::uuid`, r.PathValue("id")).Scan(&kind); err != nil {
		return nil, errBadRequest("nicht gefunden")
	}
	_, err = a.store.pool.Exec(r.Context(), `UPDATE online_publications SET enabled=$2, interval_s=$3,
		last_attempt_at = CASE WHEN $4 THEN NULL ELSE last_attempt_at END,
		last_hash = CASE WHEN $4 THEN '' ELSE last_hash END, last_error = CASE WHEN $4 THEN '' ELSE last_error END
		WHERE id=$1::uuid`, r.PathValue("id"), body.Enabled, clampInterval(kind, body.Interval), body.Now)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteOnlinePublication(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	_, err := a.store.pool.Exec(r.Context(), `DELETE FROM online_publications WHERE id=$1::uuid`, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// getOnlinePreview: Momentaufnahme ansehen, ohne sie zu senden (pruefen, was
// uebertragen wuerde).
func (a *APIServer) getOnlinePreview(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	var kind, src string
	if err := a.store.pool.QueryRow(r.Context(), `SELECT kind, source_id::text FROM online_publications WHERE id=$1::uuid`,
		r.PathValue("id")).Scan(&kind, &src); err != nil {
		return nil, errBadRequest("nicht gefunden")
	}
	snap, _, err := (&OnlineSync{store: a.store}).snapshot(r.Context(), kind, src)
	if err != nil {
		return nil, errBadRequest(err.Error())
	}
	return snap, nil
}
