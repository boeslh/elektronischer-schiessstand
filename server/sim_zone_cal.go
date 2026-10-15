// ============================================================================
// sim_zone_cal.go – "One-Click"-Foto-Kalibrierung mit Zonen (siehe
// sim_zones.go): POST /api/sessions/{id}/photo-calibrate-zones.
//
//  1. Basissatz: Foto-Kalibrierung an ALLEN zugeordneten Schuessen mit den
//     gewaehlten Optionen (inkl. Geometrie, falls angehakt). Dieser Satz
//     funktioniert auch auf Geraeten ohne Zonen-Unterstuetzung.
//  2. Je Zone (nach der Lage des Lochs im Foto): ausgehend vom Basissatz
//     Schallgeschwindigkeit, Mikrofon-Offsets und Nachkorrektur an den
//     Schuessen dieser Zone (zoneFitLM). Wie viele Werte frei sind, haengt
//     von der Zahl der Schuesse ab - zu wenige Schuesse wuerden sonst nur die
//     Messfehler nachbilden. Grenzen aus der Leave-one-out-Pruefung an der
//     Serie vom 12.10.2026 (calib_study_test.go TestStudyOneClick):
//     >= 6 Schuesse: Offsets + Nachkorrektur + Schallgeschwindigkeit
//     >= 4 Schuesse: Nachkorrektur + Schallgeschwindigkeit
//     >= 3 Schuesse: nur Nachkorrektur
//     sonst: Zone uebernimmt den Basissatz
//  3. Bewertung vorher/nachher je Zone mit der vollstaendigen (zweistufigen)
//     Neuberechnung.
//
// ============================================================================
package main

import (
	"math"
	"net/http"
	"sort"
)

type zoneCalStats struct {
	N       int     `json:"n"`
	Invalid int     `json:"invalid"`
	RMSMM   float64 `json:"rms_mm"`
	P50MM   float64 `json:"p50_mm"`
	P90MM   float64 `json:"p90_mm"`
	MaxMM   float64 `json:"max_mm"`
	Below1  float64 `json:"below1"` // Anteil < 1 mm (ungueltige zaehlen als Fehlschlag)
}

func zoneStats(errs []float64, invalid int) zoneCalStats {
	st := zoneCalStats{N: len(errs) + invalid, Invalid: invalid}
	if len(errs) == 0 {
		return st
	}
	s := append([]float64(nil), errs...)
	sort.Float64s(s)
	var sum float64
	below := 0
	for _, e := range s {
		sum += e * e
		if e < 1 {
			below++
		}
	}
	q := func(f float64) float64 { return s[int(math.Min(float64(len(s)-1), math.Floor(f*float64(len(s)))))] }
	st.RMSMM = math.Sqrt(sum / float64(len(s)))
	st.P50MM, st.P90MM, st.MaxMM = q(0.5), q(0.9), s[len(s)-1]
	st.Below1 = float64(below) / float64(st.N)
	return st
}

type zoneCalZone struct {
	Zone    string       `json:"zone"`
	Shots   int          `json:"shots"`
	CheckMM *float64     `json:"check_mm"` // Kreuzpruefung: mittlerer Fehler je weggelassenem Schuss
	Fitted  string       `json:"fitted"`   // was angepasst wurde (Klartext)
	Set     *ZoneSet     `json:"set"`
	Before  zoneCalStats `json:"before"`
	After   zoneCalStats `json:"after"`
}

type zoneCalShot struct {
	ShotNo    int      `json:"shot_no"`
	Zone      string   `json:"zone"` // nach Foto-Lage
	TruthX    float64  `json:"truth_x_mm"`
	TruthY    float64  `json:"truth_y_mm"`
	BeforeErr *float64 `json:"before_err_mm"`
	AfterX    *float64 `json:"after_x_mm"`
	AfterY    *float64 `json:"after_y_mm"`
	AfterErr  *float64 `json:"after_err_mm"`
	AfterZone string   `json:"after_zone"` // Zone laut Neuberechnung
}

