package main

// Papierscheiben: Vergabe der Seriennummer (manuell / fortlaufend) und
// Etikettendruck auf einem Datamax-Etikettendrucker (DPL, z. B. E-4203).
//
// Der Drucker ist in CUPS als Roh-Warteschlange eingerichtet (lokal per
// Parallelport/USB2Parallel, z. B. "parallel:/dev/lp0" bzw. "usb://..." oder
// freigegeben von einem anderen Rechner); der Server schickt die DPL-Daten
// unveraendert mit "lp -o raw".
//
// Etikett 45 x 30 mm (Luecke 2 mm, erkennt der Drucker ueber den
// Lueckensensor):
//   Zeile 1: Barcode Code 128 mit der Seriennummer
//   Zeile 2: Scheibenname, Seriennummer
//   Zeile 3: Teilnehmernummer, Name

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ----------------------------------------------------------------------------
// Einstellungen
// ----------------------------------------------------------------------------

type PapierSettings struct {
	SerialMode   string `json:"serial_mode"` // manuell | fortlaufend
	LabelPrinter string `json:"label_printer"`
	// LabelAutoPrint: Labels der Papierscheiben gleich nach dem Bezahlen in
	// der Kasse drucken (postBezahlen).
	LabelAutoPrint bool `json:"label_auto_print"`
	// Positionierung (siehe dplJob): Vorschub zum Abreissen in mm (nil =
	// Einstellung des Druckers) und Verschiebung des Druckbeginns in mm.
	LabelPresentMM   *float64 `json:"label_present_mm"`
	LabelSOPAdjustMM float64  `json:"label_sop_adjust_mm"`
	// LabelPitchMM: fester Etikettenabstand (Etikett + Luecke) in mm, wenn
	// der Lueckensensor die Luecke nicht sicher erkennt; nil = Lueckensensor.
	LabelPitchMM *float64 `json:"label_pitch_mm"`
}

func (s *Store) GetPapierSettings(ctx context.Context) (PapierSettings, error) {
	var st PapierSettings
	err := s.pool.QueryRow(ctx,
		`SELECT paper_serial_mode, label_printer, label_auto_print,
		        label_present_mm::float8, label_sop_adjust_mm::float8, label_pitch_mm::float8
		 FROM app_settings WHERE id=1`).
		Scan(&st.SerialMode, &st.LabelPrinter, &st.LabelAutoPrint, &st.LabelPresentMM, &st.LabelSOPAdjustMM,
			&st.LabelPitchMM)
	return st, err
}

func (s *Store) SetPapierSettings(ctx context.Context, st PapierSettings) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE app_settings SET paper_serial_mode=$1, label_printer=$2, label_auto_print=$3,
		        label_present_mm=$4, label_sop_adjust_mm=$5, label_pitch_mm=$6 WHERE id=1`,
		st.SerialMode, st.LabelPrinter, st.LabelAutoPrint, st.LabelPresentMM, st.LabelSOPAdjustMM,
		st.LabelPitchMM)
	return err
}

func (s *Store) paperSerialMode(ctx context.Context, tx pgx.Tx) (string, error) {
	var mode string
	err := tx.QueryRow(ctx, `SELECT paper_serial_mode FROM app_settings WHERE id=1`).Scan(&mode)
	return mode, err
}

// nextPaperSerial vergibt die naechste noch nicht verwendete Seriennummer.
// Nummern, die bereits (z. B. manuell eingetippt, in irgendeinem
// Preisschiessen) vergeben sind, werden uebersprungen; Luecken durch
// zurueckgerollte Transaktionen sind gewollt erlaubt.
func nextPaperSerial(ctx context.Context, tx pgx.Tx) (string, error) {
	for i := 0; i < 100000; i++ {
		var n int64
		if err := tx.QueryRow(ctx, `SELECT nextval('ps_paper_serial_seq')`).Scan(&n); err != nil {
			return "", err
		}
		serial := strconv.FormatInt(n, 10)
		var taken bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM ps_kauf_scheiben WHERE physical_serial_no = $1)`,
			serial).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return serial, nil
		}
	}
	return "", errors.New("keine freie Seriennummer gefunden")
}

