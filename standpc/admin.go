// ============================================================================
// admin.go – passwortgeschuetzte Admin-GUI direkt auf dem Stand-PC (Plan
// Phase C, siehe protokoll-referenz.md Firmware Rev 4.11.2). Erlaubt
// Kalibrierung + Konfigurationsaenderungen auch im Offline-Betrieb, da die
// Befehle lokal vom Stand-PC ausgehen statt vom Server ferngesteuert zu
// werden. Das Passwort wird zentral vom Server gesetzt und per
// PUT /api/admin-password-hash gepusht (siehe web.go
// handlePutAdminPasswordHash) - hier wird nur noch der bcrypt-Hash geprueft.
// ============================================================================
package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

//go:embed web/admin.html
var adminHTML []byte

const adminSessionCookie = "standpc_admin_session"
const adminSessionTTL = 12 * time.Hour

// adminConfigKey beschreibt eine SET-Konfigurationszeile fuer die generische
// Key/Value-Tabelle der Admin-GUI (siehe protokoll-referenz.md Abschnitt 5.2)
// - EIN Eingabefeld + "Speichern" je Zeile, kein Sonderformular je Parameter.
type adminConfigKey struct {
	Key     string `json:"key"`
	Range   string `json:"range"`
	Default string `json:"default"`
	Reboot  bool   `json:"reboot"`
	Desc    string `json:"desc"`
}

