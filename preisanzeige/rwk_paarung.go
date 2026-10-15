// ============================================================================
// rwk_paarung.go – Paarungswettkampf in den Anzeigen (events.runde_modus =
// 'paarung'). Regeln wie server/rwk_paarung.go computePaarung(), hier aber
// live nach Hochrechnung: je Paarung bekommt der nach HR vordere Schuetze
// den Punkt (gruener Punkt im Trefferbild, der andere grau), bei gerader
// Paarungszahl und Punktgleichstand zaehlt die Summe der Paarungs-HR als
// weiterer Punkt. Nach Abschluss ist HR = Ergebnis.
// ============================================================================
package main

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// rundeStechen: ein Stechschuss (server/rwk_stechen.go StechenShot).
type rundeStechen struct {
	No        int
	Shot      bool
	Ring      int
	Decimal   float64
	SessionID string
}

// attachRundeStechen: Stechschuesse je Starter an die Eintraege haengen.
func attachRundeStechen(ctx context.Context, pool *pgxpool.Pool, eventID string, entries []rundeEntry) {
	if len(entries) == 0 {
		return
	}
	rows, err := pool.Query(ctx, `
		SELECT se.starter_id::text, se.stechen_no, se.id::text, sh.ring IS NOT NULL,
		       COALESCE(sh.ring, 0), COALESCE(sh.decimal_value, 0)::float8
		FROM sessions se
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
		return
	}
	defer rows.Close()
	by := map[string][]rundeStechen{}
	for rows.Next() {
		var sid string
		var s rundeStechen
		if rows.Scan(&sid, &s.No, &s.SessionID, &s.Shot, &s.Ring, &s.Decimal) == nil {
			by[sid] = append(by[sid], s)
		}
	}
	for i := range entries {
		entries[i].Stechen = by[entries[i].StarterID]
	}
}

// decideRundeStechen: wie server/rwk_stechen.go decideStechen - die ersten
// drei Stechschuesse in der Wertung der Disziplin, danach Zehntel.
func decideRundeStechen(h, g []rundeStechen, isDecimal bool) int {
	val := func(s rundeStechen) float64 {
		if isDecimal || s.No > 3 {
			return s.Decimal
		}
		return float64(s.Ring)
	}
	for n := 1; ; n++ {
		var hs, gs *rundeStechen
		for i := range h {
			if h[i].No == n {
				hs = &h[i]
			}
		}
		for i := range g {
			if g[i].No == n {
				gs = &g[i]
			}
		}
		if hs == nil || gs == nil || !hs.Shot || !gs.Shot {
			return -1
		}
		if hv, gv := val(*hs), val(*gs); hv != gv {
			if hv > gv {
				return 0
			}
			return 1
		}
	}
}

type rundePair struct {
	No             int
	Heim, Gast     *rundeEntry
	HeimHR, GastHR float64
	Leader         int // 0 Heim, 1 Gast, -1 offen/Gleichstand
}

type rundePaarung struct {
	Pairs            []rundePair
	HeimPts, GastPts int // inkl. Mannschaftspunkt
	HeimSum, GastSum float64
	TotalPoint       int // 0 Heim, 1 Gast, -1 keiner
}

// loadRundeModus: 'mannschaft' oder 'paarung'.
func loadRundeModus(ctx context.Context, pool *pgxpool.Pool, eventID string) string {
	var m string
	if pool.QueryRow(ctx, `SELECT runde_modus FROM events WHERE id = $1::uuid`, eventID).Scan(&m) != nil {
		return "mannschaft"
	}
	return m
}

func computeRundePaarung(entries []rundeEntry, heimID, gastID string, isDecimal bool, matchShots map[string]int) rundePaarung {
	res := rundePaarung{TotalPoint: -1}
	byNo := map[int]*rundePair{}
	for i := range entries {
		e := &entries[i]
		if e.PairNo <= 0 || e.Role == "AK" || (e.TeamID != heimID && e.TeamID != gastID) {
			continue
		}
		p := byNo[e.PairNo]
		if p == nil {
			p = &rundePair{No: e.PairNo, Leader: -1}
			byNo[e.PairNo] = p
		}
		if e.TeamID == heimID {
			p.Heim = e
		} else {
			p.Gast = e
		}
	}
	hr := func(e *rundeEntry) (float64, bool) {
		if e == nil || e.ShotCount == 0 {
			return 0, false
		}
		v, ok := hochrechnung(e.value(isDecimal), e.ShotCount, matchShots[e.LastName+"\x00"+e.FirstName])
		if !ok {
			v = e.value(isDecimal)
		}
		return v, true
	}
	for _, p := range byNo {
		var hs, gs bool
		p.HeimHR, hs = hr(p.Heim)
		p.GastHR, gs = hr(p.Gast)
		res.HeimSum += p.HeimHR
		res.GastSum += p.GastHR
		if hs || gs {
			switch {
			case p.HeimHR > p.GastHR:
				p.Leader = 0
				res.HeimPts++
			case p.GastHR > p.HeimHR:
				p.Leader = 1
				res.GastPts++
			case p.Heim != nil && p.Gast != nil:
				// Gleichstand: Stechschuesse entscheiden
				switch decideRundeStechen(p.Heim.Stechen, p.Gast.Stechen, isDecimal) {
				case 0:
					p.Leader = 0
					res.HeimPts++
				case 1:
					p.Leader = 1
					res.GastPts++
				}
			}
		}
		res.Pairs = append(res.Pairs, *p)
	}
	sort.Slice(res.Pairs, func(i, j int) bool { return res.Pairs[i].No < res.Pairs[j].No })
	if n := len(res.Pairs); n > 0 && n%2 == 0 && res.HeimPts == res.GastPts {
		switch {
		case res.HeimSum > res.GastSum:
			res.TotalPoint = 0
			res.HeimPts++
		case res.GastSum > res.HeimSum:
			res.TotalPoint = 1
			res.GastPts++
		}
	}
	return res
}

// dotFor: Punkt fuer die Seite (0 Heim, 1 Gast) einer Paarung.
func (p rundePair) dotFor(side int) string {
	switch p.Leader {
	case -1:
		return ""
	case side:
		return "lead"
	}
	return "trail"
}
