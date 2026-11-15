// ============================================================================
// strip_match.go – Bandkontrolle: Löcher auf dem Foto des Papierbands den
// gespeicherten Schüssen einer Scheibe zuordnen (siehe strip_check.go für
// Speicherung/HTTP und web/bandkontrolle.html für Bilderkennung und Anzeige).
//
// Das Band (Breite z.B. 90 mm) läuft senkrecht durch die Scheibe und wird nach
// jeder Auslösung um den Vorschub F weitertransportiert. Ein Schuss i an der
// Scheibenposition (x, y), vor dem f_i Vorschübe ausgeführt wurden, liegt auf
// dem Band daher bei
//
//	U = sx*x + u0                    (quer, mm ab Band-Mittellinie)
//	V = sv*y + q*F*(f_i + g_i) + v0  (längs, mm ab Bildanfang)
//
// sx, sv, q = ±1 beschreiben, wie das Band fotografiert wurde (gedreht,
// gespiegelt = Rückseite) und in welche Richtung es läuft; u0/v0 sind die
// unbekannte Lage des Bandes, F der tatsächliche Vorschub (wird angepasst,
// Startwert = eingestellter Vorschub) und g_i zusätzliche/fehlende
// Vorschübe gegenüber der Zählung (z.B. manueller Scheibenwechsel, Vorschub
// noch beschäftigt).
//
// Ablauf:
//  1. Für jede Orientierung und eine Reihe von Vorschub-Skalierungen stimmt
//     jedes Paar (Schuss, Loch) für eine Bandlage (u0, v0) ab (Hough-Prinzip).
//     Mit festgelegten Zuordnungen (Anker) kommen die Kandidaten nur aus
//     diesen Paaren.
//  2. Die besten Kandidaten werden verfeinert: Zuordnung (eins zu eins),
//     Sprünge im Vorschub per dynamischer Programmierung, Ausgleichsrechnung
//     für u0, v0, F - mehrfach im Wechsel.
//  3. Eindeutig ist die Zuordnung, wenn die beste Lösung klar besser ist als
//     die beste davon verschiedene. Sonst muss mindestens ein Loch von Hand
//     einem Schuss zugeordnet werden.
//
// Weil u0 und v0 frei sind, zeigt der Vergleich die Lage der Schüsse
// zueinander. Eine gemeinsame Verschiebung aller Schüsse quer zum Band wird
// getrennt als Versatz zur Band-Mittellinie ausgewiesen; längs zum Band
// gibt es ohne Markierung auf dem Band keinen absoluten Bezug.
// ============================================================================
package main

import (
	"errors"
	"math"
	"sort"
)

type stripHole struct {
	U float64 `json:"u"`
	V float64 `json:"v"`
}

type stripShot struct {
	ShotNo    int     `json:"shot_no"`
	Label     string  `json:"label"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	X         float64 `json:"x_mm"`
	Y         float64 `json:"y_mm"`
	HasPos    bool    `json:"has_pos"`
	Corrected bool    `json:"corrected"`
	// Feed: Anzahl der Vorschübe vor diesem Schuss (seit Beginn der Scheibe)
	Feed int `json:"feed"`
	// FeedAfter: dieser Schuss hat einen Vorschub ausgelöst
	FeedAfter bool `json:"feed_after"`
	// TargetChange: vor diesem Schuss gab es einen Leervorschub
	// (Scheibenwechsel bzw. Probe -> Wertung, bereits in Feed enthalten)
	TargetChange bool `json:"target_change,omitempty"`
}

type stripAnchor struct {
	Hole   int `json:"hole"`
	ShotNo int `json:"shot_no"`
}

type stripMatchInput struct {
	Holes   []stripHole
	Shots   []stripShot
	VMin    float64 // Bandausschnitt auf dem Foto (längs, mm)
	VMax    float64
	FeedMM  float64
	Anchors []stripAnchor
}

type stripShotResult struct {
	ShotNo     int      `json:"shot_no"`
	Label      string   `json:"label"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	Corrected  bool     `json:"corrected"`
	X          float64  `json:"x_mm"`
	Y          float64  `json:"y_mm"`
	Feed       int      `json:"feed"`
	ExtraFeeds int      `json:"extra_feeds"`
	Hole       int      `json:"hole"` // -1 = kein Loch zugeordnet
	Anchored   bool     `json:"anchored"`
	OnPhoto    bool     `json:"on_photo"`
	NearEdge   bool     `json:"near_edge"` // laut Zuordnung am Bildrand (Loch evtl. abgeschnitten)
	PredU      float64  `json:"pred_u"`
	PredV      float64  `json:"pred_v"`
	BandX      *float64 `json:"band_x_mm,omitempty"`
	BandY      *float64 `json:"band_y_mm,omitempty"`
	DX         *float64 `json:"dx_mm,omitempty"` // Ergebnis - Band
	DY         *float64 `json:"dy_mm,omitempty"`
	D          *float64 `json:"d_mm,omitempty"`
	FeedStep   *float64 `json:"feed_step_mm,omitempty"` // gemessener Vorschub seit dem vorigen zugeordneten Schuss
}

