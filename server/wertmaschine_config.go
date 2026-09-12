// ============================================================================
// wertmaschine_config.go – Konfigurationsstring fuer die Disag-Wertmaschine
// (RM IV: "SCH=...;"-Schluessel-Wert-String, RM III: fester 9-stelliger
// Zahlencode), berechnet aus Disziplin+Scheibe statt in einer zweiten,
// von Hand zu pflegenden Zuordnungstabelle im wertmaschine-Dienst - siehe
// Konzept .claude/plans/wise-scribbling-abelson.md Abschnitt 3.
//
// Alle Papier-Disziplinen bekommen Teiler-Auswertung mit fester
// Teilergrenze 2000 (in 1/100mm, siehe center_distance-Konvention) als
// Startwert - eine feinere Konfigurierbarkeit je Disziplin ist bewusst
// nicht Teil dieser ersten Umsetzung.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"strings"
)

// nameToDisagSch bildet dieselben Scheiben-Kuerzel wie target_geometry.go
// nameToTargetNo auf den RM-IV-Scheibentyp-Katalog ab (SCH=...).
var nameToDisagSch = map[string]string{"LG": "LG10", "ZS": "ZS", "SP": "GK10", "LP": "LP"}

// nameToRMIIIType bildet dieselben Kuerzel auf die RM-III-Digit-1-Codes ab
// (offizielle Einstellungsstring-Tabelle, RMIIIBA_1.pdf: 1=Luftgewehr,
// 2=Luftpistole, 4=LG Laufende Scheibe, 5=Zimmerstutzen, 6=Kleinkaliber -
// "SP" (Sportpistole/KK-Scheibe, siehe target_geometry.go InnerTenDashed)
// faellt unter Kleinkaliber).
var nameToRMIIIType = map[string]int{"LG": 1, "LP": 2, "ZS": 5, "SP": 6}

func matchDisagSchByName(name string) (string, bool) {
	for _, word := range strings.Fields(strings.ToUpper(name)) {
		if v, ok := nameToDisagSch[word]; ok {
			return v, true
		}
	}
	return "", false
}

func matchRMIIITypeByName(name string) (int, bool) {
	for _, word := range strings.Fields(strings.ToUpper(name)) {
		if v, ok := nameToRMIIIType[word]; ok {
			return v, true
		}
	}
	return 0, false
}

// rmiiiSeriesLengthCode bildet eine Gesamtschusszahl auf den naechstgroesseren
// RM-III-Serienlaengen-Code ab (0=1,1=10,2=20,3=30,4=40,5=50,6=60,7=80,
// 8=100,9=120 - siehe RMEinst.frm/Einlesen.frm Kommentarblock).
func rmiiiSeriesLengthCode(totalShots int) int {
	steps := []int{1, 10, 20, 30, 40, 50, 60, 80, 100, 120}
	for i, s := range steps {
		if totalShots <= s {
			return i
		}
	}
	return len(steps) - 1
}

type disagDisciplineFields struct {
	targetName     string
	matchShotCount int
	shotsPerSeries int
	decimalScoring bool
	caliberMM      *float64
	rmiiiOverride  string
}

func (s *Store) disagDisciplineFieldsFor(ctx context.Context, disciplineID string) (disagDisciplineFields, error) {
	var f disagDisciplineFields
	err := s.pool.QueryRow(ctx, `
		SELECT tg.name, d.match_shot_count, d.shots_per_series, d.decimal_scoring,
		       d.wertmaschine_caliber_mm, COALESCE(d.rmiii_config_override,'')
		FROM disciplines d JOIN targets tg ON tg.id = d.target_id
		WHERE d.id = $1`, disciplineID,
	).Scan(&f.targetName, &f.matchShotCount, &f.shotsPerSeries, &f.decimalScoring,
		&f.caliberMM, &f.rmiiiOverride)
	if err != nil {
		return f, fmt.Errorf("Disziplin/Scheibe: %w", err)
	}
	return f, nil
}

// BuildDisagConfig liefert den Konfigurationsstring fuer die Disag-
// Wertmaschine (RM IV: key=value-String OHNE Pruefsumme/CR - das haengt der
// wertmaschine-Dienst selbst an, siehe disag/rmiv.go dort; RM III: 9-stelliger
// Zahlencode) fuer eine Disziplin. Fuer RM III wird - falls gesetzt - das
// manuelle Override (disciplines.rmiii_config_override, siehe
// migrations/058) unveraendert zurueckgegeben statt berechnet zu werden; die
// automatische Berechnung diente mit echter Hardware mehrfach als
// nachweislich unzuverlaessige Ausgangsbasis.
func (s *Store) BuildDisagConfig(ctx context.Context, disciplineID, protocol string) (string, error) {
	f, err := s.disagDisciplineFieldsFor(ctx, disciplineID)
	if err != nil {
		return "", err
	}

	switch protocol {
	case "rmiv":
		sch, ok := matchDisagSchByName(f.targetName)
		if !ok {
			return "", errBadRequest(fmt.Sprintf("keine Disag-Scheibenzuordnung fuer %q gefunden", f.targetName))
		}
		cfg := fmt.Sprintf("SCH=%s;SZI=%d;SGE=%d;SSC=1;TEA=ZT;TEG=2000;", sch, f.shotsPerSeries, f.matchShotCount)
		if f.caliberMM != nil {
			cfg = fmt.Sprintf("KAL=%.2f;", *f.caliberMM) + cfg
		}
		return cfg, nil
	case "rmiii":
		if f.rmiiiOverride != "" {
			return f.rmiiiOverride, nil
		}
		return computeRMIIIConfig(f)
	default:
		return "", errBadRequest("unbekanntes Protokoll (rmiv|rmiii erwartet)")
	}
}

