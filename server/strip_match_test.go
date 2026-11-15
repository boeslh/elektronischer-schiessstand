package main

import (
	"math"
	"math/rand"
	"testing"
)

type stripScenario struct {
	n           int     // Schüsse
	spreadX     float64 // Streuung der Schüsse (mm)
	spreadY     float64
	feed        float64 // tatsächlicher Vorschub
	nominal     float64 // eingestellter Vorschub
	noise       float64 // Messfehler Foto (mm)
	sx, sv, q   float64
	visibleFrom int // erster sichtbarer Schuss (Index), -1 = alle
	visibleTo   int
	extraHoles  int
	gapAfter    int // nach diesem Index ein zusätzlicher Vorschub (-1 = keiner)
	seed        int64
}

// build erzeugt Schüsse und Löcher; liefert zusätzlich je Loch den wahren Schussindex (-1 = fremdes Loch).
func (sc stripScenario) build() (stripMatchInput, []int) {
	rng := rand.New(rand.NewSource(sc.seed))
	in := stripMatchInput{FeedMM: sc.nominal}
	var truth []int
	feeds := 0
	u0, v0 := 1.3, 40.0
	minV, maxV := math.Inf(1), math.Inf(-1)
	for i := 0; i < sc.n; i++ {
		x := rng.NormFloat64() * sc.spreadX
		y := rng.NormFloat64() * sc.spreadY
		s := stripShot{ShotNo: i + 1, Label: stripItoa(i + 1), Kind: "match", Status: "valid", X: x, Y: y, HasPos: true, Feed: i, FeedAfter: true}
		in.Shots = append(in.Shots, s)
		if i == sc.gapAfter+1 && sc.gapAfter >= 0 {
			feeds++
		}
		u := sc.sx*x + u0 + rng.NormFloat64()*sc.noise
		v := sc.sv*y + sc.q*sc.feed*float64(i+feeds) + v0 + rng.NormFloat64()*sc.noise
		if sc.visibleFrom < 0 || (i >= sc.visibleFrom && i <= sc.visibleTo) {
			in.Holes = append(in.Holes, stripHole{U: u, V: v})
			truth = append(truth, i)
			minV, maxV = math.Min(minV, v), math.Max(maxV, v)
		}
	}
	for k := 0; k < sc.extraHoles; k++ {
		// fremde Löcher (vorige Scheibe) hinter dem letzten sichtbaren Ende
		v := minV - sc.q*0 - float64(k+1)*sc.feed
		if sc.q < 0 {
			v = maxV + float64(k+1)*sc.feed
		}
		in.Holes = append(in.Holes, stripHole{U: u0 + rng.NormFloat64()*2, V: v})
		truth = append(truth, -1)
		minV, maxV = math.Min(minV, v), math.Max(maxV, v)
	}
	in.VMin, in.VMax = minV-8, maxV+8
	return in, truth
}

