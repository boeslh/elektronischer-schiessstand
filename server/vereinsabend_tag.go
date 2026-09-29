// ============================================================================
// vereinsabend_tag.go – Tagesansichten der Vereinsabend-Saison
// (web/vereinsabende.html, Tabs Tagesergebnisse/Tageswertung/Anwesenheit).
//
// Ein "Vereinsabend" ist weiterhin kein eigenes Datenobjekt (siehe
// vereinsabend_wertungen.go-Dateikopf): alle Ansichten hier filtern die
// gekauften Scheiben einer Saison auf ihren effektiven Schiesstag
// (schiesstag_override, sonst sessions.finished_at::date).
//
// Anwesenheit = Kauf einer Scheibe mit scoring_mode='teilnahme' - laeuft
// ueber den normalen Kassen-Bezahlschritt (Store.Bezahlen, inkl.
// Guthaben-Limit und autoFinishTeilnahmeScheiben), damit Preis/Guthaben/
// Kassenauswertung identisch zum manuellen Kauf an der Kasse bleiben.
// ============================================================================
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// parseTagParam liest ?datum=YYYY-MM-DD (leer = heute, Server-Ortszeit).
func parseTagParam(r *http.Request) (string, time.Time, error) {
	d := r.URL.Query().Get("datum")
	if d == "" {
		d = time.Now().Format("2006-01-02")
	}
	t, err := time.ParseInLocation("2006-01-02", d, time.Local)
	if err != nil {
		return "", time.Time{}, errBadRequest("datum muss im Format JJJJ-MM-TT sein")
	}
	return d, t, nil
}

// ----------------------------------------------------------------------------
// Tagesergebnisse
// ----------------------------------------------------------------------------

type VATagesergebnis struct {
	KaufScheibeID        string     `json:"kauf_scheibe_id"`
	TeilnehmerID         string     `json:"teilnehmer_id"`
	TeilnehmerNr         int        `json:"teilnehmer_nr"`
	Nachname             string     `json:"nachname"`
	Vorname              string     `json:"vorname"`
	Verein               string     `json:"verein"`
	Klasse               string     `json:"klasse"`
	ScheibeID            string     `json:"scheibe_id"`
	ScheibeName          string     `json:"scheibe_name"`
	ScoringMode          string     `json:"scoring_mode"`
	AuswertungUnsichtbar bool       `json:"auswertung_unsichtbar"`
	VorNachschuss        bool       `json:"vor_nachschuss"`
	SchiesstagOverride   *string    `json:"schiesstag_override"`
	StartedAt            *time.Time `json:"started_at"`
	FinishedAt           *time.Time `json:"finished_at"`
	ShotCount            *int64     `json:"shot_count"`
	Ringe                *int64     `json:"ringe"`
	Zehntel              *float64   `json:"zehntel"`
	BesterTeiler         *float64   `json:"bester_teiler"`
	Serien               []int64    `json:"serien"`
	// fuer Grafische Ansicht (/ergebnis-ansicht) und PDF (/api/sessions/{id}/pdf)
	SessionID      string `json:"session_id"`
	Discipline     string `json:"discipline"`
	ShotsPerSeries int    `json:"shots_per_series"`
	DecimalScoring bool   `json:"decimal_scoring"`
}

