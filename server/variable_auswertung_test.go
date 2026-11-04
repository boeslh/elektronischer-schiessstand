package main

import (
	"reflect"
	"testing"
)

func vaF(v float64) *float64 { return &v }

// vaSch baut eine Scheibe aus Zehntelwerten und Teilern (je Schuss).
func vaSch(shooter, disc string, decimals []float64, teiler []float64) vaScheibe {
	s := vaScheibe{SessionID: shooter + disc, ShooterID: shooter, Name: shooter, DisciplineID: disc, Disziplin: disc}
	for i, d := range decimals {
		ring := float64(int(d))
		s.shots = append(s.shots, vaShot{Ring: vaF(ring), Decimal: vaF(d), Teiler: vaF(teiler[i]), Count: 1})
	}
	return s
}

func vaReihenfolge(list []VAEinzel) []string {
	out := []string{}
	for _, e := range list {
		out = append(out, e.Name)
	}
	return out
}

func TestVAZerlegen(t *testing.T) {
	for _, c := range []struct {
		v         float64
		vor, nach int
	}{{9.7, 9, 7}, {10.9, 10, 9}, {10.0, 10, 0}, {0.3, 0, 3}} {
		if vor, nach := vaZerlegen(c.v); vor != c.vor || nach != c.nach {
			t.Errorf("%v: %d/%d", c.v, vor, nach)
		}
	}
}

func TestVAWertungsarten(t *testing.T) {
	a := vaSch("A", "LG", []float64{9.7, 10.2}, []float64{300, 150})
	b := vaSch("B", "LG", []float64{10.9, 8.1}, []float64{40, 900})
	list := []vaScheibe{a, b}

	for _, c := range []struct {
		name string
		cfg  VAConfig
		want map[string]float64
		erst string
	}{
		{"Ringe Summe", VAConfig{Wertung: vaRinge, Basis: vaBasisSumme}, map[string]float64{"A": 19, "B": 18}, "A"},
		{"Zehntel Summe", VAConfig{Wertung: vaZehntel, Basis: vaBasisSumme}, map[string]float64{"A": 19.9, "B": 19}, "A"},
		{"Teiler", VAConfig{Wertung: vaTeiler}, map[string]float64{"A": 150, "B": 40}, "B"},
		{"Vor+Nach", VAConfig{Wertung: vaVorNach, Basis: vaBasisSchuss}, map[string]float64{"A": 16, "B": 19}, "B"},
		{"Nachkomma", VAConfig{Wertung: vaNachkomma, Basis: vaBasisSchuss}, map[string]float64{"A": 7, "B": 9}, "B"},
		{"Ziel 222 Teiler", VAConfig{Wertung: vaTeiler, Ziel: vaF(222)}, map[string]float64{"A": 150, "B": 40}, "A"},
		{"Ziel 18 Ringe", VAConfig{Wertung: vaRinge, Basis: vaBasisSumme, Ziel: vaF(18)}, map[string]float64{"A": 19, "B": 18}, "B"},
	} {
		res := evaluateVariable(list, c.cfg)
		if res.Einzel[0].Name != c.erst {
			t.Errorf("%s: erster %s", c.name, res.Einzel[0].Name)
		}
		for _, e := range res.Einzel {
			if e.Wert != c.want[e.Name] {
				t.Errorf("%s: %s = %v, erwartet %v", c.name, e.Name, e.Wert, c.want[e.Name])
			}
		}
	}
}

func TestVAFaktoren(t *testing.T) {
	lg := vaSch("A", "LG", []float64{10}, []float64{100})
	lp := vaSch("B", "LP", []float64{10}, []float64{100})
	lp.ScheibeID = "s1"
	cfg := VAConfig{Wertung: vaTeiler, Faktoren: map[string]VAFaktor{
		"d:LP": {Ring: 2, Teiler: 0.5},
		"s:s1": {Teiler: 0.25}, // Scheibe vor Disziplin, Ring bleibt von der Disziplin
	}}
	res := evaluateVariable([]vaScheibe{lg, lp}, cfg)
	if res.Einzel[0].Name != "B" || res.Einzel[0].Wert != 25 {
		t.Errorf("Teiler-Faktor: %+v", res.Einzel[0])
	}
	cfg.Wertung, cfg.Basis = vaRinge, vaBasisSumme
	res = evaluateVariable([]vaScheibe{lg, lp}, cfg)
	if res.Einzel[0].Name != "B" || res.Einzel[0].Wert != 20 {
		t.Errorf("Ring-Faktor: %+v", res.Einzel[0])
	}
}