func checkStrip(t *testing.T, in stripMatchInput, truth []int, wantClear bool) *stripMatchResult {
	t.Helper()
	res, err := runStripMatch(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Clear != wantClear {
		t.Fatalf("clear=%v, erwartet %v (%s) score %.2f alt %.2f", res.Clear, wantClear, res.Message, res.Score, res.AltScore)
	}
	if !wantClear {
		return res
	}
	wrong := 0
	for j, ti := range truth {
		got := res.HoleShot[j]
		want := 0
		if ti >= 0 {
			want = in.Shots[ti].ShotNo
		}
		if got != want {
			wrong++
			t.Errorf("Loch %d: Schuss %d, erwartet %d", j, got, want)
		}
	}
	if wrong > 0 {
		t.Fatalf("%d falsche Zuordnungen", wrong)
	}
	return res
}

func TestStripMatch_GoodShooterAllVisible(t *testing.T) {
	sc := stripScenario{n: 30, spreadX: 1, spreadY: 1, feed: 15, nominal: 15, noise: 0.2, sx: -1, sv: 1, q: -1,
		visibleFrom: -1, gapAfter: -1, seed: 1}
	in, truth := sc.build()
	res := checkStrip(t, in, truth, true)
	if math.Abs(res.FeedFit-15) > 0.05 || res.RMS > 0.6 {
		t.Errorf("Vorschub %.3f, RMS %.3f", res.FeedFit, res.RMS)
	}
	if math.Abs(res.OffsetX-(-res.SX*1.3)) > 0.3 {
		t.Errorf("Versatz %.3f", res.OffsetX)
	}
}

func TestStripMatch_BadShooterPartialWithForeignHoles(t *testing.T) {
	sc := stripScenario{n: 40, spreadX: 10, spreadY: 12, feed: 15, nominal: 15, noise: 0.3, sx: 1, sv: -1, q: 1,
		visibleFrom: 12, visibleTo: 33, extraHoles: 3, gapAfter: -1, seed: 2}
	in, truth := sc.build()
	checkStrip(t, in, truth, true)
}

func TestStripMatch_FeedDeviation(t *testing.T) {
	sc := stripScenario{n: 25, spreadX: 4, spreadY: 5, feed: 15.45, nominal: 15, noise: 0.25, sx: 1, sv: 1, q: 1,
		visibleFrom: -1, gapAfter: -1, seed: 3}
	in, truth := sc.build()
	res := checkStrip(t, in, truth, true)
	if math.Abs(res.FeedFit-15.45) > 0.05 || math.Abs(res.FeedSteps.Mean-15.45) > 0.1 {
		t.Errorf("Vorschub fit %.3f, Schritte %.3f", res.FeedFit, res.FeedSteps.Mean)
	}
}

func TestStripMatch_ExtraFeed(t *testing.T) {
	sc := stripScenario{n: 24, spreadX: 3, spreadY: 4, feed: 15, nominal: 15, noise: 0.25, sx: 1, sv: 1, q: -1,
		visibleFrom: -1, gapAfter: 9, seed: 4}
	in, truth := sc.build()
	res := checkStrip(t, in, truth, true)
	if res.FeedJumps != 1 {
		t.Errorf("Vorschub-Sprünge %d, erwartet 1", res.FeedJumps)
	}
}

func TestStripMatch_AmbiguousNeedsAnchor(t *testing.T) {
	// sehr enges Trefferbild, Band auf dem Foto an beiden Enden abgeschnitten:
	// Verschiebung um einen Vorschub passt praktisch genauso gut
	sc := stripScenario{n: 40, spreadX: 0.05, spreadY: 0.05, feed: 15, nominal: 15, noise: 0.2, sx: 1, sv: 1, q: 1,
		visibleFrom: 10, visibleTo: 29, gapAfter: -1, seed: 5}
	in, truth := sc.build()
	in.VMin, in.VMax = in.Holes[0].V-2, in.Holes[len(in.Holes)-1].V+2
	checkStrip(t, in, truth, false)
	in.Anchors = []stripAnchor{{Hole: 4, ShotNo: in.Shots[truth[4]].ShotNo}}
	checkStrip(t, in, truth, true)
}

func TestStripMatch_ManySeeds(t *testing.T) {
	for seed := int64(10); seed < 40; seed++ {
		sc := stripScenario{n: 30, spreadX: 6, spreadY: 9, feed: 15, nominal: 15, noise: 0.3,
			sx: float64(1 - 2*(seed%2)), sv: float64(1 - 2*((seed/2)%2)), q: float64(1 - 2*((seed/4)%2)),
			visibleFrom: int(seed % 7), visibleTo: 22 + int(seed%7), gapAfter: -1, seed: seed}
		if sc.visibleFrom == 0 {
			sc.extraHoles = int(seed % 3) // fremde Löcher nur vor dem ersten Schuss der Scheibe
		}
		in, truth := sc.build()
		res, err := runStripMatch(in)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Clear {
			t.Errorf("seed %d: nicht eindeutig (%.2f/%.2f)", seed, res.Score, res.AltScore)
			continue
		}
		for j, ti := range truth {
			want := 0
			if ti >= 0 {
				want = in.Shots[ti].ShotNo
			}
			if res.HoleShot[j] != want {
				t.Errorf("seed %d: Loch %d -> %d, erwartet %d", seed, j, res.HoleShot[j], want)
			}
		}
	}
}

// Leervorschub beim Wechsel Probe -> Wertung (standpc/targetchange.go): der
// Stand meldet ihn per paper_feeds_before, er steckt damit bereits in Feed -
// die Zuordnung darf dafuer KEINEN abweichenden Vorschub melden.
func TestStripMatch_KnownTargetChange(t *testing.T) {
	sc := stripScenario{n: 24, spreadX: 3, spreadY: 4, feed: 15, nominal: 15, noise: 0.25, sx: 1, sv: 1, q: -1,
		visibleFrom: -1, gapAfter: 4, seed: 6}
	in, truth := sc.build()
	for i := range in.Shots {
		if i >= 5 {
			in.Shots[i].Feed++
			in.Shots[i].Kind = "match"
		} else {
			in.Shots[i].Kind = "sighting"
		}
	}
	in.Shots[5].TargetChange = true
	res := checkStrip(t, in, truth, true)
	if res.FeedJumps != 0 {
		t.Errorf("Vorschub-Sprünge %d, erwartet 0 (Leervorschub bekannt)", res.FeedJumps)
	}
}