// ListVereinsabendTagesergebnisse liefert alle Scheiben der Saison, deren
// effektiver Schiesstag datum ist - plus (ohne Override) die an diesem Tag
// begonnenen, noch nicht beendeten Scheiben (FinishedAt=nil, "laeuft").
func (s *Store) ListVereinsabendTagesergebnisse(ctx context.Context, preisschiessenID, datum string) ([]VATagesergebnis, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ks.id, pt.id, pt.teilnehmer_nr, sh.last_name, sh.first_name,
		       COALESCE(cl.name,''), COALESCE(clx.name,''),
		       psc.id, psc.name, d.scoring_mode, psc.auswertung_unsichtbar,
		       ks.ist_vor_nachschuss, ks.schiesstag_override::text,
		       s.started_at, s.finished_at,
		       vsr.shot_count, vsr.total_rings, vsr.total_decimal::float8,
		       vsr.best_center_distance::float8,
		       (SELECT array_agg(x.rings ORDER BY x.series_no) FROM v_series_results x
		        WHERE x.session_id = ks.session_id),
		       s.id, d.name, d.shots_per_series, d.decimal_scoring
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k          ON k.id = ks.kauf_id
		JOIN ps_teilnehmer pt     ON pt.id = k.teilnehmer_id
		JOIN shooters sh          ON sh.id = pt.shooter_id
		LEFT JOIN clubs cl        ON cl.id = sh.club_id
		LEFT JOIN shooter_classes clx ON clx.id = pt.class_id
		JOIN ps_scheiben psc      ON psc.id = ks.scheibe_id
		JOIN disciplines d        ON d.id = psc.discipline_id
		JOIN sessions s           ON s.id = ks.session_id
		LEFT JOIN v_session_results vsr ON vsr.session_id = ks.session_id
		WHERE ks.preisschiessen_id = $1
		  AND (COALESCE(ks.schiesstag_override, s.finished_at::date) = $2::date
		       OR (ks.schiesstag_override IS NULL AND s.finished_at IS NULL
		           AND s.started_at::date = $2::date))
		ORDER BY sh.last_name, sh.first_name, s.finished_at NULLS LAST`, preisschiessenID, datum)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VATagesergebnis{}
	for rows.Next() {
		var x VATagesergebnis
		if err := rows.Scan(&x.KaufScheibeID, &x.TeilnehmerID, &x.TeilnehmerNr, &x.Nachname, &x.Vorname,
			&x.Verein, &x.Klasse, &x.ScheibeID, &x.ScheibeName, &x.ScoringMode, &x.AuswertungUnsichtbar,
			&x.VorNachschuss, &x.SchiesstagOverride, &x.StartedAt, &x.FinishedAt,
			&x.ShotCount, &x.Ringe, &x.Zehntel, &x.BesterTeiler, &x.Serien,
			&x.SessionID, &x.Discipline, &x.ShotsPerSeries, &x.DecimalScoring); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (a *APIServer) getVereinsabendTagesergebnisse(w http.ResponseWriter, r *http.Request) (any, error) {
	datum, t, err := parseTagParam(r)
	if err != nil {
		return nil, err
	}
	ps, err := a.store.GetPreisschiessen(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	rows, err := a.store.ListVereinsabendTagesergebnisse(r.Context(), ps.ID, datum)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"datum":            datum,
		"wochentag_zaehlt": weekdayAllowed(t, ps.SaisonWochentage),
		"ergebnisse":       rows,
	}, nil
}

// ----------------------------------------------------------------------------
// Tageswertung
// ----------------------------------------------------------------------------

type VATageswertungEintrag struct {
	Platz             int      `json:"platz"`
	TeilnehmerID      string   `json:"teilnehmer_id"`
	TeilnehmerNr      int      `json:"teilnehmer_nr"`
	Nachname          string   `json:"nachname"`
	Vorname           string   `json:"vorname"`
	Verein            string   `json:"verein"`
	Klasse            string   `json:"klasse"`
	Ring              *float64 `json:"ring"`
	Teiler            *float64 `json:"teiler"`
	RingPunkte        float64  `json:"ring_punkte"`
	TeilerPunkte      float64  `json:"teiler_punkte"`
	AnwesenheitPunkte float64  `json:"anwesenheit_punkte"`
	Punkte            float64  `json:"punkte"`
	VorNachschuss     bool     `json:"vor_nachschuss"` // nur Vor-/Nachschuss-Scheiben an diesem Tag -> kein Anwesenheitsbonus
}

type VATageswertung struct {
	WertungID    string                  `json:"wertung_id"`
	ShortDesc    string                  `json:"short_desc"`
	LongDesc     string                  `json:"long_desc"`
	Wertungsfeld string                  `json:"wertungsfeld"`
	Eintraege    []VATageswertungEintrag `json:"eintraege"`
}

// tageswertungFromTage platziert die Tagesergebnisse EINER Wertung an EINEM
// Tag nach Punkten (absteigend; gleiche Punkte = gleicher Platz).
func tageswertungFromTage(tage []saisonTag, datum string) []VATageswertungEintrag {
	out := []VATageswertungEintrag{}
	for _, t := range tage {
		if t.tag != datum {
			continue
		}
		out = append(out, VATageswertungEintrag{
			TeilnehmerID: t.meta.teilnehmerID, TeilnehmerNr: t.meta.startNr,
			Nachname: t.meta.nachname, Vorname: t.meta.vorname,
			Verein: t.meta.verein, Klasse: t.meta.klasse,
			Ring: t.ringWert, Teiler: t.teilerWert,
			RingPunkte: t.ringPunkte, TeilerPunkte: t.teilerPunkte, AnwesenheitPunkte: t.anwPunkte,
			Punkte: t.punkte(), VorNachschuss: !t.anwesendOK,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Punkte != out[j].Punkte {
			return out[i].Punkte > out[j].Punkte
		}
		if out[i].Nachname != out[j].Nachname {
			return out[i].Nachname < out[j].Nachname
		}
		return out[i].Vorname < out[j].Vorname
	})
	for i := range out {
		if i > 0 && out[i].Punkte == out[i-1].Punkte {
			out[i].Platz = out[i-1].Platz
		} else {
			out[i].Platz = i + 1
		}
	}
	return out
}

func (a *APIServer) getVereinsabendTageswertung(w http.ResponseWriter, r *http.Request) (any, error) {
	datum, t, err := parseTagParam(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	ps, err := a.store.GetPreisschiessen(ctx, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	wertungen, err := a.store.ListWertungen(ctx, ps.ID)
	if err != nil {
		return nil, err
	}
	out := []VATageswertung{}
	for _, wt := range wertungen {
		if wt.Typ != "punkte_saison" {
			continue
		}
		raw, err := loadPunkteSaisonRows(ctx, a.store.pool, wt)
		if err != nil {
			return nil, fmt.Errorf("Wertung %q: %w", wt.ShortDesc, err)
		}
		out = append(out, VATageswertung{
			WertungID: wt.ID, ShortDesc: wt.ShortDesc, LongDesc: wt.LongDesc, Wertungsfeld: wt.Wertungsfeld,
			Eintraege: tageswertungFromTage(aggregatePunkteSaisonTage(raw, wt, ps.SaisonWochentage), datum),
		})
	}
	return map[string]any{
		"datum":            datum,
		"wochentag_zaehlt": weekdayAllowed(t, ps.SaisonWochentage),
		"wertungen":        out,
	}, nil
}

// ----------------------------------------------------------------------------
// Saisonwertung
// ----------------------------------------------------------------------------

// VASaisonwertungEintrag: Summe aller Tageswertungen eines Teilnehmers an
// den gueltigen Wochentagen im Saison-Zeitraum - bester Ring/Teiler, Punkte
// addiert.
type VASaisonwertungEintrag struct {
	Platz             int      `json:"platz"`
	TeilnehmerID      string   `json:"teilnehmer_id"`
	TeilnehmerNr      int      `json:"teilnehmer_nr"`
	Nachname          string   `json:"nachname"`
	Vorname           string   `json:"vorname"`
	Verein            string   `json:"verein"`
	Klasse            string   `json:"klasse"`
	Abende            int      `json:"abende"`
	BesterRing        *float64 `json:"bester_ring"`
	BesterTeiler      *float64 `json:"bester_teiler"`
	RingPunkte        float64  `json:"ring_punkte"`
	TeilerPunkte      float64  `json:"teiler_punkte"`
	AnwesenheitPunkte float64  `json:"anwesenheit_punkte"`
	Punkte            float64  `json:"punkte"`
}

type VASaisonwertung struct {
	WertungID    string                   `json:"wertung_id"`
	ShortDesc    string                   `json:"short_desc"`
	LongDesc     string                   `json:"long_desc"`
	Wertungsfeld string                   `json:"wertungsfeld"`
	Eintraege    []VASaisonwertungEintrag `json:"eintraege"`
	Summary      VASaisonSummary          `json:"summary"`
}

// saisonwertungFromTage summiert die Tagesergebnisse (aggregatePunkteSaisonTage,
// bereits auf die Saison-Wochentage gefiltert) je Teilnehmer ueber den
// Zeitraum von..bis (JJJJ-MM-TT, leer = offen): Punkte addiert, Ring/Teiler
// jeweils der beste Tageswert. Platzierung nach Punkten (gleich = gleicher Platz).
func saisonwertungFromTage(tage []saisonTag, von, bis string) []VASaisonwertungEintrag {
	idx := map[string]int{}
	out := []VASaisonwertungEintrag{}
	for _, t := range tage {
		if (von != "" && t.tag < von) || (bis != "" && t.tag > bis) {
			continue
		}
		i, ok := idx[t.meta.teilnehmerID]
		if !ok {
			i = len(out)
			idx[t.meta.teilnehmerID] = i
			out = append(out, VASaisonwertungEintrag{
				TeilnehmerID: t.meta.teilnehmerID, TeilnehmerNr: t.meta.startNr,
				Nachname: t.meta.nachname, Vorname: t.meta.vorname,
				Verein: t.meta.verein, Klasse: t.meta.klasse,
			})
		}
		e := &out[i]
		e.Abende++
		if t.ringWert != nil && (e.BesterRing == nil || *t.ringWert > *e.BesterRing) {
			e.BesterRing = t.ringWert
		}
		if t.teilerWert != nil && (e.BesterTeiler == nil || *t.teilerWert < *e.BesterTeiler) {
			e.BesterTeiler = t.teilerWert
		}
		e.RingPunkte += t.ringPunkte
		e.TeilerPunkte += t.teilerPunkte
		e.AnwesenheitPunkte += t.anwPunkte
		e.Punkte += t.punkte()
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Punkte != out[j].Punkte {
			return out[i].Punkte > out[j].Punkte
		}
		if out[i].Nachname != out[j].Nachname {
			return out[i].Nachname < out[j].Nachname
		}
		return out[i].Vorname < out[j].Vorname
	})
	for i := range out {
		if i > 0 && out[i].Punkte == out[i-1].Punkte {
			out[i].Platz = out[i-1].Platz
		} else {
			out[i].Platz = i + 1
		}
	}
	return out
}

func (a *APIServer) getVereinsabendSaisonwertung(w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	ps, err := a.store.GetPreisschiessen(ctx, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	wertungen, err := a.store.ListWertungen(ctx, ps.ID)
	if err != nil {
		return nil, err
	}
	von, bis := dateOnly(ps.StartsOn), dateOnly(ps.EndsOn)
	out := []VASaisonwertung{}
	for _, wt := range wertungen {
		if wt.Typ != "punkte_saison" {
			continue
		}
		raw, err := loadPunkteSaisonRows(ctx, a.store.pool, wt)
		if err != nil {
			return nil, fmt.Errorf("Wertung %q: %w", wt.ShortDesc, err)
		}
		tage := aggregatePunkteSaisonTage(raw, wt, ps.SaisonWochentage)
		out = append(out, VASaisonwertung{
			WertungID: wt.ID, ShortDesc: wt.ShortDesc, LongDesc: wt.LongDesc, Wertungsfeld: wt.Wertungsfeld,
			Eintraege: saisonwertungFromTage(tage, von, bis),
			Summary:   saisonSummary(tage, von, bis),
		})
	}
	return map[string]any{
		"von": von, "bis": bis, "wochentage": ps.SaisonWochentage,
		"wertungen": out,
	}, nil
}

// dateOnly kuerzt ein Datum/Zeitstempel auf JJJJ-MM-TT (leer bleibt leer).
func dateOnly(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// ----------------------------------------------------------------------------
// Anwesenheit
// ----------------------------------------------------------------------------

type VAAnwesenheitEintrag struct {
	KaufScheibeID string `json:"kauf_scheibe_id"`
	ScheibeID     string `json:"scheibe_id"`
	ScheibeName   string `json:"scheibe_name"`
	VorNachschuss bool   `json:"vor_nachschuss"`
}

type VAAnwesenheitTeilnehmer struct {
	TeilnehmerID string                 `json:"teilnehmer_id"`
	TeilnehmerNr int                    `json:"teilnehmer_nr"`
	Nachname     string                 `json:"nachname"`
	Vorname      string                 `json:"vorname"`
	Verein       string                 `json:"verein"`
	Klasse       string                 `json:"klasse"`
	Guthaben     float64                `json:"guthaben"`
	Anwesenheit  []VAAnwesenheitEintrag `json:"anwesenheit"` // Teilnahme-Scheiben an diesem Tag
	Geschossen   int                    `json:"geschossen"`  // sonstige Scheiben an diesem Tag
}

type VATeilnahmeScheibe struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

func (s *Store) listTeilnahmeScheiben(ctx context.Context, preisschiessenID string) ([]VATeilnahmeScheibe, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT psc.id, psc.name, psc.price::float8
		FROM ps_scheiben psc JOIN disciplines d ON d.id = psc.discipline_id
		WHERE psc.preisschiessen_id = $1 AND d.scoring_mode = 'teilnahme' AND psc.active
		ORDER BY psc.sort_order, psc.name`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (VATeilnahmeScheibe, error) {
		var x VATeilnahmeScheibe
		err := row.Scan(&x.ID, &x.Name, &x.Price)
		return x, err
	})
}