type stripStats struct {
	N    int     `json:"n"`
	Mean float64 `json:"mean"`
	SD   float64 `json:"sd"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
}

type stripMatchResult struct {
	Clear        bool              `json:"clear"`
	Message      string            `json:"message"`
	SX           float64           `json:"sx"`
	SV           float64           `json:"sv"`
	Q            float64           `json:"q"`
	U0           float64           `json:"u0"`
	V0           float64           `json:"v0"`
	FeedNominal  float64           `json:"feed_nominal_mm"`
	FeedFit      float64           `json:"feed_fit_mm"`
	OffsetX      float64           `json:"offset_x_mm"` // Ergebnis gegenüber Band-Mittellinie (alle Schüsse gemeinsam)
	Score        float64           `json:"score"`
	AltScore     float64           `json:"alt_score"`
	Matched      int               `json:"matched"`
	WithPos      int               `json:"with_pos"`
	OnPhoto      int               `json:"on_photo"`
	Missing      int               `json:"missing"`     // laut Zuordnung auf dem Foto, aber kein Loch
	ExtraHoles   int               `json:"extra_holes"` // Löcher ohne Schuss
	RMSX         float64           `json:"rms_x_mm"`
	RMSY         float64           `json:"rms_y_mm"`
	RMS          float64           `json:"rms_mm"`
	MaxD         float64           `json:"max_d_mm"`
	FeedSteps    stripStats        `json:"feed_steps"`
	Shots        []stripShotResult `json:"shots"`
	HoleShot     []int             `json:"hole_shot"` // je Loch die Schussnummer, 0 = keine
	FeedJumps    int               `json:"feed_jumps"`
	AnchorErrors []string          `json:"anchor_errors,omitempty"`
}

const (
	stripSigma     = 0.8 // mm, erwartete Messgenauigkeit Foto <-> Ergebnis
	stripMaxGap    = 6   // max. zusätzliche Vorschübe
	stripMinGap    = -3  // max. fehlende Vorschübe
	stripJumpCost  = 6.0 // Kosten je Vorschub-Sprung (in Einheiten d²/2σ²)
	stripMissCost  = 4.5
	stripOutMargin = 3.0 // mm: Vorhersage so weit außerhalb des Fotos zählt als "nicht auf dem Foto"
)

type stripHyp struct {
	sx, sv, q float64
	u0, v0, f float64
	g         []int // je Schuss zusätzliche Vorschübe
}

type stripMatcher struct {
	in      stripMatchInput
	holeIdx []int // Löcher nach V sortiert
	tol     float64
	anchor  map[int]int // shotIndex -> hole
}

func (m *stripMatcher) pred(h *stripHyp, i int) (float64, float64) {
	s := &m.in.Shots[i]
	g := 0
	if h.g != nil {
		g = h.g[i]
	}
	return h.sx*s.X + h.u0, h.sv*s.Y + h.q*h.f*float64(s.Feed+g) + h.v0
}

func (m *stripMatcher) onPhoto(v float64) bool {
	return v >= m.in.VMin-stripOutMargin && v <= m.in.VMax+stripOutMargin
}

// nearest: nächstes Loch zur Position (u, v) innerhalb tol, sonst -1.
func (m *stripMatcher) nearest(u, v float64) (int, float64) {
	holes := m.in.Holes
	lo := sort.Search(len(m.holeIdx), func(k int) bool { return holes[m.holeIdx[k]].V >= v-m.tol })
	best, bd := -1, m.tol
	for k := lo; k < len(m.holeIdx); k++ {
		hh := holes[m.holeIdx[k]]
		if hh.V > v+m.tol {
			break
		}
		if d := math.Hypot(hh.U-u, hh.V-v); d < bd {
			best, bd = m.holeIdx[k], d
		}
	}
	return best, bd
}

// shotCost: Kosten eines Schusses bei gegebenen Zusatz-Vorschüben (für DP und Bewertung).
func (m *stripMatcher) shotCost(h *stripHyp, i, g int) float64 {
	s := &m.in.Shots[i]
	if !s.HasPos {
		return 0
	}
	u := h.sx*s.X + h.u0
	v := h.sv*s.Y + h.q*h.f*float64(s.Feed+g) + h.v0
	if j, ok := m.anchor[i]; ok {
		hh := m.in.Holes[j]
		d := math.Hypot(hh.U-u, hh.V-v)
		return math.Min(d*d/(2*stripSigma*stripSigma), 50)
	}
	if !m.onPhoto(v) {
		return stripMissCost * 0.3 // außerhalb: weder Treffer noch Fehler, leicht ungünstiger als ein Treffer
	}
	_, d := m.nearest(u, v)
	return math.Min(d*d/(2*stripSigma*stripSigma), stripMissCost)
}

// assign: Zuordnung Schuss -> Loch (eins zu eins, nächste zuerst).
func (m *stripMatcher) assign(h *stripHyp) []int {
	n := len(m.in.Shots)
	res := make([]int, n)
	type cand struct {
		i, j int
		d    float64
	}
	var cs []cand
	used := map[int]bool{}
	for i := range res {
		res[i] = -1
		if j, ok := m.anchor[i]; ok {
			res[i] = j
			used[j] = true
		}
	}
	for i := range m.in.Shots {
		if res[i] >= 0 || !m.in.Shots[i].HasPos {
			continue
		}
		u, v := m.pred(h, i)
		// alle Löcher in Reichweite als Kandidaten (für die Konfliktauflösung)
		holes := m.in.Holes
		lo := sort.Search(len(m.holeIdx), func(k int) bool { return holes[m.holeIdx[k]].V >= v-m.tol })
		for k := lo; k < len(m.holeIdx); k++ {
			j := m.holeIdx[k]
			if holes[j].V > v+m.tol {
				break
			}
			if d := math.Hypot(holes[j].U-u, holes[j].V-v); d < m.tol {
				cs = append(cs, cand{i, j, d})
			}
		}
	}
	sort.Slice(cs, func(a, b int) bool { return cs[a].d < cs[b].d })
	for _, c := range cs {
		if res[c.i] >= 0 || used[c.j] {
			continue
		}
		res[c.i] = c.j
		used[c.j] = true
	}
	return res
}

// score: Güte einer Hypothese (höher = besser).
func (m *stripMatcher) score(h *stripHyp, asg []int) float64 {
	sc := 0.0
	for i, s := range m.in.Shots {
		if !s.HasPos {
			continue
		}
		u, v := m.pred(h, i)
		if asg[i] >= 0 {
			hh := m.in.Holes[asg[i]]
			d := math.Hypot(hh.U-u, hh.V-v)
			sc += math.Exp(-d * d / (2 * stripSigma * stripSigma))
		} else if m.onPhoto(v) && v > m.in.VMin+m.tol && v < m.in.VMax-m.tol {
			sc -= 0.5 // müsste auf dem Foto sein, kein Loch
		}
	}
	if h.g != nil {
		for i := 1; i < len(h.g); i++ {
			if h.g[i] != h.g[i-1] {
				sc -= 0.5
			}
		}
	}
	return sc
}

// gaps: Zusatz-Vorschübe je Schuss per DP (Sprünge kosten stripJumpCost).
func (m *stripMatcher) gaps(h *stripHyp) []int {
	n := len(m.in.Shots)
	ng := stripMaxGap - stripMinGap + 1
	cost := make([]float64, ng)
	back := make([][]int8, n)
	h0 := *h
	h0.g = nil
	for gi := range cost {
		g := gi + stripMinGap
		cost[gi] = stripJumpCost*math.Abs(float64(g)) + m.shotCost(&h0, 0, g)
	}
	next := make([]float64, ng)
	for i := 1; i < n; i++ {
		back[i] = make([]int8, ng)
		for gi := range next {
			best, bk := math.Inf(1), 0
			for pj := range cost {
				c := cost[pj] + stripJumpCost*math.Abs(float64(gi-pj))
				if c < best {
					best, bk = c, pj
				}
			}
			next[gi] = best + m.shotCost(&h0, i, gi+stripMinGap)
			back[i][gi] = int8(bk)
		}
		cost, next = next, cost
	}
	bi := 0
	for gi := range cost {
		if cost[gi] < cost[bi] || (cost[gi] == cost[bi] && stripAbsInt(gi+stripMinGap) < stripAbsInt(bi+stripMinGap)) {
			bi = gi
		}
	}
	g := make([]int, n)
	for i := n - 1; i >= 0; i-- {
		g[i] = bi + stripMinGap
		if i > 0 {
			bi = int(back[i][bi])
		}
	}
	return g
}

func stripAbsInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// fit: Ausgleichsrechnung u0, v0 (und F, wenn genug Vorschub-Spanne) aus der Zuordnung.
func (m *stripMatcher) fit(h *stripHyp, asg []int, fitFeed bool) {
	var su, n float64
	var ts, rs []float64
	for i, j := range asg {
		if j < 0 {
			continue
		}
		s := &m.in.Shots[i]
		hh := m.in.Holes[j]
		su += hh.U - h.sx*s.X
		g := 0
		if h.g != nil {
			g = h.g[i]
		}
		ts = append(ts, h.q*float64(s.Feed+g))
		rs = append(rs, hh.V-h.sv*s.Y)
		n++
	}
	if n == 0 {
		return
	}
	h.u0 = su / n
	var mt, mr float64
	for k := range ts {
		mt += ts[k]
		mr += rs[k]
	}
	mt /= n
	mr /= n
	var stt, str float64
	for k := range ts {
		stt += (ts[k] - mt) * (ts[k] - mt)
		str += (ts[k] - mt) * (rs[k] - mr)
	}
	if fitFeed && n >= 3 && stt/n >= 2 {
		f := str / stt
		nom := m.in.FeedMM
		if f > nom*0.85 && f < nom*1.15 {
			h.f = f
		}
	}
	h.v0 = mr - h.f*mt
}

// refine: Zuordnung, Vorschub-Sprünge und Ausgleich im Wechsel.
func (m *stripMatcher) refine(h stripHyp) (stripHyp, []int, float64) {
	h.g = nil
	asg := m.assign(&h)
	m.fit(&h, asg, false)
	for it := 0; it < 5; it++ {
		h.g = m.gaps(&h)
		asg = m.assign(&h)
		m.fit(&h, asg, it >= 1)
	}
	h.g = m.gaps(&h)
	asg = m.assign(&h)
	return h, asg, m.score(&h, asg)
}

type stripCand struct {
	h     stripHyp
	votes float64
}

func runStripMatch(in stripMatchInput) (*stripMatchResult, error) {
	if in.FeedMM <= 0 {
		return nil, errors.New("Vorschub fehlt")
	}
	if len(in.Holes) == 0 {
		return nil, errors.New("keine Löcher markiert")
	}
	m := &stripMatcher{in: in, anchor: map[int]int{}}
	m.tol = math.Min(0.4*in.FeedMM, 6)
	m.holeIdx = make([]int, len(in.Holes))
	for i := range m.holeIdx {
		m.holeIdx[i] = i
	}
	sort.Slice(m.holeIdx, func(a, b int) bool { return in.Holes[m.holeIdx[a]].V < in.Holes[m.holeIdx[b]].V })

	shotIdx := map[int]int{}
	withPos := 0
	for i, s := range in.Shots {
		shotIdx[s.ShotNo] = i
		if s.HasPos {
			withPos++
		}
	}
	if withPos == 0 {
		return nil, errors.New("keine Schüsse mit Trefferlage")
	}
	var anchorErr []string
	usedHole := map[int]bool{}
	for _, a := range in.Anchors {
		i, ok := shotIdx[a.ShotNo]
		if !ok || !in.Shots[i].HasPos || a.Hole < 0 || a.Hole >= len(in.Holes) || usedHole[a.Hole] {
			anchorErr = append(anchorErr, "Zuordnung zu Schuss "+stripItoa(a.ShotNo)+" ungültig - ignoriert")
			continue
		}
		m.anchor[i] = a.Hole
		usedHole[a.Hole] = true
	}

	// 1. Kandidaten sammeln
	var cands []stripCand
	scales := []float64{1}
	for k := 1; k <= 15; k++ {
		scales = append(scales, 1+0.004*float64(k), 1-0.004*float64(k))
	}
	for _, sx := range []float64{1, -1} {
		for _, sv := range []float64{1, -1} {
			for _, q := range []float64{1, -1} {
				for _, sc := range scales {
					f := in.FeedMM * sc
					if len(m.anchor) > 0 {
						for i, j := range m.anchor {
							s := in.Shots[i]
							hh := in.Holes[j]
							cands = append(cands, stripCand{h: stripHyp{sx: sx, sv: sv, q: q, f: f,
								u0: hh.U - sx*s.X, v0: hh.V - sv*s.Y - q*f*float64(s.Feed)}})
						}
						continue
					}
					cands = append(cands, m.vote(sx, sv, q, f, 3)...)
				}
			}
		}
	}
	// Grobbewertung, die besten verfeinern
	for k := range cands {
		h := cands[k].h
		cands[k].votes = m.score(&h, m.assign(&h))
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].votes > cands[b].votes })
	if len(cands) > 40 {
		cands = cands[:40]
	}
	type refined struct {
		h   stripHyp
		asg []int
		sc  float64
	}
	var rs []refined
	for _, c := range cands {
		h, asg, sc := m.refine(c.h)
		rs = append(rs, refined{h, asg, sc})
	}
	sort.Slice(rs, func(a, b int) bool { return rs[a].sc > rs[b].sc })
	best := rs[0]
	alt := -math.MaxFloat64
	for _, r := range rs[1:] {
		if stripDistinct(best.asg, r.asg, best.h, r.h) {
			alt = r.sc
			break
		}
	}

	res := m.result(best.h, best.asg, best.sc)
	res.AnchorErrors = anchorErr
	if alt > -math.MaxFloat64 {
		res.AltScore = alt
	}
	switch {
	case res.Matched == 0:
		res.Message = "Keine Zuordnung gefunden - bitte ein Loch von Hand einem Schuss zuordnen."
	case len(m.anchor) > 0:
		res.Clear = true
		res.Message = "Zuordnung ausgehend von der festgelegten Zuordnung."
	case alt > -math.MaxFloat64 && best.sc-alt < math.Max(2, 0.1*best.sc):
		res.Message = "Zuordnung nicht eindeutig (eine andere Zuordnung passt fast genauso gut) - bitte mindestens ein Loch von Hand einem Schuss zuordnen."
	case res.Matched < 3:
		res.Message = "Zu wenige Löcher zugeordnet - bitte ein Loch von Hand einem Schuss zuordnen."
	default:
		res.Clear = true
		res.Message = "Zuordnung eindeutig."
	}
	return res, nil
}

func stripItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// vote: Hough-Abstimmung über (u0, v0) für eine Orientierung/Vorschub,
// liefert die top besten Bandlagen.
func (m *stripMatcher) vote(sx, sv, q, f float64, top int) []stripCand {
	type key struct{ u, v int32 }
	bins := map[key]float64{}
	for _, s := range m.in.Shots {
		if !s.HasPos {
			continue
		}
		for _, hh := range m.in.Holes {
			u0 := hh.U - sx*s.X
			v0 := hh.V - sv*s.Y - q*f*float64(s.Feed)
			bins[key{int32(math.Round(u0)), int32(math.Round(v0))}]++
		}
	}
	type kv struct {
		k key
		n float64
	}
	var all []kv
	for k := range bins {
		n := 0.0
		for du := int32(-1); du <= 1; du++ {
			for dv := int32(-1); dv <= 1; dv++ {
				n += bins[key{k.u + du, k.v + dv}]
			}
		}
		all = append(all, kv{k, n})
	}
	sort.Slice(all, func(a, b int) bool { return all[a].n > all[b].n })
	var out []stripCand
	for _, e := range all {
		if len(out) >= top {
			break
		}
		near := false
		for _, o := range out {
			if math.Abs(o.h.u0-float64(e.k.u)) <= 2 && math.Abs(o.h.v0-float64(e.k.v)) <= 2 {
				near = true
			}
		}
		if near {
			continue
		}
		out = append(out, stripCand{h: stripHyp{sx: sx, sv: sv, q: q, f: f, u0: float64(e.k.u), v0: float64(e.k.v)}, votes: e.n})
	}
	return out
}

// stripDistinct: zwei Lösungen unterscheiden sich wesentlich (andere
// Orientierung oder andere Zuordnung bei mehr als einem Viertel der Schüsse).
func stripDistinct(a, b []int, ha, hb stripHyp) bool {
	diff, n := 0, 0
	for i := range a {
		if a[i] < 0 && b[i] < 0 {
			continue
		}
		n++
		if a[i] != b[i] {
			diff++
		}
	}
	if n == 0 {
		return false
	}
	if ha.sx != hb.sx || ha.sv != hb.sv || ha.q != hb.q {
		return diff > 0
	}
	return float64(diff) > 0.25*float64(n)
}

func (m *stripMatcher) result(h stripHyp, asg []int, sc float64) *stripMatchResult {
	in := m.in
	r := &stripMatchResult{SX: h.sx, SV: h.sv, Q: h.q, U0: h.u0, V0: h.v0,
		FeedNominal: in.FeedMM, FeedFit: h.f, Score: sc, HoleShot: make([]int, len(in.Holes))}
	var sxx, syy, sdx, n float64
	prev := -1
	var steps []float64
	for i, s := range in.Shots {
		u, v := m.pred(&h, i)
		g := 0
		if h.g != nil {
			g = h.g[i]
		}
		_, anchored := m.anchor[i]
		sr := stripShotResult{ShotNo: s.ShotNo, Label: s.Label, Kind: s.Kind, Status: s.Status, Corrected: s.Corrected,
			X: s.X, Y: s.Y, Feed: s.Feed, ExtraFeeds: g, Hole: asg[i], Anchored: anchored,
			OnPhoto: s.HasPos && m.onPhoto(v), PredU: u, PredV: v}
		if s.HasPos {
			r.WithPos++
			if sr.OnPhoto {
				r.OnPhoto++
			}
		}
		if j := asg[i]; j >= 0 {
			hh := in.Holes[j]
			bx := h.sx * (hh.U - h.u0)
			by := h.sv * (hh.V - h.q*h.f*float64(s.Feed+g) - h.v0)
			dx, dy := s.X-bx, s.Y-by
			d := math.Hypot(dx, dy)
			sr.BandX, sr.BandY, sr.DX, sr.DY, sr.D = &bx, &by, &dx, &dy, &d
			sr.OnPhoto = true
			r.HoleShot[j] = s.ShotNo
			r.Matched++
			sxx += dx * dx
			syy += dy * dy
			sdx += s.X - h.sx*hh.U
			n++
			r.MaxD = math.Max(r.MaxD, d)
			if prev >= 0 {
				p := in.Shots[prev]
				pg := 0
				if h.g != nil {
					pg = h.g[prev]
				}
				nf := float64(s.Feed + g - p.Feed - pg)
				if nf > 0 {
					ph := in.Holes[asg[prev]]
					st := h.q * ((hh.V - ph.V) - h.sv*(s.Y-p.Y)) / nf
					sr.FeedStep = &st
					steps = append(steps, st)
				}
			}
			prev = i
		} else if sr.OnPhoto && v > in.VMin+m.tol && v < in.VMax-m.tol {
			r.Missing++
		} else if sr.OnPhoto {
			sr.NearEdge = true
		}
		if i > 0 && h.g != nil && h.g[i] != h.g[i-1] {
			r.FeedJumps++
		}
		r.Shots = append(r.Shots, sr)
	}
	for _, s := range r.HoleShot {
		if s == 0 {
			r.ExtraHoles++
		}
	}
	if n > 0 {
		r.RMSX = math.Sqrt(sxx / n)
		r.RMSY = math.Sqrt(syy / n)
		r.RMS = math.Sqrt((sxx + syy) / n)
		r.OffsetX = sdx / n
	}
	r.FeedSteps = stripStatsOf(steps)
	return r
}

func stripStatsOf(v []float64) stripStats {
	s := stripStats{N: len(v)}
	if len(v) == 0 {
		return s
	}
	s.Min, s.Max = v[0], v[0]
	for _, x := range v {
		s.Mean += x
		s.Min = math.Min(s.Min, x)
		s.Max = math.Max(s.Max, x)
	}
	s.Mean /= float64(len(v))
	for _, x := range v {
		s.SD += (x - s.Mean) * (x - s.Mean)
	}
	if len(v) > 1 {
		s.SD = math.Sqrt(s.SD / float64(len(v)-1))
	} else {
		s.SD = 0
	}
	return s
}