// paperScheibenIDs liefert die Scheiben (aus scheibeIDs), deren Disziplin auf
// Papier gewertet wird.
func paperScheibenIDs(ctx context.Context, tx pgx.Tx, scheibeIDs []string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT sc.id::text FROM ps_scheiben sc JOIN disciplines d ON d.id = sc.discipline_id
		WHERE sc.id = ANY($1::uuid[]) AND d.scoring_mode = 'papier'`, scheibeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------------------
// Etikett
// ----------------------------------------------------------------------------

type paperLabel struct {
	Serial       string
	ScheibeName  string
	TeilnehmerNr int
	Name         string
}

// Masse in DPL-Metrikeinheiten (0,1 mm), Ursprung unten links. Feinschliff
// nach Probedruck hier.
const (
	labelWidthDots   = 360 // 45 mm bei 203 dpi
	labelMaxChars    = 34  // Schrift 2, einfache Breite, auf 45 mm
	labelBarcodeRow  = 150 // Unterkante Barcode
	labelBarcodeH    = 120 // 12 mm hoch
	labelLine2Row    = 85
	labelLine3Row    = 25
	labelTextLeftCol = 20
)

// labelASCII ersetzt Umlaute und entfernt Zeichen, die die eingebauten
// Druckerschriften nicht sicher darstellen.
func labelASCII(s string) string {
	r := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue", "ß", "ss")
	s = r.Replace(s)
	var b strings.Builder
	for _, c := range s {
		if c >= 0x20 && c < 0x7f {
			b.WriteRune(c)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}

func truncLabel(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "."
}

// dplLabel erzeugt einen DPL-Druckauftrag fuer ein Etikett.
func dplLabel(l paperLabel) []byte {
	serial := labelASCII(l.Serial)
	// Code 128 (Zeichensatz B): 11 Module je Zeichen + Start, Pruefzeichen,
	// Stopp (2 Module laenger). Schmalster Strich 2 Punkte (0,25 mm), wenn
	// das auf das Etikett passt, sonst 1 Punkt.
	modules := 11*(len(serial)+3) + 2
	narrow := 2
	if modules*2 > labelWidthDots-24 {
		narrow = 1
	}
	widthDots := modules * narrow
	col := (labelWidthDots - widthDots) / 2
	if col < 0 {
		col = 0
	}
	col = col * 10 / 8 // Punkte -> 0,1 mm (8 Punkte/mm)

	name2 := labelASCII(l.ScheibeName)
	line2Tail := " " + serial
	name2 = truncLabel(name2, labelMaxChars-len(line2Tail))
	line2 := name2 + line2Tail
	line3 := truncLabel(fmt.Sprintf("%d %s", l.TeilnehmerNr, labelASCII(l.Name)), labelMaxChars)

	var b bytes.Buffer
	b.WriteString("\x02e\r") // Lueckensensor
	b.WriteString("\x02L\r") // Etikett-Formatierung
	b.WriteString("D11\r")   // Punktgroesse 1x1
	b.WriteString("m\r")     // Masseinheit 0,1 mm
	// Barcode: Drehung 1, 'e' = Code 128 ohne Klartext, breiter/schmaler
	// Strich, Hoehe, Zeile, Spalte, Daten
	fmt.Fprintf(&b, "1e%d%d%03d%04d%04d%s\r", narrow, narrow, labelBarcodeH, labelBarcodeRow, col, serial)
	// Text: Drehung 1, Schrift 2, Breite x1, Hoehe x2
	fmt.Fprintf(&b, "1212000%04d%04d%s\r", labelLine2Row, labelTextLeftCol, line2)
	fmt.Fprintf(&b, "1212000%04d%04d%s\r", labelLine3Row, labelTextLeftCol, line3)
	b.WriteString("Q0001\r")
	b.WriteString("E\r")
	return b.Bytes()
}

// Druckbeginn (Start of Print) der Modelle ohne Display ab Werk: 0220
// Zoll/100 = 559 Zehntel-mm vom Mediensensor (DPL-Handbuch, <STX>O).
const dplDefaultSOP = 559

// dplJob stellt einem oder mehreren Labels die Positionierbefehle voran.
// Alle Befehle gelten nur bis zum Ausschalten des Druckers (keine dauerhafte
// Konfiguration) und wirken bei Modellen ohne Display nur, wenn im
// Druckermenue die Optionssteuerung (Option Control) auf "Host" steht.
//
//	<STX>m      Masseinheit 0,1 mm fuer die folgenden Befehle
//	<STX>cnnnn  fester Etikettenabstand statt Lueckensensor; c0000 schaltet
//	            den Lueckensensor wieder ein (gilt sonst bis zum Ausschalten)
//	<STX>Onnnn  Druckbeginn ab Mediensensor (nur bei Verschiebung != 0)
//	<STX>Kfnnnn Vorschub nach dem Druck relativ zum naechsten Druckbeginn;
//	            der Drucker zieht das Etikett vor dem naechsten Druck zurueck
//	<STX>fnnn   dasselbe fuer aeltere Modelle (Abstand ab Mediensensor)
func dplJob(st PapierSettings, labels ...[]byte) []byte {
	var b bytes.Buffer
	b.WriteString("\x02m")
	pitch := 0
	if st.LabelPitchMM != nil {
		pitch = int(math.Round(*st.LabelPitchMM * 10))
	}
	fmt.Fprintf(&b, "\x02c%04d", pitch)
	sop := dplDefaultSOP + int(math.Round(st.LabelSOPAdjustMM*10))
	if st.LabelSOPAdjustMM != 0 {
		if sop < 50 {
			sop = 50
		}
		fmt.Fprintf(&b, "\x02O%04d", sop)
	}
	if st.LabelPresentMM != nil {
		present := int(math.Round(*st.LabelPresentMM * 10))
		fmt.Fprintf(&b, "\x02Kf%04d\r", present)
		if f := sop + present; present > 0 && f <= 999 {
			fmt.Fprintf(&b, "\x02f%03d", f)
		}
	}
	for _, l := range labels {
		b.Write(l)
	}
	return b.Bytes()
}

// dplAlignmentLabel: Rahmen 1 mm innerhalb der Etikettenkante und
// Mittelkreuz - zeigt, wie weit der Druck auf dem Etikett verschoben ist.
func dplAlignmentLabel() []byte {
	var b bytes.Buffer
	b.WriteString("\x02L\r")
	b.WriteString("D11\r")
	b.WriteString("m\r")
	b.WriteString("1X1100000100010B430280004004\r") // Rahmen 43 x 28 mm ab 1/1 mm
	b.WriteString("1X1100001490000L450002\r")       // waagrechte Mittellinie
	b.WriteString("1X1100000000224L002300\r")       // senkrechte Mittellinie
	b.WriteString("1211000002200030Ausrichtung\r")
	b.WriteString("Q0001\r")
	b.WriteString("E\r")
	return b.Bytes()
}

func printRaw(ctx context.Context, printer, title string, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lp", "-d", printer, "-o", "raw", "-t", title, "-")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Druckauftrag fehlgeschlagen: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// kaufLabels liefert die Etiketten (Einheiten mit Seriennummer) eines nicht
// zurueckgegebenen Kaufs im Preisschiessen.
func (s *Store) kaufLabels(ctx context.Context, preisschiessenID, kaufID string) ([]string, []paperLabel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ks.id::text, ks.physical_serial_no, sc.name, t.teilnehmer_nr,
		       trim(sh.first_name || ' ' || sh.last_name)
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k       ON k.id = ks.kauf_id
		JOIN ps_teilnehmer t   ON t.id = k.teilnehmer_id
		JOIN shooters sh       ON sh.id = t.shooter_id
		JOIN ps_scheiben sc    ON sc.id = ks.scheibe_id
		WHERE ks.kauf_id = $1 AND ks.preisschiessen_id = $2
		  AND k.returned_at IS NULL AND ks.physical_serial_no IS NOT NULL
		ORDER BY ks.serial_no`, kaufID, preisschiessenID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ids []string
	var labels []paperLabel
	for rows.Next() {
		var id string
		var l paperLabel
		if err := rows.Scan(&id, &l.Serial, &l.ScheibeName, &l.TeilnehmerNr, &l.Name); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		labels = append(labels, l)
	}
	return ids, labels, rows.Err()
}

