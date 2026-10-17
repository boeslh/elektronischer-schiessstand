// ============================================================================
// online_snapshot.go – Momentaufnahmen fuer die Online-Anzeige (online/).
//
// Datensparsamkeit: Es werden nur fertige Anzeigedaten uebertragen, keine
// Stammdaten. Personenbezogen sind ausschliesslich
//   - Preisschiessen: Startnummer, Name und Verein der Teilnehmer, die in
//     einer sichtbaren Wertungsliste stehen (aus ps_wertung_ergebnisse),
//   - Rundenwettkampf: Name der Starter des Wettkampfs mit Ergebnissen,
//     Serien und den Treffern der laufenden Serie.
//
// Nicht uebertragen werden u. a. Geburtsdatum, Passnummer, Sportklasse,
// Kontaktdaten, Guthaben und Mitglieder ohne Teilnahme.
//
// Die Formate sind das Gegenstueck zu online/publish.go (PSSnapshot,
// RWKSnapshot) - Aenderungen immer an beiden Stellen und onlineFormat
// erhoehen.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const onlineFormat = 1

// ── Preisschiessen ───────────────────────────────────────────────────────

type onlinePS struct {
	Format      int              `json:"format"`
	GeneratedAt time.Time        `json:"generated_at"`
	Name        string           `json:"name"`
	Art         string           `json:"art"`
	StartsOn    string           `json:"starts_on"`
	EndsOn      string           `json:"ends_on"`
	Finished    bool             `json:"finished"`
	Berechnet   string           `json:"berechnet"`
	Stats       onlinePSStats    `json:"stats"`
	Wertungen   []onlinePSListe  `json:"wertungen"`
	Vereine     []onlinePSVListe `json:"vereine"`
}

type onlinePSStats struct {
	Vereine    int `json:"vereine"`
	Teilnehmer int `json:"teilnehmer"`
	Scheiben   int `json:"scheiben"`
	Schuss     int `json:"schuss"`
}

type onlinePSListe struct {
	Key          string          `json:"key"`
	Name         string          `json:"name"`
	Beschreibung string          `json:"beschreibung"`
	Feld         string          `json:"feld"`
	AnzSumme     int             `json:"anz_summe"`
	Zeilen       []onlinePSZeile `json:"zeilen"`
}

type onlinePSZeile struct {
	Platz  int       `json:"platz"`
	Nr     int       `json:"nr"`
	Name   string    `json:"name"`
	Verein string    `json:"verein"`
	Werte  []float64 `json:"werte"`
	Summe  float64   `json:"summe"`
	Preis  string    `json:"preis"`
}

type onlinePSVListe struct {
	Typ       string           `json:"typ"`
	Name      string           `json:"name"`
	WertTitel string           `json:"wert_titel"`
	Zeilen    []onlinePSVZeile `json:"zeilen"`
}

type onlinePSVZeile struct {
	Platz      int    `json:"platz"`
	Verein     string `json:"verein"`
	Gastgeber  bool   `json:"gastgeber"`
	Teilnehmer int    `json:"teilnehmer"`
	Wert       string `json:"wert"`
	Preis      string `json:"preis"`
}

func onlineNum(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
}

// onlineGewinne: Platz -> Preistext fuer eine Liste.
func (s *Store) onlineGewinne(ctx context.Context, psID, where string, arg string) map[int]string {
	out := map[int]string{}
	rows, err := s.pool.Query(ctx, `SELECT platz, betrag::float8, sachpreis FROM ps_gewinne
		WHERE preisschiessen_id=$1::uuid AND `+where, psID, arg)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var platz int
		var betrag *float64
		var sach *string
		if rows.Scan(&platz, &betrag, &sach) != nil {
			continue
		}
		var parts []string
		if betrag != nil {
			parts = append(parts, onlineNum(*betrag)+" €")
		}
		if sach != nil && *sach != "" {
			parts = append(parts, *sach)
		}
		out[platz] = strings.Join(parts, " + ")
	}
	return out
}

