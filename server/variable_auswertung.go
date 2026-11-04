// ============================================================================
// variable_auswertung.go – frei konfigurierbare Auswertung (Kachel Auswertung,
// Reiter "Variable Auswertung", web/variable-auswertung.js).
//
// Zwei Schritte:
//  1. Auswahl: VAFilter bestimmt die Scheiben (Sessions) - je Kriterium
//     mehrere Werte (ODER), die Kriterien untereinander UND. Wettkampf,
//     Preisschiessen und Scheibe beschreiben gemeinsam die Herkunft und
//     sind deshalb untereinander ODER (eine Scheibe stammt nie aus beidem).
//  2. Auswertung: VAConfig bestimmt Wertungsart, Faktoren je Disziplin/
//     Scheibe, Zielergebnis, Umgang mit mehreren Scheiben je Schuetze und die
//     Gruppenwertung (Mannschaft, Verein, Gau, Sportklasse, Altersklasse).
//
// Gerechnet wird live je Anfrage (evaluateVariable ist rein und ohne DB
// testbar). Gespeichert wird nur die Konfiguration, in saved_auswertungen
// mit type='variabel' (migrations/011).
//
// Intern hat jedes Ergebnis einen Score "kleiner = besser": bei Ringen der
// negierte Wert, beim Teiler der Wert, mit Zielergebnis der Abstand zum Ziel.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	vaRinge     = "ringe"
	vaZehntel   = "zehntel"
	vaTeiler    = "teiler"
	vaAdler     = "adler"
	vaVorNach   = "vor_nach"  // 9,7 -> 9+7 = 16
	vaNachkomma = "nachkomma" // 9,7 -> 7

	vaBasisSumme  = "summe"
	vaBasisSchuss = "bester_schuss"

	vaMehrfachSumme  = "summe_beste"
	vaMehrfachMittel = "mittelwert"

	vaGruppeMannschaft = "mannschaft"
	vaGruppeVerein     = "verein"
	vaGruppeGau        = "gau"
	vaGruppeKlasse     = "klasse"
	vaGruppeAlter      = "altersklasse" // maennlich + weiblich gleichen Alters

	// Wert eines Ueberzahl-Schusses im Stand-PC, kein echter Teiler
	vaKeinTeiler = 99999.0
	// Obergrenze der Scheiben-Liste in der Antwort (nur Anzeige)
	vaMaxScheibenListe = 500
)

type VAFilter struct {
	DateFrom          string   `json:"date_from"`
	DateTo            string   `json:"date_to"`
	EventIDs          []string `json:"event_ids"`
	PreisschiessenIDs []string `json:"preisschiessen_ids"`
	DisciplineIDs     []string `json:"discipline_ids"`
	ScheibeIDs        []string `json:"scheibe_ids"`
	ClubIDs           []string `json:"club_ids"`
	ClassIDs          []string `json:"class_ids"`
	// NurVollstaendig: nur Scheiben mit allen Wertungsschuessen der Disziplin
	NurVollstaendig bool `json:"nur_vollstaendig"`
}

type VAFaktor struct {
	Ring   float64 `json:"ring"`
	Teiler float64 `json:"teiler"`
}

type VAConfig struct {
	Filter  VAFilter `json:"filter"`
	Wertung string   `json:"wertung"` // ringe | zehntel | teiler | adler | vor_nach | nachkomma
	// Basis: Summe aller Schuesse einer Scheibe oder bester Schuss (Teiler
	// ist immer bester Schuss, Adler nutzt beide Listen)
	Basis string `json:"basis"`
	// Ziel: Zielergebnis, wer am naechsten dran ist gewinnt (z.B. 222 Teiler)
	Ziel *float64 `json:"ziel"`
	// Mehrfach: mehrere Scheiben eines Schuetzen - Summe der besten AnzScheiben
	// oder Mittelwert aller Scheiben
	Mehrfach string `json:"mehrfach"`
	// AnzScheiben: so viele beste Scheiben zaehlen bei summe_beste (0 = 1)
	AnzScheiben int `json:"anz_scheiben"`
	// Faktoren: Schluessel "s:<ps_scheiben.id>" oder "d:<disciplines.id>",
	// die Scheibe hat Vorrang vor der Disziplin; 0/fehlend = 1
	Faktoren map[string]VAFaktor `json:"faktoren"`
	Gruppe   string              `json:"gruppe"` // "" | mannschaft | verein | gau | klasse | altersklasse
	// GruppeBesteN: so viele beste Schuetzen je Gruppe zaehlen (0 = alle)
	GruppeBesteN int `json:"gruppe_beste_n"`
	// PDFSpalten: Zusatzspalten der PDF-Liste neben Platz, Name und Ergebnis
	// (web/variable-auswertung.js, SPALTEN); nur fuer die Ausgabe gespeichert
	PDFSpalten []string `json:"pdf_spalten"`
	PDFQuer    bool     `json:"pdf_quer"`
}