// adminConfigKeys: Betriebsparameter (wirken sofort, kein Reboot noetig).
var adminConfigKeys = []adminConfigKey{
	{"LANE", "1-999", "1", false, "Bahnnummer"},
	{"DEBOUNCE", "10-5000 (ms)", "100", false, "Sperrzeit NACH einem gueltigen Schuss"},
	{"REJECTLOCK", "0-2000 (ms)", "5", false, "Sperrzeit NACH einem Reject (z.B. Muendungsknall) - bewusst kurz, siehe DEBOUNCE"},
	{"WINDOW", "1-50 (ms)", "1", false, "Mindest-Sammelfenster"},
	{"DEBUG", "0-3", "0", false, "3 = zusaetzliche Diagnosefelder"},
	{"OUTLIER", "0-500000 (0.001mm)", "5000", false, "Schwelle fuer pos_res_um in clean-Bewertung (nur ALGO=CLASSIC)"},
	{"RADIUS", "0-500000 (0.001mm)", "4000", false, "Umkreis fuer cluster_hits (nur ALGO=CLASSIC)"},
	{"MINCLUSTER", "0-20", "2", false, "Mindest-cluster_hits fuer clean (nur ALGO=CLASSIC)"},
	{"MAXPRECISION", "0-500000 (0.001mm)", "2000", false, "Max. precision_um fuer clean (nur ALGO=CLASSIC)"},
	{"MINMICS", "3-6", "5", false, "Mindestzahl Mics, sonst reject"},
	{"TDOA", "100-5000 (us)", "750", false, "Geometrie-Plausibilitaetsfenster"},
	{"ALGO", "CLASSIC|RIM", "CLASSIC", false, "Auswertepfad - RIM: Ringpuffer+Piezo-Anker-Trigger, robuster gegen Muendungsknall"},
	{"PELLETR", "0.0-10.0 (mm)", "2.25", false, "Akustischer Lochrand-Radius fuer ALGO=RIM (2.25 = 4,5mm-Diabolo, 0=Punktquelle)"},
	{"MAXSIGMA", "0.0-50.0 (mm)", "2.0", false, "'clean'-Schwelle fuer ALGO=RIM (Gegenstueck zu MAXPRECISION/RADIUS/MINCLUSTER)"},
	{"STANDOFFPAPER", "5.0-100.0 (mm)", "28.0", false, "Mic-Standoff (rechtwinklig zur Scheibe)"},
	{"MICHALFX", "5.0-300.0 (mm)", "115.0", false, "horizontaler Mic-Abstand zur Mittellinie"},
	{"BSHIFTPCT", "0-100 (%)", "30", false, "Kugeldurchmesser-Korrektur, 0=aus"},
	{"BSHIFTCAP", "0.0-20.0 (mm)", "3.0", false, "Kappung der Korrektur je Mikrofon"},
	{"CLUSTERCORR", "0|1", "1", false, "Endposition aus allen kugeldurchmesser-korrigierten Schnittpunkten mitteln (ab Firmware 4.12.0)"},
	{"CLUSTERITER", "0-10", "5", false, "Nachzentrier-Durchgaenge des Clusters (ab Firmware 4.12.0)"},
	{"LSQ", "0|1", "0", false, "Endposition per Ausgleichsrechnung ueber alle Mics verfeinern (ab Firmware 4.12.0)"},
	{"LSQPELLETR", "0.0-5.0 (mm)", "2.25", false, "Lochrand-Radius fuer LSQ (ab Firmware 4.12.0)"},
	{"PIEZO", "0|1", "1", false, "Piezo als Trigger-Bestaetigung nutzen"},
	{"PIEZOMIN", "0-5000 (us)", "100", false, "nur PAPER-Modus relevant"},
	{"PIEZOMAX", "0-5000 (us)", "1400", false, "Ausreisser-Obergrenze, beide Modi"},
	{"OFFSETX", "-50000..50000 (0.001mm)", "0", false, "konstanter Nachkorrektur-Offset x"},
	{"OFFSETY", "-50000..50000 (0.001mm)", "0", false, "konstanter Nachkorrektur-Offset y"},
	{"SOUNDSPEED", "300.0-400.0 (m/s)", "342.5", false, "Schallgeschwindigkeit bei DHT=0 bzw. ohne gueltige Messung - Nachkommastelle ab Firmware 4.15.0, rein manuell, wird von CAL START nicht veraendert"},
	{"DHT", "0|1", "0", false, "Schallgeschwindigkeit aus Temperatur/Luftfeuchte des DHT22 an GPIO15 (ab Firmware 4.15.0)"},
	{"CAPTURE", "ISR|MCPWM", "ISR", true, "Zeitstempel der Mics: ISR = GPIO-Interrupt, MCPWM = Hardware-Capture (GPIO23 unbeschaltet lassen). Wirkt erst nach Neustart; danach CAL RESET + neue Kalibrierung, da die Offsets zum Erfassungsmodus gehoeren (ab Firmware 4.13.0)"},
	{"WALKK", "0.0-1.0", "0.0", false, "Pulsbreiten-Korrektur t += WALKK*(Breite - Mittel), 0 = aus, nur bei aktivem MCPWM wirksam (ab Firmware 4.14.0)"},
	{"WALKCAL", "0|1", "0", false, "CAL START bestimmt WALKK mit (ab Firmware 4.14.0)"},
	{"WALKMAX", "5-500 (us)", "150", false, "groesste plausible Pulsbreite, laengere gelten als ungueltig (ab Firmware 4.14.0)"},
	{"ZONES", "0|1", "0", false, "Zonen-Kalibrierung: je Bereich der Scheibe eigener Satz (Loch der Vorsatzscheibe / neben dem Band / Band + Vorsatz), Saetze kommen per Freigabe aus dem Simulator (ab Firmware 4.17.0)"},
	{"ZHOLE", "0-60000 (0.001mm)", "0", false, "Radius des Lochs der Vorsatzscheibe - setzt der Stand-PC beim Scheibenwechsel automatisch (LG 15250, LP 29750) (ab Firmware 4.17.0)"},
	{"ZBAND", "0-100000 (0.001mm)", "45000", false, "halbe Breite des Schussbands (ab Firmware 4.17.0)"},
	{"ZBLEND", "0-10000 (0.001mm)", "2250", false, "Ueberblendung an den Zonengrenzen (ab Firmware 4.17.0)"},
	{"ZCALA", "<m/s>,<ofs0..5>,<offx>,<offy> | OFF", "OFF", false, "Satz Zone A (Loch der Vorsatzscheibe) - normalerweise per Freigabe aus dem Simulator (ab Firmware 4.17.0)"},
	{"ZCALB", "<m/s>,<ofs0..5>,<offx>,<offy> | OFF", "OFF", false, "Satz Zone B (neben dem Schussband) (ab Firmware 4.17.0)"},
	{"ZCALC", "<m/s>,<ofs0..5>,<offx>,<offy> | OFF", "OFF", false, "Satz Zone C (Schussband + Vorsatzscheibe) (ab Firmware 4.17.0)"},
	{"RAWLOG", "0|1", "0", false, "zu jedem gueltigen Schuss alle Rohdaten (Flanken + Parameter) senden - der Simulator kann den Schuss dann exakt nachrechnen (ab Firmware 4.16.0)"},
	{"OFS0", "-20000..20000 (ns)", "0", false, "Mikrofon-Timing-Offset 0 (Referenz, normalerweise 0) - sonst per CAL START/Kalibrierung vom Server"},
	{"OFS1", "-20000..20000 (ns)", "0", false, "Mikrofon-Timing-Offset 1 - sonst per CAL START/Kalibrierung vom Server"},
	{"OFS2", "-20000..20000 (ns)", "0", false, "Mikrofon-Timing-Offset 2 - sonst per CAL START/Kalibrierung vom Server"},
	{"OFS3", "-20000..20000 (ns)", "0", false, "Mikrofon-Timing-Offset 3 - sonst per CAL START/Kalibrierung vom Server"},
	{"OFS4", "-20000..20000 (ns)", "0", false, "Mikrofon-Timing-Offset 4 - sonst per CAL START/Kalibrierung vom Server"},
	{"OFS5", "-20000..20000 (ns)", "0", false, "Mikrofon-Timing-Offset 5 - sonst per CAL START/Kalibrierung vom Server"},
	{"PAPERFEED", "10.0-100.0 (mm)", "50.0", false, "Vorschubstrecke je Ausloesung"},
	{"PAPERSPEED", "0.5-30.0 (mm/s)", "5.0", false, "Geschwindigkeit 1. Haelfte"},
	{"PAPERAUTO", "0|1", "1", false, "automatischen Vorschub ueberhaupt ausfuehren"},
	{"PAPERTRIGGER", "ANY|PIEZO|CLEAN", "PIEZO", false, "wann automatisch vorgeschoben wird"},
	{"PAPERDIR", "0|1", "1", false, "Vorschub-Drehrichtung invertieren"},
	{"PAPERJOGSPEED", "1.0-100.0 (mm/s)", "75.0", false, "Geschwindigkeit manueller Dauerbetrieb"},
	{"CALSHOTS", "3-20", "10", false, "Anzahl Kalibrier-Schuesse fuer CAL START"},
	{"MICEN0", "0|1", "1", false, "Mikrofonkanal 0 beruecksichtigen"},
	{"MICEN1", "0|1", "1", false, "Mikrofonkanal 1 beruecksichtigen"},
	{"MICEN2", "0|1", "1", false, "Mikrofonkanal 2 beruecksichtigen"},
	{"MICEN3", "0|1", "1", false, "Mikrofonkanal 3 beruecksichtigen"},
	{"MICEN4", "0|1", "1", false, "Mikrofonkanal 4 beruecksichtigen"},
	{"MICEN5", "0|1", "1", false, "Mikrofonkanal 5 beruecksichtigen"},
}

