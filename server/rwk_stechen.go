// ============================================================================
// rwk_stechen.go – Stechschuesse im Paarungswettkampf.
//
// Endet eine Paarung unentschieden, wird sie durch einzelne Stechschuesse
// entschieden - je Paarung ein eigener Durchgang an den beiden Staenden der
// Schuetzen, ausgeloest ueber die Standsteuerung (Staende markieren ->
// "Stechschuss"). Die ersten drei Stechschuesse zaehlen in der Wertung der
// Disziplin (Ganzring oder Zehntel), ab dem vierten immer Zehntel. Die
// Paarung ist entschieden, sobald in einem Durchgang ein Schuetze besser ist.
//
// Jeder Stechschuss ist eine eigene Session (sessions.stechen_no/stechen_of,
// migrations/083) mit genau einem Wertungsschuss ohne Probe: der Stand-PC
// beginnt dadurch mit leerer Scheibe, das Ergebnis der Wertungs-Session
// bleibt unveraendert (siehe ActiveSessionForLane).
// ============================================================================
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"
)

// stechenRingRounds: so viele Stechschuesse zaehlen in der Wertung der
// Disziplin, danach immer Zehntel.
const stechenRingRounds = 3

// StechenShot: ein Stechschuss (Durchgang No) - Shot=false, solange der
// Stechschuss-Durchgang laeuft und noch kein Schuss gefallen ist.
type StechenShot struct {
	No      int     `json:"no"`
	Shot    bool    `json:"shot"`
	Ring    int     `json:"ring"`
	Decimal float64 `json:"decimal"`
	LaneNo  int     `json:"lane_no"`
}

// stechenValue: Wert eines Stechschusses fuer den Vergleich.
func stechenValue(s StechenShot, isDecimal bool) float64 {
	if isDecimal || s.No > stechenRingRounds {
		return s.Decimal
	}
	return float64(s.Ring)
}

// decideStechen: Sieger nach Stechschuessen (0 Heim, 1 Gast, -1 offen) und
// die Zahl der abgeschlossenen Durchgaenge.
func decideStechen(heim, gast []StechenShot, isDecimal bool) (winner, done int) {
	byNo := func(list []StechenShot) map[int]StechenShot {
		m := map[int]StechenShot{}
		for _, s := range list {
			m[s.No] = s
		}
		return m
	}
	h, g := byNo(heim), byNo(gast)
	for n := 1; ; n++ {
		hs, ok1 := h[n]
		gs, ok2 := g[n]
		if !ok1 || !ok2 || !hs.Shot || !gs.Shot {
			return -1, n - 1
		}
		hv, gv := stechenValue(hs, isDecimal), stechenValue(gs, isDecimal)
		switch {
		case hv > gv:
			return 0, n
		case gv > hv:
			return 1, n
		}
	}
}