type vaShot struct {
	Ring    *float64
	Decimal *float64
	Teiler  *float64
	Penalty float64
	Count   int // aggregate_count: >1 = Serie/Gesamtwert, kein Einzelschuss
}

// vaScheibe: eine Scheibe (Session) mit allem, was Filteranzeige und Wertung
// brauchen.
type vaScheibe struct {
	SessionID      string `json:"session_id"`
	ShooterID      string `json:"shooter_id"`
	Name           string `json:"name"`
	Verein         string `json:"verein"`
	Klasse         string `json:"klasse"`
	DisciplineID   string `json:"discipline_id"`
	Disziplin      string `json:"disziplin"`
	DecimalScoring bool   `json:"-"`
	ScheibeID      string `json:"scheibe_id"`
	Scheibe        string `json:"scheibe"`
	Herkunft       string `json:"herkunft"`
	Datum          string `json:"datum"`
	ShotCount      int    `json:"shot_count"`
	TeamID         string `json:"-"`
	Team           string `json:"-"`
	ClubID         string `json:"-"`
	GauID          string `json:"-"`
	Gau            string `json:"-"`
	ClassID        string `json:"-"`
	AlterID        string `json:"-"` // vaAlterID, "" ohne Klasse
	Altersklasse   string `json:"-"`
	shots          []vaShot
}

type VAEinzel struct {
	Platz      int      `json:"platz"`
	ShooterID  string   `json:"shooter_id"`
	Name       string   `json:"name"`
	Verein     string   `json:"verein"`
	Klasse     string   `json:"klasse"`
	Gau        string   `json:"gau"`
	Mannschaft string   `json:"mannschaft"`
	Wert       float64  `json:"wert"`
	Abstand    *float64 `json:"abstand,omitempty"` // zum Zielergebnis
	// WertTeiler: zweiter Wert der Adler-Wertung (Wert = Ringe)
	WertTeiler *float64 `json:"wert_teiler,omitempty"`
	Scheiben   []string `json:"scheiben"` // gewertete Scheiben
	Anzahl     int      `json:"anzahl"`
	// Gewertet: nur in VAGruppe.Mitglieder - zaehlt fuers Gruppenergebnis
	Gewertet bool `json:"gewertet"`
	score    float64
}

type VAGruppe struct {
	Platz   int      `json:"platz"`
	Name    string   `json:"name"`
	Wert    float64  `json:"wert"`
	Abstand *float64 `json:"abstand,omitempty"`
	Anzahl  int      `json:"anzahl"` // gewertete Schuetzen
	// Mitglieder: alle Schuetzen der Gruppe, beste zuerst, Platz innerhalb der
	// Gruppe; die nach GruppeBesteN stehen mit Gewertet=false dabei
	Mitglieder []VAEinzel `json:"mitglieder"`
	score      float64
}

type VAErgebnis struct {
	Einzel   []VAEinzel `json:"einzel"`
	Gruppen  []VAGruppe `json:"gruppen"`
	Hinweise []string   `json:"hinweise"`
}

func vaRound(v float64) float64 { return math.Round(v*1000) / 1000 }

// vaZerlegen trennt einen Zehntelwert in Vor- und Nachkommastelle (9,7 ->
// 9 und 7) - ueber Zehntel gerechnet, sonst waere 9,7 binaer 9,6999...
func vaZerlegen(v float64) (vor, nach int) {
	zehntel := int(math.Round(v * 10))
	return zehntel / 10, zehntel % 10
}

func vaFaktoren(cfg VAConfig, s vaScheibe) (ring, teiler float64) {
	ring, teiler = 1, 1
	for _, key := range []string{"d:" + s.DisciplineID, "s:" + s.ScheibeID} {
		f, ok := cfg.Faktoren[key]
		if !ok {
			continue
		}
		if f.Ring != 0 {
			ring = f.Ring
		}
		if f.Teiler != 0 {
			teiler = f.Teiler
		}
	}
	return
}

// vaSchussWert liefert den Wert eines Einzelschusses fuer die Wertungsart.
func vaSchussWert(sh vaShot, wertung string) (float64, bool) {
	switch wertung {
	case vaRinge:
		if sh.Ring != nil {
			return *sh.Ring, true
		}
	case vaZehntel:
		if sh.Decimal != nil {
			return *sh.Decimal, true
		}
	case vaTeiler:
		if sh.Teiler != nil && *sh.Teiler < vaKeinTeiler {
			return *sh.Teiler, true
		}
	case vaVorNach:
		if sh.Decimal != nil && sh.Count == 1 {
			vor, nach := vaZerlegen(*sh.Decimal)
			return float64(vor + nach), true
		}
	case vaNachkomma:
		if sh.Decimal != nil && sh.Count == 1 {
			_, nach := vaZerlegen(*sh.Decimal)
			return float64(nach), true
		}
	}
	return 0, false
}