// adminNetworkKeys: die 9 Keys, die das Netzwerk-Sicherheitsnetz ausloesen
// (siehe protokoll-referenz.md Abschnitt 7.6) - eigener Tabellenabschnitt
// "Netzwerk" mit Warnhinweis zum 3-Minuten-Bestaetigungsfenster.
var adminNetworkKeys = []adminConfigKey{
	{"SSID", "Text", "\"\" (WLAN aus)", true, "WLAN-Name"},
	{"PASS", "Text", "\"\"", true, "WLAN-Passwort"},
	{"HOST", "IP/Hostname", "192.168.1.10", true, "Ziel-Host (Stand-PC)"},
	{"PORT", "1-65535", "9000", true, "TCP-Port des Stand-PC"},
	{"STATIC", "0|1", "0", true, "statische IP an/aus"},
	{"IP", "IPv4", "\"\"", true, "statische IP-Adresse"},
	{"GW", "IPv4", "\"\"", true, "Gateway"},
	{"SUBNET", "IPv4-Maske", "255.255.255.0", true, "Subnetzmaske"},
	{"DNS", "IPv4 oder leer", "\"\" (=Gateway)", true, "DNS-Server"},
}

func (ws *WebServer) registerAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", ws.handleAdminPage)
	mux.HandleFunc("POST /admin/login", ws.handleAdminLogin)
	mux.HandleFunc("POST /admin/logout", ws.handleAdminLogout)
	mux.HandleFunc("GET /admin/api/keys", ws.requireAdmin(ws.handleAdminKeys))
	mux.HandleFunc("GET /admin/api/status", ws.requireAdmin(ws.handleAdminStatus))
	mux.HandleFunc("GET /admin/api/status/passive", ws.requireAdmin(ws.handleAdminStatusPassive))
	mux.HandleFunc("POST /admin/api/set", ws.requireAdmin(ws.handleAdminSet))
	mux.HandleFunc("POST /admin/api/cal", ws.requireAdmin(ws.handleAdminCal))
	mux.HandleFunc("POST /admin/api/cal/restore", ws.requireAdmin(ws.handleAdminCalRestore))
	mux.HandleFunc("GET /admin/api/transfer", ws.requireAdmin(ws.handleAdminTransfer))
	mux.HandleFunc("POST /admin/api/transfer/apply", ws.requireAdmin(ws.handleAdminTransferApply))
	mux.HandleFunc("GET /admin/api/transfer/configs", ws.requireAdmin(ws.handleAdminTransferConfigs))
	mux.HandleFunc("POST /admin/api/transfer/select", ws.requireAdmin(ws.handleAdminTransferSelect))
	mux.HandleFunc("GET /admin/api/config-history", ws.requireAdmin(ws.handleAdminConfigHistory))
	mux.HandleFunc("GET /admin/api/config-restore", ws.requireAdmin(ws.handleAdminConfigRestoreCheck))
	mux.HandleFunc("POST /admin/api/config-restore/apply", ws.requireAdmin(ws.handleAdminConfigRestoreApply))
	mux.HandleFunc("POST /admin/api/action", ws.requireAdmin(ws.handleAdminAction))
	mux.HandleFunc("POST /admin/api/net", ws.requireAdmin(ws.handleAdminNet))
}