// ComputeRMIIIConfigPreview berechnet den RM-III-Einstellungsstring aus
// uebergebenen Parametern statt aus einer bereits gespeicherten Disziplin -
// fuer den "Standard einsetzen"-Button in disciplines.html, der so auch fuer
// eine noch nicht gespeicherte (neue) Disziplin bzw. fuer noch ungespeicherte
// Aenderungen im Formular funktioniert (statt nur fuer den zuletzt in der DB
// gespeicherten Stand).
func (s *Store) ComputeRMIIIConfigPreview(ctx context.Context, targetID string, matchShotCount, shotsPerSeries int, decimalScoring bool) (string, error) {
	var targetName string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM targets WHERE id=$1`, targetID).Scan(&targetName); err != nil {
		return "", fmt.Errorf("Scheibe: %w", err)
	}
	return computeRMIIIConfig(disagDisciplineFields{
		targetName:     targetName,
		matchShotCount: matchShotCount,
		shotsPerSeries: shotsPerSeries,
		decimalScoring: decimalScoring,
	})
}

// computeRMIIIConfig berechnet den 9-stelligen Einstellungsstring aus den
// Disziplin-/Scheibendaten. Einstellungsstring "Ziffer1..9" - Bedeutung je
// Ziffer laut offizieller RMIIIBA_1/2.pdf-Tabelle (Kapitel Computeranschluss):
//
//	1: 1=LG,2=LP,4=LG-Laufende,5=ZS,6=KK              -> typ
//	2: 1=Einzelscheibe,2=5er Band,3=10er Band         -> fest 1 (Einzelscheibe)
//	3: 0..9 = 1/10/20/30/40/50/60/80/100/120er Serie  -> rmiiiSeriesLengthCode
//	4: 1=Ganze Ringe,2=Zehntel,4=Ganze+Teiler,5=Zehntel+Teiler (7/8: 0.01T-Aufloesung, ungenutzt)
//	5-6: Teilergrenze, 01..16 (14=2000T)               -> teilergrenzeCode
//	7: 1=Teiler markieren, 2..7=messen mit Teilungsfaktor (2=TF 1.0=unskaliert)
//	8: 1=Drucker ein, 2=Drucker aus, 3=Teilerwert nicht aufdrucken
//	9: 1,2,5 Schuss pro Scheibe (5 nur bei Einzelscheibe)
//
// WICHTIG (Hardware-Test-Ergebnis, siehe Konzept): Ziffer 7/8 haben sich am
// echten Geraet als eigenwillig erwiesen - Ziffer 7=2 ("Teiler messen")
// hat teils eine "Keine Berechtigung"-Meldung ausgeloest (Lesen hat trotzdem
// funktioniert), Ziffer 7=1 ("Teiler markieren") lieferte dafuer KEINEN
// numerischen Teilerwert mehr. Welche Ziffer(n) das Geraet tatsaechlich
// benoetigt, ist nicht abschliessend geklaert - im Zweifel ist das manuelle
// Override (disciplines.rmiii_config_override) die zuverlaessigere Wahl.
func computeRMIIIConfig(f disagDisciplineFields) (string, error) {
	typ, ok := matchRMIIITypeByName(f.targetName)
	if !ok {
		return "", errBadRequest(fmt.Sprintf("keine Disag-Scheibenzuordnung fuer %q gefunden", f.targetName))
	}
	ringFormat := 4 // Ganze Ringe + Teiler
	if f.decimalScoring {
		ringFormat = 5 // Zehntel Ringe + Teiler
	}
	// Ziffer 2 (Scheibenlaenge): LG-Disziplinen mit >=10 Schuss werden
	// ueblicherweise auf 10er-Baendern geschossen, nicht auf Einzelscheiben -
	// nur ein Vorschlagswert, in der Bedienoberflaeche (disciplines.html)
	// direkt am Einstellungsstring editierbar (Ziffer 2 wird dort nicht
	// separat gespeichert).
	bandType := 1 // Einzelscheibe
	if typ == 1 && f.matchShotCount >= 10 {
		bandType = 3 // 10er Band
	}
	const teilergrenzeCode = 14 // 2000T, siehe Konzept Abschnitt 2.4
	const teilerMitTF1 = 2      // Teiler messen, Teilungsfaktor 1.0 (unskaliert)
	const druckerAus = 2        // 1=ein, 2=aus
	schussProKarte := 1         // Grundeinstellung nach Einschalten
	if f.shotsPerSeries == 2 || f.shotsPerSeries == 5 {
		schussProKarte = f.shotsPerSeries
	}
	return fmt.Sprintf("%d%d%d%d%02d%d%d%d",
		typ, bandType, rmiiiSeriesLengthCode(f.matchShotCount), ringFormat,
		teilergrenzeCode, teilerMitTF1, druckerAus, schussProKarte), nil
}
