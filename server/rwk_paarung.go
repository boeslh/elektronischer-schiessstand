// ============================================================================
// rwk_paarung.go – Paarungswettkampf (events.runde_modus = 'paarung').
//
// Je Paarung (starters.pair_no) schiesst ein Heim- gegen einen Gastschuetzen;
// das bessere Ergebnis (Zehntel bei Zehntelwertung, sonst Ringe) bringt der
// Mannschaft einen Punkt. Es gewinnt die Mannschaft mit den meisten Punkten.
// Bei gerader Paarungszahl (4 Schuetzen) und Punktgleichstand zaehlt das
// Mannschaftsergebnis (Summe der gepaarten Schuetzen) als weiterer Punkt.
// Gleichstand in einer Paarung wird durch Stechschuesse entschieden - die
// sind noch nicht umgesetzt, die Paarung wird nur als "Stechen" markiert.
//
// Die Live-Anzeigen (preisanzeige/rundenanzeige.go) rechnen dieselbe Logik
// mit der Hochrechnung statt des Ergebnisses.
// ============================================================================
package main

import (
	"net/http"
	"sort"
)

type paarungPair struct {
	No      int                   `json:"no"`
	Heim    *RundenwettkampfEntry `json:"heim"`
	Gast    *RundenwettkampfEntry `json:"gast"`
	HeimVal float64               `json:"heim_val"`
	GastVal float64               `json:"gast_val"`
	Winner  int                   `json:"winner"`  // 0 Heim, 1 Gast, -1 offen/Stechen
	Stechen bool                  `json:"stechen"` // Gleichstand, Stechen noch nicht entschieden
	// ByStechen: per Stechschuss entschieden; StechenDone: abgeschlossene
	// Stechschuss-Durchgaenge (rwk_stechen.go)
	ByStechen   bool `json:"by_stechen"`
	StechenDone int  `json:"stechen_done"`
}

type paarungResult struct {
	Modus      string        `json:"modus"`
	IsDecimal  bool          `json:"is_decimal"`
	HeimName   string        `json:"heim_name"`
	GastName   string        `json:"gast_name"`
	Pairs      []paarungPair `json:"pairs"`
	HeimPoints int           `json:"heim_points"` // inkl. Mannschaftspunkt
	GastPoints int           `json:"gast_points"`
	HeimTotal  float64       `json:"heim_total"` // Summe der gepaarten Schuetzen
	GastTotal  float64       `json:"gast_total"`
	TotalPoint int           `json:"total_point"` // Mannschaftspunkt: 0 Heim, 1 Gast, -1 keiner
	Winner     int           `json:"winner"`      // 0 Heim, 1 Gast, -1 unentschieden/offen
	Unpaired   int           `json:"unpaired"`    // S/E-Starter ohne Paarung
}

// paarungValue: Ergebnis (bzw. mit hr Hochrechnung) eines Starters.
func paarungValue(e *RundenwettkampfEntry, isDecimal, hr bool) float64 {
	if e == nil || e.ShotCount == 0 {
		return 0
	}
	v := float64(e.TotalRings)
	if isDecimal {
		v = e.TotalDecimal
	}
	if hr && e.MatchShotCount > 0 && e.ShotCount < e.MatchShotCount {
		v = v / float64(e.ShotCount) * float64(e.MatchShotCount)
	}
	return v
}