// handleAdminPage liefert die Seite immer aus (kein serverseitiges Gating) -
// die Seite selbst zeigt zuerst ein Login-Formular und prueft ihren
// Session-Status per GET /admin/api/status.
func (ws *WebServer) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(adminHTML)
}

func (ws *WebServer) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "ungueltiger Body", http.StatusBadRequest)
		return
	}

	ws.mu.Lock()
	hash := ws.adminPasswordHash
	ws.mu.Unlock()

	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		http.Error(w, "falsches Passwort", http.StatusUnauthorized)
		return
	}

	token := newSessionToken()
	ws.mu.Lock()
	ws.adminSessions[token] = time.Now().Add(adminSessionTTL)
	ws.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(adminSessionTTL.Seconds()),
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (ws *WebServer) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminSessionCookie); err == nil {
		ws.mu.Lock()
		delete(ws.adminSessions, c.Value)
		ws.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: adminSessionCookie, Value: "", Path: "/admin", MaxAge: -1})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// requireAdmin gated jede /admin/api/*-Route auf ein gueltiges,
// nicht-abgelaufenes Session-Cookie.
func (ws *WebServer) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(adminSessionCookie)
		if err != nil {
			http.Error(w, "nicht angemeldet", http.StatusUnauthorized)
			return
		}
		ws.mu.Lock()
		exp, ok := ws.adminSessions[c.Value]
		if ok && time.Now().After(exp) {
			delete(ws.adminSessions, c.Value)
			ok = false
		}
		ws.mu.Unlock()
		if !ok {
			http.Error(w, "Sitzung abgelaufen", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func newSessionToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (ws *WebServer) handleAdminKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"config":  adminConfigKeys,
		"network": adminNetworkKeys,
	})
}