// ListVereinsabendAnwesenheit liefert ALLE Teilnehmer der Saison mit ihren
// Scheiben am Tag datum (Teilnahme-Scheiben einzeln, sonstige als Anzahl).
func (s *Store) ListVereinsabendAnwesenheit(ctx context.Context, preisschiessenID, datum string) ([]VAAnwesenheitTeilnehmer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pt.id, pt.teilnehmer_nr, sh.last_name, sh.first_name,
		       COALESCE(cl.name,''), COALESCE(clx.name,''), COALESCE(g.guthaben,0)::float8
		FROM ps_teilnehmer pt
		JOIN shooters sh ON sh.id = pt.shooter_id
		LEFT JOIN clubs cl ON cl.id = sh.club_id
		LEFT JOIN shooter_classes clx ON clx.id = pt.class_id
		LEFT JOIN v_ps_guthaben g ON g.teilnehmer_id = pt.id
		WHERE pt.preisschiessen_id = $1
		ORDER BY sh.last_name, sh.first_name`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (VAAnwesenheitTeilnehmer, error) {
		x := VAAnwesenheitTeilnehmer{Anwesenheit: []VAAnwesenheitEintrag{}}
		err := row.Scan(&x.TeilnehmerID, &x.TeilnehmerNr, &x.Nachname, &x.Vorname, &x.Verein, &x.Klasse, &x.Guthaben)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]int, len(out))
	for i, t := range out {
		idx[t.TeilnehmerID] = i
	}

	eRows, err := s.pool.Query(ctx, `
		SELECT k.teilnehmer_id, ks.id, psc.id, psc.name, d.scoring_mode, ks.ist_vor_nachschuss
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k     ON k.id = ks.kauf_id
		JOIN ps_scheiben psc ON psc.id = ks.scheibe_id
		JOIN disciplines d   ON d.id = psc.discipline_id
		JOIN sessions s      ON s.id = ks.session_id
		WHERE ks.preisschiessen_id = $1
		  AND COALESCE(ks.schiesstag_override, s.finished_at::date) = $2::date
		ORDER BY psc.sort_order, psc.name`, preisschiessenID, datum)
	if err != nil {
		return nil, err
	}
	defer eRows.Close()
	for eRows.Next() {
		var tid, mode string
		var e VAAnwesenheitEintrag
		if err := eRows.Scan(&tid, &e.KaufScheibeID, &e.ScheibeID, &e.ScheibeName, &mode, &e.VorNachschuss); err != nil {
			return nil, err
		}
		i, ok := idx[tid]
		if !ok {
			continue
		}
		if mode == "teilnahme" {
			out[i].Anwesenheit = append(out[i].Anwesenheit, e)
		} else {
			out[i].Geschossen++
		}
	}
	return out, eRows.Err()
}

func (a *APIServer) getVereinsabendAnwesenheit(w http.ResponseWriter, r *http.Request) (any, error) {
	datum, t, err := parseTagParam(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	ps, err := a.store.GetPreisschiessen(ctx, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	scheiben, err := a.store.listTeilnahmeScheiben(ctx, ps.ID)
	if err != nil {
		return nil, err
	}
	tn, err := a.store.ListVereinsabendAnwesenheit(ctx, ps.ID, datum)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"datum":              datum,
		"heute":              time.Now().Format("2006-01-02"),
		"wochentag_zaehlt":   weekdayAllowed(t, ps.SaisonWochentage),
		"teilnahme_scheiben": scheiben,
		"teilnehmer":         tn,
	}, nil
}

type vaAnwesenheitResult struct {
	TeilnehmerID string `json:"teilnehmer_id"`
	OK           bool   `json:"ok"`
	Fehler       string `json:"fehler,omitempty"`
}

// AddVereinsabendAnwesenheit kauft fuer einen Teilnehmer die Teilnahme-
// Scheibe scheibeID (normaler Kassen-Bezahlschritt ohne Barzahlung, also
// zu Lasten des Guthabens bis zum konfigurierten Minus-Limit) und ordnet
// sie - falls datum nicht heute ist - per Schiesstag-Korrektur (OHNE
// Vor-/Nachschuss-Bedeutung, der Schuetze war ja anwesend) diesem Tag zu.
func (s *Store) AddVereinsabendAnwesenheit(ctx context.Context, preisschiessenID, teilnehmerID, scheibeID, datum, actor string) error {
	var exists, sameDay bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM ps_teilnehmer WHERE id=$1 AND preisschiessen_id=$2),
		       EXISTS (
		         SELECT 1 FROM ps_kauf_scheiben ks
		         JOIN ps_kaeufe k ON k.id = ks.kauf_id
		         JOIN sessions s  ON s.id = ks.session_id
		         WHERE k.teilnehmer_id = $1 AND ks.scheibe_id = $3
		           AND COALESCE(ks.schiesstag_override, s.finished_at::date) = $4::date)`,
		teilnehmerID, preisschiessenID, scheibeID, datum).Scan(&exists, &sameDay); err != nil {
		return err
	}
	if !exists {
		return &httpError{code: 404, msg: "Teilnehmer gehoert nicht zu dieser Saison"}
	}
	if sameDay {
		return &httpError{code: 409, msg: "bereits eingetragen"}
	}

	kaufIDs, _, err := s.Bezahlen(ctx, teilnehmerID, []CartItem{{Typ: "scheibe", ScheibeID: scheibeID}}, 0, false)
	if err != nil {
		return err
	}
	var ksID string
	var sessionID, finishedDay *string
	if err := s.pool.QueryRow(ctx, `
		SELECT ks.id, ks.session_id::text, s.finished_at::date::text
		FROM ps_kauf_scheiben ks LEFT JOIN sessions s ON s.id = ks.session_id
		WHERE ks.kauf_id = ANY($1) LIMIT 1`, kaufIDs).Scan(&ksID, &sessionID, &finishedDay); err != nil {
		return err
	}
	if sessionID == nil {
		// autoFinishTeilnahmeScheiben ist best-effort (siehe dort) - ohne
		// Session hat die Scheibe keinen Schiesstag und zaehlt nirgends.
		return errors.New("Scheibe gekauft, aber nicht automatisch abgeschlossen (siehe Server-Log)")
	}
	if finishedDay == nil || *finishedDay != datum {
		return s.SetSchiesstagOverride(ctx, ksID, &datum, actor)
	}
	return nil
}

