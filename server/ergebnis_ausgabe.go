package main

// ergebnis_ausgabe.go – Ausgabe des Ergebnisses an den Schuetzen direkt am
// Stand-PC ("📤 Ergebnis"):
//
//   - E-Mail: Einzelergebnis-PDF (buildSessionPDF) an die in den Stammdaten
//     hinterlegte Adresse, Versand ueber den in den Einstellungen gepflegten
//     Mailserver (app_settings.smtp_*, migrations/077).
//   - Druck: dasselbe PDF an den in den Einstellungen gewaehlten CUPS-Drucker
//     (lp -d <drucker>).
//   - QR-Code: kompakte Textdarstellung mit bis zu 60 Schuessen inkl.
//     Koordinaten fuer die App "My Shootings" (Format siehe buildResultQR).
//
// Die Stand-Endpunkte (/api/lanes/{no}/ergebnis-ausgabe...) sind wie alle
// anderen Stand-PC-Endpunkte ohne Rolle erreichbar. Sie arbeiten deshalb nur
// mit der JUENGSTEN Session des Stands (keine beliebige Session-ID von aussen)
// und Mail/Druck sind pro Session begrenzt (ausgabeRateLimit).

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"rsc.io/qr"
)

// ----------------------------------------------------------------------------
// Einstellungen (Mailserver / Drucker)
// ----------------------------------------------------------------------------

type AusgabeSettings struct {
	SMTPHost        string `json:"smtp_host"`
	SMTPPort        int    `json:"smtp_port"`
	SMTPSecurity    string `json:"smtp_security"` // starttls | ssl | none
	SMTPUser        string `json:"smtp_user"`
	SMTPPassword    string `json:"-"`
	SMTPPasswordSet bool   `json:"smtp_password_set"`
	SMTPFrom        string `json:"smtp_from"`
	SMTPFromName    string `json:"smtp_from_name"`
	PrinterName     string `json:"printer_name"`
}

func (s AusgabeSettings) mailConfigured() bool {
	return s.SMTPHost != "" && s.SMTPFrom != ""
}

func (s *Store) GetAusgabeSettings(ctx context.Context) (AusgabeSettings, error) {
	var st AusgabeSettings
	err := s.pool.QueryRow(ctx, `
		SELECT smtp_host, smtp_port, smtp_security, smtp_user, smtp_password,
		       smtp_from, smtp_from_name, printer_name
		FROM app_settings WHERE id=1`).Scan(&st.SMTPHost, &st.SMTPPort, &st.SMTPSecurity,
		&st.SMTPUser, &st.SMTPPassword, &st.SMTPFrom, &st.SMTPFromName, &st.PrinterName)
	st.SMTPPasswordSet = st.SMTPPassword != ""
	return st, err
}