func TestVABesteScheibenUndGleichstand(t *testing.T) {
	list := []vaScheibe{
		vaSch("A", "LG", []float64{9}, []float64{100}),
		vaSch("A", "LP", []float64{10}, []float64{50}),
		vaSch("A", "X", []float64{8}, []float64{500}),
		vaSch("B", "LG", []float64{10}, []float64{10}), // nur eine Scheibe
		vaSch("C", "LG", []float64{9}, []float64{60}),
		vaSch("C", "LP", []float64{10}, []float64{90}),
	}
	res := evaluateVariable(list, VAConfig{Wertung: vaRinge, Basis: vaBasisSumme, AnzScheiben: 2})
	if got := vaReihenfolge(res.Einzel); !reflect.DeepEqual(got, []string{"A", "C", "B"}) {
		t.Errorf("Reihenfolge %v", got)
	}
	if res.Einzel[0].Wert != 19 || res.Einzel[0].Platz != 1 || res.Einzel[1].Platz != 1 || res.Einzel[2].Platz != 3 {
		t.Errorf("Plaetze: %+v", res.Einzel)
	}
	// Teiler: B hat nur eine Scheibe, die kleine Summe darf nicht gewinnen
	res = evaluateVariable(list, VAConfig{Wertung: vaTeiler, AnzScheiben: 2})
	if res.Einzel[len(res.Einzel)-1].Name != "B" {
		t.Errorf("Teiler: %v", vaReihenfolge(res.Einzel))
	}
}

func TestVAAdler(t *testing.T) {
	list := []vaScheibe{
		vaSch("Ring", "LG", []float64{10.9, 10.9}, []float64{400, 400}),
		vaSch("Teiler", "LG", []float64{5, 5}, []float64{10, 900}),
		vaSch("Mitte", "LG", []float64{9, 9}, []float64{100, 100}),
	}
	res := evaluateVariable(list, VAConfig{Wertung: vaAdler})
	if got := vaReihenfolge(res.Einzel); !reflect.DeepEqual(got, []string{"Ring", "Teiler", "Mitte"}) {
		t.Errorf("Adler %v", got)
	}
	if res.Einzel[1].WertTeiler == nil || *res.Einzel[1].WertTeiler != 10 || res.Einzel[1].Wert != 10 {
		t.Errorf("Adler-Werte %+v", res.Einzel[1])
	}
	// Zehntel-Disziplin: Ringliste nutzt den Zehntelwert
	list[0].DecimalScoring = true
	res = evaluateVariable(list, VAConfig{Wertung: vaAdler})
	if res.Einzel[0].Wert != 21.8 {
		t.Errorf("Adler Zehntel %+v", res.Einzel[0])
	}
}