// handleAdminStatus fragt STATUS/SHOW/SHOWNET/CAL STATUS frisch beim ESP32 ab
// (Antworten laufen ueber den normalen dispatchLine()-Pfad in den
// DeviceState, siehe transport.go) und liefert danach den aktuellen
// Gesamtzustand zurueck. Einzelne Befehle duerfen fehlschlagen (z.B. kein
// Geraet verbunden) - dann bleibt der jeweils zuletzt bekannte Stand erhalten.
func (ws *WebServer) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	for _, cmd := range []string{"STATUS", "SHOW", "SHOWNET", "CAL STATUS"} {
		ws.cmds.SendCommand(cmd, 0)
	}
	// Zonen-Saetze (Firmware 4.17.0) - nur wenn das Geraet Zonen kennt
	if c := ws.deviceState.Snapshot().Config; c != nil && c.Zones != nil {
		ws.cmds.SendCommand("SHOWZONES", 0)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.deviceState.Snapshot())
}

// handleAdminStatusPassive liefert den zuletzt bekannten DeviceState-Stand,
// OHNE selbst ein Kommando an den ESP32 zu senden (kein STATUS/SHOW/SHOWNET/
// CAL STATUS-Roundtrip). Der ESP32 meldet waehrend einer laufenden
// Kalibrierung ohnehin pro Schuss unaufgefordert ein neues "cal"-Telegramm
// (siehe schiessstand_firmware.ino processShot -> calActive-Zweig), das
// bereits passiv ueber dispatchLine() in deviceState landet - fuer das
// Nachziehen des Kalibrier-Fortschritts in der Admin-GUI reicht daher reines
// Mitlesen. Aktives Nachfragen im Sekundentakt erzeugt unnoetigen
// WLAN/TCP-Verkehr zum ESP32, der die zeitkritische TDOA-Erfassung waehrend
// der Kalibrierung stoeren kann - siehe web/admin.html calPollTimer.
func (ws *WebServer) handleAdminStatusPassive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.deviceState.Snapshot())
}

// sendAdminCommand fuehrt einen Befehl aus und liefert entweder die
// Rohantwort(en) oder einen Fehler (kein Geraet verbunden, Timeout, oder ein
// "error"-Telegramm - Firmware-Fehlermeldungen werden 1:1 durchgereicht).
func (ws *WebServer) sendAdminCommand(w http.ResponseWriter, cmd string) {
	lines, err := ws.cmds.SendCommand(cmd, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"responses": lines})
}

func (ws *WebServer) handleAdminSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		http.Error(w, "ungueltiger Body", http.StatusBadRequest)
		return
	}
	ws.sendAdminCommand(w, setCommand(body.Key, body.Value))
	go ws.requestConfigSnapshot() // Aenderung im Konfigurationsverlauf festhalten
}

// handleAdminCal: CAL START/ABORT/RESET - fuer den atomaren Restore siehe
// handleAdminCalRestore (nutzt CAL IMPORT statt eine feste Aktion).
func (ws *WebServer) handleAdminCal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "ungueltiger Body", http.StatusBadRequest)
		return
	}
	switch body.Action {
	case "START", "ABORT", "RESET", "STATUS":
		ws.sendAdminCommand(w, "CAL "+body.Action)
	default:
		http.Error(w, "unbekannte Aktion", http.StatusBadRequest)
	}
}

// currentOrQueriedMAC liefert die Geraete-ID; ist sie noch unbekannt (kein
// Telegramm seit Start des Stand-PCs), wird sie per STATUS aktiv erfragt.
// "" = kein Geraet erreichbar.
func (ws *WebServer) currentOrQueriedMAC() string {
	if mac := ws.deviceState.CurrentMAC(); mac != "" {
		return mac
	}
	var st StatusTelegram
	if err := ws.queryTelegram("STATUS", "status", &st); err != nil {
		return ""
	}
	return st.MAC
}