func (s *Store) BuildOnlinePS(ctx context.Context, psID string) (*onlinePS, error) {
	out := &onlinePS{Format: onlineFormat, GeneratedAt: time.Now()}
	var starts, ends *time.Time
	var active bool
	if err := s.pool.QueryRow(ctx, `SELECT name, kind, starts_on, ends_on, active FROM preisschiessen WHERE id=$1::uuid`, psID).
		Scan(&out.Name, &out.Art, &starts, &ends, &active); err != nil {
		return nil, fmt.Errorf("Preisschießen nicht gefunden: %w", err)
	}
	if starts != nil {
		out.StartsOn = starts.Format("2006-01-02")
	}
	if ends != nil {
		out.EndsOn = ends.Format("2006-01-02")
	}
	today := time.Now().Format("2006-01-02")
	out.Finished = !active || (out.EndsOn != "" && out.EndsOn < today)

	var calc *time.Time
	s.pool.QueryRow(ctx, `SELECT last_finished_at FROM ps_auswertung_status WHERE preisschiessen_id=$1::uuid`, psID).Scan(&calc)
	out.Berechnet = "noch nicht berechnet"
	if calc != nil {
		out.Berechnet = calc.In(time.Local).Format("02.01.2006 15:04")
	}
	s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM ps_verein_teilnahme WHERE preisschiessen_id=$1::uuid),
		(SELECT count(*) FROM ps_teilnehmer WHERE preisschiessen_id=$1::uuid),
		(SELECT count(*) FROM ps_kauf_scheiben WHERE preisschiessen_id=$1::uuid),
		(SELECT count(*) FROM shots sh JOIN ps_kauf_scheiben ks ON ks.session_id=sh.session_id
		  WHERE ks.preisschiessen_id=$1::uuid AND sh.status<>'annulled')`, psID).
		Scan(&out.Stats.Vereine, &out.Stats.Teilnehmer, &out.Stats.Scheiben, &out.Stats.Schuss)

	// sichtbare Wertungslisten (wie in der Preisanzeige)
	rows, err := s.pool.Query(ctx, `SELECT id::text, short_desc, COALESCE(long_desc,''), COALESCE(wertungsfeld,''), anz_summe
		FROM ps_wertungen WHERE preisschiessen_id=$1::uuid AND visible ORDER BY sort_order, short_desc`, psID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l onlinePSListe
		if err := rows.Scan(&l.Key, &l.Name, &l.Beschreibung, &l.Feld, &l.AnzSumme); err != nil {
			rows.Close()
			return nil, err
		}
		out.Wertungen = append(out.Wertungen, l)
	}
	rows.Close()
	for i := range out.Wertungen {
		l := &out.Wertungen[i]
		gew := s.onlineGewinne(ctx, psID, "wertung_id=$2::uuid", l.Key)
		rr, err := s.pool.Query(ctx, `SELECT COALESCE(platz,0), start_nr, nachname||' '||vorname, COALESCE(verein,''),
			werte::float8[], summe::float8 FROM ps_wertung_ergebnisse WHERE ps_wertung_id=$1::uuid ORDER BY platz NULLS LAST, start_nr`, l.Key)
		if err != nil {
			return nil, err
		}
		for rr.Next() {
			var z onlinePSZeile
			if err := rr.Scan(&z.Platz, &z.Nr, &z.Name, &z.Verein, &z.Werte, &z.Summe); err != nil {
				rr.Close()
				return nil, err
			}
			z.Preis = gew[z.Platz]
			l.Zeilen = append(l.Zeilen, z)
		}
		rr.Close()
		// stabiler Schluessel ohne interne ID
		l.Key = fmt.Sprintf("%d", i+1)
	}

	// Vereins-Auswertungen
	vrows, err := s.ComputeVereinsAuswertung(ctx, psID)
	if err == nil && len(vrows) > 0 {
		for _, typ := range []struct{ typ, name, wert string }{
			{"anzahl", "Vereine – Teilnehmer Anzahl", "Anzahl"},
			{"prozent", "Vereine – Teilnehmer Prozent", "Prozent"},
			{"punkte", "Vereine – Teilnehmer Punkte", "Punkte"},
		} {
			gew := s.onlineGewinne(ctx, psID, "verein_typ=$2", typ.typ)
			l := onlinePSVListe{Typ: typ.typ, Name: typ.name, WertTitel: typ.wert}
			for _, r := range vrows {
				z := onlinePSVZeile{Verein: r.ClubName, Gastgeber: r.Gastgeber, Teilnehmer: r.Anzahl}
				switch typ.typ {
				case "anzahl":
					z.Platz, z.Wert = r.PlatzAnzahl, fmt.Sprintf("%d", r.Anzahl)
				case "prozent":
					z.Platz, z.Wert = r.PlatzProzent, "–"
					if r.Prozent != nil {
						z.Wert = onlineNum(*r.Prozent) + " %"
					}
				case "punkte":
					z.Platz, z.Wert = r.PlatzPunkte, onlineNum(r.Punkte)
				}
				z.Preis = gew[z.Platz]
				l.Zeilen = append(l.Zeilen, z)
			}
			sort.SliceStable(l.Zeilen, func(i, j int) bool { return l.Zeilen[i].Platz < l.Zeilen[j].Platz })
			out.Vereine = append(out.Vereine, l)
		}
	}
	return out, nil
}

// ── Rundenwettkampf ──────────────────────────────────────────────────────

type onlineRWK struct {
	Format      int                       `json:"format"`
	GeneratedAt time.Time                 `json:"generated_at"`
	Name        string                    `json:"name"`
	Liga        string                    `json:"liga"`
	Ort         string                    `json:"ort"`
	Datum       string                    `json:"datum"`
	Disziplin   string                    `json:"disziplin"`
	Status      string                    `json:"status"`
	Modus       string                    `json:"modus"`
	BestOf      int                       `json:"best_of"`
	Decimal     bool                      `json:"decimal"`
	Teams       [2]onlineRWKTeam          `json:"teams"`
	Pairs       []onlineRWKPair           `json:"pairs"`
	Starter     []onlineRWKStarter        `json:"starter"`
	Geos        map[string]TargetGeometry `json:"geos"`
}

type onlineRWKTeam struct {
	Name   string  `json:"name"`
	Verein string  `json:"verein"`
	Total  float64 `json:"total"`
	HR     float64 `json:"hr"`
	Points int     `json:"points"`
}

type onlineRWKPair struct {
	No     int  `json:"no"`
	Heim   int  `json:"heim"`
	Gast   int  `json:"gast"`
	Leader int  `json:"leader"`
	Stech  bool `json:"stechen"`
}

type onlineRWKStarter struct {
	Team       int            `json:"team"`
	Name       string         `json:"name"`
	Role       string         `json:"role"`
	PairNo     int            `json:"pair_no"`
	Shots      int            `json:"shots"`
	MatchShots int            `json:"match_shots"`
	Total      float64        `json:"total"`
	HR         float64        `json:"hr"`
	Counts     bool           `json:"counts"`
	Series     []float64      `json:"series"`
	Lane       int            `json:"lane"`
	Stechen    int            `json:"stechen"`
	Tile       *onlineRWKTile `json:"tile,omitempty"`
}

type onlineRWKTile struct {
	Geo   string           `json:"geo"`
	Shots []onlineTileShot `json:"shots"`
	Last  string           `json:"last"`
	Probe bool             `json:"probe"`
}

type onlineTileShot struct {
	No      int     `json:"no"`
	X       float64 `json:"x_mm"`
	Y       float64 `json:"y_mm"`
	Ring    int     `json:"ring"`
	Decimal float64 `json:"decimal"`
}

func (s *Store) BuildOnlineRWK(ctx context.Context, eventID string) (*onlineRWK, error) {
	comp, err := s.GetCompetition(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("Wettkampf nicht gefunden: %w", err)
	}
	if comp.Type != "runde" {
		return nil, fmt.Errorf("kein Rundenwettkampf")
	}
	out := &onlineRWK{Format: onlineFormat, GeneratedAt: time.Now(), Name: comp.Name, Liga: comp.Liga, Ort: comp.Location,
		Disziplin: comp.DisciplineName, Status: comp.Status, Modus: comp.RundeModus, Geos: map[string]TargetGeometry{}}
	if len(comp.StartsOn) >= 10 {
		out.Datum = comp.StartsOn[:10]
	}
	if comp.BestOf != nil {
		out.BestOf = *comp.BestOf
	}
	// Mannschaften (Heim/Gast)
	rows, err := s.pool.Query(ctx, `SELECT cp.sort_order, COALESCE(t.name,''), COALESCE(c.name,'')
		FROM competition_participants cp LEFT JOIN teams t ON t.id=cp.team_id LEFT JOIN clubs c ON c.id=t.club_id
		WHERE cp.event_id=$1::uuid ORDER BY cp.sort_order LIMIT 2`, eventID)
	if err == nil {
		for rows.Next() {
			var so int
			var tn, cn string
			if rows.Scan(&so, &tn, &cn) == nil && so >= 0 && so <= 1 {
				out.Teams[so] = onlineRWKTeam{Name: tn, Verein: cn}
			}
		}
		rows.Close()
	}

	entries, err := s.ListRundenwettkampfResults(ctx, eventID)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.DecimalScoring {
			out.Decimal = true
		}
	}
	val := func(e *RundenwettkampfEntry) float64 {
		if e.ShotCount == 0 {
			return 0
		}
		if out.Decimal {
			return e.TotalDecimal
		}
		return float64(e.TotalRings)
	}
	// Reihenfolge je Mannschaft: S/E nach Ergebnis, dann AK
	idx := map[string]int{}
	for team := 0; team < 2; team++ {
		var countable, ak []*RundenwettkampfEntry
		for i := range entries {
			e := &entries[i]
			if e.TeamSide != team {
				continue
			}
			if e.Role == "AK" {
				ak = append(ak, e)
			} else {
				countable = append(countable, e)
			}
		}
		less := func(l []*RundenwettkampfEntry) func(i, j int) bool {
			return func(i, j int) bool {
				if val(l[i]) != val(l[j]) {
					return val(l[i]) > val(l[j])
				}
				return l[i].InnerTens > l[j].InnerTens
			}
		}
		sort.SliceStable(countable, less(countable))
		sort.SliceStable(ak, less(ak))
		var hrs []float64
		for k, e := range append(countable, ak...) {
			st := onlineRWKStarter{Team: team, Name: e.LastName + ", " + e.FirstName, Role: e.Role, PairNo: e.PairNo,
				Shots: e.ShotCount, MatchShots: e.MatchShotCount, Total: val(e),
				HR: paarungValue(e, out.Decimal, true)}
			if e.Role != "AK" {
				if comp.RundeModus == "paarung" {
					st.Counts = e.PairNo > 0
				} else {
					st.Counts = out.BestOf <= 0 || k < out.BestOf
					if st.Counts {
						out.Teams[team].Total += st.Total
					}
					hrs = append(hrs, st.HR)
				}
			}
			for _, sx := range e.Stechen {
				if sx.No > st.Stechen {
					st.Stechen = sx.No
				}
			}
			s.onlineTile(ctx, e, &st, out)
			idx[e.StarterID] = len(out.Starter)
			out.Starter = append(out.Starter, st)
		}
		sort.Sort(sort.Reverse(sort.Float64Slice(hrs)))
		if out.BestOf > 0 && len(hrs) > out.BestOf {
			hrs = hrs[:out.BestOf]
		}
		for _, h := range hrs {
			out.Teams[team].HR += h
		}
	}

	if comp.RundeModus == "paarung" {
		pr := computePaarung(entries, true)
		out.Teams[0].Points, out.Teams[1].Points = pr.HeimPoints, pr.GastPoints
		final := computePaarung(entries, false) // Ringe der gepaarten Schuetzen
		out.Teams[0].Total, out.Teams[1].Total = final.HeimTotal, final.GastTotal
		out.Teams[0].HR, out.Teams[1].HR = pr.HeimTotal, pr.GastTotal
		for _, p := range pr.Pairs {
			op := onlineRWKPair{No: p.No, Heim: -1, Gast: -1, Leader: p.Winner, Stech: p.Stechen}
			if p.Heim != nil {
				op.Heim = idx[p.Heim.StarterID]
			}
			if p.Gast != nil {
				op.Gast = idx[p.Gast.StarterID]
			}
			out.Pairs = append(out.Pairs, op)
		}
	}
	return out, nil
}

// onlineTile: Trefferbild der laufenden Serie (bzw. des laufenden
// Stechschusses) und Seriensummen eines Starters.
func (s *Store) onlineTile(ctx context.Context, e *RundenwettkampfEntry, st *onlineRWKStarter, out *onlineRWK) {
	sid := e.SessionID
	if sid == "" {
		return
	}
	var lane, perSeries int
	s.pool.QueryRow(ctx, `SELECT COALESCE(l.lane_no,0), COALESCE(d.shots_per_series,10)
		FROM sessions se LEFT JOIN lanes l ON l.id=se.lane_id JOIN disciplines d ON d.id=se.discipline_id
		WHERE se.id=$1::uuid`, sid).Scan(&lane, &perSeries)
	st.Lane = lane
	if rows, err := s.pool.Query(ctx, `SELECT COALESCE(decimal_total,0)::float8, COALESCE(rings,0)::int
		FROM v_series_results WHERE session_id=$1::uuid ORDER BY series_no`, sid); err == nil {
		for rows.Next() {
			var d float64
			var r int
			if rows.Scan(&d, &r) == nil {
				if out.Decimal {
					st.Series = append(st.Series, d)
				} else {
					st.Series = append(st.Series, float64(r))
				}
			}
		}
		rows.Close()
	}
	// Trefferbild: laufender Stechschuss, sonst laufende Serie, vor der
	// Wertung die Probeschuesse
	shotSession := sid
	if len(e.Stechen) > 0 {
		var stSid string
		if s.pool.QueryRow(ctx, `SELECT id::text FROM sessions WHERE starter_id=$1::uuid AND stechen_no IS NOT NULL
			ORDER BY stechen_no DESC LIMIT 1`, e.StarterID).Scan(&stSid) == nil {
			shotSession = stSid
		}
	}
	geo, err := s.resolveTargetGeometry(ctx, shotSession)
	if err != nil {
		return
	}
	key := geo.Name
	if key == "" {
		key = "scheibe"
	}
	out.Geos[key] = geo
	tile := &onlineRWKTile{Geo: key, Shots: []onlineTileShot{}}
	rows, err := s.pool.Query(ctx, `SELECT kind::text, COALESCE(corrected_x_mm,x_mm)::float8, COALESCE(corrected_y_mm,y_mm)::float8,
		COALESCE(corrected_ring,ring,0), COALESCE(corrected_decimal_value,decimal_value,0)::float8
		FROM shots WHERE session_id=$1::uuid AND status='valid' AND x_mm IS NOT NULL ORDER BY shot_no`, shotSession)
	if err != nil {
		return
	}
	var sighting, match []onlineTileShot
	for rows.Next() {
		var kind string
		var t onlineTileShot
		if rows.Scan(&kind, &t.X, &t.Y, &t.Ring, &t.Decimal) != nil {
			continue
		}
		if kind == "sighting" {
			t.No = len(sighting) + 1
			sighting = append(sighting, t)
		} else {
			t.No = len(match) + 1
			match = append(match, t)
		}
	}
	rows.Close()
	if len(match) > 0 {
		if perSeries <= 0 {
			perSeries = len(match)
		}
		start := ((len(match) - 1) / perSeries) * perSeries
		if shotSession != sid {
			start = 0 // Stechschuss: alle Schuesse dieser Sitzung
		}
		tile.Shots = match[start:]
	} else {
		tile.Shots, tile.Probe = sighting, len(sighting) > 0
	}
	if n := len(tile.Shots); n > 0 {
		l := tile.Shots[n-1]
		if out.Decimal || st.Stechen > 3 {
			tile.Last = strings.Replace(fmt.Sprintf("%.1f", l.Decimal), ".", ",", 1)
		} else {
			tile.Last = fmt.Sprintf("%d", l.Ring)
		}
	}
	st.Tile = tile
}