type zoneCalReport struct {
	Params   SimParams     `json:"params"`
	Zones    []zoneCalZone `json:"zones"`
	Before   zoneCalStats  `json:"before"`
	BaseOnly zoneCalStats  `json:"base_only"` // nur Basissatz (ohne Zonen)
	After    zoneCalStats  `json:"after"`
	Shots    []zoneCalShot `json:"shots"`
	// Pairs: endgueltige Loch-Schuss-Zuordnung (nur bei Zuordnung auf dem
	// Server, siehe sim_zone_assign.go)
	Pairs      []zoneCalPair  `json:"pairs,omitempty"`
	BaseReport PhotoCalReport `json:"base_report"`
}

type zoneCalInput struct {
	Raw    SimShotRaw
	TruthX float64
	TruthY float64
}

// zoneFullFitMin: ab dieser Schusszahl werden in einer Zone auch die
// Mikrofon-Offsets angepasst.
var zoneFullFitMin = 6

// zoneRidgeNs: Offset-Abweichung vom Basissatz, die so viel kostet wie 1 mm
// Lagefehler (0 = keine Bindung). 12000 ns war in der Leave-one-out-Pruefung
// (Serie 12.10.2026) am besten: 72 % < 1 mm gegenueber 62 % ohne Bindung.
var zoneRidgeNs = 12000.0

// zoneFitOptions: freie Werte einer Zone abhaengig von der Schusszahl.
func zoneFitOptions(n int) (PhotoCalOptions, string, bool) {
	switch {
	case n >= zoneFullFitMin:
		return PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true}, "Offsets, Nachkorrektur, Schallgeschwindigkeit", true
	case n >= 4:
		return PhotoCalOptions{Offset: true, SoundSpeed: true}, "Nachkorrektur, Schallgeschwindigkeit", true
	case n >= 3:
		return PhotoCalOptions{Offset: true}, "nur Nachkorrektur", true
	}
	return PhotoCalOptions{}, "zu wenige Schüsse – Basissatz", false
}

// ZoneCalibrate: siehe Dateikopf. zones liefert nur Grenzen/Ueberblendung.
func ZoneCalibrate(in []zoneCalInput, p SimParams, opt PhotoCalOptions, zones ZoneParams) (zoneCalReport, error) {
	if zones.HoleRadiusMM <= 0 {
		return zoneCalReport{}, errBadRequest("Lochradius der Vorsatzscheibe fehlt")
	}
	toCal := func(xs []zoneCalInput, q SimParams) []PhotoCalShot {
		var out []PhotoCalShot
		for _, s := range xs {
			out = append(out, PhotoCalShot{ShotNo: s.Raw.ShotNo, AirNs: s.Raw.airFor(q), PiezoNs: s.Raw.PiezoNs, TruthX: s.TruthX, TruthY: s.TruthY})
		}
		return out
	}

	// 1. Basissatz
	base := p
	base.Zones = nil
	baseRep, err := PhotoCalibrate(toCal(in, base), base, opt)
	if err != nil {
		return zoneCalReport{}, err
	}
	global := baseRep.Params
	global.Zones = nil

	// 2. Zonen
	zp := ZoneParams{Enabled: true, HoleRadiusMM: zones.HoleRadiusMM, BandHalfMM: zones.BandHalfMM, BlendMM: zones.BlendMM}
	byZone := map[string][]zoneCalInput{}
	for _, s := range in {
		z := zp.ZoneOf(s.TruthX, s.TruthY)
		byZone[z] = append(byZone[z], s)
	}
	rep := zoneCalReport{BaseReport: baseRep}
	for _, name := range zoneNames {
		zs := byZone[name]
		zr := zoneCalZone{Zone: name, Shots: len(zs)}
		zr.Set, zr.Fitted, zr.CheckMM = zoneChooseFit(name, zs, global)
		switch name {
		case "A":
			zp.A = zr.Set
		case "B":
			zp.B = zr.Set
		case "C":
			zp.C = zr.Set
		}
		rep.Zones = append(rep.Zones, zr)
	}
	final := global
	final.Zones = &zp
	rep.Params = final

	// 3. Bewertung
	type acc struct {
		errs []float64
		inv  int
	}
	eval := func(q SimParams, zoneOf func(zoneCalInput) string, shotOut func(i int, r SimResult, e *float64)) (map[string]*acc, *acc) {
		per := map[string]*acc{}
		all := &acc{}
		for i, s := range in {
			z := zoneOf(s)
			if per[z] == nil {
				per[z] = &acc{}
			}
			r := SolveSimShot(s.Raw, q)
			var e *float64
			if r.PosValid {
				v := math.Hypot(float64(r.XUm)/1000-s.TruthX, float64(r.YUm)/1000-s.TruthY)
				e = &v
				per[z].errs = append(per[z].errs, v)
				all.errs = append(all.errs, v)
			} else {
				per[z].inv++
				all.inv++
			}
			if shotOut != nil {
				shotOut(i, r, e)
			}
		}
		return per, all
	}
	zoneOf := func(s zoneCalInput) string { return zp.ZoneOf(s.TruthX, s.TruthY) }
	rep.Shots = make([]zoneCalShot, len(in))
	for i, s := range in {
		rep.Shots[i] = zoneCalShot{ShotNo: s.Raw.ShotNo, Zone: zoneOf(s), TruthX: s.TruthX, TruthY: s.TruthY}
	}
	perB, allB := eval(p, zoneOf, func(i int, r SimResult, e *float64) { rep.Shots[i].BeforeErr = e })
	_, allBase := eval(global, zoneOf, nil)
	perA, allA := eval(final, zoneOf, func(i int, r SimResult, e *float64) {
		rep.Shots[i].AfterErr = e
		rep.Shots[i].AfterZone = r.Zone
		if r.PosValid {
			x, y := float64(r.XUm)/1000, float64(r.YUm)/1000
			rep.Shots[i].AfterX, rep.Shots[i].AfterY = &x, &y
		}
	})
	rep.Before = zoneStats(allB.errs, allB.inv)
	rep.BaseOnly = zoneStats(allBase.errs, allBase.inv)
	rep.After = zoneStats(allA.errs, allA.inv)
	for k := range rep.Zones {
		z := rep.Zones[k].Zone
		if a := perB[z]; a != nil {
			rep.Zones[k].Before = zoneStats(a.errs, a.inv)
		}
		if a := perA[z]; a != nil {
			rep.Zones[k].After = zoneStats(a.errs, a.inv)
		}
	}
	return rep, nil
}