// attachStechen: Stechschuesse je Starter an die Eintraege haengen.
func (s *Store) attachStechen(ctx context.Context, eventID string, entries []RundenwettkampfEntry) error {
	rows, err := s.pool.Query(ctx, `
		SELECT se.starter_id::text, se.stechen_no, l.lane_no,
		       sh.ring IS NOT NULL,
		       COALESCE(sh.ring, 0), COALESCE(sh.decimal_value, 0)::float8
		FROM sessions se
		JOIN lanes l ON l.id = se.lane_id
		LEFT JOIN LATERAL (
			SELECT COALESCE(corrected_ring, ring) AS ring,
			       COALESCE(corrected_decimal_value, decimal_value) AS decimal_value
			FROM shots
			WHERE session_id = se.id AND kind = 'match' AND status = 'valid'
			ORDER BY shot_no LIMIT 1
		) sh ON TRUE
		WHERE se.event_id = $1::uuid AND se.stechen_no IS NOT NULL
		  AND se.starter_id IS NOT NULL AND se.status::text <> 'aborted'
		ORDER BY se.stechen_no`, eventID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byStarter := map[string][]StechenShot{}
	for rows.Next() {
		var sid string
		var st StechenShot
		if err := rows.Scan(&sid, &st.No, &st.LaneNo, &st.Shot, &st.Ring, &st.Decimal); err != nil {
			return err
		}
		byStarter[sid] = append(byStarter[sid], st)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range entries {
		entries[i].Stechen = byStarter[entries[i].StarterID]
	}
	return nil
}

// ── Stechschuss starten ──────────────────────────────────────────────────

// stechenLane: aktive Session eines markierten Stands.
type stechenLane struct {
	LaneNo                        int
	LaneID, SessionID, ParentID   string
	StarterID, ShooterID, EventID string
	DisciplineID, CalibrationID   string
}

// StartStechen: naechsten Stechschuss-Durchgang fuer die Paarung auf den
// beiden Staenden starten. Liefert die Durchgangsnummer.
func (s *Store) StartStechen(ctx context.Context, laneNos []int) (int, error) {
	if len(laneNos) != 2 || laneNos[0] == laneNos[1] {
		return 0, errors.New("Für einen Stechschuss genau die zwei Stände einer Paarung markieren")
	}
	var lanes [2]stechenLane
	for i, no := range laneNos {
		l := &lanes[i]
		l.LaneNo = no
		err := s.pool.QueryRow(ctx, `
			SELECT l.id::text, se.id::text, COALESCE(se.stechen_of, se.id)::text,
			       COALESCE(se.starter_id::text, (SELECT st.id::text FROM starters st
			           WHERE st.event_id = se.event_id AND st.shooter_id = se.shooter_id LIMIT 1), ''),
			       COALESCE(se.shooter_id::text,''), COALESCE(se.event_id::text,''),
			       se.discipline_id::text, se.calibration_id::text
			FROM sessions se JOIN lanes l ON l.id = se.lane_id
			WHERE l.lane_no = $1 AND se.status IN ('assigned','sighting','match','paused')
			ORDER BY se.started_at DESC NULLS LAST LIMIT 1`, no).Scan(
			&l.LaneID, &l.SessionID, &l.ParentID, &l.StarterID, &l.ShooterID, &l.EventID,
			&l.DisciplineID, &l.CalibrationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("Stand %d ist nicht belegt – der Schütze muss noch am Stand sein", no)
		}
		if err != nil {
			return 0, err
		}
		if l.EventID == "" || l.StarterID == "" {
			return 0, fmt.Errorf("Stand %d: Schütze gehört zu keinem Wettkampf", no)
		}
	}
	if lanes[0].EventID != lanes[1].EventID {
		return 0, errors.New("Die Schützen gehören zu verschiedenen Wettkämpfen")
	}
	comp, err := s.GetCompetition(ctx, lanes[0].EventID)
	if err != nil {
		return 0, err
	}
	if comp.Type != "runde" || comp.RundeModus != "paarung" {
		return 0, errors.New("Stechschüsse gibt es nur im Paarungswettkampf")
	}
	entries, err := s.ListRundenwettkampfResults(ctx, comp.ID)
	if err != nil {
		return 0, err
	}
	pr := computePaarung(entries, false)
	var pair *paarungPair
	for i := range pr.Pairs {
		p := &pr.Pairs[i]
		if p.Heim == nil || p.Gast == nil {
			continue
		}
		a, b := p.Heim.StarterID, p.Gast.StarterID
		if (a == lanes[0].StarterID && b == lanes[1].StarterID) || (a == lanes[1].StarterID && b == lanes[0].StarterID) {
			pair = p
		}
	}
	if pair == nil {
		return 0, errors.New("Die beiden Schützen bilden keine Paarung")
	}
	if !pair.Stechen {
		return 0, fmt.Errorf("Paarung %d ist nicht unentschieden – kein Stechschuss nötig", pair.No)
	}
	// laufender Durchgang noch ohne beide Schuesse?
	maxNo := 0
	for _, e := range []*RundenwettkampfEntry{pair.Heim, pair.Gast} {
		for _, st := range e.Stechen {
			if st.No > maxNo {
				maxNo = st.No
			}
		}
	}
	if maxNo > pair.StechenDone {
		return 0, fmt.Errorf("Stechschuss %d der Paarung %d ist noch nicht von beiden Schützen abgegeben", maxNo, pair.No)
	}
	round := pair.StechenDone + 1

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	for _, l := range lanes {
		if _, err := tx.Exec(ctx, `
			UPDATE sessions SET status = 'finished', finished_at = now()
			WHERE id = $1::uuid AND status IN ('assigned','sighting','match','paused')`, l.SessionID); err != nil {
			return 0, err
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO sessions (lane_id, calibration_id, discipline_id, shooter_id, starter_id, event_id,
			                      status, started_at, stechen_no, stechen_of)
			VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4,'')::uuid, $5::uuid, $6::uuid,
			        'match', now(), $7, $8::uuid)
			RETURNING id::text`,
			l.LaneID, l.CalibrationID, l.DisciplineID, l.ShooterID, l.StarterID, l.EventID,
			round, l.ParentID).Scan(&id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_log (action, session_id, lane_id, actor, details)
			VALUES ('session_started', $1::uuid, $2::uuid, 'server',
			        jsonb_build_object('stechen_no', $3::int, 'pair_no', $4::int))`,
			id, l.LaneID, round, pair.No); err != nil {
			return 0, err
		}
	}
	return round, tx.Commit(ctx)
}