// vaScheibenWert rechnet den Wert einer Scheibe; ok=false, wenn sie fuer
// diese Wertung keinen Wert hat (z.B. Gesamtwert ohne Ringe, oder Einzel-
// schuss-Wertung bei einer Serien-/Gesamteingabe).
func vaScheibenWert(s vaScheibe, cfg VAConfig) (wert float64, ok bool) {
	fRing, fTeiler := vaFaktoren(cfg, s)
	faktor := fRing
	if cfg.Wertung == vaTeiler {
		faktor = fTeiler
	}
	besser := func(kandidat, bisher float64) bool {
		if cfg.Ziel != nil {
			return math.Abs(kandidat-*cfg.Ziel) < math.Abs(bisher-*cfg.Ziel)
		}
		if cfg.Wertung == vaTeiler {
			return kandidat < bisher
		}
		return kandidat > bisher
	}

	if cfg.Wertung == vaTeiler || cfg.Basis == vaBasisSchuss {
		for _, sh := range s.shots {
			if sh.Count > 1 && cfg.Wertung != vaTeiler {
				continue // Serie/Gesamtwert ist kein Schuss
			}
			v, gueltig := vaSchussWert(sh, cfg.Wertung)
			if !gueltig {
				continue
			}
			v *= faktor
			if !ok || besser(v, wert) {
				wert, ok = v, true
			}
		}
		return vaRound(wert), ok
	}

	for _, sh := range s.shots {
		v, gueltig := vaSchussWert(sh, cfg.Wertung)
		if !gueltig {
			return 0, false // eine Summe aus Teilwerten waere irrefuehrend
		}
		wert += v - sh.Penalty
	}
	return vaRound(wert * faktor), len(s.shots) > 0
}

func vaScore(wert float64, cfg VAConfig) float64 {
	switch {
	case cfg.Ziel != nil:
		return vaRound(math.Abs(wert - *cfg.Ziel))
	case cfg.Wertung == vaTeiler:
		return wert
	}
	return -wert
}

type vaWertung struct {
	scheibe string
	wert    float64
	score   float64
}

// vaSchuetzen rangiert die Schuetzen einer Scheibenmenge: je Schuetze zaehlen
// die besten N Scheiben, wer weniger hat, steht hinter denen mit voller Anzahl
// (beim Teiler waere eine kleinere Summe sonst ein Vorteil). Beim Mittelwert
// zaehlen alle Scheiben und ihre Anzahl spielt fuer den Rang keine Rolle.
func vaSchuetzen(scheiben []vaScheibe, cfg VAConfig) (out []VAEinzel, ohneWert int) {
	mittel := cfg.Mehrfach == vaMehrfachMittel
	n := cfg.AnzScheiben
	if n < 1 {
		n = 1
	}
	byShooter := map[string][]vaWertung{}
	first := map[string]vaScheibe{}
	for _, s := range scheiben {
		wert, ok := vaScheibenWert(s, cfg)
		if !ok {
			ohneWert++
			continue
		}
		label := s.Scheibe
		if label == "" {
			label = s.Disziplin
		}
		byShooter[s.ShooterID] = append(byShooter[s.ShooterID],
			vaWertung{scheibe: label, wert: wert, score: vaScore(wert, cfg)})
		if _, seen := first[s.ShooterID]; !seen {
			first[s.ShooterID] = s
		}
	}
	for id, list := range byShooter {
		sort.SliceStable(list, func(i, j int) bool { return list[i].score < list[j].score })
		if !mittel && len(list) > n {
			list = list[:n]
		}
		s := first[id]
		e := VAEinzel{ShooterID: id, Name: s.Name, Verein: s.Verein, Klasse: s.Klasse, Gau: s.Gau, Mannschaft: s.Team, Anzahl: len(list)}
		for _, w := range list {
			e.Wert += w.wert
			e.score += w.score
			e.Scheiben = append(e.Scheiben, w.scheibe)
		}
		if mittel {
			e.Wert /= float64(e.Anzahl)
			e.score /= float64(e.Anzahl)
		}
		e.Wert, e.score = vaRound(e.Wert), vaRound(e.score)
		if cfg.Ziel != nil {
			abstand := e.score
			e.Abstand = &abstand
		}
		out = append(out, e)
	}
	gleichVoll := func(a, b VAEinzel) bool { return mittel || a.Anzahl == b.Anzahl }
	sort.Slice(out, func(i, j int) bool {
		if !gleichVoll(out[i], out[j]) {
			return out[i].Anzahl > out[j].Anzahl
		}
		if out[i].score != out[j].score {
			return out[i].score < out[j].score
		}
		return out[i].Name < out[j].Name
	})
	for i := range out {
		out[i].Platz = i + 1
		if i > 0 && gleichVoll(out[i], out[i-1]) && out[i].score == out[i-1].score {
			out[i].Platz = out[i-1].Platz
		}
	}
	return out, ohneWert
}

