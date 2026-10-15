package main

import (
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHochrechnung(t *testing.T) {
	if v, ok := hochrechnung(180, 20, 40); !ok || v != 360 {
		t.Errorf("180 nach 20 von 40: %v", v)
	}
	if v, ok := hochrechnung(377, 40, 40); !ok || v != 377 {
		t.Errorf("fertig: %v", v)
	}
	if _, ok := hochrechnung(0, 0, 40); ok {
		t.Error("ohne Schuss keine Hochrechnung")
	}
	if c, r := rwkDynGrid(5); c != 3 || r != 2 {
		t.Errorf("5 Staende -> %dx%d", c, r)
	}
}

func TestRwkTeamHR(t *testing.T) {
	e := []rundeEntry{
		{LastName: "A", TeamID: "t", Role: "S", ShotCount: 20, TotalRings: 180},
		{LastName: "B", TeamID: "t", Role: "S", ShotCount: 40, TotalRings: 377},
		{LastName: "C", TeamID: "t", Role: "AK", ShotCount: 40, TotalRings: 390},
		{LastName: "D", TeamID: "u", Role: "S", ShotCount: 10, TotalRings: 95},
	}
	ms := map[string]int{"A\x00": 40, "B\x00": 40, "C\x00": 40, "D\x00": 40}
	if hr := rwkTeamHR(e, "t", false, ms, 5); math.Abs(hr-737) > 1e-9 {
		t.Errorf("Team-HR %v, erwartet 737 (AK zaehlt nicht)", hr)
	}
}

// TestRwkDynSample schreibt eine Beispielseite (RWK_DYN_SAMPLE=<Datei>) mit
// erfundenen Daten - zur Sichtpruefung ohne echte Sessions.
func TestRwkDynSample(t *testing.T) {
	out := os.Getenv("RWK_DYN_SAMPLE")
	if out == "" {
		t.Skip("RWK_DYN_SAMPLE nicht gesetzt")
	}
	geoLG := targetGeoJSON{}
	for v := 1; v <= 10; v++ {
		d := 0.5 + float64(10-v)*5
		geoLG.Rings = append(geoLG.Rings, ringGeoJSON{V: v, D: d, Filled: v >= 4})
	}
	geoLP := targetGeoJSON{}
	for v := 1; v <= 10; v++ {
		d := 11.5 + float64(10-v)*16
		geoLP.Rings = append(geoLP.Rings, ringGeoJSON{V: v, D: d, Filled: v >= 7})
	}
	rng := rand.New(rand.NewSource(3))
	mk := func(n int, spread float64) []standShot {
		var s []standShot
		for i := 1; i <= n; i++ {
			x, y := rng.NormFloat64()*spread, rng.NormFloat64()*spread
			r := int(10 - math.Hypot(x, y)/2.5)
			d := 10.9 - math.Hypot(x, y)/2.5
			s = append(s, standShot{No: i, XMM: &x, YMM: &y, Ring: &r, Decimal: &d})
		}
		return s
	}
	tiles := []struct {
		t standTile
		x rwkTileExtra
	}{
		{standTile{LaneNo: 1, ShooterName: "Nico Broksch", Discipline: "LG-40", Geo: geoLG, TotalRings: 180, ShotCount: 20, Shots: mk(10, 3),
			Series: []seriesSum{{1, 92, 0}, {2, 88, 0}}}, rwkTileExtra{MatchShots: 40, DeadlineMs: time.Now().Add(27 * time.Minute).UnixMilli(), Label: "Vereinsabend • LG", HeaderColor: rwkColorNeutral}},
		{standTile{LaneNo: 4, ShooterName: "Manuela Surak", Discipline: "LG-40", Geo: geoLG, TotalRings: 377, ShotCount: 40, Shots: mk(10, 1.6),
			Series: []seriesSum{{1, 96, 0}, {2, 94, 0}, {3, 91, 0}, {4, 96, 0}}}, rwkTileExtra{MatchShots: 40, Label: "RWK: Biberbach I - Mintraching I • LG", HeaderColor: rwkDefaultTeamColors[1]}},
		{standTile{LaneNo: 10, ShooterName: "Christian Mayr", Discipline: "LG-40", Geo: geoLG, TotalRings: 240, ShotCount: 26, Shots: mk(6, 2.5),
			Series: []seriesSum{{1, 91, 0}, {2, 92, 0}, {3, 57, 0}}}, rwkTileExtra{MatchShots: 40, DeadlineMs: time.Now().Add(4 * time.Minute).UnixMilli(), Label: "Vereinsabend • LG", HeaderColor: rwkColorNeutral}},
		{standTile{LaneNo: 11, ShooterName: "Günter Fleischmann", Discipline: "LP-40", Geo: geoLP, TotalRings: 344, ShotCount: 40, Shots: mk(10, 14),
			Series: []seriesSum{{1, 86, 0}, {2, 84, 0}, {3, 88, 0}, {4, 86, 0}}}, rwkTileExtra{MatchShots: 40, Label: "RWK: Biberbach I - Mintraching I • LP", HeaderColor: rwkDefaultTeamColors[0]}},
	}
	for i := range tiles {
		if n := len(tiles[i].t.Shots); n > 0 {
			l := tiles[i].t.Shots[n-1]
			tiles[i].t.LastShot = &l
		}
	}
	var lanes strings.Builder
	lanes.WriteString(`<div class="lane-page" data-page="0" style="grid-template-columns:repeat(2,1fr);grid-template-rows:repeat(2,1fr)">`)
	for _, tt := range tiles {
		lanes.WriteString(renderRwkTile(tt.t, tt.x))
	}
	lanes.WriteString(`</div>`)
	side := renderRwkCard(0, "RWK LP: SV Tannengrün Biberbach I – Isarschützen Mintraching I",
		[2]rwkSideTeam{{Name: "SV Tannengrün Biberbach I", Club: "SV Tannengrün Biberbach", Total: 1359, HR: 1359, Color: rwkDefaultTeamColors[0], Started: true,
			Rows: []rwkShooterRow{{"Philipp Göttler", "LP", "S", false, 40, 372, []float64{95, 92, 92, 93}}, {"Andreas Junge", "LP", "S", false, 40, 354, []float64{87, 92, 89, 86}},
				{"Günter Fleischmann", "LP", "S", false, 40, 344, []float64{86, 84, 88, 86}}, {"Alexander Wildenauer", "LP", "S", false, 40, 289, []float64{74, 78, 74, 63}}}},
			{Name: "Isarschützen Mintraching I", Club: "Isar-Schützen Mintraching e.V.", Total: 1310, HR: 1384, Color: rwkDefaultTeamColors[1], Started: true,
				Rows: []rwkShooterRow{{"Hubert Bösl", "LP", "S", false, 40, 373, []float64{96, 92, 92, 93}}, {"Phineas Leander", "LP", "S", false, 40, 356, []float64{86, 91, 92, 87}},
					{"Christian Goetz", "LP", "S", false, 30, 250, []float64{85, 79, 86}}, {"Christian Kaessner", "LP", "AK", false, 0, 0, nil}}}}, false) +
		renderRwkCard(1, "RWK LG: Biberbach II – Biberbach III",
			[2]rwkSideTeam{{Name: "SV Tannengrün Biberbach II", Total: 532, HR: 1450, Color: rwkDefaultTeamColors[2], Started: true},
				{Name: "SV Tannengrün Biberbach III", Total: 488, HR: 1402, Color: rwkDefaultTeamColors[3], Started: true}}, false) +
		`<div class="rwk-dots"><span data-idx="0"></span><span data-idx="1"></span></div>`
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := rwkDynParams{}
	p.defaults()
	p.RefreshSeconds = 3600
	writeRwkDynPage(f, anzeigeSlot{Name: "RWK-Dynamisch"}, p, lanes.String(), side)
}

func TestPalette(t *testing.T) {
	p := rwkDynParams{TeamColors: []string{"#112233", "kaputt", "", "#abcdef"}}
	pal := p.palette()
	if len(pal) != 10 || pal[0] != "#112233" || pal[1] != rwkDefaultTeamColors[1] || pal[3] != "#abcdef" {
		t.Fatalf("Palette %v", pal)
	}
	c := rwkColors{palette: pal}
	if h, g := c.pair(5); h != pal[0] || g != pal[1] {
		t.Errorf("6. Paarung soll wieder bei Farbe 1 beginnen: %s %s", h, g)
	}
}

func TestComputeRundePaarung(t *testing.T) {
	ms := map[string]int{}
	mk := func(name, team string, pair, shots, rings int) rundeEntry {
		ms[name+"\x00"] = 40
		return rundeEntry{LastName: name, TeamID: team, Role: "S", PairNo: pair, ShotCount: shots, TotalRings: rings}
	}
	es := []rundeEntry{
		mk("a", "H", 1, 20, 190), mk("b", "G", 1, 40, 375), // HR 380 > 375 -> Heim
		mk("c", "H", 2, 40, 360), mk("d", "G", 2, 40, 370),
		mk("e", "H", 3, 40, 350), mk("f", "G", 3, 40, 349),
		mk("g", "H", 4, 40, 340), mk("h", "G", 4, 40, 341),
	}
	r := computeRundePaarung(es, "H", "G", false, ms)
	// 2:2 -> Summe Heim 380+360+350+340=1430, Gast 375+370+349+341=1435 -> Gast
	if r.HeimPts != 2 || r.GastPts != 3 || r.TotalPoint != 1 {
		t.Fatalf("%+v", r)
	}
	if r.Pairs[0].dotFor(0) != "lead" || r.Pairs[0].dotFor(1) != "trail" {
		t.Fatal("dots")
	}
}

func TestDecideRundeStechen(t *testing.T) {
	sh := func(no, ring int, dec float64) rundeStechen {
		return rundeStechen{No: no, Shot: true, Ring: ring, Decimal: dec}
	}
	h := []rundeStechen{sh(1, 10, 10.1), sh(2, 9, 9.9), sh(3, 10, 10.0), sh(4, 10, 10.6)}
	g := []rundeStechen{sh(1, 10, 10.8), sh(2, 9, 9.2), sh(3, 10, 10.9), sh(4, 10, 10.2)}
	if w := decideRundeStechen(h, g, false); w != 0 {
		t.Fatalf("ab 4 Zehntel: %d", w)
	}
	if w := decideRundeStechen(h[:1], g[:1], true); w != 1 {
		t.Fatalf("Zehntelwertung: %d", w)
	}
	if w := decideRundeStechen(h[:2], g[:2], false); w != -1 {
		t.Fatalf("offen: %d", w)
	}
}

func TestTeamBestOf(t *testing.T) {
	mk := func(r int) rundeEntry { return rundeEntry{TeamID: "t", Role: "S", ShotCount: 40, TotalRings: r} }
	es := []rundeEntry{mk(380), mk(370), mk(360), mk(350), mk(340), mk(330)}
	ms := map[string]int{"\x00": 40}
	for _, c := range []struct {
		bestOf int
		want   float64
		shown  int
	}{{4, 1460, 5}, {5, 1800, 5}, {0, 2130, 6}, {6, 2130, 6}} {
		list, total := teamDisplayList(es, "t", false, c.bestOf)
		if total != c.want || len(list) != c.shown {
			t.Errorf("bestOf %d: total %v (soll %v), angezeigt %d (soll %d)", c.bestOf, total, c.want, len(list), c.shown)
		}
		if hr := rwkTeamHR(es, "t", false, ms, c.bestOf); hr != c.want {
			t.Errorf("bestOf %d: HR %v (soll %v)", c.bestOf, hr, c.want)
		}
	}
}