func TestVAGruppen(t *testing.T) {
	mk := func(sh, club string, d float64) vaScheibe {
		s := vaSch(sh, "LG", []float64{d}, []float64{100})
		s.ClubID, s.Verein = club, club
		return s
	}
	list := []vaScheibe{mk("A1", "A", 10), mk("A2", "A", 9), mk("A3", "A", 1), mk("B1", "B", 10), mk("B2", "B", 10), mk("C1", "C", 10)}
	ohne := vaSch("X", "LG", []float64{10}, []float64{100})
	res := evaluateVariable(append(list, ohne), VAConfig{Wertung: vaRinge, Basis: vaBasisSumme, Gruppe: vaGruppeVerein, GruppeBesteN: 2})
	if len(res.Gruppen) != 3 || res.Gruppen[0].Name != "B" || res.Gruppen[0].Wert != 20 ||
		res.Gruppen[1].Name != "A" || res.Gruppen[1].Wert != 19 || res.Gruppen[2].Name != "C" {
		t.Errorf("Gruppen %+v", res.Gruppen)
	}
	if len(res.Hinweise) != 1 {
		t.Errorf("Hinweise %v", res.Hinweise)
	}
	// Gau und Mannschaft stehen fuer die Ausgabe am Einzelergebnis
	mit := vaSch("Z", "LG", []float64{10}, []float64{100})
	mit.Gau, mit.Team = "Gau 1", "Team 1"
	if e := evaluateVariable([]vaScheibe{mit}, VAConfig{Wertung: vaRinge, Basis: vaBasisSumme}).Einzel[0]; e.Gau != "Gau 1" || e.Mannschaft != "Team 1" {
		t.Errorf("Einzel %+v", e)
	}
	// Nicht gewertete Mitglieder bleiben dabei, mit Platz in der Gruppe
	a := res.Gruppen[1]
	if a.Anzahl != 2 || len(a.Mitglieder) != 3 ||
		!a.Mitglieder[0].Gewertet || !a.Mitglieder[1].Gewertet || a.Mitglieder[2].Gewertet ||
		a.Mitglieder[2].Name != "A3" || a.Mitglieder[2].Platz != 3 {
		t.Errorf("Mitglieder %+v", a)
	}
}

func TestVAOhneWert(t *testing.T) {
	// Gesamteingabe in Zehntel: kein Ringwert, kein Einzelschuss
	s := vaScheibe{ShooterID: "A", Name: "A", shots: []vaShot{{Decimal: vaF(412.7), Count: 40}}}
	res := evaluateVariable([]vaScheibe{s}, VAConfig{Wertung: vaRinge, Basis: vaBasisSumme})
	if len(res.Einzel) != 0 || len(res.Hinweise) != 1 {
		t.Errorf("Ringe: %+v", res)
	}
	res = evaluateVariable([]vaScheibe{s}, VAConfig{Wertung: vaZehntel, Basis: vaBasisSumme})
	if len(res.Einzel) != 1 || res.Einzel[0].Wert != 412.7 {
		t.Errorf("Zehntel: %+v", res)
	}
	res = evaluateVariable([]vaScheibe{s}, VAConfig{Wertung: vaZehntel, Basis: vaBasisSchuss})
	if len(res.Einzel) != 0 {
		t.Errorf("bester Schuss: %+v", res)
	}
}

func TestVAAltersklassen(t *testing.T) {
	klassen := []vaKlasse{
		{"10", "0", "Herren I"}, {"11", "0", "Damen I"},
		{"20", "0", "Schüler männlich"}, {"21", "0", "Schüler weiblich"},
		{"70", "2", "Senioren I männlich"}, {"71", "2", "Senioren I weiblich"},
		{"99", "0", "Gast"}, {"X", "0", "Sonder"},
	}
	namen := vaAltersNamen(klassen)
	for k, want := range map[vaKlasse]string{
		{Code: "21", Typ: "0"}: "Schüler",
		{Code: "20", Typ: "0"}: "Schüler",
		{Code: "11", Typ: "0"}: "Herren I / Damen I",
		{Code: "71", Typ: "2"}: "Senioren I",
		{Code: "99", Typ: "0"}: "Gast",
		{Code: "X", Typ: "0"}:  "Sonder",
	} {
		if got := namen[vaAlterID(k)]; got != want {
			t.Errorf("%+v: %q, erwartet %q", k, got, want)
		}
	}
	// gleiche Nummer bei anderem Typ ist eine andere Klasse
	if vaAlterID(vaKlasse{Code: "70", Typ: "0"}) == vaAlterID(vaKlasse{Code: "70", Typ: "2"}) {
		t.Error("Typ muss die Altersklasse trennen")
	}
}

