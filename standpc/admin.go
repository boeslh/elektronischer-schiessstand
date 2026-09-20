// ============================================================================
// admin.go – passwortgeschuetzte Admin-GUI direkt auf dem Stand-PC (Plan
// Phase C, siehe protokoll-referenz.md Firmware Rev 4.9.0). Erlaubt
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
	"fmt"
	"net/http"
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
	{"DEBOUNCE", "10-5000 (ms)", "100", false, "Sperrzeit nach einer Ausloesung"},
	{"WINDOW", "1-50 (ms)", "1", false, "Mindest-Sammelfenster"},
	{"DEBUG", "0-3", "0", false, "3 = zusaetzliche Diagnosefelder"},
	{"OUTLIER", "0-500000 (0.001mm)", "5000", false, "Schwelle fuer pos_res_um in clean-Bewertung"},
	{"RADIUS", "0-500000 (0.001mm)", "200", false, "Umkreis fuer cluster_hits"},
	{"MINCLUSTER", "0-20", "2", false, "Mindest-cluster_hits fuer clean"},
	{"MAXPRECISION", "0-500000 (0.001mm)", "2000", false, "Max. precision_um fuer clean"},
	{"MINMICS", "3-6", "5", false, "Mindestzahl Mics, sonst reject"},
	{"TDOA", "100-5000 (us)", "750", false, "Geometrie-Plausibilitaetsfenster"},
	{"TARGET", "STEEL|PAPER", "STEEL", false, "Geometrie-Preset"},
	{"STANDOFFSTEEL", "5.0-100.0 (mm)", "30.0", false, "Mic-Standoff im STEEL-Modus"},
	{"STANDOFFPAPER", "5.0-100.0 (mm)", "28.0", false, "Mic-Standoff im PAPER-Modus"},
	{"MICHALFX", "5.0-300.0 (mm)", "115.0", false, "horizontaler Mic-Abstand zur Mittellinie"},
	{"BSHIFTPCT", "0-100 (%)", "50", false, "Kugeldurchmesser-Korrektur, 0=aus"},
	{"BSHIFTCAP", "0.0-20.0 (mm)", "3.0", false, "Kappung der Korrektur je Mikrofon"},
	{"PIEZO", "0|1", "1", false, "Piezo als Trigger-Bestaetigung nutzen"},
	{"PIEZOMIN", "0-5000 (us)", "100", false, "nur PAPER-Modus relevant"},
	{"PIEZOMAX", "0-5000 (us)", "1400", false, "Ausreisser-Obergrenze, beide Modi"},
	{"OFFSETX", "-50000..50000 (0.001mm)", "0", false, "konstanter Nachkorrektur-Offset x"},
	{"OFFSETY", "-50000..50000 (0.001mm)", "0", false, "konstanter Nachkorrektur-Offset y"},
	{"SOUNDSPEED", "300-400 (m/s)", "355", false, "rein manuell, wird von CAL START nicht veraendert"},
	{"PAPERFEED", "10.0-100.0 (mm)", "50.0", false, "Vorschubstrecke je Ausloesung"},
	{"PAPERSPEED", "0.5-30.0 (mm/s)", "5.0", false, "Geschwindigkeit 1. Haelfte"},
	{"PAPERAUTO", "0|1", "1", false, "automatischen Vorschub ueberhaupt ausfuehren"},
	{"PAPERTRIGGER", "ANY|PIEZO|CLEAN", "PIEZO", false, "wann automatisch vorgeschoben wird"},
	{"PAPERDIR", "0|1", "1", false, "Vorschub-Drehrichtung invertieren"},
	{"PAPERJOGSPEED", "1.0-100.0 (mm/s)", "75.0", false, "Geschwindigkeit manueller Dauerbetrieb"},
	{"CALSHOTS", "3-20", "5", false, "Anzahl Kalibrier-Schuesse fuer CAL START"},
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
	ws.sendAdminCommand(w, fmt.Sprintf("SET %s=%s", body.Key, body.Value))
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

// handleAdminCalRestore holt das zuletzt gesicherte Kalibrierungs-Backup vom
// Server (fuer die aktuell bekannte Geraete-ID) und schreibt es atomar per
// CAL IMPORT zurueck (siehe calibration_backup.go fetchCalibrationBackup).
func (ws *WebServer) handleAdminCalRestore(w http.ResponseWriter, r *http.Request) {
	mac := ws.deviceState.CurrentMAC()
	cal, err := fetchCalibrationBackup(ws.cfg.ServerURL, mac)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	cmd := fmt.Sprintf("CAL IMPORT OFS0=%d,OFS1=%d,OFS2=%d,OFS3=%d,OFS4=%d,OFS5=%d,SOUNDSPEED=%d",
		cal.OffsetsNs[0], cal.OffsetsNs[1], cal.OffsetsNs[2],
		cal.OffsetsNs[3], cal.OffsetsNs[4], cal.OffsetsNs[5], cal.SoundMps)
	ws.sendAdminCommand(w, cmd)
}

// handleAdminAction: ACTION LIGHT=ON|OFF|AUTO, ACTION TARGETCHANGE.
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