// vaAdlerListe: abwechselnd aus Ring- und Teilerliste, wie die Adler-Wertung der
// Preisschiessen (computeAdler). Ringe je Disziplin: Zehntel, wenn sie in
// Zehnteln gewertet wird.
func vaAdlerListe(scheiben []vaScheibe, cfg VAConfig) ([]VAEinzel, int) {
	ringCfg, teilerCfg := cfg, cfg
	ringCfg.Ziel, teilerCfg.Ziel = nil, nil
	ringCfg.Wertung, ringCfg.Basis = vaRinge, vaBasisSumme
	teilerCfg.Wertung = vaTeiler

	ringScheiben := make([]vaScheibe, len(scheiben))
	for i, sc := range scheiben {
		ringScheiben[i] = sc
		if !sc.DecimalScoring {
			continue
		}
		shots := make([]vaShot, len(sc.shots))
		copy(shots, sc.shots)
		for j := range shots {
			shots[j].Ring = shots[j].Decimal
		}
		ringScheiben[i].shots = shots
	}
	ringe, ohneWert := vaSchuetzen(ringScheiben, ringCfg)
	teiler, _ := vaSchuetzen(scheiben, teilerCfg)

	toErgebnis := func(list []VAEinzel) []PSWertungErgebnis {
		out := make([]PSWertungErgebnis, len(list))
		for i, e := range list {
			out[i] = PSWertungErgebnis{TeilnehmerID: e.ShooterID, Platz: e.Platz}
		}
		return out
	}
	ringByID := map[string]VAEinzel{}
	for _, e := range ringe {
		ringByID[e.ShooterID] = e
	}
	teilerByID := map[string]VAEinzel{}
	for _, e := range teiler {
		teilerByID[e.ShooterID] = e
	}

	var out []VAEinzel
	for i, m := range computeAdler(toErgebnis(teiler), toErgebnis(ringe)) {
		r, hatRing := ringByID[m.TeilnehmerID]
		t, hatTeiler := teilerByID[m.TeilnehmerID]
		e := r
		if !hatRing {
			e = t
			e.Wert = 0
		}
		e.Platz = i + 1
		e.Abstand = nil
		if hatTeiler {
			v := t.Wert
			e.WertTeiler = &v
		}
		out = append(out, e)
	}
	return out, ohneWert
}

// vaKlasse: Sportklasse. Die Nummer (code) ist nur je Typ eindeutig.
type vaKlasse struct {
	Code string
	Typ  string
	Name string
}

// vaAlterID: die Klassen eines Alters haben dieselbe ID - weiblich ist immer
// die Nummer der maennlichen Klasse + 1 (20/21). Eine nicht numerische Nummer
// bleibt fuer sich.
func vaAlterID(k vaKlasse) string {
	n, err := strconv.Atoi(k.Code)
	if err != nil {
		return k.Typ + ":" + k.Code
	}
	return k.Typ + ":" + strconv.Itoa(n-n%2)
}

// vaAltersNamen: Anzeigename je Altersklasse, "Schüler männlich" + "Schüler
// weiblich" -> "Schüler", sonst beide Namen ("Herren I / Damen I"). klassen
// sind nach Nummer sortiert.
func vaAltersNamen(klassen []vaKlasse) map[string]string {
	je := map[string][]string{}
	for _, k := range klassen {
		id := vaAlterID(k)
		je[id] = append(je[id], k.Name)
	}
	out := make(map[string]string, len(je))
	for id, namen := range je {
		out[id] = strings.Join(namen, " / ")
		if len(namen) == 2 {
			m := strings.TrimSuffix(namen[0], " männlich")
			if m != namen[0] && m == strings.TrimSuffix(namen[1], " weiblich") {
				out[id] = m
			}
		}
	}
	return out
}

func vaGruppenKey(s vaScheibe, gruppe string) (key, name string) {
	switch gruppe {
	case vaGruppeMannschaft:
		return s.TeamID, s.Team
	case vaGruppeVerein:
		return s.ClubID, s.Verein
	case vaGruppeGau:
		return s.GauID, s.Gau
	case vaGruppeKlasse:
		return s.ClassID, s.Klasse
	case vaGruppeAlter:
		return s.AlterID, s.Altersklasse
	}
	return "", ""
}