// POST /api/lanes/stechen {lane_nos:[a,b]}
func (a *APIServer) startStechen(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		LaneNos []int `json:"lane_nos"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body")
	}
	round, err := a.store.StartStechen(r.Context(), body.LaneNos)
	if err != nil {
		return nil, errBadRequest(err.Error())
	}
	return map[string]any{"ok": true, "stechen_no": round}, nil
}

// ── offene Stechen (Hinweis in der Standsteuerung) ───────────────────────

type openStechen struct {
	EventID   string `json:"event_id"`
	EventName string `json:"event_name"`
	PairNo    int    `json:"pair_no"`
	Heim      string `json:"heim"`
	Gast      string `json:"gast"`
	HeimLane  int    `json:"heim_lane"` // Stand, an dem der Schuetze gerade ist (0 = keiner)
	GastLane  int    `json:"gast_lane"`
	Done      int    `json:"done"`    // abgeschlossene Durchgaenge
	Running   int    `json:"running"` // laufender Durchgang (0 = keiner)
	NextRound int    `json:"next_round"`
	Decimal   bool   `json:"decimal"` // naechster Durchgang in Zehntel
}

// GET /api/stechen/offen: unentschiedene Paarungen laufender
// Paarungswettkaempfe, deren Schuetzen beide alle Wertungsschuesse haben.
func (a *APIServer) listOpenStechen(w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	rows, err := a.store.pool.Query(ctx, `
		SELECT id::text FROM events
		WHERE type = 'runde' AND runde_modus = 'paarung' AND status = 'running' AND active`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	out := []openStechen{}
	for _, id := range ids {
		comp, err := a.store.GetCompetition(ctx, id)
		if err != nil {
			continue
		}
		entries, err := a.store.ListRundenwettkampfResults(ctx, id)
		if err != nil {
			return nil, err
		}
		lanes := a.store.activeLaneByStarter(ctx, id)
		pr := computePaarung(entries, false)
		for _, p := range pr.Pairs {
			if !p.Stechen || p.Heim == nil || p.Gast == nil {
				continue
			}
			complete := func(e *RundenwettkampfEntry) bool {
				return e.MatchShotCount == 0 || e.ShotCount >= e.MatchShotCount
			}
			if !complete(p.Heim) || !complete(p.Gast) {
				continue
			}
			o := openStechen{EventID: id, EventName: comp.Name, PairNo: p.No,
				Heim: p.Heim.LastName + ", " + p.Heim.FirstName, Gast: p.Gast.LastName + ", " + p.Gast.FirstName,
				HeimLane: lanes[p.Heim.StarterID], GastLane: lanes[p.Gast.StarterID],
				Done: p.StechenDone, NextRound: p.StechenDone + 1}
			for _, e := range []*RundenwettkampfEntry{p.Heim, p.Gast} {
				for _, st := range e.Stechen {
					if st.No > p.StechenDone {
						o.Running = st.No
					}
				}
			}
			o.Decimal = pr.IsDecimal || o.NextRound > stechenRingRounds
			out = append(out, o)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PairNo < out[j].PairNo })
	return out, nil
}

// activeLaneByStarter: Stand der aktiven Session je Starter eines Wettkampfs.
func (s *Store) activeLaneByStarter(ctx context.Context, eventID string) map[string]int {
	out := map[string]int{}
	rows, err := s.pool.Query(ctx, `
		SELECT st.id::text, l.lane_no
		FROM sessions se
		JOIN lanes l ON l.id = se.lane_id
		JOIN starters st ON st.event_id = se.event_id
		     AND (st.id = se.starter_id OR (se.starter_id IS NULL AND st.shooter_id = se.shooter_id))
		WHERE se.event_id = $1::uuid AND se.status IN ('assigned','sighting','match','paused')`, eventID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var no int
		if rows.Scan(&sid, &no) == nil {
			out[sid] = no
		}
	}
	return out
}