// photoCalibrateZones: POST /api/sessions/{id}/photo-calibrate-zones
//
// Mit holes_mm (alle markierten Loecher) ordnet der Server selbst zu (siehe
// sim_zone_assign.go): Start mit den Paaren aus dem Browser, Kandidaten sind
// die Schuesse aus shot_nos (leer = alle mit Rohdaten) ohne excluded.
func (a *APIServer) photoCalibrateZones(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Params   SimParams       `json:"params"`
		Optimize PhotoCalOptions `json:"optimize"`
		Zones    ZoneParams      `json:"zones"`
		Pairs    []struct {
			ShotNo int     `json:"shot_no"`
			XMM    float64 `json:"x_mm"`
			YMM    float64 `json:"y_mm"`
		} `json:"pairs"`
		Holes    [][2]float64 `json:"holes_mm"`
		ShotNos  []int        `json:"shot_nos"`
		Excluded []int        `json:"excluded"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	raw, err := a.store.SessionShotsRaw(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	byNo := make(map[int]SimShotRaw, len(raw))
	for _, s := range raw {
		byNo[s.ShotNo] = s
	}
	excluded := map[int]bool{}
	for _, n := range body.Excluded {
		excluded[n] = true
	}
	var in []zoneCalInput
	seen := map[int]bool{}
	for _, pr := range body.Pairs {
		s, ok := byNo[pr.ShotNo]
		if !ok || !s.HasRaw || seen[pr.ShotNo] || excluded[pr.ShotNo] {
			continue
		}
		seen[pr.ShotNo] = true
		in = append(in, zoneCalInput{Raw: s, TruthX: pr.XMM, TruthY: pr.YMM})
	}
	var pairs []zoneCalPair
	if len(body.Holes) > 0 {
		allowed := map[int]bool{}
		for _, n := range body.ShotNos {
			allowed[n] = true
		}
		var cands []SimShotRaw
		for _, s := range raw {
			if s.HasRaw && !excluded[s.ShotNo] && (len(allowed) == 0 || allowed[s.ShotNo]) {
				cands = append(cands, s)
			}
		}
		in, pairs = zoneAssign(cands, body.Holes, in, body.Params, body.Optimize)
	}
	rep, err := ZoneCalibrate(in, body.Params, body.Optimize, body.Zones)
	if err != nil {
		return nil, err
	}
	rep.Pairs = pairs
	return rep, nil
}