// vaGruppen: je Gruppe die Schuetzen rangieren und die besten M addieren; die
// uebrigen bleiben als nicht gewertet in der Mitgliederliste. Gruppen mit
// weniger gewerteten Schuetzen stehen hinter vollstaendigen.
func vaGruppen(scheiben []vaScheibe, cfg VAConfig) (out []VAGruppe, ohneGruppe int) {
	byKey := map[string][]vaScheibe{}
	names := map[string]string{}
	for _, s := range scheiben {
		key, name := vaGruppenKey(s, cfg.Gruppe)
		if key == "" {
			ohneGruppe++
			continue
		}
		byKey[key] = append(byKey[key], s)
		names[key] = name
	}
	for key, list := range byKey {
		mitglieder, _ := vaSchuetzen(list, cfg)
		if len(mitglieder) == 0 {
			continue
		}
		gewertet := len(mitglieder)
		if cfg.GruppeBesteN > 0 && gewertet > cfg.GruppeBesteN {
			gewertet = cfg.GruppeBesteN
		}
		g := VAGruppe{Name: names[key], Anzahl: gewertet, Mitglieder: mitglieder}
		for i := 0; i < gewertet; i++ {
			mitglieder[i].Gewertet = true
			g.Wert += mitglieder[i].Wert
			g.score += mitglieder[i].score
		}
		g.Wert, g.score = vaRound(g.Wert), vaRound(g.score)
		if cfg.Ziel != nil {
			abstand := g.score
			g.Abstand = &abstand
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Anzahl != out[j].Anzahl {
			return out[i].Anzahl > out[j].Anzahl
		}
		if out[i].score != out[j].score {
			return out[i].score < out[j].score
		}
		return out[i].Name < out[j].Name
	})
	for i := range out {
		out[i].Platz = i + 1
		if i > 0 && out[i].Anzahl == out[i-1].Anzahl && out[i].score == out[i-1].score {
			out[i].Platz = out[i-1].Platz
		}
	}
	return out, ohneGruppe
}

func vaNormalize(cfg *VAConfig) error {
	switch cfg.Wertung {
	case vaRinge, vaZehntel, vaTeiler, vaAdler, vaVorNach, vaNachkomma:
	default:
		return errBadRequest("unbekannte Wertungsart")
	}
	switch cfg.Basis {
	case vaBasisSumme, vaBasisSchuss:
	case "":
		cfg.Basis = vaBasisSumme
		if cfg.Wertung == vaVorNach || cfg.Wertung == vaNachkomma {
			cfg.Basis = vaBasisSchuss
		}
	default:
		return errBadRequest("unbekannte Basis")
	}
	switch cfg.Mehrfach {
	case vaMehrfachSumme, vaMehrfachMittel:
	case "":
		cfg.Mehrfach = vaMehrfachSumme
	default:
		return errBadRequest("unbekannte Berechnung für mehrere Ergebnisse")
	}
	switch cfg.Gruppe {
	case "", vaGruppeMannschaft, vaGruppeVerein, vaGruppeGau, vaGruppeKlasse, vaGruppeAlter:
	default:
		return errBadRequest("unbekannte Gruppenart")
	}
	if cfg.AnzScheiben < 0 || cfg.GruppeBesteN < 0 {
		return errBadRequest("Anzahl darf nicht negativ sein")
	}
	return nil
}

func evaluateVariable(scheiben []vaScheibe, cfg VAConfig) VAErgebnis {
	res := VAErgebnis{Einzel: []VAEinzel{}, Gruppen: []VAGruppe{}, Hinweise: []string{}}
	var ohneWert int
	if cfg.Wertung == vaAdler {
		res.Einzel, ohneWert = vaAdlerListe(scheiben, cfg)
		if cfg.Ziel != nil {
			res.Hinweise = append(res.Hinweise, "Das Zielergebnis wird bei der Adler-Wertung nicht verwendet.")
		}
		if cfg.Gruppe != "" {
			res.Hinweise = append(res.Hinweise, "Die Adler-Wertung ist eine Platzliste und ergibt keine Gruppensumme – die Gruppenwertung entfällt.")
		}
	} else {
		res.Einzel, ohneWert = vaSchuetzen(scheiben, cfg)
		if cfg.Gruppe != "" {
			var ohneGruppe int
			res.Gruppen, ohneGruppe = vaGruppen(scheiben, cfg)
			if ohneGruppe > 0 {
				res.Hinweise = append(res.Hinweise, fmt.Sprintf(
					"%d Scheibe(n) ohne Zuordnung zur Gruppe wurden in der Gruppenwertung nicht berücksichtigt.", ohneGruppe))
			}
		}
	}
	if ohneWert > 0 {
		res.Hinweise = append(res.Hinweise, fmt.Sprintf(
			"%d Scheibe(n) haben für diese Wertung keinen Wert (z. B. Serien-/Gesamteingabe ohne Einzelschüsse) und wurden nicht gewertet.", ohneWert))
	}
	return res
}