func TestVAGruppenKlassen(t *testing.T) {
	mk := func(sh, klasse string, d float64) vaScheibe {
		s := vaSch(sh, "LG", []float64{d}, []float64{100})
		k := vaKlasse{Code: klasse, Typ: "0"}
		s.ClassID, s.Klasse = klasse, klasse
		s.AlterID, s.Altersklasse = vaAlterID(k), "Alter "+vaAlterID(k)
		return s
	}
	list := []vaScheibe{mk("M", "20", 9), mk("W", "21", 10), mk("H", "10", 8)}
	alter := evaluateVariable(list, VAConfig{Wertung: vaRinge, Basis: vaBasisSumme, Gruppe: vaGruppeAlter})
	if len(alter.Gruppen) != 2 || alter.Gruppen[0].Name != "Alter 0:20" || alter.Gruppen[0].Wert != 19 || alter.Gruppen[0].Anzahl != 2 {
		t.Errorf("Altersklassen %+v", alter.Gruppen)
	}
	klasse := evaluateVariable(list, VAConfig{Wertung: vaRinge, Basis: vaBasisSumme, Gruppe: vaGruppeKlasse})
	if len(klasse.Gruppen) != 3 {
		t.Errorf("Sportklassen %+v", klasse.Gruppen)
	}
	ohne := vaSch("X", "LG", []float64{10}, []float64{100})
	res := evaluateVariable([]vaScheibe{ohne}, VAConfig{Wertung: vaRinge, Basis: vaBasisSumme, Gruppe: vaGruppeAlter})
	if len(res.Gruppen) != 0 || len(res.Hinweise) != 1 {
		t.Errorf("ohne Klasse %+v", res)
	}
}

func TestVAMittelwert(t *testing.T) {
	zweite := func(sh string, d float64) vaScheibe {
		s := vaSch(sh, "LG", []float64{d}, []float64{100})
		s.SessionID += "2"
		return s
	}
	list := []vaScheibe{
		vaSch("A", "LG", []float64{10}, []float64{100}), zweite("A", 9), zweite("A", 8),
		vaSch("B", "LG", []float64{9.5}, []float64{100}),
	}
	cfg := VAConfig{Wertung: vaRinge, Basis: vaBasisSumme, Mehrfach: vaMehrfachMittel, AnzScheiben: 1}
	// A: (10+9+8)/3 = 9 alle Scheiben, trotz Anzahl 1; B hat nur eine Scheibe
	res := evaluateVariable(list, cfg)
	if res.Einzel[0].Name != "A" || res.Einzel[0].Wert != 9 || res.Einzel[0].Anzahl != 3 {
		t.Errorf("Mittelwert %+v", res.Einzel)
	}

	// weniger Scheiben ist beim Mittelwert kein Nachteil
	list[3] = vaSch("B", "LG", []float64{10}, []float64{100})
	res = evaluateVariable(list, cfg)
	if !reflect.DeepEqual(vaReihenfolge(res.Einzel), []string{"B", "A"}) {
		t.Errorf("Reihenfolge %v", vaReihenfolge(res.Einzel))
	}

	// gleicher Mittelwert bei verschiedener Anzahl ist derselbe Platz
	list[3] = vaSch("B", "LG", []float64{9}, []float64{100})
	res = evaluateVariable(list, cfg)
	if res.Einzel[0].Platz != 1 || res.Einzel[1].Platz != 1 {
		t.Errorf("Platz %+v", res.Einzel)
	}

	// Abstaende zum Ziel werden gemittelt, 200 und 244 treffen 222 nicht
	ziel := vaF(222)
	teiler := VAConfig{Wertung: vaTeiler, Mehrfach: vaMehrfachMittel, Ziel: ziel}
	s1 := vaSch("A", "LG", []float64{9}, []float64{200})
	s2 := vaSch("A", "LG", []float64{9}, []float64{244})
	s2.SessionID += "2"
	res = evaluateVariable([]vaScheibe{s1, s2}, teiler)
	if res.Einzel[0].Wert != 222 || *res.Einzel[0].Abstand != 22 {
		t.Errorf("Ziel %+v", res.Einzel[0])
	}

	// Vorgabe bleibt die Summe der besten, auch fuer gespeicherte Auswertungen
	alt := VAConfig{Wertung: vaRinge}
	if err := vaNormalize(&alt); err != nil || alt.Mehrfach != vaMehrfachSumme {
		t.Errorf("Normalize %v %q", err, alt.Mehrfach)
	}
}