// SetAusgabeSettings speichert alles ausser dem Passwort; password != nil
// setzt es zusaetzlich ("" = loeschen).
func (s *Store) SetAusgabeSettings(ctx context.Context, st AusgabeSettings, password *string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE app_settings SET smtp_host=$1, smtp_port=$2, smtp_security=$3,
		       smtp_user=$4, smtp_from=$5, smtp_from_name=$6, printer_name=$7,
		       smtp_password=COALESCE($8, smtp_password)
		WHERE id=1`, st.SMTPHost, st.SMTPPort, st.SMTPSecurity, st.SMTPUser,
		st.SMTPFrom, st.SMTPFromName, st.PrinterName, password)
	return err
}

// listPrinters liefert die in CUPS eingerichteten Drucker (lpstat -e) und den
// CUPS-Standarddrucker.
func listPrinters() (printers []string, def string, err error) {
	out, err := exec.Command("lpstat", "-e").Output()
	if err != nil {
		return nil, "", fmt.Errorf("lpstat -e: %v (CUPS installiert?)", err)
	}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			printers = append(printers, l)
		}
	}
	if d, err := exec.Command("lpstat", "-d").Output(); err == nil {
		// "Standardziel des Systems: X" bzw. "system default destination: X"
		if i := strings.LastIndex(string(d), ":"); i >= 0 {
			def = strings.TrimSpace(string(d)[i+1:])
		}
	}
	return printers, def, nil
}

func (a *APIServer) getAusgabeSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "settings"); err != nil {
		return nil, err
	}
	st, err := a.store.GetAusgabeSettings(r.Context())
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

func (a *APIServer) putAusgabeSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "settings"); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		AusgabeSettings
		SMTPPassword *string `json:"smtp_password"`
	}](r)
	if err != nil {
		return nil, err
	}
	st := body.AusgabeSettings
	st.SMTPHost = strings.TrimSpace(st.SMTPHost)
	st.SMTPUser = strings.TrimSpace(st.SMTPUser)
	st.SMTPFrom = strings.TrimSpace(st.SMTPFrom)
	st.SMTPFromName = strings.TrimSpace(st.SMTPFromName)
	st.PrinterName = strings.TrimSpace(st.PrinterName)
	switch st.SMTPSecurity {
	case "starttls", "ssl", "none":
	default:
		return nil, errBadRequest("Verschlüsselung muss starttls, ssl oder none sein")
	}
	if st.SMTPPort <= 0 || st.SMTPPort > 65535 {
		return nil, errBadRequest("ungültiger SMTP-Port")
	}
	if st.SMTPFrom != "" {
		if _, err := mail.ParseAddress(st.SMTPFrom); err != nil {
			return nil, errBadRequest("ungültige Absenderadresse")
		}
	}
	if err := a.store.SetAusgabeSettings(r.Context(), st, body.SMTPPassword); err != nil {
		return nil, err
	}
	return a.getAusgabeSettings(w, r)
}

func (a *APIServer) postAusgabeTestMail(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "settings"); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		To string `json:"to"`
	}](r)
	if err != nil {
		return nil, err
	}
	st, err := a.store.GetAusgabeSettings(r.Context())
	if err != nil {
		return nil, err
	}
	if !st.mailConfigured() {
		return nil, errBadRequest("Mailserver und Absender zuerst speichern")
	}
	if err := sendMail(st, body.To, "Testmail Schießstand",
		"Diese Testmail bestätigt, dass der Mailversand des Schießstand-Servers funktioniert.\n", "", nil); err != nil {
		return nil, &httpError{code: http.StatusBadGateway, msg: err.Error()}
	}
	return map[string]bool{"ok": true}, nil
}

func (a *APIServer) postAusgabeTestPrint(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireTile(w, r, "settings"); err != nil {
		return nil, err
	}
	st, err := a.store.GetAusgabeSettings(r.Context())
	if err != nil {
		return nil, err
	}
	if st.PrinterName == "" {
		return nil, errBadRequest("Drucker zuerst auswählen und speichern")
	}
	pdf, tr := newReportPDF()
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 16)
	pdf.Cell(0, 10, tr("Testseite Schießstand-Server"))
	pdf.Ln(12)
	pdf.SetFont("Helvetica", "", 11)
	pdf.Cell(0, 8, tr("Drucker: "+st.PrinterName+" – "+time.Now().Format("02.01.2006 15:04")))
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	if err := printPDF(r.Context(), st.PrinterName, "Testseite", buf.Bytes()); err != nil {
		return nil, &httpError{code: http.StatusBadGateway, msg: err.Error()}
	}
	return map[string]bool{"ok": true}, nil
}

// ----------------------------------------------------------------------------
// Versand / Druck
// ----------------------------------------------------------------------------

// sendMail verschickt eine Textmail, optional mit einem PDF-Anhang.
func sendMail(st AusgabeSettings, to, subject, text, attachName string, attachment []byte) error {
	toAddr, err := mail.ParseAddress(strings.TrimSpace(to))
	if err != nil {
		return fmt.Errorf("ungültige Empfängeradresse")
	}
	from := mail.Address{Name: st.SMTPFromName, Address: st.SMTPFrom}
	addr := net.JoinHostPort(st.SMTPHost, strconv.Itoa(st.SMTPPort))
	tlsCfg := &tls.Config{ServerName: st.SMTPHost}

	var conn net.Conn
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if st.SMTPSecurity == "ssl" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("Mailserver nicht erreichbar: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, st.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP: %v", err)
	}
	defer c.Close()
	if st.SMTPSecurity == "starttls" {
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %v", err)
		}
	}
	if st.SMTPUser != "" {
		// PlainAuth verweigert unverschluesselte Verbindungen (ausser localhost)
		if err := c.Auth(smtp.PlainAuth("", st.SMTPUser, st.SMTPPassword, st.SMTPHost)); err != nil {
			return fmt.Errorf("Anmeldung am Mailserver fehlgeschlagen: %v", err)
		}
	}
	if err := c.Mail(st.SMTPFrom); err != nil {
		return fmt.Errorf("Absender abgelehnt: %v", err)
	}
	if err := c.Rcpt(toAddr.Address); err != nil {
		return fmt.Errorf("Empfänger abgelehnt: %v", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %v", err)
	}
	if _, err := wc.Write(buildMailMessage(from, *toAddr, subject, text, attachName, attachment)); err != nil {
		return fmt.Errorf("SMTP: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("Mail nicht angenommen: %v", err)
	}
	return c.Quit()
}

func buildMailMessage(from, to mail.Address, subject, text, attachName string, attachment []byte) []byte {
	var b bytes.Buffer
	boundary := fmt.Sprintf("schiessstand-%d", time.Now().UnixNano())
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n",
		from.String(), to.String(), mime.QEncoding.Encode("utf-8", subject),
		time.Now().Format(time.RFC1123Z))
	textPart := func() {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		writeBase64Lines(&b, []byte(text))
	}
	if attachment == nil {
		textPart()
		return b.Bytes()
	}
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n--%s\r\n", boundary, boundary)
	textPart()
	fmt.Fprintf(&b, "--%s\r\nContent-Type: application/pdf\r\nContent-Transfer-Encoding: base64\r\n"+
		"Content-Disposition: attachment; filename=%q\r\n\r\n", boundary, attachName)
	writeBase64Lines(&b, attachment)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func writeBase64Lines(b *bytes.Buffer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
}

func printPDF(ctx context.Context, printer, title string, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lp", "-d", printer, "-t", title, "-")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Druckauftrag fehlgeschlagen: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ausgabeRateLimit: hoechstens 3 Mails bzw. 3 Ausdrucke je Session in 10
// Minuten - die Stand-Endpunkte sind ohne Anmeldung erreichbar.
var ausgabeRateLimit = struct {
	sync.Mutex
	hits map[string][]time.Time
}{hits: map[string][]time.Time{}}

func ausgabeAllowed(key string) bool {
	ausgabeRateLimit.Lock()
	defer ausgabeRateLimit.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, t := range ausgabeRateLimit.hits[key] {
		if now.Sub(t) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= 3 {
		ausgabeRateLimit.hits[key] = keep
		return false
	}
	ausgabeRateLimit.hits[key] = append(keep, now)
	return true
}

// ----------------------------------------------------------------------------
// Stand-Endpunkte
// ----------------------------------------------------------------------------

// laneAusgabeSession: juengste Session des Stands mit mindestens einem
// Wertungsschuss aus den letzten 12 Stunden (laufend oder bereits beendet,
// Wartungs-Sessions ausgenommen).
type laneAusgabeSession struct {
	SessionID   string
	ShooterName string
	Email       string
	Discipline  string
	TargetName  string
	StartedAt   time.Time
}

func (s *Store) latestAusgabeSession(ctx context.Context, laneNo int) (*laneAusgabeSession, error) {
	var ls laneAusgabeSession
	err := s.pool.QueryRow(ctx, `
		SELECT se.id::text, COALESCE(sh.first_name || ' ' || sh.last_name, ''),
		       COALESCE(sh.email, ''), d.name, tg.name, se.started_at
		FROM sessions se
		JOIN lanes l       ON l.id  = se.lane_id
		JOIN disciplines d ON d.id  = se.discipline_id
		JOIN targets tg    ON tg.id = d.target_id
		LEFT JOIN shooters sh ON sh.id = se.shooter_id
		WHERE l.lane_no = $1 AND NOT se.maintenance
		  AND se.started_at > now() - interval '12 hours'
		  AND EXISTS (SELECT 1 FROM shots s WHERE s.session_id = se.id AND s.kind = 'match')
		ORDER BY se.started_at DESC LIMIT 1`, laneNo).Scan(&ls.SessionID, &ls.ShooterName,
		&ls.Email, &ls.Discipline, &ls.TargetName, &ls.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &ls, err
}

// maskEmail: "max.mustermann@example.de" -> "ma…@example.de" (Anzeige am
// Stand, die volle Adresse geht nicht an den Stand-PC).
func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at < 0 {
		return "…"
	}
	local := []rune(e[:at])
	if len(local) > 2 {
		local = local[:2]
	}
	return string(local) + "…" + e[at:]
}

// laneAusgabe liest Standnummer und Session; wantSession != "" muss der
// juengsten Session entsprechen (sonst ist inzwischen ein neuer Schuetze dran).
func (a *APIServer) laneAusgabe(r *http.Request, wantSession string) (*laneAusgabeSession, error) {
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errBadRequest("ungültige Standnummer")
	}
	ls, err := a.store.latestAusgabeSession(r.Context(), no)
	if err != nil {
		return nil, err
	}
	if ls == nil {
		return nil, &httpError{code: http.StatusNotFound, msg: "Kein Ergebnis mit Wertungsschüssen auf diesem Stand."}
	}
	if wantSession != "" && wantSession != ls.SessionID {
		return nil, &httpError{code: http.StatusConflict, msg: "Das Ergebnis ist nicht mehr aktuell - bitte neu öffnen."}
	}
	return ls, nil
}

func (a *APIServer) getLaneErgebnisAusgabe(w http.ResponseWriter, r *http.Request) (any, error) {
	ls, err := a.laneAusgabe(r, "")
	if err != nil {
		return nil, err
	}
	st, err := a.store.GetAusgabeSettings(r.Context())
	if err != nil {
		return nil, err
	}
	shots, err := a.store.getSessionMatchShots(r.Context(), ls.SessionID)
	if err != nil {
		return nil, err
	}
	res := map[string]any{
		"session_id":      ls.SessionID,
		"shooter_name":    ls.ShooterName,
		"discipline":      ls.Discipline,
		"started_at":      ls.StartedAt,
		"shot_count":      len(shots),
		"mail_configured": st.mailConfigured(),
		"has_email":       ls.Email != "",
		"email_masked":    "",
		"print_available": st.PrinterName != "",
		"qr_max_shots":    qrMaxShots,
	}
	if ls.Email != "" {
		res["email_masked"] = maskEmail(ls.Email)
	}
	return res, nil
}

func (a *APIServer) postLaneErgebnisMail(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		SessionID string `json:"session_id"`
	}](r)
	if err != nil {
		return nil, err
	}
	ls, err := a.laneAusgabe(r, body.SessionID)
	if err != nil {
		return nil, err
	}
	if ls.Email == "" {
		return nil, errBadRequest("Für diesen Schützen ist keine E-Mail-Adresse hinterlegt.")
	}
	st, err := a.store.GetAusgabeSettings(r.Context())
	if err != nil {
		return nil, err
	}
	if !st.mailConfigured() {
		return nil, errBadRequest("Kein Mailserver eingerichtet.")
	}
	if !ausgabeAllowed("mail:" + ls.SessionID) {
		return nil, &httpError{code: http.StatusTooManyRequests, msg: "Das Ergebnis wurde gerade schon mehrfach verschickt."}
	}
	pdf, header, shots, err := a.buildSessionPDF(r.Context(), ls.SessionID)
	if err != nil {
		return nil, err
	}
	date := ls.StartedAt.Local().Format("02.01.2006")
	text := fmt.Sprintf("Hallo %s,\n\nim Anhang findest du dein Ergebnis vom %s (%s, Stand %d, %d Wertungsschüsse).\n\nGut Schuss!\n",
		ls.ShooterName, date, header.Discipline, header.LaneNo, len(shots))
	name := fmt.Sprintf("Ergebnis_%s_%s.pdf", ls.StartedAt.Local().Format("2006-01-02_1504"), pdfFilenameSafe(header.ShooterName))
	if err := sendMail(st, ls.Email, "Dein Ergebnis vom "+date+" – "+header.Discipline, text, name, pdf); err != nil {
		return nil, &httpError{code: http.StatusBadGateway, msg: err.Error()}
	}
	return map[string]any{"ok": true, "email_masked": maskEmail(ls.Email)}, nil
}

func (a *APIServer) postLaneErgebnisPrint(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		SessionID string `json:"session_id"`
	}](r)
	if err != nil {
		return nil, err
	}
	ls, err := a.laneAusgabe(r, body.SessionID)
	if err != nil {
		return nil, err
	}
	st, err := a.store.GetAusgabeSettings(r.Context())
	if err != nil {
		return nil, err
	}
	if st.PrinterName == "" {
		return nil, errBadRequest("Kein Drucker eingerichtet.")
	}
	if !ausgabeAllowed("print:" + ls.SessionID) {
		return nil, &httpError{code: http.StatusTooManyRequests, msg: "Das Ergebnis wurde gerade schon mehrfach gedruckt."}
	}
	pdf, header, _, err := a.buildSessionPDF(r.Context(), ls.SessionID)
	if err != nil {
		return nil, err
	}
	if err := printPDF(r.Context(), st.PrinterName, "Ergebnis "+header.ShooterName, pdf); err != nil {
		return nil, &httpError{code: http.StatusBadGateway, msg: err.Error()}
	}
	return map[string]any{"ok": true, "printer": st.PrinterName}, nil
}

// getLaneErgebnisQR liefert den QR-Code als SVG (eigener Handler, kein JSON).
func (a *APIServer) getLaneErgebnisQR(w http.ResponseWriter, r *http.Request) {
	ls, err := a.laneAusgabe(r, r.URL.Query().Get("session_id"))
	if err == nil {
		var text string
		if text, err = a.buildResultQR(r.Context(), ls); err == nil {
			var svg []byte
			if svg, err = qrSVG(text); err == nil {
				w.Header().Set("Content-Type", "image/svg+xml")
				w.Header().Set("Cache-Control", "no-store")
				w.Write(svg)
				return
			}
		}
	}
	code := http.StatusInternalServerError
	var he *httpError
	if errors.As(err, &he) {
		code = he.code
	}
	http.Error(w, err.Error(), code)
}

// ----------------------------------------------------------------------------
// QR-Code fuer die App
// ----------------------------------------------------------------------------

const qrMaxShots = 60

// buildResultQR erzeugt den QR-Inhalt (Version 1), Felder durch '|' getrennt:
//
//	MS1|<id>|<JJJJMMTTHHMM>|<Name>|<Klasse>|<Disziplin>|<lg|lp|zs|sp|x>|<z|r>|<Schuss/Serie>|<Innenzehner>|<bester Teiler>|<Schuesse>
//
//	id            31-Bit-Zahl aus der Session-UUID (Dublettenerkennung in der App)
//	z|r           Zehntelwertung bzw. ganze Ringe
//	bester Teiler in 1/100 mm wie im PDF (leer = keiner)
//	Schuesse      ';'-getrennt "x,y,w[,r]": x/y in 1/10 mm (Scheibenmitte = 0,
//	              y nach oben), w = Zehntelwert in Zehntelringen (10.4 -> 104,
//	              auch bei Ringwertung; annullierte Schuesse 0), r = ganzer
//	              Ring nur wenn er nicht floor(w/10) ist (Ringberuehrung bei
//	              Ringwertung)
//
// Mehr als qrMaxShots Wertungsschuesse werden abgelehnt - der Code waere am
// Bildschirm nicht mehr zuverlaessig lesbar.
func (a *APIServer) buildResultQR(ctx context.Context, ls *laneAusgabeSession) (string, error) {
	header, err := a.store.GetSessionResultHeader(ctx, ls.SessionID)
	if err != nil {
		return "", err
	}
	shots, err := a.store.getSessionMatchShots(ctx, ls.SessionID)
	if err != nil {
		return "", err
	}
	if len(shots) > qrMaxShots {
		return "", errBadRequest(fmt.Sprintf("Der QR-Code fasst höchstens %d Schüsse (Ergebnis hat %d).", qrMaxShots, len(shots)))
	}
	clean := func(s string) string {
		return strings.NewReplacer("|", "/", ";", ",", "\n", " ", "\r", " ").Replace(strings.TrimSpace(s))
	}
	var id uint32
	for _, c := range strings.ReplaceAll(ls.SessionID, "-", "")[:8] {
		v, _ := strconv.ParseUint(string(c), 16, 8)
		id = id<<4 | uint32(v)
	}
	id &= 0x7fffffff

	targetType := "x"
	switch tn := strings.ToUpper(ls.TargetName); {
	case strings.HasPrefix(tn, "LG"):
		targetType = "lg"
	case strings.HasPrefix(tn, "LP"):
		targetType = "lp"
	case strings.HasPrefix(tn, "ZS"):
		targetType = "zs"
	case strings.HasPrefix(tn, "SP"):
		targetType = "sp"
	}
	scoring := "r"
	if header.DecimalScoring {
		scoring = "z"
	}
	inner := 0
	best := math.Inf(1)
	parts := make([]string, len(shots))
	for i, s := range shots {
		val, ring := 0, 0
		if s.Status != "annulled" {
			val, ring = int(math.Round(s.Decimal*10)), s.Ring
			if s.InnerTen {
				inner++
			}
			best = math.Min(best, s.CenterDistance)
		}
		parts[i] = fmt.Sprintf("%d,%d,%d", int(math.Round(s.XMM*10)), int(math.Round(s.YMM*10)), val)
		if ring != val/10 {
			parts[i] += "," + strconv.Itoa(ring)
		}
	}
	bestStr := ""
	if !math.IsInf(best, 1) {
		bestStr = strconv.Itoa(int(math.Round(best)))
	}
	return strings.Join([]string{
		"MS1", strconv.FormatUint(uint64(id), 10), ls.StartedAt.Local().Format("200601021504"),
		clean(header.ShooterName), clean(header.SportKlasse), clean(header.Discipline),
		targetType, scoring, strconv.Itoa(header.ShotsPerSeries), strconv.Itoa(inner), bestStr,
		strings.Join(parts, ";"),
	}, "|"), nil
}

// qrSVG rendert den Text als QR-Code (Fehlerkorrektur L - groesste Kapazitaet,
// am Bildschirm ohne Verschmutzung ausreichend) mit 4 Modulen Ruhezone.
func qrSVG(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil, err
	}
	n := code.Size + 8
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.Bytes(), nil
}