// ----------------------------------------------------------------------------
// Datenbank
// ----------------------------------------------------------------------------

func orEmptyArr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// loadVariableScheiben laedt die Scheiben der Auswahl samt Schuessen.
func (s *Store) loadVariableScheiben(ctx context.Context, f VAFilter) ([]vaScheibe, error) {
	var from, to any
	if f.DateFrom != "" {
		from = f.DateFrom
	}
	if f.DateTo != "" {
		to = f.DateTo
	}
	rows, err := s.pool.Query(ctx, `
		SELECT se.id::text, sh.id::text, sh.last_name || ', ' || sh.first_name,
		       COALESCE(cl.name,''), COALESCE(sc.name,''),
		       d.id::text, d.name, d.decimal_scoring,
		       COALESCE(psc.id::text,''), COALESCE(psc.name,''),
		       COALESCE(ev.name, ps.name, ''),
		       COALESCE(COALESCE(se.started_at, se.finished_at)::date::text,''),
		       vsr.shot_count::int,
		       COALESCE(t.id::text,''), COALESCE(t.name,''),
		       COALESCE(cl.id::text,''), COALESCE(g.id::text,''), COALESCE(g.name,''),
		       COALESCE(sc.id::text,''), COALESCE(sc.code,''), COALESCE(sc.type::text,'')
		FROM sessions se
		JOIN v_session_results vsr ON vsr.session_id = se.id
		JOIN shooters sh           ON sh.id = se.shooter_id
		JOIN disciplines d         ON d.id = se.discipline_id
		LEFT JOIN clubs cl         ON cl.id = sh.club_id
		LEFT JOIN gaue g           ON g.id = cl.gau_id
		LEFT JOIN events ev        ON ev.id = se.event_id
		LEFT JOIN ps_kauf_scheiben ks ON ks.session_id = se.id
		LEFT JOIN ps_kaeufe k      ON k.id = ks.kauf_id
		LEFT JOIN ps_scheiben psc  ON psc.id = ks.scheibe_id
		LEFT JOIN preisschiessen ps ON ps.id = psc.preisschiessen_id
		LEFT JOIN ps_teilnehmer pt ON pt.id = k.teilnehmer_id
		LEFT JOIN LATERAL (
			SELECT st.team_id, st.class_id FROM starters st
			WHERE st.event_id = se.event_id AND st.shooter_id = se.shooter_id
			LIMIT 1
		) st ON true
		LEFT JOIN teams t ON t.id = st.team_id
		LEFT JOIN shooter_classes sc ON sc.id = COALESCE(pt.class_id, st.class_id)
		WHERE se.status <> 'aborted' AND NOT se.maintenance AND se.stechen_of IS NULL
		  AND k.returned_at IS NULL
		  AND ($1::date IS NULL OR COALESCE(se.started_at, se.finished_at)::date >= $1::date)
		  AND ($2::date IS NULL OR COALESCE(se.started_at, se.finished_at)::date <= $2::date)
		  AND (cardinality($3::uuid[]) + cardinality($4::uuid[]) + cardinality($5::uuid[]) = 0
		       OR se.event_id = ANY($3::uuid[])
		       OR (ps.id = ANY($4::uuid[]) AND (cardinality($5::uuid[]) = 0 OR psc.id = ANY($5::uuid[])))
		       OR (cardinality($4::uuid[]) = 0 AND psc.id = ANY($5::uuid[])))
		  AND (cardinality($6::uuid[]) = 0 OR d.id = ANY($6::uuid[]))
		  AND (cardinality($7::uuid[]) = 0 OR sh.club_id = ANY($7::uuid[]))
		  AND (cardinality($8::uuid[]) = 0 OR COALESCE(pt.class_id, st.class_id) = ANY($8::uuid[]))
		  AND (NOT $9::bool OR vsr.shot_count >= d.match_shot_count)
		ORDER BY 12 DESC, 3`,
		from, to, orEmptyArr(f.EventIDs), orEmptyArr(f.PreisschiessenIDs), orEmptyArr(f.ScheibeIDs),
		orEmptyArr(f.DisciplineIDs), orEmptyArr(f.ClubIDs), orEmptyArr(f.ClassIDs), f.NurVollstaendig)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []vaScheibe
	index := map[string]int{}
	var ids []string
	klassen := map[string]vaKlasse{} // Session -> Klasse
	for rows.Next() {
		var x vaScheibe
		var k vaKlasse
		if err := rows.Scan(&x.SessionID, &x.ShooterID, &x.Name, &x.Verein, &x.Klasse,
			&x.DisciplineID, &x.Disziplin, &x.DecimalScoring, &x.ScheibeID, &x.Scheibe, &x.Herkunft,
			&x.Datum, &x.ShotCount, &x.TeamID, &x.Team, &x.ClubID, &x.GauID, &x.Gau,
			&x.ClassID, &k.Code, &k.Typ); err != nil {
			return nil, err
		}
		if x.ClassID != "" {
			klassen[x.SessionID] = k
		}
		index[x.SessionID] = len(out)
		ids = append(ids, x.SessionID)
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(ids) == 0 {
		return out, nil
	}

	alle, err := s.vaKlassen(ctx)
	if err != nil {
		return nil, err
	}
	altersNamen := vaAltersNamen(alle)
	for i := range out {
		if k, ok := klassen[out[i].SessionID]; ok {
			out[i].AlterID = vaAlterID(k)
			out[i].Altersklasse = altersNamen[out[i].AlterID]
		}
	}

	shots, err := s.pool.Query(ctx, `
		SELECT effective_session_id::text, eff_ring::float8, eff_decimal_value::float8,
		       eff_center_distance::float8, penalty_rings::float8, aggregate_count
		FROM v_scoring_shots WHERE effective_session_id = ANY($1::uuid[])
		ORDER BY shot_no`, ids)
	if err != nil {
		return nil, err
	}
	defer shots.Close()
	for shots.Next() {
		var sid string
		var sh vaShot
		var count int16
		if err := shots.Scan(&sid, &sh.Ring, &sh.Decimal, &sh.Teiler, &sh.Penalty, &count); err != nil {
			return nil, err
		}
		sh.Count = int(count)
		if i, ok := index[sid]; ok {
			out[i].shots = append(out[i].shots, sh)
		}
	}
	return out, shots.Err()
}

func (s *Store) vaKlassen(ctx context.Context) ([]vaKlasse, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT code, COALESCE(type::text,''), name FROM shooter_classes
		ORDER BY length(code), code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []vaKlasse
	for rows.Next() {
		var k vaKlasse
		if err := rows.Scan(&k.Code, &k.Typ, &k.Name); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

type vaFaktorZeile struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Typ    string `json:"typ"` // disziplin | scheibe
	Anzahl int    `json:"anzahl"`
}

// vaFaktorZeilen: Disziplinen und Scheiben der Auswahl, fuer die Faktor-Tabelle.
func vaFaktorZeilen(scheiben []vaScheibe) []vaFaktorZeile {
	byKey := map[string]*vaFaktorZeile{}
	add := func(key, label, typ string) {
		z, ok := byKey[key]
		if !ok {
			z = &vaFaktorZeile{Key: key, Label: label, Typ: typ}
			byKey[key] = z
		}
		z.Anzahl++
	}
	for _, s := range scheiben {
		add("d:"+s.DisciplineID, s.Disziplin, "disziplin")
		if s.ScheibeID != "" {
			add("s:"+s.ScheibeID, s.Scheibe+" ("+s.Herkunft+")", "scheibe")
		}
	}
	out := make([]vaFaktorZeile, 0, len(byKey))
	for _, z := range byKey {
		out = append(out, *z)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Typ != out[j].Typ {
			return out[i].Typ < out[j].Typ
		}
		return out[i].Label < out[j].Label
	})
	return out
}

type vaOption struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Gruppe string `json:"gruppe,omitempty"` // Scheibe: ID des Preisschiessens
}