func (a *APIServer) postVereinsabendAnwesenheit(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireTile(w, r, "preisschiessen")
	if err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Datum         string   `json:"datum"`
		ScheibeID     string   `json:"scheibe_id"`
		TeilnehmerIDs []string `json:"teilnehmer_ids"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body")
	}
	if _, err := time.Parse("2006-01-02", body.Datum); err != nil {
		return nil, errBadRequest("datum muss im Format JJJJ-MM-TT sein")
	}
	if body.ScheibeID == "" || len(body.TeilnehmerIDs) == 0 {
		return nil, errBadRequest("scheibe_id und teilnehmer_ids erforderlich")
	}
	ctx := r.Context()
	psID := r.PathValue("id")
	scheiben, err := a.store.listTeilnahmeScheiben(ctx, psID)
	if err != nil {
		return nil, err
	}
	valid := false
	for _, sc := range scheiben {
		valid = valid || sc.ID == body.ScheibeID
	}
	if !valid {
		return nil, errBadRequest("keine aktive Teilnahme-Scheibe dieser Saison")
	}

	actor := role.RoleKey
	results := make([]vaAnwesenheitResult, 0, len(body.TeilnehmerIDs))
	for _, tid := range body.TeilnehmerIDs {
		res := vaAnwesenheitResult{TeilnehmerID: tid, OK: true}
		if err := a.store.AddVereinsabendAnwesenheit(ctx, psID, tid, body.ScheibeID, body.Datum, actor); err != nil {
			res.OK, res.Fehler = false, err.Error()
		}
		results = append(results, res)
	}
	return map[string]any{"results": results}, nil
}

// RemoveVereinsabendAnwesenheit nimmt eine Teilnahme-Scheibe zurueck
// (Fehleintrag). Die normale Ruckgabe verweigert das, weil
// autoFinishTeilnahmeScheiben bereits einen Platzhalter-Schuss angelegt hat
// ("bereits beschossen") - hier wird dieser Schuss deshalb zuerst annulliert
// (Schuesse werden nie geloescht, siehe prevent_shot_delete), die Session
// abgebrochen und danach wie bei Ruckgabe erstattet. Nur fuer Teilnahme-
// Scheiben, die als einzelne Scheibe (nicht als Teil eines Sets) gekauft
// wurden.
func (s *Store) RemoveVereinsabendAnwesenheit(ctx context.Context, preisschiessenID, kaufScheibeID, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var kaufID, teilnehmerID, kaufTyp, mode string
	var sessionID *string
	var preis float64
	if err := tx.QueryRow(ctx, `
		SELECT k.id, k.teilnehmer_id, k.typ, k.preis::float8, d.scoring_mode, ks.session_id::text
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k     ON k.id = ks.kauf_id
		JOIN ps_scheiben psc ON psc.id = ks.scheibe_id
		JOIN disciplines d   ON d.id = psc.discipline_id
		WHERE ks.id = $1 AND ks.preisschiessen_id = $2 AND k.returned_at IS NULL`,
		kaufScheibeID, preisschiessenID).Scan(&kaufID, &teilnehmerID, &kaufTyp, &preis, &mode, &sessionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &httpError{code: 404, msg: "Anwesenheit nicht gefunden"}
		}
		return err
	}
	if mode != "teilnahme" {
		return errBadRequest("nur Teilnahme-Scheiben koennen hier ausgetragen werden")
	}
	if kaufTyp != "scheibe" {
		return errBadRequest("Scheibe ist Teil eines Sets - Rueckgabe bitte ueber die Kasse")
	}

	if sessionID != nil {
		rows, err := tx.Query(ctx, `
			UPDATE shots SET status='annulled'
			WHERE session_id=$1 AND status <> 'annulled' RETURNING id::text`, *sessionID)
		if err != nil {
			return err
		}
		shotIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, shotID := range shotIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO audit_log (action, session_id, shot_id, actor, details)
				VALUES ('shot_annulled', $1::uuid, $2::uuid, $3, jsonb_build_object('reason','Anwesenheit ausgetragen'))`,
				*sessionID, shotID, actor); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE sessions SET status='aborted' WHERE id=$1`, *sessionID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE ps_kaeufe SET returned_at = now() WHERE id=$1`, kaufID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ps_kauf_scheiben WHERE kauf_id=$1`, kaufID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ps_guthaben_buchungen (teilnehmer_id, typ, betrag, kauf_id)
		VALUES ($1,'rueckgabe',$2,$3)`, teilnehmerID, preis, kaufID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *APIServer) deleteVereinsabendAnwesenheit(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireTile(w, r, "preisschiessen")
	if err != nil {
		return nil, err
	}
	if err := a.store.RemoveVereinsabendAnwesenheit(r.Context(), r.PathValue("id"), r.PathValue("ksid"),
		role.RoleKey); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
