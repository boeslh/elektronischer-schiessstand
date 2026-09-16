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
	rmivOverride   string
	// bandType/shotsPerCard: 0 = automatisch (Heuristik, siehe
	// disagBandHeuristic) - nur von den *Preview-Funktionen gesetzt, wenn
	// die Bedienoberflaeche einen bereits manuell gewaehlten Wert (Scheiben-
	// laenge-/Schuss-pro-Scheibe-Auswahlfelder in disciplines.html)
	// beibehalten will, statt ihn durch die Heuristik zu ueberschreiben. Der
	// tatsaechliche Laufzeit-Pfad (BuildDisagConfig/disagDisciplineFieldsFor)
	// setzt diese Felder nie (es gibt dafuer keine eigene DB-Spalte - beide
	// Protokolle kodieren eine manuelle Wahl stattdessen direkt im jeweiligen
	// Override-String), verhaelt sich also unveraendert wie bisher.
	bandType     int
	shotsPerCard int
}

// disagBandHeuristic liefert einen Vorschlag fuer Scheibenlaenge (1=Einzel-
// scheibe, 2=5er-Band, 3=10er-Band - RM-III-Ziffer-2-Kodierung, bei RM-IV
// auf SCH=LGES/LG5/LG10 abgebildet) und Schuss/Scheibe - gemeinsam von
// computeRMIIIConfig (Ziffer 2/9) und computeRMIVConfig (SCH=-Variante/SSC=)
// verwendet, damit beide Protokolle bei gleichen Eingabedaten denselben
// Vorschlag liefern (siehe disciplines.html: die Auswahlfelder wirken sich
// jetzt auf beide Einstellungsstrings gemeinsam aus).
func disagBandHeuristic(isLG bool, matchShotCount, shotsPerSeries int) (bandType, shotsPerCard int) {
	bandType = 1 // Einzelscheibe
	if isLG && matchShotCount >= 10 {
		bandType = 3 // 10er Band
	}
	shotsPerCard = 1 // Grundeinstellung nach Einschalten
	if shotsPerSeries == 2 || shotsPerSeries == 5 {
		shotsPerCard = shotsPerSeries
	}
	return
}

func (s *Store) disagDisciplineFieldsFor(ctx context.Context, disciplineID string) (disagDisciplineFields, error) {
	var f disagDisciplineFields
	err := s.pool.QueryRow(ctx, `
		SELECT tg.name, d.match_shot_count, d.shots_per_series, d.decimal_scoring,
		       d.wertmaschine_caliber_mm, COALESCE(d.rmiii_config_override,''), COALESCE(d.rmiv_config_override,'')
		FROM disciplines d JOIN targets tg ON tg.id = d.target_id
		WHERE d.id = $1`, disciplineID,
	).Scan(&f.targetName, &f.matchShotCount, &f.shotsPerSeries, &f.decimalScoring,
		&f.caliberMM, &f.rmiiiOverride, &f.rmivOverride)
	if err != nil {
		return f, fmt.Errorf("Disziplin/Scheibe: %w", err)
	}
	return f, nil
}

// BuildDisagConfig liefert den Konfigurationsstring fuer die Disag-
// Wertmaschine (RM IV/RMIII-Win: key=value-String OHNE Pruefsumme/CR - das
// haengt der wertmaschine-Dienst selbst an, siehe disag/rmiv.go dort; RM
// III: 9-stelliger Zahlencode) fuer eine Disziplin. In beiden Faellen wird -
// falls gesetzt - das jeweilige manuelle Override (disciplines.
// rmiii_config_override bzw. rmiv_config_override, siehe migrations/058
// bzw. 066) unveraendert zurueckgegeben statt berechnet; die automatische
// RM-III-Berechnung diente mit echter Hardware mehrfach als nachweislich
// unzuverlaessige Ausgangsbasis - fuer RM IV/RMIII-Win gilt dieselbe
// Vorsicht, bis die automatische Berechnung an echter Hardware bestaetigt
// ist (siehe computeRMIVConfig-Kommentar).
func (s *Store) BuildDisagConfig(ctx context.Context, disciplineID, protocol string) (string, error) {
	f, err := s.disagDisciplineFieldsFor(ctx, disciplineID)
	if err != nil {
		return "", err
	}

	switch protocol {
	case "rmiv":
		if f.rmivOverride != "" {
			return f.rmivOverride, nil
		}
		return computeRMIVConfig(f)
	case "rmiii":
		if f.rmiiiOverride != "" {
			return f.rmiiiOverride, nil
		}
		return computeRMIIIConfig(f)
	default:
		return "", errBadRequest("unbekanntes Protokoll (rmiv|rmiii erwartet)")
	}
}