func (s *Store) vaOptionen(ctx context.Context, sql string) ([]vaOption, error) {
	rows, err := s.pool.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []vaOption{}
	for rows.Next() {
		var o vaOption
		if err := rows.Scan(&o.ID, &o.Name, &o.Gruppe); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type VariableAuswertung struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	UpdatedAt time.Time       `json:"created_at"`
}

// ----------------------------------------------------------------------------
// HTTP
// ----------------------------------------------------------------------------

// GET /api/variable-auswertung/optionen - Auswahllisten fuer den Filter
func (a *APIServer) getVariableOptionen(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "auswertung"); err != nil {
		return nil, err
	}
	ctx := r.Context()
	res := map[string]any{}
	for key, sql := range map[string]string{
		"wettkaempfe":    `SELECT id::text, name || COALESCE(' (' || to_char(starts_on,'DD.MM.YYYY') || ')',''), '' FROM events ORDER BY starts_on DESC NULLS LAST, name`,
		"preisschiessen": `SELECT id::text, name, '' FROM preisschiessen ORDER BY starts_on DESC NULLS LAST, name`,
		"disziplinen":    `SELECT id::text, name, '' FROM disciplines WHERE active ORDER BY name`,
		"scheiben": `SELECT sc.id::text, sc.name || ' (' || p.name || ')', p.id::text
		             FROM ps_scheiben sc JOIN preisschiessen p ON p.id = sc.preisschiessen_id
		             ORDER BY p.starts_on DESC NULLS LAST, sc.sort_order, sc.name`,
		"vereine": `SELECT id::text, name, '' FROM clubs ORDER BY name`,
		"klassen": `SELECT id::text, name, '' FROM shooter_classes ORDER BY name`,
	} {
		list, err := a.store.vaOptionen(ctx, sql)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		res[key] = list
	}
	return res, nil
}