// handleAdminCalRestore holt die Kalibrierungs-Sicherung vom Server und
// schreibt sie atomar per CAL IMPORT zurueck. Reihenfolge:
//  1. Sicherung genau dieser Platine (mac) - Normalfall.
//  2. Gibt es keine (typisch: Platine getauscht, neue mac), die juengste
//     Sicherung des STANDS - aber nur nach ausdruecklicher Bestaetigung
//     (Body {"from_lane":true}): Die Mikrofon-Offsets gleichen Laufzeit-
//     unterschiede von Mikrofonen, Kabeln UND Eingangsstufen der Platine
//     aus - nach einem Tausch sind sie ein guter Ausgangspunkt, sollten aber
//     per CAL START oder Foto-Kalibrierung ueberprueft werden. Ohne
//     Bestaetigung antwortet der Handler mit 409 und den Daten der
//     gefundenen Sicherung, damit die Oberflaeche nachfragen kann.
//
// Nach einer Uebernahme vom Stand wird die Sicherung zusaetzlich unter der
// neuen mac abgelegt - beim naechsten Mal greift dann direkt Schritt 1.
func (ws *WebServer) handleAdminCalRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FromLane bool `json:"from_lane"`
	}
	json.NewDecoder(r.Body).Decode(&body) // leerer Body = Normalfall

	mac := ws.currentOrQueriedMAC()
	if mac == "" {
		http.Error(w, "Kein ESP32 verbunden - die Kalibrierung kann nur auf ein erreichbares Gerät zurückgeschrieben werden.", http.StatusBadGateway)
		return
	}
	source := "mac"
	cal, err := fetchCalibrationBackup(ws.cfg.ServerURL, mac)
	if errors.Is(err, errNoBackup) {
		laneCal, laneErr := fetchCalibrationBackupByLane(ws.cfg.ServerURL, ws.cfg.LaneNo)
		if errors.Is(laneErr, errNoBackup) {
			http.Error(w, fmt.Sprintf("Weder für dieses Gerät (%s) noch für Stand %d ist eine Kalibrierung auf dem Server gesichert.", mac, ws.cfg.LaneNo), http.StatusNotFound)
			return
		}
		if laneErr != nil {
			http.Error(w, laneErr.Error(), http.StatusBadGateway)
			return
		}
		if !body.FromLane {
			writeJSON(w, http.StatusConflict, map[string]any{
				"need_confirm": true, "current_mac": mac, "lane_no": ws.cfg.LaneNo, "backup": laneCal,
			})
			return
		}
		cal, err, source = laneCal, nil, "lane"
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// Erfassungsmodus: Offsets aus dem ISR-Betrieb passen nicht zu MCPWM und
	// umgekehrt (Firmware Rev 4.13.0) - nur auf ein Geraet im selben Modus
	// zurueckspielen.
	var st StatusTelegram
	if err := ws.queryTelegram("STATUS", "status", &st); err != nil {
		http.Error(w, "Gerätestatus nicht lesbar: "+err.Error(), http.StatusBadGateway)
		return
	}
	if msg := captureMismatch(cal.Capture, st.Capture); msg != "" {
		when := ""
		if cal.UpdatedAt != nil {
			when = " vom " + cal.UpdatedAt.Local().Format("02.01.2006 15:04")
		}
		msg = "Gesicherte Gerätekalibrierung" + when + ": " + msg +
			" Eine im Simulator bestimmte Kalibrierung bitte über „Kalibrierung vom Simulator übernehmen“ → „Freigabe prüfen“ auf das Gerät bringen."
		// 412 statt 409: 409 ist fuer die Rueckfrage "Sicherung des Stands
		// uebernehmen?" belegt (admin.html calRestore)
		http.Error(w, msg, http.StatusPreconditionFailed)
		return
	}
	cfg := ws.deviceState.Snapshot().Config
	if err := ws.sendChecked(calImportCommand(cal.OffsetsNs, cal.soundSet(), cfg)); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// WALKK kennt CAL IMPORT nicht - separat setzen (nur wenn gesichert und
	// vom Geraet unterstuetzt).
	if cal.WalkK != nil && cfg != nil && cfg.WalkK != nil {
		if err := ws.sendChecked(fmt.Sprintf("SET WALKK=%.3f", *cal.WalkK)); err != nil {
			http.Error(w, "Offsets übernommen, WALKK fehlgeschlagen: "+err.Error(), http.StatusBadGateway)
			return
		}
	}
	if source == "lane" && cal.MAC != mac {
		capture := ""
		if cal.Capture != nil {
			capture = *cal.Capture
		}
		ws.uploadCalibrationBackup(CalTelegram{MAC: mac, OffsetsNs: cal.OffsetsNs, SoundMps: cal.SoundMps,
			SoundSetMps: cal.SoundSetMps, WalkK: cal.WalkK, capture: capture})
	}
	go ws.requestConfigSnapshot()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "source": source, "backup_mac": cal.MAC, "mac": mac})
}