// ----------------------------------------------------------------------------
// HTTP
// ----------------------------------------------------------------------------

// GET /api/settings/papierscheiben
func (a *APIServer) getPapierSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "settings"); err != nil {
		return nil, err
	}
	st, err := a.store.GetPapierSettings(r.Context())
	if err != nil {
		return nil, err
	}
	printers, def, perr := listPrinters()
	res := map[string]any{"settings": st, "printers": printers, "default_printer": def}
	if perr != nil {
		res["printer_error"] = perr.Error()
	}
	return res, nil
}

// PUT /api/settings/papierscheiben
func (a *APIServer) putPapierSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "settings"); err != nil {
		return nil, err
	}
	st, err := decodeBody[PapierSettings](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	st.LabelPrinter = strings.TrimSpace(st.LabelPrinter)
	if st.LabelPresentMM != nil && (*st.LabelPresentMM < 0 || *st.LabelPresentMM > 40) {
		return nil, errBadRequest("Vorschub zum Abreißen: 0 bis 40 mm")
	}
	if st.LabelPitchMM != nil && (*st.LabelPitchMM < 10 || *st.LabelPitchMM > 200) {
		return nil, errBadRequest("Etikettenabstand: 10 bis 200 mm")
	}
	if st.LabelSOPAdjustMM < -20 || st.LabelSOPAdjustMM > 20 {
		return nil, errBadRequest("Druckbeginn verschieben: -20 bis +20 mm")
	}
	if st.SerialMode != "manuell" && st.SerialMode != "fortlaufend" {
		return nil, errBadRequest(`Seriennummer muss "manuell" oder "fortlaufend" sein`)
	}
	if err := a.store.SetPapierSettings(r.Context(), st); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// POST /api/settings/papierscheiben/test-label
func (a *APIServer) postPapierTestLabel(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "settings"); err != nil {
		return nil, err
	}
	st, err := a.store.GetPapierSettings(r.Context())
	if err != nil {
		return nil, err
	}
	if st.LabelPrinter == "" {
		return nil, errBadRequest("Etikettendrucker zuerst auswählen und speichern")
	}
	data := dplJob(st, dplLabel(paperLabel{Serial: "123456", ScheibeName: "Testscheibe", TeilnehmerNr: 99, Name: "Testetikett Müller"}))
	if err := printRaw(r.Context(), st.LabelPrinter, "Testetikett", data); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// POST /api/settings/papierscheiben/printer-action {"action":"align"}
//
//	align  Ausrichtungs-Etikett (Rahmen + Mittelkreuz) mit den eingestellten
//	       Positionierbefehlen drucken
//
// Einmessen geht beim E-4203 nur am Geraet (<STX>K}Q kennen erst die Modelle
// der Class Series 2) - Anleitung in den Einstellungen.
func (a *APIServer) postPapierPrinterAction(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "settings"); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Action string `json:"action"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	st, err := a.store.GetPapierSettings(r.Context())
	if err != nil {
		return nil, err
	}
	if st.LabelPrinter == "" {
		return nil, errBadRequest("Etikettendrucker zuerst auswählen und speichern")
	}
	var data []byte
	var title string
	switch body.Action {
	case "align":
		data, title = dplJob(st, dplAlignmentLabel()), "Ausrichtungs-Etikett"
	default:
		return nil, errBadRequest(`action muss "align" sein`)
	}
	if err := printRaw(r.Context(), st.LabelPrinter, title, data); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// GET /api/papierscheiben/config - fuer die Kasse: Art der Seriennummer und
// ob ein Etikettendrucker eingerichtet ist.
func (a *APIServer) getPapierConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	st, err := a.store.GetPapierSettings(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{"serial_mode": st.SerialMode, "label_printer_set": st.LabelPrinter != "",
		"label_auto_print": st.LabelAutoPrint && st.LabelPrinter != ""}, nil
}

// POST /api/preisschiessen/{id}/kaeufe/{kid}/label - druckt die Etiketten
// aller Papierscheiben eines Kaufs (erneuter Aufruf = Ersatz-Etikett).
func (a *APIServer) postKaufLabel(w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	st, err := a.store.GetPapierSettings(ctx)
	if err != nil {
		return nil, err
	}
	n, err := a.printKaufLabels(ctx, st, r.PathValue("id"), []string{r.PathValue("kid")})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errBadRequest("Keine Papierscheibe mit Seriennummer in diesem Kauf")
	}
	return map[string]any{"ok": true, "count": n}, nil
}

// printKaufLabels druckt die Labels aller Papierscheiben (mit Seriennummer)
// der angegebenen Kaeufe in einem Druckauftrag und vermerkt den Druck.
// Liefert die Anzahl gedruckter Labels (0 = keine Papierscheibe dabei).
func (a *APIServer) printKaufLabels(ctx context.Context, st PapierSettings, preisschiessenID string, kaufIDs []string) (int, error) {
	if st.LabelPrinter == "" {
		return 0, errBadRequest("Kein Etikettendrucker eingerichtet (Einstellungen → Papierscheiben)")
	}
	var ids []string
	var labels []paperLabel
	for _, kid := range kaufIDs {
		i, l, err := a.store.kaufLabels(ctx, preisschiessenID, kid)
		if err != nil {
			return 0, err
		}
		ids = append(ids, i...)
		labels = append(labels, l...)
	}
	if len(labels) == 0 {
		return 0, nil
	}
	var parts [][]byte
	for _, l := range labels {
		parts = append(parts, dplLabel(l))
	}
	data := dplJob(st, parts...)
	if err := printRaw(ctx, st.LabelPrinter, "Etikett "+labels[0].Serial, data); err != nil {
		return 0, err
	}
	if _, err := a.store.pool.Exec(ctx, `
		UPDATE ps_kauf_scheiben SET label_printed_at = now(), label_print_count = label_print_count + 1
		WHERE id = ANY($1::uuid[])`, ids); err != nil {
		return 0, err
	}
	return len(labels), nil
}