// POST /api/variable-auswertung/berechnen
func (a *APIServer) postVariableBerechnen(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "auswertung"); err != nil {
		return nil, err
	}
	cfg, err := decodeBody[VAConfig](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body: " + err.Error())
	}
	if err := vaNormalize(&cfg); err != nil {
		return nil, err
	}
	scheiben, err := a.store.loadVariableScheiben(r.Context(), cfg.Filter)
	if err != nil {
		return nil, err
	}
	liste := scheiben
	if len(liste) > vaMaxScheibenListe {
		liste = liste[:vaMaxScheibenListe]
	}
	if liste == nil {
		liste = []vaScheibe{}
	}
	res := evaluateVariable(scheiben, cfg)
	return map[string]any{
		"scheiben_gesamt": len(scheiben),
		"scheiben":        liste,
		"faktor_zeilen":   vaFaktorZeilen(scheiben),
		"einzel":          res.Einzel,
		"gruppen":         res.Gruppen,
		"hinweise":        res.Hinweise,
	}, nil
}

// POST /api/variable-auswertung/pdf - Body vaPDFRequest (vom Browser aus der
// angezeigten Ergebnisliste gebaut, wie die Listen der Vereinsabende).
func (a *APIServer) postVariablePDF(w http.ResponseWriter, r *http.Request) {
	if _, err := a.requireTile(w, r, "auswertung"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	req, err := decodeBody[vaPDFRequest](r)
	if err != nil {
		http.Error(w, "ungültiger Body: "+err.Error(), http.StatusBadRequest)
		return
	}
	zeilen := len(req.Rows)
	for _, sec := range req.Sections {
		zeilen += len(sec.Rows)
	}
	if zeilen > 5000 || len(req.Sections) > 20 {
		http.Error(w, "Liste zu groß", http.StatusBadRequest)
		return
	}
	pdf := buildVereinsabendPDF(req)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+pdfFilenameSafe(req.Title)+`.pdf"`)
	if err := pdf.Output(w); err != nil {
		http.Error(w, "PDF-Ausgabe fehlgeschlagen: "+err.Error(), http.StatusInternalServerError)
	}
}

// GET /api/variable-auswertungen - gespeicherte Auswertungen
func (a *APIServer) listVariableAuswertungen(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "auswertung"); err != nil {
		return nil, err
	}
	rows, err := a.store.pool.Query(r.Context(), `
		SELECT id::text, name, params, created_at FROM saved_auswertungen
		WHERE type = 'variabel' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VariableAuswertung{}
	for rows.Next() {
		var x VariableAuswertung
		if err := rows.Scan(&x.ID, &x.Name, &x.Config, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PUT /api/variable-auswertungen - speichern unter einem Namen; ein vorhandener
// Name wird ueberschrieben.
func (a *APIServer) putVariableAuswertung(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "auswertung"); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Name   string   `json:"name"`
		Config VAConfig `json:"config"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body: " + err.Error())
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return nil, errBadRequest("Name erforderlich")
	}
	if err := vaNormalize(&body.Config); err != nil {
		return nil, err
	}
	data, err := json.Marshal(body.Config)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	var id string
	err = a.store.pool.QueryRow(ctx, `
		UPDATE saved_auswertungen SET params=$2::jsonb, created_at=now()
		WHERE type='variabel' AND name=$1 RETURNING id::text`, body.Name, string(data)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = a.store.pool.QueryRow(ctx, `
			INSERT INTO saved_auswertungen (name, type, params) VALUES ($1,'variabel',$2::jsonb)
			RETURNING id::text`, body.Name, string(data)).Scan(&id)
	}
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": id}, nil
}

// DELETE /api/variable-auswertungen/{id}
func (a *APIServer) deleteVariableAuswertung(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "auswertung"); err != nil {
		return nil, err
	}
	_, err := a.store.pool.Exec(r.Context(),
		`DELETE FROM saved_auswertungen WHERE id=$1 AND type='variabel'`, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