// calImportCommand baut den CAL-IMPORT-Befehl. SOUNDSPEED mit Nachkommastelle
// erst ab Firmware 4.15.0 (erkennbar an sound_set_mps im SHOW), sonst
// ganzzahlig gerundet.
func calImportCommand(ofs [6]int, sound float64, cfg *ConfigTelegram) string {
	ss := fmt.Sprintf("%d", int(math.Round(sound)))
	if cfg != nil && cfg.SoundSetMps != nil {
		ss = strconv.FormatFloat(math.Round(sound*10)/10, 'f', 1, 64)
	}
	return fmt.Sprintf("CAL IMPORT OFS0=%d,OFS1=%d,OFS2=%d,OFS3=%d,OFS4=%d,OFS5=%d,SOUNDSPEED=%s",
		ofs[0], ofs[1], ofs[2], ofs[3], ofs[4], ofs[5], ss)
}

// captureMismatch: Fehlermeldung, wenn eine Kalibrierung in einem anderen
// Erfassungsmodus (ISR/MCPWM) bestimmt wurde, als das Geraet gerade
// verwendet - "" wenn passend oder einer der beiden Modi unbekannt ist
// (Sicherung bzw. Firmware vor Rev 4.13.0 = ISR-Betrieb).
func captureMismatch(calCapture *string, deviceCapture string) string {
	cc := "isr"
	if calCapture != nil && *calCapture != "" {
		cc = strings.ToLower(*calCapture)
	}
	dc := strings.ToLower(deviceCapture)
	if dc == "" {
		dc = "isr"
	}
	if cc == dc {
		return ""
	}
	return fmt.Sprintf("Die Kalibrierung wurde im Erfassungsmodus %s bestimmt, das Gerät arbeitet aber mit %s. "+
		"Die Mikrofon-Offsets passen nicht zueinander - bitte neu kalibrieren (CAL START oder im Simulator) oder den Erfassungsmodus angleichen.",
		strings.ToUpper(cc), strings.ToUpper(dc))
}

// handleAdminAction: ACTION LIGHT=ON|OFF|AUTO, ACTION TARGETCHANGE, REBOOT.
func (ws *WebServer) handleAdminAction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cmd string `json:"cmd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "ungueltiger Body", http.StatusBadRequest)
		return
	}
	switch body.Cmd {
	case "LIGHT=ON", "LIGHT=OFF", "LIGHT=AUTO", "TARGETCHANGE":
		ws.sendAdminCommand(w, "ACTION "+body.Cmd)
	case "REBOOT":
		// Neustart des ESP32 - noetig, damit SET CAPTURE bzw. Netzwerk-
		// Aenderungen wirksam werden (reboot_required in der SET-Antwort).
		ws.sendAdminCommand(w, "REBOOT")
	default:
		http.Error(w, "unbekannte Aktion", http.StatusBadRequest)
	}
}

// handleAdminNet: NET STATUS, NET CONFIRM (siehe protokoll-referenz.md
// Abschnitt 5.7/7.6 - Sicherheitsnetz fuer Netzwerk-Fernkonfiguration).
func (ws *WebServer) handleAdminNet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cmd string `json:"cmd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "ungueltiger Body", http.StatusBadRequest)
		return
	}
	switch body.Cmd {
	case "STATUS", "CONFIRM":
		ws.sendAdminCommand(w, "NET "+body.Cmd)
	default:
		http.Error(w, "unbekannte Aktion", http.StatusBadRequest)
	}
}