// computePaarung: Auswertung siehe Dateikopf. Heim/Gast aus TeamSide
// (Reihenfolge der Mannschaften im Wettkampf), sonst nach erstem Auftreten.
func computePaarung(entries []RundenwettkampfEntry, hr bool) paarungResult {
	res := paarungResult{Modus: "paarung", TotalPoint: -1, Winner: -1}
	for _, e := range entries {
		if e.DecimalScoring {
			res.IsDecimal = true
		}
	}
	side := map[string]int{}
	for _, e := range entries {
		if e.TeamID != "" && e.TeamSide >= 0 && e.TeamSide <= 1 {
			side[e.TeamID] = e.TeamSide
		}
	}
	for _, e := range entries {
		if _, ok := side[e.TeamID]; e.TeamID != "" && !ok && len(side) < 2 {
			used := map[int]bool{}
			for _, s := range side {
				used[s] = true
			}
			if !used[0] {
				side[e.TeamID] = 0
			} else {
				side[e.TeamID] = 1
			}
		}
	}
	byNo := map[int]*paarungPair{}
	for i := range entries {
		e := &entries[i]
		s, ok := side[e.TeamID]
		if !ok || e.Role == "AK" {
			continue
		}
		if s == 0 {
			res.HeimName = e.TeamName
		} else {
			res.GastName = e.TeamName
		}
		if e.PairNo <= 0 {
			res.Unpaired++
			continue
		}
		p := byNo[e.PairNo]
		if p == nil {
			p = &paarungPair{No: e.PairNo, Winner: -1}
			byNo[e.PairNo] = p
		}
		if s == 0 {
			p.Heim = e
		} else {
			p.Gast = e
		}
	}
	for _, p := range byNo {
		p.HeimVal = paarungValue(p.Heim, res.IsDecimal, hr)
		p.GastVal = paarungValue(p.Gast, res.IsDecimal, hr)
		res.HeimTotal += p.HeimVal
		res.GastTotal += p.GastVal
		hs := p.Heim != nil && p.Heim.ShotCount > 0
		gs := p.Gast != nil && p.Gast.ShotCount > 0
		switch {
		case !hs && !gs:
			// noch offen
		case p.HeimVal > p.GastVal:
			p.Winner = 0
		case p.GastVal > p.HeimVal:
			p.Winner = 1
		default:
			var hst, gst []StechenShot
			if p.Heim != nil {
				hst = p.Heim.Stechen
			}
			if p.Gast != nil {
				gst = p.Gast.Stechen
			}
			w, done := decideStechen(hst, gst, res.IsDecimal)
			p.StechenDone = done
			if w >= 0 {
				p.Winner, p.ByStechen = w, true
			} else {
				p.Stechen = true
			}
		}
		switch p.Winner {
		case 0:
			res.HeimPoints++
		case 1:
			res.GastPoints++
		}
		res.Pairs = append(res.Pairs, *p)
	}
	sort.Slice(res.Pairs, func(i, j int) bool { return res.Pairs[i].No < res.Pairs[j].No })
	if len(res.Pairs)%2 == 0 && res.HeimPoints == res.GastPoints && len(res.Pairs) > 0 {
		switch {
		case res.HeimTotal > res.GastTotal:
			res.TotalPoint = 0
			res.HeimPoints++
		case res.GastTotal > res.HeimTotal:
			res.TotalPoint = 1
			res.GastPoints++
		}
	}
	switch {
	case res.HeimPoints > res.GastPoints:
		res.Winner = 0
	case res.GastPoints > res.HeimPoints:
		res.Winner = 1
	}
	return res
}

// GET /api/auswertung/rundenwettkampf/paarung?event_id=...
// {modus:"mannschaft"} bei Mannschaftswettkampf, sonst paarungResult.
func (a *APIServer) getRundenPaarung(w http.ResponseWriter, r *http.Request) (any, error) {
	eventID := r.URL.Query().Get("event_id")
	if eventID == "" {
		return nil, errBadRequest("event_id erforderlich")
	}
	comp, err := a.store.GetCompetition(r.Context(), eventID)
	if err != nil {
		return nil, err
	}
	if comp.RundeModus != "paarung" {
		return map[string]string{"modus": "mannschaft"}, nil
	}
	entries, err := a.store.ListRundenwettkampfResults(r.Context(), eventID)
	if err != nil {
		return nil, err
	}
	return computePaarung(entries, r.URL.Query().Get("hr") == "1"), nil
}
