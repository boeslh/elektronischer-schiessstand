//go:build study

// ============================================================================
// calib_study_test.go – Untersuchung von Kalibrierverfahren gegen die
// Fotowahrheit (Referenzserien mit gespeichertem Foto, siehe study/).
//
// Nicht Teil der normalen Tests (Build-Tag "study"). Aufruf z.B.:
//
//	SCHIESSSTAND_DSN=... go test -tags study -run Study -v -timeout 15h .
//
// Daten: Schuesse der Referenz-Sessions (Rohdaten aus der DB) und die
// wahren Trefferlagen aus study/data/truth_<session>.json (erzeugt von
// study/truth.js aus den Foto-Markierungen).
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

var studySessions = []string{
	"acc1e961-f169-483e-964b-56382323e8a9",
	"6d11a607-6fa2-442c-9130-784f767f4bac",
}

type refShot struct {
	Session string
	ShotNo  int
	Sh      SimShotRaw
	TX, TY  float64 // Fotowahrheit, mm
}

func studyDSN(t *testing.T) string {
	if d := os.Getenv("SCHIESSSTAND_DSN"); d != "" {
		return d
	}
	t.Skip("SCHIESSSTAND_DSN nicht gesetzt")
	return ""
}

// loadRefShots laedt alle Referenzschuesse; truthVariant waehlt die
// Fotowahrheit (Dateisuffix, "" = Standard).
func loadRefShots(t *testing.T, truthVariant string) []refShot {
	st, err := NewStore(context.Background(), studyDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	var out []refShot
	for _, sid := range studySessions {
		raw, err := st.SessionShotsRaw(context.Background(), sid)
		if err != nil {
			t.Fatal(err)
		}
		byNo := map[int]SimShotRaw{}
		for _, s := range raw {
			byNo[s.ShotNo] = s
		}
		b, err := os.ReadFile(filepath.Join("study", "data", "truth_"+sid+truthVariant+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var tr struct {
			Pairs []struct {
				ShotNo    int     `json:"shot_no"`
				X         float64 `json:"x_mm"`
				Y         float64 `json:"y_mm"`
				Ambiguous bool    `json:"ambiguous"`
			} `json:"pairs"`
		}
		if err := json.Unmarshal(b, &tr); err != nil {
			t.Fatal(err)
		}
		for _, p := range tr.Pairs {
			s, ok := byNo[p.ShotNo]
			if !ok || !s.HasRaw || p.Ambiguous {
				continue
			}
			out = append(out, refShot{Session: sid, ShotNo: p.ShotNo, Sh: s, TX: p.X, TY: p.Y})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Kennzahlen
// ---------------------------------------------------------------------------

type errStats struct {
	N, Invalid int
	Below1     float64 // Anteil < 1 mm
	P50, P90   float64
	RMS, Max   float64
}

func (e errStats) String() string {
	return fmt.Sprintf("n=%2d ung=%d  <1mm %5.1f%%  P50 %5.2f  P90 %5.2f  RMS %5.2f  max %5.2f",
		e.N, e.Invalid, 100*e.Below1, e.P50, e.P90, e.RMS, e.Max)
}

func statsOf(errs []float64, invalid int) errStats {
	s := errStats{N: len(errs), Invalid: invalid}
	if len(errs) == 0 {
		return s
	}
	e := append([]float64(nil), errs...)
	sort.Float64s(e)
	var s2 float64
	below := 0
	for _, v := range e {
		s2 += v * v
		if v < 1 {
			below++
		}
	}
	q := func(p float64) float64 { return e[int(math.Min(float64(len(e)-1), math.Ceil(p*float64(len(e)))-1))] }
	s.Below1 = float64(below) / float64(len(e)+invalid)
	s.P50, s.P90 = q(0.5), q(0.9)
	s.RMS = math.Sqrt(s2 / float64(len(e)))
	s.Max = e[len(e)-1]
	return s
}

// locator: Verfahren, das aus einem Schuss eine Position bestimmt.
type locator func(r refShot) (x, y float64, ok bool)

func evalLocator(shots []refShot, loc locator) (errStats, []float64) {
	var errs []float64
	invalid := 0
	for _, r := range shots {
		x, y, ok := loc(r)
		if !ok {
			invalid++
			continue
		}
		errs = append(errs, math.Hypot(x-r.TX, y-r.TY))
	}
	return statsOf(errs, invalid), errs
}

func simLocator(p SimParams) locator {
	return func(r refShot) (float64, float64, bool) {
		res := SolveSimShot(r.Sh, p)
		return float64(res.XUm) / 1000, float64(res.YUm) / 1000, res.PosValid
	}
}

func split(shots []refShot, sid string) (in, out []refShot) {
	for _, r := range shots {
		if r.Session == sid {
			in = append(in, r)
		} else {
			out = append(out, r)
		}
	}
	return
}

func loadParams(t *testing.T, file string) SimParams {
	b, err := os.ReadFile(filepath.Join("study", "data", file))
	if err != nil {
		t.Fatal(err)
	}
	var p SimParams
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func studyLog(t *testing.T, format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	t.Log(line)
	f, err := os.OpenFile(filepath.Join("study", "results.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintln(f, line)
		f.Close()
	}
}

// ---------------------------------------------------------------------------
// 1. Bestandsaufnahme + Restanalyse je Mikrofon
// ---------------------------------------------------------------------------

// TestStudyBaseline: Geraet, Nutzer-Kalibrierung, und die Laufzeit-Reste je
// Mikrofon gegen die Fotowahrheit (nominale Geometrie) - als CSV fuer die
// Analyse: Abstand, Pulsbreite, Rest.
func TestStudyBaseline(t *testing.T) {
	shots := loadRefShots(t, "")
	studyLog(t, "== Baseline (%d Referenzschuesse)", len(shots))
	dev, _ := evalLocator(shots, func(r refShot) (float64, float64, bool) { return r.Sh.XMM, r.Sh.YMM, true })
	studyLog(t, "Geraet (beim Schuss)              %s", dev)
	user := loadParams(t, "params_hwcapture.json")
	us, _ := evalLocator(shots, simLocator(user))
	studyLog(t, "Nutzer-Kalibrierung HWCapture     %s", us)

	// Restanalyse mit den Nutzerparametern
	var sb strings.Builder
	sb.WriteString("session,shot,mic,dist_mm,width_ns,resid_ns,tx,ty\n")
	for _, r := range shots {
		air := r.Sh.airFor(SimParams{TdoaUs: 750})
		var w [6]*int64
		if r.Sh.Raw.usable() && r.Sh.Raw.hasEdges() {
			_, w = r.Sh.Raw.airFromRaw(SimParams{TdoaUs: 750})
		}
		micX, micY, so, snd, _ := solveParams(user)
		px, py := r.TX-float64(user.OffsetXUm)/1000, r.TY-float64(user.OffsetYUm)/1000
		var seen [6]bool
		var tNs [6]int64
		for i := 0; i < 6; i++ {
			if len(air[i]) > 0 {
				seen[i] = true
				tNs[i] = air[i][0] - user.MicOffsetNs[i]
			}
		}
		res := micTimeResiduals(tNs, seen, px, py, micX, micY, so, snd, 2.25)
		for i := 0; i < 6; i++ {
			if res[i] == nil {
				continue
			}
			d := math.Hypot(px-float64(micX[i]), py-float64(micY[i]))
			ws := ""
			if w[i] != nil {
				ws = fmt.Sprint(*w[i])
			}
			fmt.Fprintf(&sb, "%s,%d,%d,%.2f,%s,%.0f,%.2f,%.2f\n", r.Session[:8], r.ShotNo, i, d, ws, *res[i], r.TX, r.TY)
		}
	}
	os.WriteFile(filepath.Join("study", "residuals.csv"), []byte(sb.String()), 0o644)
}

// linFit: y = a + b*x (kleinste Quadrate), Rest-Streuung.
func linFit(x, y []float64) (a, b, resStd, r float64) {
	n := float64(len(x))
	var sx, sy, sxx, sxy, syy float64
	for i := range x {
		sx += x[i]
		sy += y[i]
		sxx += x[i] * x[i]
		sxy += x[i] * y[i]
		syy += y[i] * y[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return sy / n, 0, 0, 0
	}
	b = (n*sxy - sx*sy) / den
	a = (sy - b*sx) / n
	var s2 float64
	for i := range x {
		e := y[i] - a - b*x[i]
		s2 += e * e
	}
	resStd = math.Sqrt(s2 / n)
	r = (n*sxy - sx*sy) / math.Sqrt(den*(n*syy-sy*sy))
	return
}

// TestStudyResidualAnalysis wertet study/residuals.csv aus.
func TestStudyResidualAnalysis(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("study", "residuals.csv"))
	if err != nil {
		t.Skip("erst TestStudyBaseline")
	}
	type row struct {
		mic     int
		d, w, r float64
		hasW    bool
	}
	var rows []row
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n")[1:] {
		f := strings.Split(l, ",")
		var rw row
		fmt.Sscan(f[2], &rw.mic)
		fmt.Sscan(f[3], &rw.d)
		if f[4] != "" {
			fmt.Sscan(f[4], &rw.w)
			rw.hasW = true
		}
		fmt.Sscan(f[5], &rw.r)
		rows = append(rows, rw)
	}
	studyLog(t, "== Laufzeit-Reste je Mikrofon (Nutzer-Kalibrierung, Lochrand-Modell, ns)")
	var all, allAfterOfs []float64
	for m := 0; m < 6; m++ {
		var d, w, r, wr []float64
		for _, x := range rows {
			if x.mic != m {
				continue
			}
			d = append(d, x.d)
			r = append(r, x.r)
			if x.hasW {
				w = append(w, x.w)
				wr = append(wr, x.r)
			}
		}
		a, bd, sd, rd := linFit(d, r)
		_, bw, sw, rw := linFit(w, wr)
		var mean, s2 float64
		for _, v := range r {
			mean += v
		}
		mean /= float64(len(r))
		for _, v := range r {
			s2 += (v - mean) * (v - mean)
			allAfterOfs = append(allAfterOfs, v-mean)
		}
		all = append(all, r...)
		studyLog(t, "Mic %d n=%2d  mittel %6.0f  Streuung %5.0f | vs Abstand: %6.1f ns/mm (r=%5.2f, Rest %4.0f) a=%6.0f | vs Pulsbreite: %6.3f ns/ns (r=%5.2f, Rest %4.0f)",
			m, len(r), mean, math.Sqrt(s2/float64(len(r))), bd, rd, sd, a, bw, rw, sw)
	}
	rms := func(v []float64) float64 {
		s := 0.0
		for _, x := range v {
			s += x * x
		}
		return math.Sqrt(s / float64(len(v)))
	}
	studyLog(t, "gesamt RMS %.0f ns, nach Abzug Mittel je Mic %.0f ns (1 mm Laufweg = %.0f ns)", rms(all), rms(allAfterOfs), 1/0.000343)
}

// ---------------------------------------------------------------------------
// 2. Physikalisches Modell, direkt an die Fotowahrheit angepasst
// ---------------------------------------------------------------------------

// shotObs: die fuer das Modell aufbereiteten Messwerte eines Schusses.
type shotObs struct {
	t      [6]float64 // erste Flanke je Mic (ns, roh, ohne Offset)
	w      [6]float64 // Pulsbreite (ns), 0 = unbekannt
	seen   [6]bool
	tx, ty float64 // Wahrheit
	ref    refShot
	z      int // Zone fuer Zonen-Modelle: 0 = keine, 1..3 = A..C
}

func obsOf(r refShot, edge func(r refShot) ([6][]int64, [6]*int64)) shotObs {
	o := shotObs{tx: r.TX, ty: r.TY, ref: r}
	air, w := edge(r)
	for i := 0; i < 6; i++ {
		if len(air[i]) > 0 {
			o.seen[i] = true
			o.t[i] = float64(air[i][0])
			if w[i] != nil {
				o.w[i] = float64(*w[i])
			}
		}
	}
	return o
}

// firstEdge: Firmware-Auswahl (erste Flanke im TDOA-Fenster) aus den Rohdaten.
func firstEdge(r refShot) ([6][]int64, [6]*int64) {
	if r.Sh.Raw.usable() && r.Sh.Raw.hasEdges() {
		return r.Sh.Raw.airFromRaw(SimParams{TdoaUs: 750})
	}
	return r.Sh.AirNs, r.Sh.AirWNs
}

// physModel: Mikrofonpositionen (x,y,z in mm), Offsets (ns), Schall (mm/ns),
// optional Pulsbreiten-Term je Mic (ns je ns Breite, bezogen auf wRef) und
// Lochrand-Radius.
type physModel struct {
	MX, MY, MZ [6]float64
	Ofs        [6]float64
	C          float64 // mm/ns
	WK         [6]float64
	WRef       float64
	PelletR    float64
	UseW       bool
	DK         [6]float64    // Laufzeit-Zusatz je Mic abhaengig vom Abstand (Pegel)
	DMode      int           // 0 = aus, 1 = linear (d-60 mm), 2 = ln(d/60)
	ZC         [3]float64    // Schall je Zone A..C (mm/ns, 0 = C)
	ZOfs       [3][6]float64 // Zusatz-Offset je Zone und Mic (ns)
}

func nominalModel(p SimParams) physModel {
	micX, micY, so, snd, _ := solveParams(p)
	var m physModel
	for i := 0; i < 6; i++ {
		m.MX[i], m.MY[i], m.MZ[i] = float64(micX[i]), float64(micY[i]), float64(so)
		m.Ofs[i] = float64(p.MicOffsetNs[i])
	}
	m.C = float64(snd)
	m.WRef = 50000
	m.PelletR = 2.25
	return m
}

// arrival: modellierte Ankunftszeit (ohne t0) an Mic i fuer Position x,y.
func (m *physModel) arrival(i int, x, y, w float64) float64 {
	return m.arrivalZ(i, x, y, w, 0)
}

// arrivalZ: wie arrival, mit Zonen-Schall/-Offset (z = 1..3 fuer A..C).
func (m *physModel) arrivalZ(i int, x, y, w float64, z int) float64 {
	dxy := math.Hypot(x-m.MX[i], y-m.MY[i]) - m.PelletR
	if dxy < 0 {
		dxy = 0
	}
	d := math.Sqrt(dxy*dxy + m.MZ[i]*m.MZ[i])
	c, ofs := m.C, m.Ofs[i]
	if z > 0 {
		if m.ZC[z-1] > 0 {
			c = m.ZC[z-1]
		}
		ofs += m.ZOfs[z-1][i]
	}
	t := d/c + ofs
	switch m.DMode {
	case 1:
		t += m.DK[i] * (d - 60)
	case 2:
		t += m.DK[i] * math.Log(d/60)
	}
	if m.UseW && w > 0 {
		t -= m.WK[i] * (w - m.WRef)
	}
	return t
}

// shotResiduals: Reste (gemessen - Modell), t0 als Mittel eliminiert.
func (m *physModel) shotResiduals(o shotObs, x, y float64, out []float64) []float64 {
	var r [6]float64
	var sum float64
	n := 0
	for i := 0; i < 6; i++ {
		if o.seen[i] {
			r[i] = o.t[i] - m.arrivalZ(i, x, y, o.w[i], o.z)
			sum += r[i]
			n++
		}
	}
	for i := 0; i < 6; i++ {
		if o.seen[i] {
			out = append(out, r[i]-sum/float64(n))
		}
	}
	return out
}

// locate: Position per Gauss-Newton (x, y; t0 eliminiert) mit Huber-
// Gewichten gegen einzelne Ausreisser-Mics. Start: x0,y0.
func (m *physModel) locate(o shotObs, x0, y0 float64, huberNs float64) (float64, float64, bool) {
	x, y := x0, y0
	for it := 0; it < 30; it++ {
		base := m.shotResiduals(o, x, y, nil)
		if len(base) < 3 {
			return 0, 0, false
		}
		const h = 0.01
		rx := m.shotResiduals(o, x+h, y, nil)
		ry := m.shotResiduals(o, x, y+h, nil)
		var a11, a12, a22, b1, b2 float64
		for k := range base {
			jx, jy := (rx[k]-base[k])/h, (ry[k]-base[k])/h
			wt := 1.0
			if huberNs > 0 && math.Abs(base[k]) > huberNs {
				wt = huberNs / math.Abs(base[k])
			}
			a11 += wt * jx * jx
			a12 += wt * jx * jy
			a22 += wt * jy * jy
			b1 += wt * jx * base[k]
			b2 += wt * jy * base[k]
		}
		det := a11*a22 - a12*a12
		if math.Abs(det) < 1e-12 {
			return 0, 0, false
		}
		dx := -(a22*b1 - a12*b2) / det
		dy := -(a11*b2 - a12*b1) / det
		x, y = x+dx, y+dy
		if math.Hypot(dx, dy) < 1e-4 {
			break
		}
	}
	return x, y, !math.IsNaN(x) && math.Abs(x) < 200 && math.Abs(y) < 200
}

// modelParam: eine frei anpassbare Groesse des Modells.
type modelParam struct {
	name string
	ptr  func(m *physModel) *float64
	step float64
}

func paramSet(kind string, useW bool) []modelParam {
	var ps []modelParam
	for i := 1; i < 6; i++ { // Ofs0 = Referenz
		i := i
		ps = append(ps, modelParam{fmt.Sprintf("OFS%d", i), func(m *physModel) *float64 { return &m.Ofs[i] }, 200})
	}
	ps = append(ps, modelParam{"C", func(m *physModel) *float64 { return &m.C }, 0.000002})
	switch strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(kind, "+pr"), "+dkg"), "+dk") {
	case "sym": // symmetrische Anordnung: MICHALFX, Halbabstand Y, Standoff
		ps = append(ps, modelParam{"MICHALFX", nil, 0.5}, modelParam{"MICHALFY", nil, 0.5}, modelParam{"STANDOFF", nil, 0.5})
	case "free": // jedes Mic frei in x/y, gemeinsamer Standoff
		for i := 0; i < 6; i++ {
			i := i
			ps = append(ps, modelParam{fmt.Sprintf("MX%d", i), func(m *physModel) *float64 { return &m.MX[i] }, 0.5},
				modelParam{fmt.Sprintf("MY%d", i), func(m *physModel) *float64 { return &m.MY[i] }, 0.5})
		}
		ps = append(ps, modelParam{"STANDOFF", nil, 0.5})
	}
	if strings.HasSuffix(kind, "+dk") || strings.HasSuffix(kind, "+dkg") {
		if strings.HasSuffix(kind, "+dkg") {
			ps = append(ps, modelParam{"DKG", nil, 10})
		} else {
			for i := 0; i < 6; i++ {
				i := i
				ps = append(ps, modelParam{fmt.Sprintf("DK%d", i), func(m *physModel) *float64 { return &m.DK[i] }, 10})
			}
		}
	}
	if strings.Contains(kind, "+pr") {
		ps = append(ps, modelParam{"PR", func(m *physModel) *float64 { return &m.PelletR }, 0.1})
	}
	if useW {
		for i := 0; i < 6; i++ {
			i := i
			ps = append(ps, modelParam{fmt.Sprintf("WK%d", i), func(m *physModel) *float64 { return &m.WK[i] }, 0.01})
		}
	}
	return ps
}

// applyParam setzt Parameter k auf v (inkl. der zusammengesetzten
// symmetrischen Geometrie-Parameter).
func applyParam(m *physModel, p modelParam, v float64) {
	switch p.name {
	case "MICHALFX":
		for i := 0; i < 6; i++ {
			m.MX[i] = math.Copysign(v, m.MX[i])
		}
	case "MICHALFY":
		for i := 0; i < 6; i++ {
			if m.MY[i] != 0 {
				m.MY[i] = math.Copysign(v, m.MY[i])
			}
		}
	case "STANDOFF":
		for i := 0; i < 6; i++ {
			m.MZ[i] = v
		}
	case "DKG":
		for i := 0; i < 6; i++ {
			m.DK[i] = v
		}
	default:
		*p.ptr(m) = v
	}
}

func getParam(m *physModel, p modelParam) float64 {
	switch p.name {
	case "MICHALFX":
		return math.Abs(m.MX[1])
	case "MICHALFY":
		return math.Abs(m.MY[1])
	case "STANDOFF":
		return m.MZ[0]
	case "DKG":
		return m.DK[0]
	}
	return *p.ptr(m)
}

// fitModel: Levenberg-Marquardt auf die Laufzeit-Reste aller Schuesse an
// der Fotowahrheit (robust: Huber).
func fitModel(m physModel, obs []shotObs, ps []modelParam, huberNs float64) (physModel, float64) {
	return fitModelR(m, ps, func(m *physModel) []float64 {
		var r []float64
		for _, o := range obs {
			r = m.shotResiduals(o, o.tx, o.ty, r)
		}
		return huberize(r, huberNs)
	})
}

func huberize(r []float64, huberNs float64) []float64 {
	if huberNs > 0 {
		for k, v := range r {
			if math.Abs(v) > huberNs {
				r[k] = math.Copysign(math.Sqrt(huberNs*(2*math.Abs(v)-huberNs)), v)
			}
		}
	}
	return r
}

// fitModelSelf: Selbstkalibrierung OHNE Wahrheit - jede Schusslage wird mit
// dem jeweiligen Modell neu bestimmt, minimiert wird die innere
// Widerspruchsfreiheit aller Schuesse (Buendelausgleich, variable Projektion).
func fitModelSelf(m physModel, obs []shotObs, ps []modelParam, huberNs float64, start [][2]float64) (physModel, float64) {
	return fitModelR(m, ps, func(m *physModel) []float64 {
		var r []float64
		for k, o := range obs {
			x, y, ok := m.locate(o, start[k][0], start[k][1], huberNs)
			if !ok {
				x, y = start[k][0], start[k][1]
			}
			r = m.shotResiduals(o, x, y, r)
		}
		return huberize(r, huberNs)
	})
}

func fitModelR(m physModel, ps []modelParam, resid func(m *physModel) []float64) (physModel, float64) {
	cost := func(r []float64) float64 {
		s := 0.0
		for _, v := range r {
			s += v * v
		}
		return s
	}
	lambda := 1e-3
	cur := resid(&m)
	c0 := cost(cur)
	n := len(ps)
	for it := 0; it < 200; it++ {
		J := make([][]float64, n)
		for k, p := range ps {
			mm := m
			v := getParam(&mm, p)
			applyParam(&mm, p, v+p.step)
			rp := resid(&mm)
			J[k] = make([]float64, len(cur))
			for j := range cur {
				J[k][j] = (rp[j] - cur[j]) / p.step
			}
		}
		A := make([][]float64, n)
		g := make([]float64, n)
		for a := 0; a < n; a++ {
			A[a] = make([]float64, n)
			for b := 0; b < n; b++ {
				s := 0.0
				for j := range cur {
					s += J[a][j] * J[b][j]
				}
				A[a][b] = s
			}
			s := 0.0
			for j := range cur {
				s += J[a][j] * cur[j]
			}
			g[a] = -s
		}
		improved := false
		for tries := 0; tries < 10; tries++ {
			Al := make([][]float64, n)
			for a := 0; a < n; a++ {
				Al[a] = append([]float64(nil), A[a]...)
				Al[a][a] += lambda * (A[a][a] + 1e-9)
			}
			d := solveDense(Al, g)
			if d == nil {
				lambda *= 10
				continue
			}
			mm := m
			for k, p := range ps {
				applyParam(&mm, p, getParam(&mm, p)+d[k]*p.step*0+d[k])
			}
			r := resid(&mm)
			if c := cost(r); c < c0 {
				m, cur, c0 = mm, r, c
				lambda = math.Max(lambda/3, 1e-9)
				improved = true
				break
			}
			lambda *= 10
		}
		if !improved {
			break
		}
	}
	return m, math.Sqrt(c0 / float64(len(cur)))
}

// solveDense: Gauss-Elimination mit Pivotsuche.
func solveDense(A [][]float64, b []float64) []float64 {
	n := len(b)
	M := make([][]float64, n)
	for i := range M {
		M[i] = append(append([]float64(nil), A[i]...), b[i])
	}
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(M[r][c]) > math.Abs(M[p][c]) {
				p = r
			}
		}
		M[c], M[p] = M[p], M[c]
		if math.Abs(M[c][c]) < 1e-30 {
			return nil
		}
		for r := 0; r < n; r++ {
			if r == c {
				continue
			}
			f := M[r][c] / M[c][c]
			for k := c; k <= n; k++ {
				M[r][k] -= f * M[c][k]
			}
		}
	}
	x := make([]float64, n)
	for i := range x {
		x[i] = M[i][n] / M[i][i]
	}
	return x
}

func modelLocator(m physModel, start SimParams, huberNs float64) func(o shotObs) (float64, float64, bool) {
	return func(o shotObs) (float64, float64, bool) {
		res := SolveSimShot(o.ref.Sh, start)
		x0, y0 := float64(res.XUm)/1000, float64(res.YUm)/1000
		if !res.PosValid {
			x0, y0 = 0, 0
		}
		return m.locate(o, x0, y0, huberNs)
	}
}

func evalObs(obs []shotObs, loc func(o shotObs) (float64, float64, bool)) errStats {
	var errs []float64
	inv := 0
	for _, o := range obs {
		x, y, ok := loc(o)
		if !ok {
			inv++
			continue
		}
		errs = append(errs, math.Hypot(x-o.tx, y-o.ty))
	}
	return statsOf(errs, inv)
}

// TestStudyPhysModel: Modellvarianten, kreuzvalidiert (Anpassung an einer
// Serie, Messung an der anderen) und zusaetzlich "in-sample" (alle).
func TestStudyPhysModel(t *testing.T) {
	shots := loadRefShots(t, "")
	user := loadParams(t, "params_hwcapture.json")
	var obs []shotObs
	for _, r := range shots {
		obs = append(obs, obsOf(r, firstEdge))
	}
	bySess := func(sid string, in bool) []shotObs {
		var out []shotObs
		for _, o := range obs {
			if (o.ref.Session == sid) == in {
				out = append(out, o)
			}
		}
		return out
	}
	studyLog(t, "== Physikalisches Modell (Position per Ausgleichsrechnung, Huber 3000 ns)")
	for _, cfg := range []struct {
		kind string
		useW bool
	}{{"none", false}, {"sym", false}, {"free", false}, {"sym", true}, {"free", true}} {
		for _, hub := range []float64{0, 3000} {
			name := fmt.Sprintf("Geometrie=%-4s Pulsbreite=%-5v Huber=%4.0f", cfg.kind, cfg.useW, hub)
			ps := paramSet(cfg.kind, cfg.useW)
			base := nominalModel(user)
			base.UseW = cfg.useW
			all, rmsAll := fitModel(base, obs, ps, hub)
			in := evalObs(obs, modelLocator(all, user, hub))
			var cvErrs []float64
			cvInv := 0
			for _, sid := range studySessions {
				m, _ := fitModel(base, bySess(sid, true), ps, hub)
				for _, o := range bySess(sid, false) {
					x, y, ok := modelLocator(m, user, hub)(o)
					if !ok {
						cvInv++
						continue
					}
					cvErrs = append(cvErrs, math.Hypot(x-o.tx, y-o.ty))
				}
			}
			studyLog(t, "%s | Fit-Rest %5.0f ns | alle: %s", name, rmsAll, in)
			studyLog(t, "%s | KREUZVALIDIERT       %s", strings.Repeat(" ", len(name)), statsOf(cvErrs, cvInv))
		}
	}
}

// TestStudyEdgeDiag: Lage aller steigenden Flanken relativ zur aus der
// Fotowahrheit erwarteten Ankunft (Modell "sym", an allen Schuessen
// angepasst; t0 robust ueber den Median). Zeigt, ob die erwartete Ankunft
// mit einer (spaeteren/frueheren) Flanke zusammenfaellt.
func TestStudyEdgeDiag(t *testing.T) {
	shots := loadRefShots(t, "")
	user := loadParams(t, "params_hwcapture.json")
	var obs []shotObs
	for _, r := range shots {
		obs = append(obs, obsOf(r, firstEdge))
	}
	m, _ := fitModel(nominalModel(user), obs, paramSet("sym", false), 3000)
	studyLog(t, "== Flankenlage relativ zur erwarteten Ankunft (us), [x] = von der Firmware gewaehlte erste Flanke, (w) Pulsbreite us")
	hist := map[int]int{}
	for _, o := range obs {
		raw := o.ref.Sh.Raw
		if raw == nil || !raw.hasEdges() {
			continue
		}
		var exp [6]float64
		var d []float64
		for i := 0; i < 6; i++ {
			exp[i] = m.arrival(i, o.tx, o.ty, 0)
			if o.seen[i] {
				d = append(d, o.t[i]-exp[i])
			}
		}
		sort.Float64s(d)
		t0 := d[len(d)/2]
		var lines []string
		for i := 0; i < 6; i++ {
			var parts []string
			for k, r := range raw.RiseNs[i] {
				rel := (float64(r) - exp[i] - t0) / 1000
				if rel < -60 || rel > 60 {
					continue
				}
				wp := pulseWidthNs(raw.RiseNs[i], raw.FallNs[i], k, 150000, "")
				ws := ""
				if wp != nil {
					ws = fmt.Sprintf("(%.0f)", float64(*wp)/1000)
				}
				tag := fmt.Sprintf("%+.1f%s", rel, ws)
				if o.seen[i] && float64(r) == o.t[i] {
					tag = "[" + tag + "]"
					hist[int(math.Round(rel))]++
				}
				parts = append(parts, tag)
			}
			lines = append(lines, fmt.Sprintf("M%d: %s", i, strings.Join(parts, " ")))
		}
		studyLog(t, "%s #%-2d (%6.1f/%6.1f)  %s", o.ref.Session[:8], o.ref.ShotNo, o.tx, o.ty, strings.Join(lines, " | "))
	}
	var ks []int
	for k := range hist {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	var hs []string
	for _, k := range ks {
		hs = append(hs, fmt.Sprintf("%+d:%d", k, hist[k]))
	}
	studyLog(t, "Verteilung gewaehlte Flanke - erwartet (us gerundet): %s", strings.Join(hs, " "))
}

// ---------------------------------------------------------------------------
// 3. Alternative Zeitmerkmale aus den Rohflanken
// ---------------------------------------------------------------------------

// pulse: steigende Flanke mit Pulsbreite (ns, -1 = unbekannt).
type pulse struct{ r, w float64 }

func pulsesOf(raw *RawEdges, i int, walkMaxNs int64) []pulse {
	var ps []pulse
	for k, r := range raw.RiseNs[i] {
		w := -1.0
		if wp := pulseWidthNs(raw.RiseNs[i], raw.FallNs[i], k, walkMaxNs, ""); wp != nil {
			w = float64(*wp)
		}
		ps = append(ps, pulse{float64(r), w})
	}
	return ps
}

// edgeFeature: liefert je Mic einen Zeitpunkt (ns) aus den Rohflanken.
type edgeFeature struct {
	name string
	f    func(ps []pulse, first float64) (float64, bool)
}

// strongIdx: erster Puls mit Breite >= minW ab dem ersten Puls (innerhalb
// maxAfter ns nach der ersten Flanke).
func strongIdx(ps []pulse, first, minW, maxAfter float64) int {
	for k, p := range ps {
		if p.r < first-1 {
			continue
		}
		if p.r > first+maxAfter {
			return -1
		}
		if p.w >= minW {
			return k
		}
	}
	return -1
}

func edgeFeatures() []edgeFeature {
	var fs []edgeFeature
	fs = append(fs, edgeFeature{"erste Flanke (Firmware)", func(ps []pulse, first float64) (float64, bool) { return first, true }})
	for _, minW := range []float64{35000, 45000, 55000} {
		minW := minW
		fs = append(fs,
			edgeFeature{fmt.Sprintf("Hauptpuls Beginn (w>=%.0fus)", minW/1000), func(ps []pulse, first float64) (float64, bool) {
				k := strongIdx(ps, first, minW, 80000)
				if k < 0 {
					return 0, false
				}
				return ps[k].r, true
			}},
			edgeFeature{fmt.Sprintf("Hauptpuls Mitte (w>=%.0fus)", minW/1000), func(ps []pulse, first float64) (float64, bool) {
				k := strongIdx(ps, first, minW, 80000)
				if k < 0 {
					return 0, false
				}
				return ps[k].r + ps[k].w/2, true
			}},
			edgeFeature{fmt.Sprintf("Hauptpuls Ende (w>=%.0fus)", minW/1000), func(ps []pulse, first float64) (float64, bool) {
				k := strongIdx(ps, first, minW, 80000)
				if k < 0 {
					return 0, false
				}
				return ps[k].r + ps[k].w, true
			}},
		)
	}
	return fs
}

// obsWithFeature: Messwerte mit dem Zeitmerkmal f; Mics ohne Merkmal fallen
// heraus (wie ein nicht ausgeloestes Mic).
func obsWithFeature(r refShot, f edgeFeature) shotObs {
	o := obsOf(r, firstEdge)
	raw := r.Sh.Raw
	if raw == nil || !raw.hasEdges() {
		return o
	}
	for i := 0; i < 6; i++ {
		if !o.seen[i] {
			continue
		}
		v, ok := f.f(pulsesOf(raw, i, 150000), o.t[i])
		if !ok {
			o.seen[i] = false
			continue
		}
		o.t[i] = v
		o.w[i] = 0
	}
	return o
}

func crossValidate(obs []shotObs, base physModel, ps []modelParam, hub float64, start SimParams) (errStats, errStats, float64) {
	all, rms := fitModel(base, obs, ps, hub)
	in := evalObs(obs, modelLocator(all, start, hub))
	var errs []float64
	inv := 0
	for _, sid := range studySessions {
		var tr, te []shotObs
		for _, o := range obs {
			if o.ref.Session == sid {
				tr = append(tr, o)
			} else {
				te = append(te, o)
			}
		}
		m, _ := fitModel(base, tr, ps, hub)
		for _, o := range te {
			x, y, ok := modelLocator(m, start, hub)(o)
			if !ok {
				inv++
				continue
			}
			errs = append(errs, math.Hypot(x-o.tx, y-o.ty))
		}
	}
	return in, statsOf(errs, inv), rms
}

func TestStudyEdgeFeatures(t *testing.T) {
	shots := loadRefShots(t, "")
	user := loadParams(t, "params_hwcapture.json")
	studyLog(t, "== Zeitmerkmale (Modell sym, Huber 3000; KV = kreuzvalidiert)")
	for _, f := range edgeFeatures() {
		var obs []shotObs
		missing := 0
		for _, r := range shots {
			o := obsWithFeature(r, f)
			for i := 0; i < 6; i++ {
				if !o.seen[i] {
					missing++
				}
			}
			obs = append(obs, o)
		}
		in, cv, rms := crossValidate(obs, nominalModel(user), paramSet("sym", false), 3000, user)
		studyLog(t, "%-34s fehlende Mics %3d | Rest %5.0f ns | alle %s", f.name, missing, rms, in)
		studyLog(t, "%-34s                   |            | KV   %s", "", cv)
	}
}

// TestStudyConsistency: innere Widerspruchsfreiheit der Zeiten je Schuss
// (Rest der Ausgleichsrechnung OHNE Fotowahrheit) gegenueber dem Rest an
// der Fotoposition - beides mit dem an allen Schuessen angepassten Modell.
func TestStudyConsistency(t *testing.T) {
	shots := loadRefShots(t, "")
	user := loadParams(t, "params_hwcapture.json")
	var obs []shotObs
	for _, r := range shots {
		obs = append(obs, obsOf(r, firstEdge))
	}
	m, _ := fitModel(nominalModel(user), obs, paramSet("free", false), 3000)
	rms := func(v []float64) float64 {
		s := 0.0
		for _, x := range v {
			s += x * x
		}
		return math.Sqrt(s / float64(len(v)))
	}
	studyLog(t, "== Innere Konsistenz (Rest ns): an eigener Loesung | an Fotoposition | Abstand Loesung-Foto mm")
	var inner, atTruth []float64
	for _, o := range obs {
		x, y, ok := modelLocator(m, user, 0)(o)
		if !ok {
			continue
		}
		ri := rms(m.shotResiduals(o, x, y, nil))
		rt := rms(m.shotResiduals(o, o.tx, o.ty, nil))
		inner = append(inner, ri)
		atTruth = append(atTruth, rt)
		studyLog(t, "%s #%-2d  %6.0f | %6.0f | %5.2f", o.ref.Session[:8], o.ref.ShotNo, ri, rt, math.Hypot(x-o.tx, y-o.ty))
	}
	studyLog(t, "Mittel: an Loesung %.0f ns, an Fotoposition %.0f ns", rms(inner), rms(atTruth))
}

// TestStudyDistanceTerm: pegelabhaengige Verzoegerung (Schwelle wird bei
// leiserem, weiter entferntem Signal spaeter erreicht) als Zusatzterm.
func TestStudyDistanceTerm(t *testing.T) {
	shots := loadRefShots(t, "")
	user := loadParams(t, "params_hwcapture.json")
	var obs []shotObs
	for _, r := range shots {
		obs = append(obs, obsOf(r, firstEdge))
	}
	studyLog(t, "== Abstandsterm (Pegel) im Modell, Huber 3000")
	for _, mode := range []int{1, 2} {
		for _, kind := range []string{"none+dkg", "none+dk", "sym+dkg", "sym+dk", "free+dkg", "free+dk"} {
			base := nominalModel(user)
			base.DMode = mode
			in, cv, rms := crossValidate(obs, base, paramSet(kind, false), 3000, user)
			studyLog(t, "mode=%d %-9s Rest %5.0f | alle %s", mode, kind, rms, in)
			studyLog(t, "                   |            KV   %s", cv)
		}
	}
}

// warp: nachgeschaltete Lagekorrektur (x,y) -> (x+dx, y+dy).
type warp func(x, y float64) (float64, float64)

// fitWarp passt eine Korrektur an Paare (Loesung -> Wahrheit) an.
// kind: "none", "shift", "affine", "quad", "field10", "field20" (3x3 Felder,
// Grenzen bei +-b mm, Verschiebung je Feld mit Schrumpfung zum Mittel).
func fitWarp(kind string, xs, ys, tx, ty []float64) warp {
	n := len(xs)
	ident := func(x, y float64) (float64, float64) { return x, y }
	if n == 0 || kind == "none" {
		return ident
	}
	var mx, my float64
	for k := range xs {
		mx += tx[k] - xs[k]
		my += ty[k] - ys[k]
	}
	mx /= float64(n)
	my /= float64(n)
	basis := func(x, y float64) []float64 {
		switch kind {
		case "affine":
			return []float64{1, x / 50, y / 50}
		case "quad":
			u, v := x/50, y/50
			return []float64{1, u, v, u * u, u * v, v * v}
		}
		return []float64{1}
	}
	switch kind {
	case "shift":
		return func(x, y float64) (float64, float64) { return x + mx, y + my }
	case "affine", "quad":
		const ridge = 0.5
		nb := len(basis(0, 0))
		fit := func(target func(k int) float64) []float64 {
			A := make([][]float64, nb)
			b := make([]float64, nb)
			for a := range A {
				A[a] = make([]float64, nb)
				if a > 0 {
					A[a][a] = ridge
				}
			}
			for k := range xs {
				f := basis(xs[k], ys[k])
				for a := 0; a < nb; a++ {
					for c := 0; c < nb; c++ {
						A[a][c] += f[a] * f[c]
					}
					b[a] += f[a] * target(k)
				}
			}
			return solveDense(A, b)
		}
		cx := fit(func(k int) float64 { return tx[k] - xs[k] })
		cy := fit(func(k int) float64 { return ty[k] - ys[k] })
		return func(x, y float64) (float64, float64) {
			f := basis(x, y)
			var dx, dy float64
			for a := range f {
				dx += cx[a] * f[a]
				dy += cy[a] * f[a]
			}
			return x + dx, y + dy
		}
	case "field10", "field20":
		b := 10.0
		if kind == "field20" {
			b = 20
		}
		cell := func(x, y float64) int {
			c := func(v float64) int {
				switch {
				case v < -b:
					return 0
				case v > b:
					return 2
				}
				return 1
			}
			return c(x)*3 + c(y)
		}
		var sx, sy, cnt [9]float64
		for k := range xs {
			c := cell(xs[k], ys[k])
			sx[c] += tx[k] - xs[k]
			sy[c] += ty[k] - ys[k]
			cnt[c]++
		}
		const shrink = 2.0 // Pseudo-Schuesse mit Gesamtmittel
		return func(x, y float64) (float64, float64) {
			c := cell(x, y)
			return x + (sx[c]+shrink*mx)/(cnt[c]+shrink), y + (sy[c]+shrink*my)/(cnt[c]+shrink)
		}
	}
	return ident
}

// TestStudyWarp: Fotomassstab-Varianten, Lochrand-Radius als Parameter und
// nachgeschaltete Lagekorrektur (u.a. 9 Felder), jeweils kreuzvalidiert.
func TestStudyWarp(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	for _, tv := range []string{"", "_circle"} {
		shots := loadRefShots(t, tv)
		var obs []shotObs
		for _, r := range shots {
			obs = append(obs, obsOf(r, firstEdge))
		}
		studyLog(t, "== Lagekorrektur nach Modell (Fotomassstab %q), Huber 3000, KV", map[string]string{"": "Schrauben", "_circle": "Innenkreis 30,5"}[tv])
		for _, kind := range []string{"sym", "sym+pr", "free", "free+pr"} {
			for _, wk := range []string{"none", "shift", "affine", "quad", "field10", "field20"} {
				var errs, errsIn []float64
				for _, sid := range studySessions {
					var tr, te []shotObs
					for _, o := range obs {
						if o.ref.Session == sid {
							tr = append(tr, o)
						} else {
							te = append(te, o)
						}
					}
					m, _ := fitModel(nominalModel(user), tr, paramSet(kind, false), 3000)
					loc := modelLocator(m, user, 3000)
					var xs, ys, txs, tys []float64
					for _, o := range tr {
						if x, y, ok := loc(o); ok {
							xs, ys, txs, tys = append(xs, x), append(ys, y), append(txs, o.tx), append(tys, o.ty)
						}
					}
					w := fitWarp(wk, xs, ys, txs, tys)
					for k := range xs {
						x, y := w(xs[k], ys[k])
						errsIn = append(errsIn, math.Hypot(x-txs[k], y-tys[k]))
					}
					for _, o := range te {
						if x, y, ok := loc(o); ok {
							x, y = w(x, y)
							errs = append(errs, math.Hypot(x-o.tx, y-o.ty))
						}
					}
				}
				studyLog(t, "%-8s %-8s Training %s", kind, wk, statsOf(errsIn, 0))
				studyLog(t, "%-8s %-8s KV       %s", "", "", statsOf(errs, 0))
			}
		}
	}
}

// TestStudyNoiseFloor: (a) Monte-Carlo - welche Zeitstreuung je Mic ist fuer
// 90% < 1 mm noetig (Modell an allen Schuessen, Lagen = Fotolagen)?
// (b) Robuste Mic-Auswahl: bestes 5er-Subset statt aller 6 Mics.
func TestStudyNoiseFloor(t *testing.T) {
	shots := loadRefShots(t, "")
	user := loadParams(t, "params_hwcapture.json")
	var obs []shotObs
	for _, r := range shots {
		obs = append(obs, obsOf(r, firstEdge))
	}
	m, _ := fitModel(nominalModel(user), obs, paramSet("sym", false), 3000)
	studyLog(t, "== Rauschgrenze: Zeitstreuung (Normalverteilung je Mic) -> Lagefehler, Lagen wie Referenzschuesse, 6 Mics")
	rng := rand.New(rand.NewSource(1))
	for _, sigma := range []float64{250, 500, 750, 1000, 1500, 2000, 3000, 4000, 5000} {
		var errs []float64
		for rep := 0; rep < 200; rep++ {
			for _, o := range obs {
				var so shotObs
				so.tx, so.ty = o.tx, o.ty
				for i := 0; i < 6; i++ {
					so.seen[i] = true
					so.t[i] = m.arrival(i, o.tx, o.ty, 0) + rng.NormFloat64()*sigma
				}
				x, y, ok := m.locate(so, o.tx+1, o.ty-1, 0)
				if ok {
					errs = append(errs, math.Hypot(x-o.tx, y-o.ty))
				}
			}
		}
		studyLog(t, "sigma %5.0f ns: %s", sigma, statsOf(errs, 0))
	}
	// gemessene Streuung: Rest an der Fotolage, je Freiheitsgrad
	var ss float64
	var dof int
	for _, o := range obs {
		for _, r := range m.shotResiduals(o, o.tx, o.ty, nil) {
			ss += r * r
		}
		dof += 5
	}
	studyLog(t, "gemessen (Rest an Fotolage, Modell sym): sigma ~ %.0f ns", math.Sqrt(ss/float64(dof)))

	studyLog(t, "== Robuste Mic-Auswahl (Modell sym an allen, in-sample)")
	for _, thr := range []float64{0, 1500, 2500, 4000} {
		var errs []float64
		dropped := 0
		for _, o := range obs {
			x, y, ok := modelLocator(m, user, 0)(o)
			if !ok {
				continue
			}
			rms := func(v []float64) float64 {
				s := 0.0
				for _, a := range v {
					s += a * a
				}
				return math.Sqrt(s / float64(len(v)))
			}
			if thr > 0 && rms(m.shotResiduals(o, x, y, nil)) > thr {
				best := math.Inf(1)
				bx, by := x, y
				for drop := 0; drop < 6; drop++ {
					oo := o
					oo.seen[drop] = false
					xx, yy, ok2 := m.locate(oo, x, y, 0)
					if !ok2 {
						continue
					}
					if r := rms(m.shotResiduals(oo, xx, yy, nil)); r < best {
						best, bx, by = r, xx, yy
					}
				}
				x, y = bx, by
				dropped++
			}
			errs = append(errs, math.Hypot(x-o.tx, y-o.ty))
		}
		studyLog(t, "Schwelle %4.0f ns (%2d Schuesse mit 5 Mics): %s", thr, dropped, statsOf(errs, 0))
	}
}

// loadShotsNoTruth: alle Schuesse mit Rohdaten einer Session (ohne Foto).
func loadShotsNoTruth(t *testing.T, sid string) []shotObs {
	st, err := NewStore(context.Background(), studyDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := st.SessionShotsRaw(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	var out []shotObs
	for _, s := range raw {
		if !s.HasRaw {
			continue
		}
		out = append(out, obsOf(refShot{Session: sid, ShotNo: s.ShotNo, Sh: s}, firstEdge))
	}
	return out
}

func withoutC(ps []modelParam) []modelParam {
	var out []modelParam
	for _, p := range ps {
		if p.name != "C" {
			out = append(out, p)
		}
	}
	return out
}

// TestStudySelfCal: Selbstkalibrierung ohne Foto (Serie 302ee29e, 42
// Schuesse, und/oder die Referenzserien selbst), gemessen an der Fotowahrheit.
func TestStudySelfCal(t *testing.T) {
	shots := loadRefShots(t, "")
	user := loadParams(t, "params_hwcapture.json")
	var ref []shotObs
	for _, r := range shots {
		ref = append(ref, obsOf(r, firstEdge))
	}
	extra := loadShotsNoTruth(t, "302ee29e-5d67-47fd-a8dc-1e4c2fe74593")
	startsOf := func(obs []shotObs) [][2]float64 {
		var st [][2]float64
		for _, o := range obs {
			res := SolveSimShot(o.ref.Sh, user)
			st = append(st, [2]float64{float64(res.XUm) / 1000, float64(res.YUm) / 1000})
		}
		return st
	}
	eval := func(m physModel) (errStats, errStats) {
		var xs, ys []float64
		var dx, dy float64
		for _, o := range ref {
			x, y, _ := modelLocator(m, user, 3000)(o)
			xs, ys = append(xs, x), append(ys, y)
			dx += o.tx - x
			dy += o.ty - y
		}
		dx /= float64(len(ref))
		dy /= float64(len(ref))
		var e1, e2 []float64
		for k, o := range ref {
			e1 = append(e1, math.Hypot(xs[k]-o.tx, ys[k]-o.ty))
			e2 = append(e2, math.Hypot(xs[k]+dx-o.tx, ys[k]+dy-o.ty))
		}
		return statsOf(e1, 0), statsOf(e2, 0)
	}
	studyLog(t, "== Selbstkalibrierung ohne Foto (Huber 3000), bewertet an den 23 Fotoschuessen")
	a, b := eval(nominalModel(user))
	studyLog(t, "Nutzer-Kalibrierung        %s | mit Verschiebung %s", a, b)
	for _, set := range []struct {
		name string
		obs  []shotObs
	}{{"302ee29e (42)", extra}, {"302ee29e+Ref (65)", append(append([]shotObs{}, extra...), ref...)}} {
		for _, kind := range []string{"none", "sym", "free"} {
			ps := withoutC(paramSet(kind, false))
			m, rms := fitModelSelf(nominalModel(user), set.obs, ps, 3000, startsOf(set.obs))
			a, b := eval(m)
			studyLog(t, "%-18s %-5s Rest %5.0f | %s | mit Verschiebung %s", set.name, kind, rms, a, b)
		}
	}
}

// TestStudyInnerBySession: innere Widerspruchsfreiheit je Serie (ohne Foto).
func TestStudyInnerBySession(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	m := nominalModel(user)
	studyLog(t, "== Innerer Rest je Serie (Nutzer-Kalibrierung, Huber 0): Median / P90 ns, Lagebereich")
	for _, sid := range append(append([]string{}, studySessions...), "302ee29e-5d67-47fd-a8dc-1e4c2fe74593") {
		obs := loadShotsNoTruth(t, sid)
		var rs, rad []float64
		for _, o := range obs {
			res := SolveSimShot(o.ref.Sh, user)
			x, y, ok := m.locate(o, float64(res.XUm)/1000, float64(res.YUm)/1000, 0)
			if !ok {
				continue
			}
			r := m.shotResiduals(o, x, y, nil)
			s := 0.0
			for _, v := range r {
				s += v * v
			}
			rs = append(rs, math.Sqrt(s/float64(len(r))))
			rad = append(rad, math.Hypot(x, y))
		}
		sort.Float64s(rs)
		sort.Float64s(rad)
		studyLog(t, "%s n=%2d  Rest Median %5.0f P90 %5.0f | Abstand Mitte Median %4.1f mm, max %4.1f", sid[:8], len(rs), rs[len(rs)/2], rs[len(rs)*9/10], rad[len(rad)/2], rad[len(rad)-1])
	}
}

// TestStudyMicLayout: 6 Mics (heutige Anordnung) gegen 4 Mics (Ecken bzw.
// Raute), Zeitstreuung sigma je Mic, optional seltene grobe Fehler.
func TestStudyMicLayout(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	m6 := nominalModel(user)
	var ms []string
	for i := 0; i < 6; i++ {
		ms = append(ms, fmt.Sprintf("M%d(%.0f/%.0f)", i, m6.MX[i], m6.MY[i]))
	}
	studyLog(t, "== Mic-Anordnung (Standoff %.0f mm): %s", m6.MZ[0], strings.Join(ms, " "))
	// 4 Mics: die vier aeussersten Ecken der heutigen Anordnung
	var corners []int
	for i := 0; i < 6; i++ {
		if m6.MY[i] != 0 {
			corners = append(corners, i)
		}
	}
	h := math.Abs(m6.MX[corners[0]])
	diamond := physModel{C: m6.C, PelletR: m6.PelletR}
	for i, p := range [][2]float64{{h, 0}, {0, h}, {-h, 0}, {0, -h}} {
		diamond.MX[i], diamond.MY[i], diamond.MZ[i] = p[0], p[1], m6.MZ[0]
	}
	layouts := []struct {
		name string
		m    physModel
		use  []int
	}{
		{"6 Mics heute", m6, []int{0, 1, 2, 3, 4, 5}},
		{"4 Mics Ecken", m6, corners},
		{"4 Mics Raute", diamond, []int{0, 1, 2, 3}},
	}
	rng := rand.New(rand.NewSource(2))
	for _, gross := range []float64{0, 0.03} {
		for _, sigma := range []float64{500, 1000, 1500, 2000, 3000} {
			for _, L := range layouts {
				var errs []float64
				for rep := 0; rep < 20000; rep++ {
					// Treffer gleichverteilt im Kreis r <= 30 mm (Wettkampfbereich)
					r := 30 * math.Sqrt(rng.Float64())
					a := 2 * math.Pi * rng.Float64()
					tx, ty := r*math.Cos(a), r*math.Sin(a)
					var o shotObs
					for _, i := range L.use {
						o.seen[i] = true
						o.t[i] = L.m.arrival(i, tx, ty, 0) + rng.NormFloat64()*sigma
						if rng.Float64() < gross {
							o.t[i] += 10000 + 20000*rng.Float64()
						}
					}
					x, y, ok := L.m.locate(o, 0, 0, 3000)
					if ok {
						errs = append(errs, math.Hypot(x-tx, y-ty))
					} else {
						errs = append(errs, 99)
					}
				}
				studyLog(t, "grobe Fehler %2.0f%% sigma %4.0f ns  %-13s %s", gross*100, sigma, L.name, statsOf(errs, 0))
			}
		}
	}
}

// TestStudyWidthVsDistance: Pulsbreite (erster Puls, breitester Puls in den
// ersten 100 us) gegen Abstand Treffer-Mikrofon.
func TestStudyWidthVsDistance(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	m := nominalModel(user)
	type pt struct{ d, w1, wmax, n float64 }
	pts := map[string][6][]pt{}
	add := func(set string, o shotObs, x, y float64) {
		raw := o.ref.Sh.Raw
		if raw == nil || !raw.hasEdges() {
			return
		}
		arr := pts[set]
		for i := 0; i < 6; i++ {
			if !o.seen[i] {
				continue
			}
			ps := pulsesOf(raw, i, 150000)
			var w1, wmax, n float64 = -1, -1, 0
			for _, p := range ps {
				if p.r < o.t[i]-1 || p.r > o.t[i]+100000 {
					continue
				}
				n++
				if w1 < 0 {
					w1 = p.w
				}
				if p.w > wmax {
					wmax = p.w
				}
			}
			if w1 <= 0 {
				continue
			}
			d := math.Sqrt(math.Pow(math.Hypot(x-m.MX[i], y-m.MY[i]), 2) + m.MZ[i]*m.MZ[i])
			arr[i] = append(arr[i], pt{d, w1 / 1000, wmax / 1000, n})
		}
		pts[set] = arr
	}
	for _, r := range loadRefShots(t, "") {
		o := obsOf(r, firstEdge)
		add("Foto (23)", o, o.tx, o.ty)
	}
	for _, o := range loadShotsNoTruth(t, "302ee29e-5d67-47fd-a8dc-1e4c2fe74593") {
		res := SolveSimShot(o.ref.Sh, user)
		add("302ee29e (42)", o, float64(res.XUm)/1000, float64(res.YUm)/1000)
	}
	studyLog(t, "== Pulsbreite vs Abstand Treffer-Mic (w1 = erster Puls, wmax = breitester Puls <=100us danach), us")
	for _, set := range []string{"Foto (23)", "302ee29e (42)"} {
		var allD, allW, allWm []float64
		for i := 0; i < 6; i++ {
			var d, w1, wm []float64
			for _, p := range pts[set][i] {
				d, w1, wm = append(d, p.d), append(w1, p.w1), append(wm, p.wmax)
			}
			allD, allW, allWm = append(allD, d...), append(allW, w1...), append(allWm, wm...)
			if len(d) < 3 {
				continue
			}
			ws := append([]float64{}, w1...)
			sort.Float64s(ws)
			ds := append([]float64{}, d...)
			sort.Float64s(ds)
			a1, b1, _, r1 := linFit(d, w1)
			_, b2, _, r2 := linFit(d, wm)
			studyLog(t, "%-13s M%d n=%2d Abstand %3.0f-%3.0f mm | w1 min %4.0f med %4.0f max %4.0f | w1 = %5.1f %+6.3f*d (r=%+.2f) | wmax %+6.3f/mm (r=%+.2f)",
				set, i, len(d), ds[0], ds[len(ds)-1], ws[0], ws[len(ws)/2], ws[len(ws)-1], a1, b1, r1, b2, r2)
		}
		_, b, _, r := linFit(allD, allW)
		_, bm, _, rm := linFit(allD, allWm)
		studyLog(t, "%-13s alle n=%d: w1 %+6.3f us/mm (r=%+.2f), wmax %+6.3f us/mm (r=%+.2f)", set, len(allD), b, r, bm, rm)
		// Klassen nach Abstand
		for _, lo := range []float64{0, 60, 80, 100, 120, 140} {
			var ws []float64
			for k := range allD {
				if allD[k] >= lo && allD[k] < lo+20 || (lo == 140 && allD[k] >= 140) {
					ws = append(ws, allW[k])
				}
			}
			if len(ws) == 0 {
				continue
			}
			sort.Float64s(ws)
			short := 0
			for _, w := range ws {
				if w < 30 {
					short++
				}
			}
			studyLog(t, "   Abstand %3.0f-%3.0f mm: n=%3d  w1 P10 %4.0f Median %4.0f P90 %4.0f  | Vorpuls (<30us) %2.0f%%", lo, lo+20, len(ws), ws[len(ws)/10], ws[len(ws)/2], ws[len(ws)*9/10], 100*float64(short)/float64(len(ws)))
		}
	}
}

// TestStudyPaperLayers: innerer Rest, Fotofehler und Vorpuls-Anteil je
// Schuss gegen die horizontale Lage (Schussband -> 1 oder 2 Papierlagen).
func TestStudyPaperLayers(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	var ref []shotObs
	for _, r := range loadRefShots(t, "") {
		ref = append(ref, obsOf(r, firstEdge))
	}
	m, _ := fitModel(nominalModel(user), ref, paramSet("sym", false), 3000)
	rms := func(v []float64) float64 {
		s := 0.0
		for _, a := range v {
			s += a * a
		}
		return math.Sqrt(s / float64(len(v)))
	}
	type row struct {
		sid          string
		no           int
		x, y         float64
		inner, err   float64
		pre, nPulses int
		wspread      float64
	}
	var rows []row
	do := func(o shotObs, truth bool) {
		x, y, ok := modelLocator(m, user, 0)(o)
		if !ok {
			return
		}
		r := row{sid: o.ref.Session[:8], no: o.ref.ShotNo, x: x, y: y, inner: rms(m.shotResiduals(o, x, y, nil)), err: -1}
		if truth {
			r.err = math.Hypot(x-o.tx, y-o.ty)
			r.x, r.y = o.tx, o.ty
		}
		raw := o.ref.Sh.Raw
		var ws []float64
		for i := 0; i < 6; i++ {
			if !o.seen[i] || raw == nil {
				continue
			}
			for _, p := range pulsesOf(raw, i, 150000) {
				if p.r == o.t[i] {
					ws = append(ws, p.w)
					if p.w > 0 && p.w < 30000 {
						r.pre++
					}
				}
			}
		}
		if len(ws) > 0 {
			sort.Float64s(ws)
			r.wspread = (ws[len(ws)-1] - ws[0]) / 1000
		}
		rows = append(rows, r)
	}
	for _, o := range ref {
		do(o, true)
	}
	for _, o := range loadShotsNoTruth(t, "302ee29e-5d67-47fd-a8dc-1e4c2fe74593") {
		do(o, false)
	}
	sort.Slice(rows, func(a, b int) bool { return math.Abs(rows[a].x) < math.Abs(rows[b].x) })
	studyLog(t, "== Papierlagen: je Schuss nach |x| sortiert (Modell sym an Foto)")
	type agg struct {
		inner, err []float64
		pre, n     int
	}
	bins := map[string]*agg{}
	binOf := func(x float64) string {
		switch ax := math.Abs(x); {
		case ax < 15:
			return "|x|<15"
		case ax < 35:
			return "15-35"
		case ax < 55:
			return "35-55"
		}
		return ">55"
	}
	for _, r := range rows {
		if r.sid != "302ee29e" {
			es := fmt.Sprintf("%5.2f", r.err)
			studyLog(t, "%s #%-2d x %6.1f y %6.1f  innen %5.0f ns  Foto %s mm  Vorpulse %d  Breitenspanne %3.0f us", r.sid, r.no, r.x, r.y, r.inner, es, r.pre, r.wspread)
		}
		k := r.sid[:3] + " " + binOf(r.x)
		if r.sid == "302ee29e" {
			k = "Rita " + binOf(r.x)
		} else {
			k = "Foto " + binOf(r.x)
		}
		a := bins[k]
		if a == nil {
			a = &agg{}
			bins[k] = a
		}
		a.inner = append(a.inner, r.inner)
		if r.err >= 0 {
			a.err = append(a.err, r.err)
		}
		a.pre += r.pre
		a.n++
	}
	var keys []string
	for k := range bins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	med := func(v []float64) float64 {
		if len(v) == 0 {
			return math.NaN()
		}
		s := append([]float64{}, v...)
		sort.Float64s(s)
		return s[len(s)/2]
	}
	for _, k := range keys {
		a := bins[k]
		studyLog(t, "%-12s n=%2d  innerer Rest Median %5.0f ns | Fotofehler Median %5.2f mm | Vorpulse je Schuss %.2f", k, a.n, med(a.inner), med(a.err), float64(a.pre)/float64(a.n))
	}
}

// TestStudyCenterResiduals: Reste je Mic in der Zentrums-Serie (Rita):
// systematischer Anteil (Mittel) gegen zufaelligen Anteil (Streuung).
func TestStudyCenterResiduals(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	var ref []shotObs
	for _, r := range loadRefShots(t, "") {
		ref = append(ref, obsOf(r, firstEdge))
	}
	rita := loadShotsNoTruth(t, "302ee29e-5d67-47fd-a8dc-1e4c2fe74593")
	msym, _ := fitModel(nominalModel(user), ref, paramSet("sym", false), 3000)
	for _, mm := range []struct {
		name string
		m    physModel
	}{{"Nutzer-Kalibrierung", nominalModel(user)}, {"Modell sym an Foto", msym}} {
		var sum, sq [6]float64
		var n [6]float64
		var inner []float64
		for _, o := range rita {
			res := SolveSimShot(o.ref.Sh, user)
			x, y, ok := mm.m.locate(o, float64(res.XUm)/1000, float64(res.YUm)/1000, 0)
			if !ok {
				continue
			}
			k := 0
			s := 0.0
			r := mm.m.shotResiduals(o, x, y, nil)
			for i := 0; i < 6; i++ {
				if o.seen[i] {
					sum[i] += r[k]
					sq[i] += r[k] * r[k]
					n[i]++
					s += r[k] * r[k]
					k++
				}
			}
			inner = append(inner, math.Sqrt(s/float64(len(r))))
		}
		var parts []string
		for i := 0; i < 6; i++ {
			mu := sum[i] / n[i]
			sd := math.Sqrt(sq[i]/n[i] - mu*mu)
			parts = append(parts, fmt.Sprintf("M%d %+5.0f±%4.0f", i, mu, sd))
		}
		sort.Float64s(inner)
		studyLog(t, "Rita (42) %-20s Rest je Mic Mittel±Streuung ns: %s | innerer Rest P10 %4.0f Median %4.0f P90 %4.0f", mm.name, strings.Join(parts, "  "), inner[len(inner)/10], inner[len(inner)/2], inner[len(inner)*9/10])
	}
	// Zentrums-Selbstkalibrierung nur Offsets (Lage um Mitte, Maßstab unkritisch):
	// Kreuzvalidierung erste/zweite Haelfte - wie stark sinkt der innere Rest?
	half := len(rita) / 2
	starts := func(obs []shotObs) [][2]float64 {
		var st [][2]float64
		for _, o := range obs {
			res := SolveSimShot(o.ref.Sh, user)
			st = append(st, [2]float64{float64(res.XUm) / 1000, float64(res.YUm) / 1000})
		}
		return st
	}
	var ofsOnly []modelParam
	for _, p := range paramSet("none", false) {
		if p.name != "C" {
			ofsOnly = append(ofsOnly, p)
		}
	}
	mc, _ := fitModelSelf(msym, rita[:half], ofsOnly, 0, starts(rita[:half]))
	var inner []float64
	var spread []float64
	var px, py []float64
	for _, o := range rita[half:] {
		res := SolveSimShot(o.ref.Sh, user)
		x, y, ok := mc.locate(o, float64(res.XUm)/1000, float64(res.YUm)/1000, 0)
		if !ok {
			continue
		}
		r := mc.shotResiduals(o, x, y, nil)
		s := 0.0
		for _, v := range r {
			s += v * v
		}
		inner = append(inner, math.Sqrt(s/float64(len(r))))
		x0, y0, _ := msym.locate(o, x, y, 0)
		spread = append(spread, math.Hypot(x-x0, y-y0))
		px, py = append(px, x), append(py, y)
	}
	sort.Float64s(inner)
	sort.Float64s(spread)
	var ofs []string
	for i := 0; i < 6; i++ {
		ofs = append(ofs, fmt.Sprintf("%+.0f", mc.Ofs[i]-msym.Ofs[i]))
	}
	studyLog(t, "Rita: Offsets aus 1. Haelfte selbstkalibriert (Aenderung ns: %s) -> 2. Haelfte innerer Rest P10 %4.0f Median %4.0f P90 %4.0f | Lageverschiebung ggue. Modell sym Median %.2f mm max %.2f",
		strings.Join(ofs, " "), inner[len(inner)/10], inner[len(inner)/2], inner[len(inner)*9/10], spread[len(spread)/2], spread[len(spread)-1])
}

// TestStudyCenterScale: Zentrums-Serie - welche Massstabs-Groesse (Schall,
// Mic-Abstaende, Standoff) erklaert den systematischen Rest? Selbstkalibriert
// an der 1. Haelfte, geprueft an der 2. Haelfte.
func TestStudyCenterScale(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	var ref []shotObs
	for _, r := range loadRefShots(t, "") {
		ref = append(ref, obsOf(r, firstEdge))
	}
	rita := loadShotsNoTruth(t, "302ee29e-5d67-47fd-a8dc-1e4c2fe74593")
	msym, _ := fitModel(nominalModel(user), ref, paramSet("sym", false), 3000)
	half := len(rita) / 2
	starts := func(obs []shotObs) [][2]float64 {
		var st [][2]float64
		for _, o := range obs {
			res := SolveSimShot(o.ref.Sh, user)
			st = append(st, [2]float64{float64(res.XUm) / 1000, float64(res.YUm) / 1000})
		}
		return st
	}
	byName := map[string]modelParam{}
	for _, p := range paramSet("sym", false) {
		byName[p.name] = p
	}
	studyLog(t, "== Zentrums-Serie: Massstabsgroessen selbstkalibriert (Basis Modell sym an Foto: C %.1f m/s, MICHALFX %.1f, MICHALFY %.1f, Standoff %.1f)",
		msym.C*1e6, math.Abs(msym.MX[1]), math.Abs(msym.MY[1]), msym.MZ[0])
	for _, set := range [][]string{{}, {"C"}, {"STANDOFF"}, {"MICHALFX"}, {"MICHALFX", "MICHALFY"}, {"C", "STANDOFF"}, {"C", "MICHALFY"}} {
		var ps []modelParam
		for _, n := range set {
			ps = append(ps, byName[n])
		}
		m := msym
		if len(ps) > 0 {
			m, _ = fitModelSelf(msym, rita[:half], ps, 0, starts(rita[:half]))
		}
		var inner, shift []float64
		for _, o := range rita[half:] {
			res := SolveSimShot(o.ref.Sh, user)
			x, y, ok := m.locate(o, float64(res.XUm)/1000, float64(res.YUm)/1000, 0)
			if !ok {
				continue
			}
			r := m.shotResiduals(o, x, y, nil)
			s := 0.0
			for _, v := range r {
				s += v * v
			}
			inner = append(inner, math.Sqrt(s/float64(len(r))))
			x0, y0, _ := msym.locate(o, x, y, 0)
			shift = append(shift, math.Hypot(x-x0, y-y0))
		}
		sort.Float64s(inner)
		sort.Float64s(shift)
		studyLog(t, "%-22s -> C %.1f m/s MICHALFX %.1f MICHALFY %.1f Standoff %.1f | 2. Haelfte innerer Rest Median %4.0f P90 %4.0f | Lage ggue. Basis Median %.2f mm",
			strings.Join(set, "+"), m.C*1e6, math.Abs(m.MX[1]), math.Abs(m.MY[1]), m.MZ[0], inner[len(inner)/2], inner[len(inner)*9/10], shift[len(shift)/2])
	}
	// Foto-Serien mit dem an der Mitte bestimmten C pruefen
	_ = user
}

// TestStudyCenterModelOnPhoto: an der Zentrums-Serie (Rita) per Schall-
// geschwindigkeit nachkalibriertes Modell an den Fotoschuessen nahe Mitte.
func TestStudyCenterModelOnPhoto(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	var ref []shotObs
	for _, r := range loadRefShots(t, "") {
		ref = append(ref, obsOf(r, firstEdge))
	}
	rita := loadShotsNoTruth(t, "302ee29e-5d67-47fd-a8dc-1e4c2fe74593")
	msym, _ := fitModel(nominalModel(user), ref, paramSet("sym", false), 3000)
	var st [][2]float64
	for _, o := range rita {
		res := SolveSimShot(o.ref.Sh, user)
		st = append(st, [2]float64{float64(res.XUm) / 1000, float64(res.YUm) / 1000})
	}
	var pC modelParam
	for _, p := range paramSet("sym", false) {
		if p.name == "C" {
			pC = p
		}
	}
	mc, _ := fitModelSelf(msym, rita, []modelParam{pC}, 0, st)
	studyLog(t, "== Zentrumsmodell (C %.1f m/s aus Rita) an Fotoschuessen", mc.C*1e6)
	for _, lim := range []float64{20, 35, 100} {
		var e1, e2 []float64
		for _, o := range ref {
			if math.Hypot(o.tx, o.ty) > lim {
				continue
			}
			x, y, _ := modelLocator(msym, user, 0)(o)
			e1 = append(e1, math.Hypot(x-o.tx, y-o.ty))
			x, y, _ = modelLocator(mc, user, 0)(o)
			e2 = append(e2, math.Hypot(x-o.tx, y-o.ty))
		}
		studyLog(t, "Radius <= %3.0f mm: Modell sym %s", lim, statsOf(e1, 0))
		studyLog(t, "                  Zentrumsmodell %s", statsOf(e2, 0))
	}
}

// zoneOf: Papierlagen nach Lage (LG): Loch der Vorsatzscheibe Ø30,5 -> nur
// Band; Band 90 mm breit (|x|<45) -> 2 Lagen; ausserhalb nur Scheibe.
func zoneOf(x, y float64) string {
	switch {
	case math.Hypot(x, y) < 15.25:
		return "Mitte (nur Band)"
	case math.Abs(x) < 45:
		return "Band (2 Lagen)"
	}
	return "aussen (nur Scheibe)"
}

// TestStudyZones: Verhalten je Papierlagen-Zone.
func TestStudyZones(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	var ref []shotObs
	for _, r := range loadRefShots(t, "") {
		ref = append(ref, obsOf(r, firstEdge))
	}
	rms := func(v []float64) float64 {
		s := 0.0
		for _, a := range v {
			s += a * a
		}
		return math.Sqrt(s / float64(len(v)))
	}
	msym, _ := fitModel(nominalModel(user), ref, paramSet("sym", false), 3000)
	zones := []string{"Band (2 Lagen)", "aussen (nur Scheibe)"}
	byZone := map[string][]shotObs{}
	for _, o := range ref {
		z := zoneOf(o.tx, o.ty)
		byZone[z] = append(byZone[z], o)
	}
	studyLog(t, "== Zonen nach Papierlagen (Foto-Schuesse; Mitte hat keine Foto-Schuesse)")
	med := func(v []float64) float64 { s := append([]float64{}, v...); sort.Float64s(s); return s[len(s)/2] }
	for _, z := range zones {
		obs := byZone[z]
		var inner, atT, errs []float64
		pre := 0
		var sum, sq [6]float64
		for _, o := range obs {
			x, y, _ := modelLocator(msym, user, 0)(o)
			inner = append(inner, rms(msym.shotResiduals(o, x, y, nil)))
			r := msym.shotResiduals(o, o.tx, o.ty, nil)
			atT = append(atT, rms(r))
			errs = append(errs, math.Hypot(x-o.tx, y-o.ty))
			for i := 0; i < 6; i++ {
				sum[i] += r[i]
				sq[i] += r[i] * r[i]
				if raw := o.ref.Sh.Raw; raw != nil {
					for _, p := range pulsesOf(raw, i, 150000) {
						if p.r == o.t[i] && p.w > 0 && p.w < 30000 {
							pre++
						}
					}
				}
			}
		}
		var parts []string
		for i := 0; i < 6; i++ {
			n := float64(len(obs))
			mu := sum[i] / n
			parts = append(parts, fmt.Sprintf("M%d %+5.0f±%4.0f", i, mu, math.Sqrt(sq[i]/n-mu*mu)))
		}
		studyLog(t, "%-21s n=%2d innerer Rest Median %5.0f | Rest an Fotolage Median %5.0f | Vorpulse je Schuss %.2f | Fehler (Modell alle) %s",
			z, len(obs), med(inner), med(atT), float64(pre)/float64(len(obs)), statsOf(errs, 0))
		studyLog(t, "%-21s Rest je Mic an Fotolage (Mittel±Streuung ns): %s", "", strings.Join(parts, "  "))
	}
	// Modell nur an einer Zone, Leave-one-out innerhalb der Zone
	studyLog(t, "-- Modell nur an der eigenen Zone angepasst (Leave-one-out) bzw. an der anderen Zone")
	for _, z := range zones {
		obs := byZone[z]
		for _, kind := range []string{"none", "sym"} {
			var loo []float64
			for k := range obs {
				var tr []shotObs
				for j, o := range obs {
					if j != k {
						tr = append(tr, o)
					}
				}
				m, _ := fitModel(nominalModel(user), tr, paramSet(kind, false), 3000)
				x, y, _ := modelLocator(m, user, 0)(obs[k])
				loo = append(loo, math.Hypot(x-obs[k].tx, y-obs[k].ty))
			}
			other := byZone[zones[0]]
			if z == zones[0] {
				other = byZone[zones[1]]
			}
			mo, _ := fitModel(nominalModel(user), other, paramSet(kind, false), 3000)
			var cross []float64
			for _, o := range obs {
				x, y, _ := modelLocator(mo, user, 0)(o)
				cross = append(cross, math.Hypot(x-o.tx, y-o.ty))
			}
			studyLog(t, "%-21s %-4s eigene Zone LOO %s", z, kind, statsOf(loo, 0))
			studyLog(t, "%-21s %-4s andere Zone     %s", "", "", statsOf(cross, 0))
		}
	}
}

// ---------------------------------------------------------------------------
// Zonen-Kalibrierung mit dem echten Simulator-Loeser (Serie 0342fdff, 12.10.)
// ---------------------------------------------------------------------------

const zoneSession = "0342fdff-9742-48f8-8321-3657f2f8ac74"

// loadSessionRef laedt die Fotowahrheit einer beliebigen Session.
func loadSessionRef(t *testing.T, sid, variant string) []refShot {
	saved := studySessions
	studySessions = []string{sid}
	defer func() { studySessions = saved }()
	return loadRefShots(t, variant)
}

// zoneByShot: Serie 0342fdff - 1-12 im Loch der Vorsatzscheibe (nur Band),
// 13-22 links/rechts neben dem Band (nur Vorsatzscheibe), 23-30 Band +
// Vorsatzscheibe.
func zoneByShot(no int) string {
	switch {
	case no <= 12:
		return "A Loch (nur Band)"
	case no <= 22:
		return "B neben Band"
	}
	return "C Band+Vorsatz"
}

// calVec: die Kalibrier-Groessen des Simulators als Vektor.
var calNames = []string{"SOUND", "MICHALFX", "STANDOFF", "OFS1", "OFS2", "OFS3", "OFS4", "OFS5", "OFFX", "OFFY"}
var calSteps = []float64{0.5, 0.5, 0.5, 300, 300, 300, 300, 300, 0.1, 0.1}

func calGet(p SimParams) []float64 {
	return []float64{p.SoundMps, p.MicHalfXMm, p.StandoffPaperMM,
		float64(p.MicOffsetNs[1]), float64(p.MicOffsetNs[2]), float64(p.MicOffsetNs[3]), float64(p.MicOffsetNs[4]), float64(p.MicOffsetNs[5]),
		float64(p.OffsetXUm) / 1000, float64(p.OffsetYUm) / 1000}
}

func calSet(p SimParams, v []float64, free []bool) SimParams {
	cur := calGet(p)
	for k := range v {
		if free[k] {
			cur[k] = v[k]
		}
	}
	p.SoundMps, p.MicHalfXMm, p.StandoffPaperMM = cur[0], cur[1], cur[2]
	for i := 1; i < 6; i++ {
		p.MicOffsetNs[i] = int64(math.Round(cur[2+i]))
	}
	p.OffsetXUm, p.OffsetYUm = int64(math.Round(cur[8]*1000)), int64(math.Round(cur[9]*1000))
	return p
}

func simErrs(shots []refShot, p SimParams) []float64 {
	var e []float64
	for _, r := range shots {
		res := SolveSimShot(r.Sh, p)
		if !res.PosValid {
			e = append(e, 20)
			continue
		}
		e = append(e, math.Hypot(float64(res.XUm)/1000-r.TX, float64(res.YUm)/1000-r.TY))
	}
	return e
}

// fitSimParams: Levenberg-Marquardt auf die Lagefehler (dx, dy je Schuss,
// Huber 1,5 mm) mit dem Simulator-Loeser; free waehlt die Groessen.
func fitSimParams(shots []refShot, base SimParams, free []bool) SimParams {
	const hub = 1.5
	resid := func(p SimParams) []float64 {
		var r []float64
		for _, s := range shots {
			res := SolveSimShot(s.Sh, p)
			dx, dy := 20.0, 20.0
			if res.PosValid {
				dx, dy = float64(res.XUm)/1000-s.TX, float64(res.YUm)/1000-s.TY
			}
			for _, v := range []float64{dx, dy} {
				if math.Abs(v) > hub {
					v = math.Copysign(math.Sqrt(hub*(2*math.Abs(v)-hub)), v)
				}
				r = append(r, v)
			}
		}
		return r
	}
	cost := func(r []float64) float64 {
		s := 0.0
		for _, v := range r {
			s += v * v
		}
		return s
	}
	var idx []int
	for k, f := range free {
		if f {
			idx = append(idx, k)
		}
	}
	v := calGet(base)
	p := base
	cur := resid(p)
	c0 := cost(cur)
	lambda := 1e-2
	for it := 0; it < 100; it++ {
		n := len(idx)
		J := make([][]float64, n)
		for a, k := range idx {
			vv := append([]float64{}, v...)
			vv[k] += calSteps[k]
			rp := resid(calSet(base, vv, free))
			J[a] = make([]float64, len(cur))
			for j := range cur {
				J[a][j] = (rp[j] - cur[j]) / calSteps[k]
			}
		}
		A := make([][]float64, n)
		g := make([]float64, n)
		for a := 0; a < n; a++ {
			A[a] = make([]float64, n)
			for b := 0; b < n; b++ {
				for j := range cur {
					A[a][b] += J[a][j] * J[b][j]
				}
			}
			for j := range cur {
				g[a] -= J[a][j] * cur[j]
			}
		}
		improved := false
		for tries := 0; tries < 8; tries++ {
			Al := make([][]float64, n)
			for a := range A {
				Al[a] = append([]float64{}, A[a]...)
				Al[a][a] += lambda*A[a][a] + 1e-9
			}
			d := solveDense(Al, g)
			if d == nil {
				lambda *= 10
				continue
			}
			vv := append([]float64{}, v...)
			for a, k := range idx {
				vv[k] += d[a]
			}
			pp := calSet(base, vv, free)
			r := resid(pp)
			if c := cost(r); c < c0-1e-9 {
				v, p, cur, c0 = vv, pp, r, c
				lambda = math.Max(lambda/3, 1e-6)
				improved = true
				break
			}
			lambda *= 10
		}
		if !improved {
			break
		}
	}
	return p
}

func freeSet(names ...string) []bool {
	f := make([]bool, len(calNames))
	for _, n := range names {
		for k, c := range calNames {
			if c == n {
				f[k] = true
			}
		}
	}
	return f
}

func paramStr(p SimParams) string {
	return fmt.Sprintf("Schall %.1f MICHALFX %.1f Standoff %.1f Offsets %v Verschiebung %.2f/%.2f",
		p.SoundMps, p.MicHalfXMm, p.StandoffPaperMM, p.MicOffsetNs, float64(p.OffsetXUm)/1000, float64(p.OffsetYUm)/1000)
}

func TestStudyZoneCal(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	sets := map[string][]string{
		"alle":                 calNames,
		"ohne Geometrie":       {"SOUND", "OFS1", "OFS2", "OFS3", "OFS4", "OFS5", "OFFX", "OFFY"},
		"Schall+Geometrie+Ver": {"SOUND", "MICHALFX", "STANDOFF", "OFFX", "OFFY"},
	}
	setOrder := []string{"Schall+Geometrie+Ver", "ohne Geometrie", "alle"}
	zones := []string{"A Loch (nur Band)", "B neben Band", "C Band+Vorsatz"}
	for _, tv := range []string{"", "_circle"} {
		shots := loadSessionRef(t, zoneSession, tv)
		byZone := map[string][]refShot{}
		for _, r := range shots {
			z := zoneByShot(r.ShotNo)
			byZone[z] = append(byZone[z], r)
		}
		studyLog(t, "== Zonen-Kalibrierung Serie 0342fdff (Fotomassstab %s), Simulator-Loeser", map[string]string{"": "Schrauben 141,5", "_circle": "Spiegel 59,5"}[tv])
		for _, z := range zones {
			studyLog(t, "Nutzer-Kalibrierung   %-18s %s", z, statsOf(simErrs(byZone[z], user), 0))
		}
		for _, sn := range setOrder {
			free := freeSet(sets[sn]...)
			// gemeinsame Kalibrierung an allen Schuessen
			pAll := fitSimParams(shots, user, free)
			for _, z := range zones {
				studyLog(t, "[%s] gemeinsam an allen    %-18s %s", sn, z, statsOf(simErrs(byZone[z], pAll), 0))
			}
			studyLog(t, "[%s] gemeinsam: %s", sn, paramStr(pAll))
			// je Zone eigene Kalibrierung: in-sample und Leave-one-out
			for _, z := range zones {
				zs := byZone[z]
				pz := fitSimParams(zs, user, free)
				var loo []float64
				for k := range zs {
					var tr []refShot
					for j := range zs {
						if j != k {
							tr = append(tr, zs[j])
						}
					}
					loo = append(loo, simErrs(zs[k:k+1], fitSimParams(tr, user, free))...)
				}
				studyLog(t, "[%s] eigene Zone   %-18s angepasst %s", sn, z, statsOf(simErrs(zs, pz), 0))
				studyLog(t, "[%s]               %-18s LOO       %s", sn, "", statsOf(loo, 0))
				studyLog(t, "[%s]               %-18s %s", sn, "", paramStr(pz))
			}
		}
	}
}

// TestStudyZoneADetail: Einzelschuesse Zone A (Loch) - Fotofehler mit
// eigener Zonen-Kalibrierung gegen innere Widerspruchsfreiheit der Zeiten.
func TestStudyZoneADetail(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	shots := loadSessionRef(t, zoneSession, "_circle")
	var a []refShot
	for _, r := range shots {
		if zoneByShot(r.ShotNo)[0] == 'A' {
			a = append(a, r)
		}
	}
	p := fitSimParams(a, user, freeSet("SOUND", "OFS1", "OFS2", "OFS3", "OFS4", "OFS5", "OFFX", "OFFY"))
	m := nominalModel(p)
	studyLog(t, "== Zone A Einzelschuesse (Kalibrierung ohne Geometrie an Zone A): %s", paramStr(p))
	for _, r := range a {
		res := SolveSimShot(r.Sh, p)
		x, y := float64(res.XUm)/1000, float64(res.YUm)/1000
		o := obsOf(r, firstEdge)
		lx, ly, _ := m.locate(o, x, y, 0)
		rr := m.shotResiduals(o, lx, ly, nil)
		s := 0.0
		for _, v := range rr {
			s += v * v
		}
		var ws []string
		if raw := r.Sh.Raw; raw != nil {
			for i := 0; i < 6; i++ {
				for _, pp := range pulsesOf(raw, i, 150000) {
					if pp.r == o.t[i] {
						ws = append(ws, fmt.Sprintf("%.0f", pp.w/1000))
					}
				}
			}
		}
		studyLog(t, "#%-2d Foto %6.2f/%6.2f  Sim %6.2f/%6.2f  Fehler %4.2f mm | innerer Rest %5.0f ns | Breiten %s", r.ShotNo, r.TX, r.TY, x, y, math.Hypot(x-r.TX, y-r.TY), math.Sqrt(s/float64(len(rr))), strings.Join(ws, ","))
	}
}

// TestStudyZoneAModel: Zone A mit dem physikalischen Modell (Offsets+Schall
// an Zone A): Rest an eigener Loesung vs. an Fotolage je Schuss.
func TestStudyZoneAModel(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	rms := func(v []float64) float64 {
		s := 0.0
		for _, a := range v {
			s += a * a
		}
		return math.Sqrt(s / float64(len(v)))
	}
	for _, z := range []byte{'A', 'B', 'C'} {
		var obs []shotObs
		for _, r := range loadSessionRef(t, zoneSession, "_circle") {
			if zoneByShot(r.ShotNo)[0] == z {
				obs = append(obs, obsOf(r, firstEdge))
			}
		}
		m, fr := fitModel(nominalModel(user), obs, paramSet("none", false), 0)
		studyLog(t, "== Zone %c physik. Modell (Offsets+Schall, an Zone angepasst): C %.1f m/s, Fit-Rest %.0f ns", z, m.C*1e6, fr)
		var errs []float64
		for _, o := range obs {
			x, y, _ := modelLocator(m, user, 0)(o)
			e := math.Hypot(x-o.tx, y-o.ty)
			errs = append(errs, e)
			studyLog(t, "#%-2d Fehler %4.2f mm | Rest an Loesung %5.0f ns, an Fotolage %5.0f ns", o.ref.ShotNo, e, rms(m.shotResiduals(o, x, y, nil)), rms(m.shotResiduals(o, o.tx, o.ty, nil)))
		}
		studyLog(t, "Zone %c in-sample %s", z, statsOf(errs, 0))
	}
}

// TestStudyZoneExport: je Zone Kalibrierung mit gemeinsamer (fester)
// Geometrie - Schall, Mic-Offsets, Verschiebung - als Simulator-Config.
func TestStudyZoneExport(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	b, _ := os.ReadFile(filepath.Join("study", "data", "params_hwcapture.json"))
	shots := loadSessionRef(t, zoneSession, "_circle")
	free := freeSet("SOUND", "OFS1", "OFS2", "OFS3", "OFS4", "OFS5", "OFFX", "OFFY")
	for _, z := range []string{"A", "B", "C"} {
		var zs []refShot
		for _, r := range shots {
			if zoneByShot(r.ShotNo)[:1] == z {
				zs = append(zs, r)
			}
		}
		p := fitSimParams(zs, user, free)
		var raw map[string]any
		json.Unmarshal(b, &raw)
		raw["sound_mps"] = math.Round(p.SoundMps*10) / 10
		raw["mic_offset_ns"] = p.MicOffsetNs
		raw["offset_x_um"] = p.OffsetXUm
		raw["offset_y_um"] = p.OffsetYUm
		out, _ := json.Marshal(raw)
		os.WriteFile(filepath.Join("study", "data", "zone_"+z+".json"), out, 0o644)
		studyLog(t, "Zone %s: %s | %s", z, paramStr(p), statsOf(simErrs(zs, p), 0))
	}
}

// TestStudyZoneLoudness: Lautstaerke-Hinweise je Zone (Serie 0342fdff):
// Pulsbreite der ersten Flanke, Anzahl Flanken in 300 us, Gesamtdauer des
// Signals (letzte Flanke im Fenster) - und Verzoegerung gegen Abstand.
func TestStudyZoneLoudness(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	shots := loadSessionRef(t, zoneSession, "_circle")
	m := nominalModel(user)
	type acc struct{ w, n, dur, d, res []float64 }
	zs := map[string]*acc{}
	for _, r := range shots {
		z := zoneByShot(r.ShotNo)
		if zs[z] == nil {
			zs[z] = &acc{}
		}
		a := zs[z]
		o := obsOf(r, firstEdge)
		raw := r.Sh.Raw
		if raw == nil || !raw.hasEdges() {
			continue
		}
		// Rest an der Fotolage mit Nutzer-Modell, je Mic gegen Abstand
		res := m.shotResiduals(o, r.TX, r.TY, nil)
		k := 0
		for i := 0; i < 6; i++ {
			if !o.seen[i] {
				continue
			}
			ps := pulsesOf(raw, i, 150000)
			cnt, last, w1 := 0.0, 0.0, -1.0
			for _, p := range ps {
				if p.r >= o.t[i]-1 && p.r <= o.t[i]+300000 {
					cnt++
					last = p.r - o.t[i]
					if w1 < 0 {
						w1 = p.w
					}
				}
			}
			a.w = append(a.w, w1/1000)
			a.n = append(a.n, cnt)
			a.dur = append(a.dur, last/1000)
			d := math.Sqrt(math.Pow(math.Hypot(r.TX-m.MX[i], r.TY-m.MY[i]), 2) + m.MZ[i]*m.MZ[i])
			a.d = append(a.d, d)
			a.res = append(a.res, res[k]/1000)
			k++
		}
	}
	med := func(v []float64) float64 { s := append([]float64{}, v...); sort.Float64s(s); return s[len(s)/2] }
	studyLog(t, "== Lautstaerke-Hinweise je Zone (Serie 0342fdff, je Mic und Schuss)")
	for _, z := range []string{"A Loch (nur Band)", "B neben Band", "C Band+Vorsatz"} {
		a := zs[z]
		_, slope, _, r := linFit(a.d, a.res)
		studyLog(t, "%-18s Breite 1. Puls Median %4.0f us | Flanken in 300us Median %2.0f | Signaldauer Median %4.0f us | Laufzeitrest vs Abstand %+.3f us/mm (r=%+.2f)",
			z, med(a.w), med(a.n), med(a.dur), slope, r)
	}
}

// TestStudyZoneSeparable: laesst sich die Zone je Schuss allein aus dem
// Signal erkennen (Median ueber die 6 Mics: Signaldauer, Flankenzahl)?
func TestStudyZoneSeparable(t *testing.T) {
	shots := loadSessionRef(t, zoneSession, "_circle")
	med := func(v []float64) float64 { s := append([]float64{}, v...); sort.Float64s(s); return s[len(s)/2] }
	studyLog(t, "== Zone je Schuss aus dem Signal (Median ueber Mics)")
	for _, r := range shots {
		o := obsOf(r, firstEdge)
		var dur, cnt []float64
		for i := 0; i < 6; i++ {
			if !o.seen[i] {
				continue
			}
			c, last := 0.0, 0.0
			for _, p := range pulsesOf(r.Sh.Raw, i, 150000) {
				if p.r >= o.t[i]-1 && p.r <= o.t[i]+300000 {
					c++
					last = p.r - o.t[i]
				}
			}
			dur, cnt = append(dur, last/1000), append(cnt, c)
		}
		studyLog(t, "#%-2d %-18s Signaldauer %4.0f us  Flanken %2.0f", r.ShotNo, zoneByShot(r.ShotNo), med(dur), med(cnt))
	}
}

// signalClass: "leise" = kurzes Signal (Median-Signaldauer < 200 us).
func signalClass(r refShot) string {
	o := obsOf(r, firstEdge)
	var dur []float64
	for i := 0; i < 6; i++ {
		if !o.seen[i] || r.Sh.Raw == nil {
			continue
		}
		last := 0.0
		for _, p := range pulsesOf(r.Sh.Raw, i, 150000) {
			if p.r >= o.t[i]-1 && p.r <= o.t[i]+300000 {
				last = p.r - o.t[i]
			}
		}
		dur = append(dur, last)
	}
	sort.Float64s(dur)
	if len(dur) > 0 && dur[len(dur)/2] < 200000 {
		return "kurz"
	}
	return "lang"
}

// TestStudyClassCal: Kalibrierung je Signalklasse (kurz/lang) statt je Zone,
// Leave-one-out, Geometrie fest.
func TestStudyClassCal(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	shots := loadSessionRef(t, zoneSession, "_circle")
	free := freeSet("SOUND", "OFS1", "OFS2", "OFS3", "OFS4", "OFS5", "OFFX", "OFFY")
	groupings := map[string]func(r refShot) string{
		"Zone":         func(r refShot) string { return zoneByShot(r.ShotNo)[:1] },
		"Signalklasse": signalClass,
		"Zone A+Klasse": func(r refShot) string {
			if zoneByShot(r.ShotNo)[0] == 'A' {
				return "A-" + signalClass(r)
			}
			return zoneByShot(r.ShotNo)[:1]
		},
		"eine Gruppe": func(r refShot) string { return "alle" },
	}
	studyLog(t, "== Kalibrierung je Gruppe (Leave-one-out, Geometrie fest), Serie 0342fdff")
	for _, gname := range []string{"eine Gruppe", "Zone", "Signalklasse", "Zone A+Klasse"} {
		g := groupings[gname]
		groups := map[string][]refShot{}
		for _, r := range shots {
			groups[g(r)] = append(groups[g(r)], r)
		}
		var all []float64
		var keys []string
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			zs := groups[k]
			var loo []float64
			for i := range zs {
				var tr []refShot
				for j := range zs {
					if j != i {
						tr = append(tr, zs[j])
					}
				}
				loo = append(loo, simErrs(zs[i:i+1], fitSimParams(tr, user, free))...)
			}
			all = append(all, loo...)
			s := statsOf(loo, 0)
			parts = append(parts, fmt.Sprintf("%s(n=%d) <1mm %.0f%% P90 %.2f", k, len(zs), s.Below1*100, s.P90))
		}
		studyLog(t, "%-14s gesamt %s | %s", gname, statsOf(all, 0), strings.Join(parts, "; "))
	}
}

// TestStudyOneClick: produktive Zonen-Kalibrierung (ZoneCalibrate) an der
// Serie 0342fdff, mit Laufzeit und Leave-one-out-Kontrolle.
func TestStudyOneClick(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	shots := loadSessionRef(t, zoneSession, "_circle")
	var in []zoneCalInput
	for _, r := range shots {
		in = append(in, zoneCalInput{Raw: r.Sh, TruthX: r.TX, TruthY: r.TY})
	}
	zones := ZoneParams{HoleRadiusMM: 29.75, BandHalfMM: 45}
	opt := PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true}
	t0 := time.Now()
	rep, err := ZoneCalibrate(in, user, opt, zones)
	if err != nil {
		t.Fatal(err)
	}
	st := func(s zoneCalStats) string {
		return fmt.Sprintf("n=%2d <1mm %3.0f%% P50 %.2f P90 %.2f RMS %.2f", s.N, s.Below1*100, s.P50MM, s.P90MM, s.RMSMM)
	}
	studyLog(t, "== One-Click Zonen-Kalibrierung (Lochradius %.2f) Laufzeit %s", zones.HoleRadiusMM, time.Since(t0).Round(time.Millisecond))
	studyLog(t, "vorher %s | nur Basissatz %s | mit Zonen %s", st(rep.Before), st(rep.BaseOnly), st(rep.After))
	for _, z := range rep.Zones {
		studyLog(t, "Zone %s (%2d Schuss, %s): %s -> %s", z.Zone, z.Shots, z.Fitted, st(z.Before), st(z.After))
	}
	// Leave-one-out: jeden Schuss weglassen, kalibrieren, an ihm messen
	for _, v := range []struct {
		lm    bool
		full  int
		ridge float64
	}{{true, 6, 0}, {true, 6, 3000}, {true, 6, 6000}, {true, 6, 12000}} {
		zoneFullFitMin, zoneRidgeNs = v.full, v.ridge
		per := map[string][]float64{}
		var loo []float64
		for k := range in {
			var tr []zoneCalInput
			tr = append(tr, in[:k]...)
			tr = append(tr, in[k+1:]...)
			r, err := ZoneCalibrate(tr, user, opt, zones)
			if err != nil {
				t.Fatal(err)
			}
			res := SolveSimShot(in[k].Raw, r.Params)
			e := math.Hypot(float64(res.XUm)/1000-in[k].TruthX, float64(res.YUm)/1000-in[k].TruthY)
			loo = append(loo, e)
			z := zones.ZoneOf(in[k].TruthX, in[k].TruthY)
			per[z] = append(per[z], e)
		}
		studyLog(t, "Leave-one-out, Ridge %5.0f ns: gesamt %s", v.ridge, statsOf(loo, 0))
		for _, z := range []string{"A", "B", "C"} {
			studyLog(t, "      Zone %s %s", z, statsOf(per[z], 0))
		}
	}
	zoneFullFitMin, zoneRidgeNs = 6, 12000
}

// ---------------------------------------------------------------------------
// Kalibrierung OHNE Foto: Selbstkalibrierung ueber die innere
// Widerspruchsfreiheit, Zonen aus der Lage der ersten Rechnung.
// ---------------------------------------------------------------------------

// selfFixC: Schallgeschwindigkeit nicht anpassen (physikalisch vorgegeben).
var selfFixC bool

// selfCalZoned: Buendelausgleich (Lagen frei) mit gemeinsamen Offsets und
// je nach Variante Schall/Offsets je Zone; ridgeNs bindet Zonen-Offsets an 0,
// ridgeOfs die gemeinsamen Offsets an den Startwert.
func selfCalZoned(m physModel, obs []shotObs, start [][2]float64, zoneSound, zoneOfs, commonOfs bool, ridgeNs, ridgeOfs float64) physModel {
	var ps []modelParam
	if commonOfs {
		for i := 1; i < 6; i++ {
			i := i
			ps = append(ps, modelParam{fmt.Sprintf("OFS%d", i), func(m *physModel) *float64 { return &m.Ofs[i] }, 200})
		}
	}
	if zoneSound {
		for z := 0; z < 3; z++ {
			z := z
			m.ZC[z] = m.C
			ps = append(ps, modelParam{fmt.Sprintf("ZC%d", z), func(m *physModel) *float64 { return &m.ZC[z] }, 0.000002})
		}
	} else if !selfFixC {
		ps = append(ps, modelParam{"C", func(m *physModel) *float64 { return &m.C }, 0.000002})
	}
	if zoneOfs {
		for z := 0; z < 3; z++ {
			for i := 1; i < 6; i++ {
				z, i := z, i
				ps = append(ps, modelParam{fmt.Sprintf("ZOFS%d_%d", z, i), func(m *physModel) *float64 { return &m.ZOfs[z][i] }, 200})
			}
		}
	}
	ofs0 := m.Ofs
	out, _ := fitModelR(m, ps, func(m *physModel) []float64 {
		var r []float64
		for k, o := range obs {
			x, y, ok := m.locate(o, start[k][0], start[k][1], 3000)
			if !ok {
				x, y = start[k][0], start[k][1]
			}
			r = m.shotResiduals(o, x, y, r)
		}
		r = huberize(r, 3000)
		if zoneOfs && ridgeNs > 0 {
			for z := 0; z < 3; z++ {
				for i := 1; i < 6; i++ {
					r = append(r, m.ZOfs[z][i]*1000/ridgeNs)
				}
			}
		}
		if commonOfs && ridgeOfs > 0 {
			for i := 1; i < 6; i++ {
				r = append(r, (m.Ofs[i]-ofs0[i])*1000/ridgeOfs)
			}
		}
		return r
	})
	return out
}

// simFromPhys: Simulator-Parameter (mit Zonen) aus einem Zonen-Modell.
func simFromPhys(base SimParams, m physModel, zoned bool, holeR float64) SimParams {
	p := base
	p.SoundMps = math.Round(m.C*1e7) / 10
	for i := 0; i < 6; i++ {
		p.MicOffsetNs[i] = int64(math.Round(m.Ofs[i] - m.Ofs[0]))
	}
	if !zoned {
		p.Zones = nil
		return p
	}
	zp := &ZoneParams{Enabled: true, HoleRadiusMM: holeR, BandHalfMM: 45}
	for z := 0; z < 3; z++ {
		c := m.C
		if m.ZC[z] > 0 {
			c = m.ZC[z]
		}
		set := &ZoneSet{SoundMps: math.Round(c*1e7) / 10, OffsetXUm: base.OffsetXUm, OffsetYUm: base.OffsetYUm}
		for i := 0; i < 6; i++ {
			set.MicOffsetNs[i] = int64(math.Round(m.Ofs[i] + m.ZOfs[z][i] - m.Ofs[0] - m.ZOfs[z][0]))
		}
		switch z {
		case 0:
			zp.A = set
		case 1:
			zp.B = set
		case 2:
			zp.C = set
		}
	}
	p.Zones = zp
	return p
}

// shiftFreeErrs: Fehler gegen das Foto nach Abzug des mittleren x/y-
// Versatzes (robust: zweiter Durchgang ohne Fehler > 3 mm); je Zone.
func shiftFreeErrs(shots []refShot, p SimParams, zp *ZoneParams) (all []float64, per map[string][]float64, dx, dy float64) {
	type pt struct{ x, y, tx, ty float64 }
	var pts []pt
	for _, r := range shots {
		res := SolveSimShot(r.Sh, p)
		if res.PosValid {
			pts = append(pts, pt{float64(res.XUm) / 1000, float64(res.YUm) / 1000, r.TX, r.TY})
		}
	}
	for pass := 0; pass < 2; pass++ {
		var sx, sy float64
		n := 0
		for _, q := range pts {
			if pass == 1 && math.Hypot(q.x+dx-q.tx, q.y+dy-q.ty) > 3 {
				continue
			}
			sx += q.tx - q.x
			sy += q.ty - q.y
			n++
		}
		if n > 0 {
			dx, dy = sx/float64(n), sy/float64(n)
		}
	}
	per = map[string][]float64{}
	for _, q := range pts {
		e := math.Hypot(q.x+dx-q.tx, q.y+dy-q.ty)
		all = append(all, e)
		z := zp.ZoneOf(q.tx, q.ty)
		per[z] = append(per[z], e)
	}
	return
}

func TestStudyNoPhoto(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	type ds struct {
		sid   string
		holeR float64
	}
	for _, d := range []ds{{zoneSession, 29.75}} {
		ref := loadSessionRef(t, d.sid, "")
		allObs := loadShotsNoTruth(t, d.sid)
		zp := &ZoneParams{Enabled: true, HoleRadiusMM: d.holeR, BandHalfMM: 45}
		var start [][2]float64
		for k := range allObs {
			res := SolveSimShot(allObs[k].ref.Sh, user)
			x, y := float64(res.XUm)/1000, float64(res.YUm)/1000
			start = append(start, [2]float64{x, y})
			allObs[k].z = map[string]int{"A": 1, "B": 2, "C": 3}[zp.ZoneOf(x, y)]
		}
		report := func(name string, p SimParams) {
			all, per, dx, dy := shiftFreeErrs(ref, p, zp)
			studyLog(t, "%-44s | Versatz %+5.2f/%+5.2f | %s", name, dx, dy, statsOf(all, 0))
			var parts []string
			for _, z := range []string{"A", "B", "C"} {
				s := statsOf(per[z], 0)
				parts = append(parts, fmt.Sprintf("%s <1mm %3.0f%% P90 %.2f", z, s.Below1*100, s.P90))
			}
			studyLog(t, "%-44s | %s", "", strings.Join(parts, " · "))
		}
		studyLog(t, "== Kalibrierung ohne Foto, Serie %s (%d Schuss, Bewertung nach Abzug eines gemeinsamen x/y-Versatzes)", d.sid[:8], len(allObs))
		report("Nutzer-Kalibrierung HWCapture", user)
		// Referenz: One-Click mit Foto (in-sample)
		var in []zoneCalInput
		for _, r := range ref {
			in = append(in, zoneCalInput{Raw: r.Sh, TruthX: r.TX, TruthY: r.TY})
		}
		if rep, err := ZoneCalibrate(in, user, PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true}, *zp); err == nil {
			report("ZIEL: One-Click mit Foto", rep.Params)
		}
		// bestehende Firmware-Kalibrierung CAL START (Offsets aus Rest-Fehler)
		var airs [][6][]int64
		for _, o := range allObs {
			airs = append(airs, o.ref.Sh.airFor(user))
		}
		ofs, _ := CalibrateMicOffsets(airs, user)
		pc := user
		pc.MicOffsetNs = ofs
		report("CAL START (Offsets, Rest-Fehler)", pc)

		base := nominalModel(user)
		variants := []struct {
			name                 string
			zSound, zOfs, common bool
			ridge, ridgeOfs      float64
		}{
			{"Selbst: Offsets + Schall", false, false, true, 0, 0},
			{"Selbst: Offsets + Schall je Zone", true, false, true, 0, 0},
			{"Selbst: Schall je Zone (Offsets fest)", true, false, false, 0, 0},
			{"Selbst: Offsets + Schall + Zonen-Offsets r6us", true, true, true, 6000, 0},
			{"Selbst: Offsets + Schall + Zonen-Offsets r3us", true, true, true, 3000, 0},
			{"Selbst: Schall je Zone + Zonen-Offsets r3us", true, true, false, 3000, 0},
			{"Schall fest: Offsets", false, false, true, 0, 0},
			{"Schall fest: Offsets + Zonen-Offsets r3us", false, true, true, 3000, 0},
			{"Schall fest: Offsets + Zonen-Offsets r1.5us", false, true, true, 1500, 0},
			{"Schall fest: Offsets r3us + Zonen-Offs r1.5us", false, true, true, 1500, 3000},
		}
		for _, v := range variants {
			selfFixC = strings.HasPrefix(v.name, "Schall fest")
			t0 := time.Now()
			m := selfCalZoned(base, allObs, start, v.zSound, v.zOfs, v.common, v.ridge, v.ridgeOfs)
			p := simFromPhys(user, m, v.zSound || v.zOfs, d.holeR)
			report(fmt.Sprintf("%s (%.1fs)", v.name, time.Since(t0).Seconds()), p)
			selfFixC = false
			if p.Zones != nil {
				studyLog(t, "%-44s | Schall A/B/C %.1f/%.1f/%.1f", "", p.Zones.A.SoundMps, p.Zones.B.SoundMps, p.Zones.C.SoundMps)
			}
		}
	}
}

// TestStudyTransfer: One-Click-Kalibrierung an einer Serie, angewendet auf
// andere Serien (andere Scheibe/anderer Tag) - Bewertung nach Abzug eines
// gemeinsamen x/y-Versatzes.
func TestStudyTransfer(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	type ds struct {
		name, sid string
		holeR     float64
	}
	sets := []ds{{"LP 12.10. 0342fdff", zoneSession, 29.75}, {"LG 11.10. acc1e961", studySessions[0], 15.25}, {"LG 11.10. 6d11a607", studySessions[1], 15.25}}
	opt := PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true}
	studyLog(t, "== Uebertragbarkeit der Zonen-Kalibrierung (nach Abzug x/y-Versatz)")
	for _, src := range sets {
		ref := loadSessionRef(t, src.sid, "")
		var in []zoneCalInput
		for _, r := range ref {
			in = append(in, zoneCalInput{Raw: r.Sh, TruthX: r.TX, TruthY: r.TY})
		}
		rep, err := ZoneCalibrate(in, user, opt, ZoneParams{HoleRadiusMM: src.holeR, BandHalfMM: 45})
		if err != nil {
			studyLog(t, "%s: %v", src.name, err)
			continue
		}
		for _, dst := range sets {
			dref := loadSessionRef(t, dst.sid, "")
			p := rep.Params
			zp := *p.Zones
			zp.HoleRadiusMM = dst.holeR
			p.Zones = &zp
			all, _, dx, dy := shiftFreeErrs(dref, p, &zp)
			base := rep.Params
			base.Zones = nil
			allB, _, _, _ := shiftFreeErrs(dref, base, &zp)
			allU, _, _, _ := shiftFreeErrs(dref, user, &zp)
			studyLog(t, "Kal. %-19s -> %-19s | Zonen %s (Versatz %+.2f/%+.2f) | nur Basissatz <1mm %.0f%% | HWCapture <1mm %.0f%%",
				src.name, dst.name, statsOf(all, 0), dx, dy, statsOf(allB, 0).Below1*100, statsOf(allU, 0).Below1*100)
		}
	}
}

// TestStudyUserOneClick: die vom Nutzer per One-Click gespeicherte
// Kalibrierung (22:22) an allen Fotoschuessen.
func TestStudyUserOneClick(t *testing.T) {
	p := loadParams(t, "user_oneclick_2222.json")
	ref := loadSessionRef(t, zoneSession, "")
	studyLog(t, "== Nutzer-One-Click 22:22 an allen Fotoschuessen")
	for _, r := range ref {
		res := SolveSimShot(r.Sh, p)
		base := p
		base.Zones = nil
		rb := SolveSimShot(r.Sh, base)
		e := math.Hypot(float64(res.XUm)/1000-r.TX, float64(res.YUm)/1000-r.TY)
		eb := math.Hypot(float64(rb.XUm)/1000-r.TX, float64(rb.YUm)/1000-r.TY)
		flag := ""
		if e > 3 {
			flag = "  <<<"
		}
		studyLog(t, "#%-2d Foto %6.1f/%6.1f Zone %s | mit Zonen %6.1f/%6.1f Fehler %5.2f | nur Basis Fehler %5.2f%s", r.ShotNo, r.TX, r.TY, res.Zone,
			float64(res.XUm)/1000, float64(res.YUm)/1000, e, eb, flag)
	}
}

// TestStudyBulletShift: Einfluss Kugeldurchmesser-Korrektur / Endpositions-
// Verfahren auf die One-Click-Kalibrierung (Fotowahrheit, alle 29 Schuesse).
func TestStudyBulletShift(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	ref := loadSessionRef(t, zoneSession, "")
	var in []zoneCalInput
	for _, r := range ref {
		in = append(in, zoneCalInput{Raw: r.Sh, TruthX: r.TX, TruthY: r.TY})
	}
	zones := ZoneParams{HoleRadiusMM: 29.75, BandHalfMM: 45}
	opt := PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true}
	type v struct {
		name string
		f    func(p *SimParams)
	}
	vs := []v{
		{"HWCapture (BSHIFT 30, CLUSTERCORR, ITER 5)", func(p *SimParams) {}},
		{"BSHIFT 0", func(p *SimParams) { p.BulletShiftPct = 0 }},
		{"BSHIFT 50", func(p *SimParams) { p.BulletShiftPct = 50 }},
		{"BSHIFT 100", func(p *SimParams) { p.BulletShiftPct = 100 }},
		{"ohne CLUSTERCORR/ITER", func(p *SimParams) { p.PosAlgo.CorrCluster = false; p.PosAlgo.Iterations = 0 }},
		{"ohne CLUSTERCORR/ITER, BSHIFT 0", func(p *SimParams) { p.PosAlgo.CorrCluster = false; p.PosAlgo.Iterations = 0; p.BulletShiftPct = 0 }},
		{"LSQ (Lochrand 2,25)", func(p *SimParams) { p.PosAlgo.LSQ = true; p.PosAlgo.LSQPelletRMm = 2.25 }},
		{"LSQ Lochrand 0 (Mitte)", func(p *SimParams) { p.PosAlgo.LSQ = true; p.PosAlgo.LSQPelletRMm = 0.001 }},
	}
	studyLog(t, "== Kugeldurchmesser-Korrektur / Endposition bei der One-Click-Kalibrierung (Serie 0342fdff)")
	for _, x := range vs {
		p := user
		x.f(&p)
		rep, err := ZoneCalibrate(in, p, opt, zones)
		if err != nil {
			studyLog(t, "%s: %v", x.name, err)
			continue
		}
		var loo []float64
		for k := range in {
			tr := append(append([]zoneCalInput{}, in[:k]...), in[k+1:]...)
			r, err := ZoneCalibrate(tr, p, opt, zones)
			if err != nil {
				continue
			}
			res := SolveSimShot(in[k].Raw, r.Params)
			loo = append(loo, math.Hypot(float64(res.XUm)/1000-in[k].TruthX, float64(res.YUm)/1000-in[k].TruthY))
		}
		studyLog(t, "%-42s nur Basis <1mm %3.0f%% | Zonen <1mm %3.0f%% P90 %.2f | LOO %s", x.name, rep.BaseOnly.Below1*100, rep.After.Below1*100, rep.After.P90MM, statsOf(loo, 0))
	}
}

// TestStudyOneClickRobust: Nutzer-Situation 22:22 nachstellen - Standard-
// Startwerte, nur 20 Paare aus dem Browser, Server ordnet selbst zu.
func TestStudyOneClickRobust(t *testing.T) {
	ref := loadSessionRef(t, zoneSession, "")
	b, _ := os.ReadFile(filepath.Join("study", "data", "truth_"+zoneSession+".json"))
	var tr struct {
		Holes [][2]float64 `json:"holes_mm"`
	}
	json.Unmarshal(b, &tr)
	truthByNo := map[int]refShot{}
	for _, r := range ref {
		truthByNo[r.ShotNo] = r
	}
	st, _ := NewStore(context.Background(), studyDSN(t))
	raw, _ := st.SessionShotsRaw(context.Background(), zoneSession)
	for _, startName := range []string{"Standard", "HWCapture"} {
		p := DefaultSimParams()
		if startName == "HWCapture" {
			p = loadParams(t, "params_hwcapture.json")
		}
		var initial []zoneCalInput
		for _, r := range ref {
			if (r.ShotNo <= 17 || (r.ShotNo >= 26 && r.ShotNo <= 28)) && r.ShotNo != 11 {
				initial = append(initial, zoneCalInput{Raw: r.Sh, TruthX: r.TX, TruthY: r.TY})
			}
		}
		var cands []SimShotRaw
		for _, s := range raw {
			if s.HasRaw && s.ShotNo != 11 {
				cands = append(cands, s)
			}
		}
		opt := PhotoCalOptions{MicOffsets: true, Offset: true}
		t0 := time.Now()
		in, pairs := zoneAssign(cands, tr.Holes, initial, p, opt)
		tAssign := time.Since(t0)
		wrong := 0
		for _, pr := range pairs {
			if r, ok := truthByNo[pr.ShotNo]; ok && math.Hypot(r.TX-pr.XMM, r.TY-pr.YMM) > 0.5 {
				wrong++
				studyLog(t, "  abweichende Zuordnung Schuss %d: Loch %.1f/%.1f statt %.1f/%.1f", pr.ShotNo, pr.XMM, pr.YMM, r.TX, r.TY)
			}
		}
		rep, err := ZoneCalibrate(in, p, opt, ZoneParams{HoleRadiusMM: 29.75, BandHalfMM: 45})
		if err != nil {
			t.Fatal(err)
		}
		var errs []float64
		worst := 0.0
		for _, r := range ref {
			res := SolveSimShot(r.Sh, rep.Params)
			e := math.Hypot(float64(res.XUm)/1000-r.TX, float64(res.YUm)/1000-r.TY)
			errs = append(errs, e)
			worst = math.Max(worst, e)
		}
		studyLog(t, "== Start %s: %d Paare (von 20), davon %d abweichend von der Handzuordnung; Zuordnung %s, gesamt %s", startName, len(pairs), wrong, tAssign.Round(time.Millisecond), time.Since(t0).Round(time.Millisecond))
		for _, z := range rep.Zones {
			c := "–"
			if z.CheckMM != nil {
				c = fmt.Sprintf("%.2f", *z.CheckMM)
			}
			studyLog(t, "   Zone %s %2d Schuss: %s (Kreuzpruefung Ø %s mm)", z.Zone, z.Shots, z.Fitted, c)
		}
		studyLog(t, "   alle 29 Fotoschuesse: %s", statsOf(errs, 0))
	}
}

// TestStudySigma9: Schnittpunkt-Cluster Σ9 mit/ohne Zonen-Kalibrierung und
// sein Zusammenhang mit dem Fotofehler.
func TestStudySigma9(t *testing.T) {
	ref := loadSessionRef(t, zoneSession, "")
	hw := loadParams(t, "params_hwcapture.json")
	fz := loadParams(t, "params_fotozonen.json")
	fzBase := fz
	fzBase.Zones = nil
	studyLog(t, "== Σ9 (roh / korrigiert, mm) gegen Fotofehler, Serie 0342fdff")
	for _, c := range []struct {
		name string
		p    SimParams
	}{{"HWCapture", hw}, {"Foto-Zonen nur Basissatz", fzBase}, {"Foto-Zonen mit Zonen", fz}} {
		var s9, s9c, errs []float64
		for _, r := range ref {
			res := SolveSimShot(r.Sh, c.p)
			if !res.PosValid || res.ClusterSumUm == nil {
				continue
			}
			s9 = append(s9, float64(*res.ClusterSumUm)/1000)
			s9c = append(s9c, float64(*res.ClusterSumCorrUm)/1000)
			errs = append(errs, math.Hypot(float64(res.XUm)/1000-r.TX, float64(res.YUm)/1000-r.TY))
		}
		med := func(v []float64) float64 { s := append([]float64{}, v...); sort.Float64s(s); return s[len(s)/2] }
		_, _, _, rr := linFit(s9c, errs)
		studyLog(t, "%-26s Σ9 roh Median %5.1f · korr. Median %5.1f · Fotofehler Median %.2f mm · Korrelation Σ9korr/Fehler r=%+.2f", c.name, med(s9), med(s9c), med(errs), rr)
	}
}

// TestStudyAssignDebug: Server-Zuordnung mit HWCapture, ohne Ausschluss.
func TestStudyAssignDebug(t *testing.T) {
	ref := loadSessionRef(t, zoneSession, "")
	b, _ := os.ReadFile(filepath.Join("study", "data", "truth_"+zoneSession+".json"))
	var tr struct {
		Holes [][2]float64 `json:"holes_mm"`
	}
	json.Unmarshal(b, &tr)
	st, _ := NewStore(context.Background(), studyDSN(t))
	raw, _ := st.SessionShotsRaw(context.Background(), zoneSession)
	p := loadParams(t, "params_hwcapture.json")
	var initial []zoneCalInput
	for _, r := range ref {
		if r.ShotNo <= 17 {
			initial = append(initial, zoneCalInput{Raw: r.Sh, TruthX: r.TX, TruthY: r.TY})
		}
	}
	var cands []SimShotRaw
	for _, s := range raw {
		if s.HasRaw {
			cands = append(cands, s)
		}
	}
	truth := map[int]refShot{}
	for _, r := range ref {
		truth[r.ShotNo] = r
	}
	_, pairs := zoneAssign(cands, tr.Holes, initial, p, PhotoCalOptions{MicOffsets: true, Offset: true})
	got := map[int]bool{}
	for _, pr := range pairs {
		got[pr.ShotNo] = true
		ok := "ok"
		if r, k := truth[pr.ShotNo]; !k || math.Hypot(r.TX-pr.XMM, r.TY-pr.YMM) > 0.5 {
			ok = "ABWEICHEND"
		}
		studyLog(t, "#%-2d Loch %6.1f/%6.1f Sim %6.1f/%6.1f d=%.1f %s", pr.ShotNo, pr.XMM, pr.YMM, pr.SimX, pr.SimY, math.Hypot(pr.XMM-pr.SimX, pr.YMM-pr.SimY), ok)
	}
	for _, s := range cands {
		if !got[s.ShotNo] {
			studyLog(t, "#%-2d ohne Loch", s.ShotNo)
		}
	}
}

const session1310 = "ca5817a6-a6b8-465c-affd-d42066ae476b"

// TestStudyAssign1310: Loecher der Serie vom 13.10. zuordnen - (a) optimal mit
// der Zonen-Kalibrierung vom 12.10. (Foto-Zonen 22:42), (b) Server-Verfahren
// (zoneAssign) ab HWCapture. Schreibt truth_<sid>.json mit (a) neu.
func TestStudyAssign1310(t *testing.T) {
	path := filepath.Join("study", "data", "truth_"+session1310+".json")
	b, _ := os.ReadFile(path)
	var tr map[string]any
	json.Unmarshal(b, &tr)
	var holes [][2]float64
	for _, h := range tr["holes_mm"].([]any) {
		hh := h.([]any)
		holes = append(holes, [2]float64{hh[0].(float64), hh[1].(float64)})
	}
	st, _ := NewStore(context.Background(), studyDSN(t))
	raw, _ := st.SessionShotsRaw(context.Background(), session1310)
	fz := loadParams(t, "params_fotozonen.json")
	var cands []SimShotRaw
	for _, s := range raw {
		if s.HasRaw {
			cands = append(cands, s)
		}
	}
	pos := make([][2]float64, len(cands))
	valid := make([]bool, len(cands))
	for i, s := range cands {
		r := SolveSimShot(s, fz)
		valid[i] = r.PosValid
		pos[i] = [2]float64{float64(r.XUm) / 1000, float64(r.YUm) / 1000}
	}
	asg := assignHolesOptimal(pos, valid, holes, 8)
	_, srv := zoneAssign(cands, holes, nil, loadParams(t, "params_hwcapture.json"), PhotoCalOptions{MicOffsets: true, Offset: true})
	srvBy := map[int]int{}
	for _, p := range srv {
		srvBy[p.ShotNo] = p.Hole
	}
	var pairs []map[string]any
	studyLog(t, "== Zuordnung Serie 13.10. (ca5817a6): Zonen-Kal. 12.10. optimal vs. Server ab HWCapture")
	for i, s := range cands {
		j, ok := asg[i]
		if !ok {
			studyLog(t, "#%-2d ohne Loch (Sim %.1f/%.1f)", s.ShotNo, pos[i][0], pos[i][1])
			continue
		}
		d := math.Hypot(pos[i][0]-holes[j][0], pos[i][1]-holes[j][1])
		second := math.Inf(1)
		for k, h := range holes {
			if k != j {
				second = math.Min(second, math.Hypot(pos[i][0]-h[0], pos[i][1]-h[1]))
			}
		}
		same := "gleich"
		if h, ok := srvBy[s.ShotNo]; !ok {
			same = "Server: kein Loch"
		} else if h != j {
			same = fmt.Sprintf("Server: anderes Loch %d", h)
		}
		studyLog(t, "#%-2d Loch %2d %6.1f/%6.1f  Abstand %.2f  zweitnaechstes %.1f  | %s", s.ShotNo, j, holes[j][0], holes[j][1], d, second, same)
		pairs = append(pairs, map[string]any{"shot_no": s.ShotNo, "x_mm": holes[j][0], "y_mm": holes[j][1], "dev_x": s.XMM, "dev_y": s.YMM, "ambiguous": second < d+1.5})
	}
	tr["pairs"] = pairs
	tr["note"] = "Zuordnung optimal (Hungarian) mit Foto-Zonen 22:42 vom 12.10., siehe TestStudyAssign1310"
	out, _ := json.MarshalIndent(tr, "", " ")
	os.WriteFile(path, out, 0o644)
}

// ---------------------------------------------------------------------------
// Vergleich aller Verfahren an der Serie vom 13.10. (und 12.10.)
// ---------------------------------------------------------------------------

func toZoneInputs(refs []refShot) []zoneCalInput {
	var in []zoneCalInput
	for _, r := range refs {
		in = append(in, zoneCalInput{Raw: r.Sh, TruthX: r.TX, TruthY: r.TY})
	}
	return in
}

// evalRefs: Fehler je Schuss (absolut) und je Zone; ungueltig = 20 mm.
func evalRefs(refs []refShot, p SimParams, zp *ZoneParams) (all []float64, per map[string][]float64) {
	per = map[string][]float64{}
	for _, r := range refs {
		res := SolveSimShot(r.Sh, p)
		e := 20.0
		if res.PosValid {
			e = math.Hypot(float64(res.XUm)/1000-r.TX, float64(res.YUm)/1000-r.TY)
		}
		all = append(all, e)
		z := zp.ZoneOf(r.TX, r.TY)
		per[z] = append(per[z], e)
	}
	return
}

func fmtZ(per map[string][]float64) string {
	var parts []string
	for _, z := range []string{"A", "B", "C"} {
		s := statsOf(per[z], 0)
		parts = append(parts, fmt.Sprintf("%s %3.0f%%/P90 %4.2f", z, s.Below1*100, s.P90))
	}
	return strings.Join(parts, " · ")
}

// looOneClick: jeder Schuss einmal weggelassen, mit den uebrigen per
// ZoneCalibrate kalibriert und an ihm gemessen.
func looOneClick(refs []refShot, base SimParams, opt PhotoCalOptions, zones ZoneParams, extra []refShot) ([]float64, map[string][]float64) {
	in := toZoneInputs(refs)
	var errs []float64
	per := map[string][]float64{}
	zp := zones
	zp.Enabled = true
	for k := range in {
		tr := append(append([]zoneCalInput{}, in[:k]...), in[k+1:]...)
		tr = append(tr, toZoneInputs(extra)...)
		rep, err := ZoneCalibrate(tr, base, opt, zones)
		e := 20.0
		if err == nil {
			res := SolveSimShot(in[k].Raw, rep.Params)
			if res.PosValid {
				e = math.Hypot(float64(res.XUm)/1000-in[k].TruthX, float64(res.YUm)/1000-in[k].TruthY)
			}
		}
		errs = append(errs, e)
		z := zp.ZoneOf(in[k].TruthX, in[k].TruthY)
		per[z] = append(per[z], e)
	}
	return errs, per
}

func TestStudyCompare1310(t *testing.T) {
	s13 := loadSessionRef(t, session1310, "")
	s12 := loadSessionRef(t, zoneSession, "")
	zones := ZoneParams{HoleRadiusMM: 29.75, BandHalfMM: 45}
	zp := zones
	zp.Enabled = true
	studyLog(t, "== VERGLEICH an Serie 13.10. (%d Fotoschuesse, Schuss 9 mehrdeutig/abgewaehlt), Fehler absolut", len(s13))

	// 1. gespeicherte Kalibrierungen Stand 1
	st, _ := NewStore(context.Background(), studyDSN(t))
	rows, err := st.pool.Query(context.Background(), `SELECT name, params FROM simulator_configs WHERE lane_no = 1 ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	type cfgRow struct {
		name string
		p    SimParams
	}
	var cfgs []cfgRow
	for rows.Next() {
		var name string
		var raw []byte
		rows.Scan(&name, &raw)
		var p SimParams
		if json.Unmarshal(raw, &p) == nil {
			cfgs = append(cfgs, cfgRow{name, p})
		}
	}
	rows.Close()
	studyLog(t, "-- 1. gespeicherte Kalibrierungen Stand 1 (unveraendert angewendet)")
	for _, c := range cfgs {
		all, per := evalRefs(s13, c.p, &zp)
		studyLog(t, "%-62.62s %s | %s", c.name, statsOf(all, 0), fmtZ(per))
	}

	user := loadParams(t, "params_hwcapture.json")
	opt := PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true}

	// 2. Uebertragung: One-Click an 12.10. -> 13.10.
	studyLog(t, "-- 2. One-Click (aktueller Code) an Serie 12.10. kalibriert, auf 13.10. angewendet")
	if rep, err := ZoneCalibrate(toZoneInputs(s12), user, opt, zones); err == nil {
		all, per := evalRefs(s13, rep.Params, &zp)
		studyLog(t, "%-62s %s | %s", "12.10. -> 13.10. mit Zonen", statsOf(all, 0), fmtZ(per))
		b := rep.Params
		b.Zones = nil
		all, per = evalRefs(s13, b, &zp)
		studyLog(t, "%-62s %s | %s", "12.10. -> 13.10. nur Basissatz", statsOf(all, 0), fmtZ(per))
	}
	if rep, err := ZoneCalibrate(toZoneInputs(s13), user, opt, zones); err == nil {
		all, per := evalRefs(s12, rep.Params, &zp)
		studyLog(t, "%-62s %s | %s", "Gegenprobe 13.10. -> 12.10. mit Zonen", statsOf(all, 0), fmtZ(per))
		for _, z := range rep.Zones {
			s := "Basissatz"
			if z.Set != nil {
				s = fmt.Sprintf("Schall %.1f OFS %v Versatz %d/%d", z.Set.SoundMps, z.Set.MicOffsetNs, z.Set.OffsetXUm, z.Set.OffsetYUm)
			}
			studyLog(t, "   13.10. Zone %s (%2d Schuss, %s): %s", z.Zone, z.Shots, z.Fitted, s)
		}
	}
	if rep, err := ZoneCalibrate(toZoneInputs(s12), user, opt, zones); err == nil {
		for _, z := range rep.Zones {
			s := "Basissatz"
			if z.Set != nil {
				s = fmt.Sprintf("Schall %.1f OFS %v Versatz %d/%d", z.Set.SoundMps, z.Set.MicOffsetNs, z.Set.OffsetXUm, z.Set.OffsetYUm)
			}
			studyLog(t, "   12.10. Zone %s (%2d Schuss, %s): %s", z.Zone, z.Shots, z.Fitted, s)
		}
	}

	// 3. Kalibrierverfahren an 13.10. selbst - Leave-one-out
	studyLog(t, "-- 3. Verfahren an Serie 13.10. kalibriert, Leave-one-out (jeder Schuss am Rest kalibriert)")
	type variant struct {
		name  string
		base  func() SimParams
		opt   PhotoCalOptions
		zoned bool
		extra []refShot
	}
	withP := func(f func(p *SimParams)) func() SimParams {
		return func() SimParams { p := user; f(&p); return p }
	}
	vs := []variant{
		{"One-Click Zonen (HWCapture-Start)", withP(func(p *SimParams) {}), opt, true, nil},
		{"One-Click Zonen + Daten 12.10.", withP(func(p *SimParams) {}), opt, true, s12},
		{"One-Click Zonen, Basis mit Geometrie", withP(func(p *SimParams) {}), PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true, Standoff: true, MicHalfX: true}, true, nil},
		{"One-Click Zonen, BSHIFT 0", withP(func(p *SimParams) { p.BulletShiftPct = 0 }), opt, true, nil},
		{"One-Click Zonen, BSHIFT 50", withP(func(p *SimParams) { p.BulletShiftPct = 50 }), opt, true, nil},
		{"One-Click Zonen, LSQ", withP(func(p *SimParams) { p.PosAlgo.LSQ = true }), opt, true, nil},
		{"One-Click Zonen, WALKK 0.5", withP(func(p *SimParams) { p.WalkK = 0.5 }), opt, true, nil},
		{"Foto-Kalibrierung ohne Zonen (Offsets+Versatz+Schall)", withP(func(p *SimParams) {}), opt, false, nil},
		{"Foto-Kalibrierung ohne Zonen, mit Geometrie", withP(func(p *SimParams) {}), PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true, Standoff: true, MicHalfX: true}, false, nil},
	}
	for _, v := range vs {
		t0 := time.Now()
		var errs []float64
		var per map[string][]float64
		if v.zoned {
			errs, per = looOneClick(s13, v.base(), v.opt, zones, v.extra)
		} else {
			per = map[string][]float64{}
			in := toZoneInputs(s13)
			for k := range in {
				var cal []PhotoCalShot
				b := v.base()
				for j, s := range in {
					if j != k {
						cal = append(cal, PhotoCalShot{ShotNo: s.Raw.ShotNo, AirNs: s.Raw.airFor(b), PiezoNs: s.Raw.PiezoNs, TruthX: s.TruthX, TruthY: s.TruthY})
					}
				}
				e := 20.0
				if rep, err := PhotoCalibrate(cal, b, v.opt); err == nil {
					if res := SolveSimShot(in[k].Raw, rep.Params); res.PosValid {
						e = math.Hypot(float64(res.XUm)/1000-in[k].TruthX, float64(res.YUm)/1000-in[k].TruthY)
					}
				}
				errs = append(errs, e)
				z := zp.ZoneOf(in[k].TruthX, in[k].TruthY)
				per[z] = append(per[z], e)
			}
		}
		studyLog(t, "%-52s %s | %s (%.0fs)", v.name, statsOf(errs, 0), fmtZ(per), time.Since(t0).Seconds())
	}

	// 4. Uebertragbarkeit zwischen den Tagen mit gemeinsamer Kalibrierung
	studyLog(t, "-- 4. One-Click an beiden Serien gemeinsam, Leave-one-out je Serie")
	e12, p12 := looOneClick(s12, user, opt, zones, s13)
	studyLog(t, "%-52s %s | %s", "Schuss aus 12.10. weggelassen", statsOf(e12, 0), fmtZ(p12))
}

// TestStudyNoise1310: innere Widerspruchsfreiheit und Vorpulse 12.10. vs 13.10.,
// dazu One-Click in-sample.
func TestStudyNoise1310(t *testing.T) {
	user := loadParams(t, "params_hwcapture.json")
	m := nominalModel(user)
	rms := func(v []float64) float64 { s := 0.0; for _, a := range v { s += a * a }; return math.Sqrt(s / float64(len(v))) }
	zones := ZoneParams{HoleRadiusMM: 29.75, BandHalfMM: 45}
	for _, sid := range []string{zoneSession, session1310} {
		refs := loadSessionRef(t, sid, "")
		var inner []float64
		pre, short := 0, 0
		for _, r := range refs {
			o := obsOf(r, firstEdge)
			res := SolveSimShot(r.Sh, user)
			x, y, ok := m.locate(o, float64(res.XUm)/1000, float64(res.YUm)/1000, 0)
			if ok {
				inner = append(inner, rms(m.shotResiduals(o, x, y, nil)))
			}
			for i := 0; i < 6; i++ {
				if !o.seen[i] || r.Sh.Raw == nil {
					continue
				}
				for _, p := range pulsesOf(r.Sh.Raw, i, 150000) {
					if p.r == o.t[i] && p.w > 0 && p.w < 30000 {
						pre++
					}
				}
			}
			if signalClass(r) == "kurz" {
				short++
			}
		}
		sort.Float64s(inner)
		rep, _ := ZoneCalibrate(toZoneInputs(refs), user, PhotoCalOptions{MicOffsets: true, Offset: true, SoundSpeed: true}, zones)
		studyLog(t, "%s: innerer Rest Median %4.0f ns P90 %4.0f · Vorpulse je Schuss %.2f · kurze Signale %d/%d · One-Click in-sample <1mm %.0f%% P90 %.2f",
			sid[:8], inner[len(inner)/2], inner[len(inner)*9/10], float64(pre)/float64(len(refs)), short, len(refs), rep.After.Below1*100, rep.After.P90MM)
	}
}

const session1410 = "5f0e815c-1343-4265-9b5f-aa3ba20e9a80"

// TestStudyOneClickTiming: One-Click wie im Browser (Server-Zuordnung mit
// allen Loechern) mit Zeitmessung je Schritt - Serie 14.10. (48 Schuesse).
func TestStudyOneClickTiming(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("study", "data", "truth_"+session1410+".json"))
	var tr struct {
		Holes [][2]float64 `json:"holes_mm"`
	}
	json.Unmarshal(b, &tr)
	st, _ := NewStore(context.Background(), studyDSN(t))
	raw, _ := st.SessionShotsRaw(context.Background(), session1410)
	var cands []SimShotRaw
	for _, s := range raw {
		if s.HasRaw {
			cands = append(cands, s)
		}
	}
	p := loadParams(t, "params_fotozonen.json")
	p.Zones = nil
	opt := PhotoCalOptions{MicOffsets: true, Offset: true}
	t0 := time.Now()
	in, pairs := zoneAssign(cands, tr.Holes, nil, p, opt)
	studyLog(t, "== One-Click-Zeit Serie 14.10. (%d Schuesse, %d Loecher): Zuordnung %d Paare in %s", len(cands), len(tr.Holes), len(pairs), time.Since(t0).Round(time.Millisecond))
	t1 := time.Now()
	rep, err := ZoneCalibrate(in, p, opt, ZoneParams{HoleRadiusMM: 29.75, BandHalfMM: 45})
	if err != nil {
		t.Fatal(err)
	}
	studyLog(t, "   Kalibrierung %s (gesamt %s) -> <1mm %.0f%% P90 %.2f", time.Since(t1).Round(time.Millisecond), time.Since(t0).Round(time.Millisecond), rep.After.Below1*100, rep.After.P90MM)
	for _, z := range rep.Zones {
		studyLog(t, "   Zone %s %2d Schuss: %s", z.Zone, z.Shots, z.Fitted)
	}
}