// computeRMIVConfig berechnet den RM-IV/RMIII-Win-Konfigurationsstring
// (SCH=...;-Schluessel-Wert-Format, siehe VB-Abend/schnittstellenbeschreibung.pdf)
// aus den Disziplin-/Scheibendaten - automatischer Vorschlagswert, per
// disciplines.rmiv_config_override uebersteuerbar (siehe BuildDisagConfig).
// NOCH NICHT AN ECHTER HARDWARE VERIFIZIERT (anders als die RM-III-Ziffern,
// wo genau deshalb ein Override eingefuehrt wurde) - TEA=ZT (Teilerwertung
// mit Zehntel-Teiler) und TEG=2000 sind Startwerte, keine getesteten
// Optimalwerte.
//
// Scheibenlaenge/Schuss-pro-Scheibe (siehe disagBandHeuristic) werden bei
// der LG-Scheibenfamilie direkt auf die passende SCH=-Variante abgebildet
// (LGES=Einzelscheibe/LG5=5er-Band/LG10=10er-Band, siehe
// VB-Abend/schnittstellenbeschreibung.pdf) bzw. als SSC= uebernommen - bei
// allen anderen Scheibentypen kennt das Protokoll laut Dokumentation keine
// Bandvarianten, dort bleibt SCH= unveraendert.
func computeRMIVConfig(f disagDisciplineFields) (string, error) {
	sch, ok := matchDisagSchByName(f.targetName)
	if !ok {
		return "", errBadRequest(fmt.Sprintf("keine Disag-Scheibenzuordnung fuer %q gefunden", f.targetName))
	}
	isLG := sch == "LG10"
	bandType, ssc := f.bandType, f.shotsPerCard
	if bandType == 0 || ssc == 0 {
		autoBand, autoSPK := disagBandHeuristic(isLG, f.matchShotCount, f.shotsPerSeries)
		if bandType == 0 {
			bandType = autoBand
		}
		if ssc == 0 {
			ssc = autoSPK
		}
	}
	if isLG {
		switch bandType {
		case 1:
			sch = "LGES"
		case 2:
			sch = "LG5"
		case 3:
			sch = "LG10"
		}
	}
	cfg := fmt.Sprintf("SCH=%s;SZI=%d;SGE=%d;SSC=%d;TEA=ZT;TEG=2000;", sch, f.shotsPerSeries, f.matchShotCount, ssc)
	if f.caliberMM != nil {
		cfg = fmt.Sprintf("KAL=%.2f;", *f.caliberMM) + cfg
	}
	return cfg, nil
}

// ComputeRMIVConfigPreview berechnet den RM-IV/RMIII-Win-Konfigurationsstring
// aus uebergebenen Parametern statt aus einer bereits gespeicherten
// Disziplin - fuer den (jetzt gemeinsamen) "Standard einsetzen"-Button in
// disciplines.html, analog ComputeRMIIIConfigPreview. bandType/shotsPerCard
// = 0 laesst computeRMIVConfig die automatische Heuristik anwenden -
// disciplines.html uebergibt hier die aktuellen Werte der (fuer beide
// Protokolle gemeinsam genutzten) Scheibenlaenge-/Schuss-pro-Scheibe-
// Auswahlfelder, wenn diese bereits eine manuelle Wahl tragen.
func (s *Store) ComputeRMIVConfigPreview(ctx context.Context, targetID string, matchShotCount, shotsPerSeries int, caliberMM *float64, bandType, shotsPerCard int) (string, error) {
	var targetName string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM targets WHERE id=$1`, targetID).Scan(&targetName); err != nil {
		return "", fmt.Errorf("Scheibe: %w", err)
	}
	return computeRMIVConfig(disagDisciplineFields{
		targetName:     targetName,
		matchShotCount: matchShotCount,
		shotsPerSeries: shotsPerSeries,
		caliberMM:      caliberMM,
		bandType:       bandType,
		shotsPerCard:   shotsPerCard,
	})
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
	// Ziffer 2 (Scheibenlaenge) und Ziffer 9 (Schuss/Scheibe): Vorschlagswert
	// aus der gemeinsamen Heuristik (siehe disagBandHeuristic), ausser die
	// Bedienoberflaeche hat bereits eine manuelle Wahl getroffen (f.bandType/
	// f.shotsPerCard != 0 - nur beim erneuten Berechnen mit bereits
	// befuelltem Feld relevant, siehe disciplines.html) - in der Bedien-
	// oberflaeche direkt am Einstellungsstring editierbar (Ziffer 2/9 werden
	// dort nicht separat gespeichert).
	bandType, schussProKarte := f.bandType, f.shotsPerCard
	if bandType == 0 || schussProKarte == 0 {
		autoBand, autoSPK := disagBandHeuristic(typ == 1, f.matchShotCount, f.shotsPerSeries)
		if bandType == 0 {
			bandType = autoBand
		}
		if schussProKarte == 0 {
			schussProKarte = autoSPK
		}
	}
	const teilergrenzeCode = 14 // 2000T, siehe Konzept Abschnitt 2.4
	const teilerMitTF1 = 2      // Teiler messen, Teilungsfaktor 1.0 (unskaliert)
	const druckerAus = 2        // 1=ein, 2=aus
	return fmt.Sprintf("%d%d%d%d%02d%d%d%d",
		typ, bandType, rmiiiSeriesLengthCode(f.matchShotCount), ringFormat,
		teilergrenzeCode, teilerMitTF1, druckerAus, schussProKarte), nil
}
