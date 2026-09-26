// ============================================================================
// calibration_transfer.go – im Simulator freigegebene Kalibrierung vom Server
// holen und auf den ESP32 schreiben (Admin-GUI, ohne serielle Verbindung).
//
// Gegenstueck zu server/calibration_transfer.go: Der Server liefert die
// fertige Liste aus Firmware-Schluessel und SET-Wert. Hier wird sie mit der
// aktuellen Geraetekonfiguration (SHOW) verglichen und NUR das Abweichende
// geschrieben:
//   - OFS0-5 + SOUNDSPEED gemeinsam per "CAL IMPORT" (atomar in der
//     Firmware: erst alles validieren, dann schreiben - ein Verbindungs-
//     abbruch hinterlaesst so keine halb uebernommenen Mikrofon-Offsets),
//   - alle uebrigen Werte einzeln per "SET <Key>=<Value>".
//
// Danach wird SHOW erneut gelesen und verglichen; das Ergebnis geht an den
// Server zurueck, damit im Simulator sichtbar ist, ob/wann die Freigabe auf
// welchem Geraet angekommen ist.
//
// Bewusst immer manuell ausgeloest (Admin-GUI), nie automatisch: eine
// Konfigurationsaenderung waehrend des Schiessbetriebs soll nicht unbemerkt
// passieren.
// ============================================================================
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type firmwareSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type calibrationTransfer struct {
	LaneNo     int               `json:"lane_no"`
	Settings   []firmwareSetting `json:"settings"`
	Note       string            `json:"note"`
	CreatedBy  string            `json:"created_by"`
	CreatedAt  time.Time         `json:"created_at"`
	AppliedAt  *time.Time        `json:"applied_at"`
	AppliedMAC *string           `json:"applied_mac"`
	AppliedOK  *bool             `json:"applied_ok"`
}

func fetchCalibrationTransfer(serverURL string, laneNo int) (*calibrationTransfer, int, error) {
	if serverURL == "" {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("kein Server konfiguriert")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/api/lanes/%d/calibration-transfer", serverURL, laneNo))
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, http.StatusNotFound, fmt.Errorf("für Stand %d ist im Simulator keine Kalibrierung freigegeben", laneNo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, http.StatusBadGateway, fmt.Errorf("Server antwortete mit %s", resp.Status)
	}
	var t calibrationTransfer
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, http.StatusBadGateway, err
	}
	return &t, http.StatusOK, nil
}

func reportCalibrationTransferApplied(serverURL string, laneNo int, createdAt time.Time, mac string, ok bool, result any) error {
	data, err := json.Marshal(map[string]any{"created_at": createdAt, "mac": mac, "ok": ok, "result": result})
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(fmt.Sprintf("%s/api/lanes/%d/calibration-transfer/applied", serverURL, laneNo),
		"application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Server antwortete mit %s", resp.Status)
	}
	return nil
}

// currentSettingValue liefert den aktuellen Geraetewert eines Firmware-
// Schluessels aus dem SHOW-Telegramm - im selben Format/derselben Einheit
// wie der SET-Befehl (MAXSIGMA: SET in mm, Telegramm in 0.001mm).
func currentSettingValue(c *ConfigTelegram, key string) (string, bool) {
	f2 := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	itoa := strconv.Itoa
	if strings.HasPrefix(key, "OFS") && len(key) == 4 {
		if k := int(key[3] - '0'); k >= 0 && k < 6 {
			return itoa(c.MicOffsetNs[k]), true
		}
	}
	if strings.HasPrefix(key, "MICEN") && len(key) == 6 {
		if k := int(key[5] - '0'); k >= 0 && k < 6 {
			return itoa(c.MicEnabled[k]), true
		}
	}
	switch key {
	case "STANDOFFPAPER":
		return f2(c.StandoffPaperMm), true
	case "MICHALFX":
		return f2(c.MicHalfXMm), true
	case "OFFSETX":
		return itoa(c.OffsetXUm), true
	case "OFFSETY":
		return itoa(c.OffsetYUm), true
	case "SOUNDSPEED":
		return itoa(c.SoundMps), true
	case "BSHIFTPCT":
		return itoa(c.BulletShiftPct), true
	case "BSHIFTCAP":
		return f2(c.BulletShiftCapMm), true
	case "RADIUS":
		return itoa(c.ClusterRadiusUm), true
	case "ALGO":
		return strings.ToUpper(c.Algo), true
	case "PELLETR":
		return f2(c.PelletRMm), true
	case "MAXSIGMA":
		return f2(float64(c.MaxSigmaUm) / 1000), true
	case "PIEZOMIN":
		return itoa(c.PiezoMinUs), true
	case "PIEZOMAX":
		return itoa(c.PiezoMaxUs), true
	case "MINMICS":
		return itoa(c.MinMics), true
	case "PIEZO":
		return itoa(c.UsePiezo), true
	// uebrige Betriebsparameter (fuer die Konfigurationssicherung, siehe
	// config_backup.go - die Simulator-Freigabe enthaelt nur die obigen)
	case "LANE":
		return itoa(c.Lane), true
	case "DEBOUNCE":
		return itoa(c.DebounceMs), true
	case "REJECTLOCK":
		return itoa(c.RejectLockoutMs), true
	case "WINDOW":
		return itoa(c.WindowMs), true
	case "DEBUG":
		return itoa(c.Debug), true
	case "OUTLIER":
		return itoa(c.OutlierUm), true
	case "MINCLUSTER":
		return itoa(c.MinClusterHits), true
	case "MAXPRECISION":
		return itoa(c.MaxPrecisionUm), true
	case "TDOA":
		return itoa(c.TDOAUs), true
	case "CALSHOTS":
		return itoa(c.CalShots), true
	case "PAPERFEED":
		return f2(c.PaperFeedMm), true
	case "PAPERSPEED":
		return f2(c.PaperSpeedMmps), true
	case "PAPERAUTO":
		return itoa(c.PaperAuto), true
	case "PAPERTRIGGER":
		return strings.ToUpper(c.PaperTrigger), true
	case "PAPERDIR":
		return itoa(c.PaperDirInvert), true
	case "PAPERJOGSPEED":
		return f2(c.PaperJogSpeedMmps), true
	}
	return "", false
}

// settingEqual: Zahlen mit Toleranz (Float-Werte kommen als %.2f), sonst
// Textvergleich ohne Gross-/Kleinschreibung (ALGO).
func settingEqual(a, b string) bool {
	fa, errA := strconv.ParseFloat(a, 64)
	fb, errB := strconv.ParseFloat(b, 64)
	if errA == nil && errB == nil {
		return math.Abs(fa-fb) < 0.005
	}
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

type transferItem struct {
	Key     string `json:"key"`
	Current string `json:"current"` // "" = unbekannt (Geraet nicht verbunden bzw. Schluessel nicht im SHOW)
	Target  string `json:"target"`
	Changed bool   `json:"changed"`
}

func diffTransfer(c *ConfigTelegram, settings []firmwareSetting) []transferItem {
	items := make([]transferItem, 0, len(settings))
	for _, s := range settings {
		it := transferItem{Key: s.Key, Target: s.Value, Changed: true}
		if c != nil {
			if cur, ok := currentSettingValue(c, s.Key); ok {
				it.Current = cur
				it.Changed = !settingEqual(cur, s.Value)
			}
		}
		items = append(items, it)
	}
	return items
}

func isCalImportKey(key string) bool {
	return key == "SOUNDSPEED" || (strings.HasPrefix(key, "OFS") && len(key) == 4)
}

// transferCommands baut die Befehlsfolge fuer die abweichenden Werte: ein
// CAL IMPORT (mit ALLEN sieben Kalibrierwerten, damit Offsets und
// Schallgeschwindigkeit garantiert zusammenpassen), dann je ein SET.
func transferCommands(items []transferItem) []string {
	var cmds []string
	calChanged := false
	var cal []string
	for _, it := range items {
		if isCalImportKey(it.Key) {
			cal = append(cal, it.Key+"="+it.Target)
			calChanged = calChanged || it.Changed
		}
	}
	if calChanged {
		cmds = append(cmds, "CAL IMPORT "+strings.Join(cal, ","))
	}
	for _, it := range items {
		if it.Changed && !isCalImportKey(it.Key) {
			cmds = append(cmds, fmt.Sprintf("SET %s=%s", it.Key, it.Target))
		}
	}
	return cmds
}

func countChanged(items []transferItem) int {
	n := 0
	for _, it := range items {
		if it.Changed {
			n++
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// handleAdminTransfer: GET /admin/api/transfer - Freigabe vom Server holen,
// Geraetekonfiguration frisch lesen und die Unterschiede liefern.
func (ws *WebServer) handleAdminTransfer(w http.ResponseWriter, r *http.Request) {
	t, status, err := fetchCalibrationTransfer(ws.cfg.ServerURL, ws.cfg.LaneNo)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	var cfg ConfigTelegram
	showErr := ws.queryTelegram("SHOW", "config", &cfg)
	var cur *ConfigTelegram
	if showErr == nil {
		cur = &cfg
	}
	items := diffTransfer(cur, t.Settings)
	resp := map[string]any{
		"lane_no": ws.cfg.LaneNo, "transfer": t, "items": items, "changed": countChanged(items),
		"mac": ws.deviceState.CurrentMAC(), "device_connected": showErr == nil,
	}
	if showErr != nil {
		resp["device_error"] = showErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// sendChecked sendet einen Befehl und wertet die Antwort aus: SendCommand
// liefert eine Firmware-Fehlermeldung ({"type":"error","msg":...}) als
// normale Antwortzeile, nicht als Fehler - ein abgelehnter Wert (z.B.
// ausserhalb des Wertebereichs) wuerde sonst als uebernommen gelten.
func (ws *WebServer) sendChecked(cmd string) error {
	lines, err := ws.cmds.SendCommand(cmd, 0)
	if err != nil {
		return err
	}
	for _, l := range lines {
		var t struct {
			Type string `json:"type"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(l, &t) == nil && t.Type == "error" {
			return fmt.Errorf("Gerät meldet Fehler: %s", t.Msg)
		}
	}
	return nil
}

// queryTelegram sendet cmd und dekodiert die erste Antwortzeile vom Typ
// typ direkt aus der Antwort - unabhaengig davon, wann dispatchLine() den
// DeviceState aktualisiert (dort wird die Zeile erst NACH der Zustellung an
// SendCommand verarbeitet).
func (ws *WebServer) queryTelegram(cmd, typ string, v any) error {
	if cfg, ok := v.(*ConfigTelegram); ok && typ == "config" && ws.deviceState != nil {
		return ws.queryConfig(cmd, cfg)
	}
	lines, err := ws.cmds.SendCommand(cmd, 0)
	if err != nil {
		return err
	}
	for _, l := range lines {
		var h struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(l, &h) == nil && h.Type == typ {
			return json.Unmarshal(l, v)
		}
	}
	return fmt.Errorf("keine %q-Antwort auf %s", typ, cmd)
}

// queryConfig: Sonderfall von queryTelegram fuer SHOW. Die Firmware (bis
// Rev 4.11.5) haengt "corr" nur an Zeilen an, die in ihren 160-Byte-
// Hilfspuffer passen - das ~900 Byte lange config-Telegramm kommt daher
// OHNE corr an, SendCommand lief in den Timeout ("Gerät nicht erreichbar:
// keine Antwort innerhalb von 2s" bei der Kalibrier-Uebernahme). Statt der
// korrelierten Antwort zaehlt deshalb das erste config-Telegramm, das nach
// dem Senden eintrifft (dispatchLine -> DeviceState.SetConfig) - funktioniert
// mit alter wie mit korrigierter Firmware gleichermassen.
func (ws *WebServer) queryConfig(cmd string, v *ConfigTelegram) error {
	seq, changed := ws.deviceState.ConfigWatch()
	errCh := make(chan error, 1)
	go func() {
		_, err := ws.cmds.SendCommand(cmd, 0)
		errCh <- err
	}()
	timeout := time.NewTimer(defaultCommandTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-changed:
			if c := ws.deviceState.ConfigAfter(seq); c != nil {
				*v = *c
				return nil
			}
			_, changed = ws.deviceState.ConfigWatch()
		case err := <-errCh:
			if err != nil {
				return err // nicht gesendet (keine Verbindung) oder Timeout
			}
			errCh = nil // corr-Antwort ohne config: weiter auf das Telegramm warten
		case <-timeout.C:
			return fmt.Errorf("keine %q-Antwort auf %s innerhalb von %s", "config", cmd, defaultCommandTimeout)
		}
	}
}

type transferStep struct {
	Cmd   string `json:"cmd"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// applyResult: Ergebnis von applySettings (Antwort der Admin-GUI und
// Rueckmeldung an den Server).
type applyResult struct {
	OK        bool           `json:"ok"`
	Steps     []transferStep `json:"steps"`
	Remaining []transferItem `json:"remaining"`
	MAC       string         `json:"mac"`
}

// applySettings schreibt die von settings abweichenden Werte auf das Geraet
// (CAL IMPORT fuer OFS0-5/SOUNDSPEED, sonst SET) und prueft per SHOW nach.
// Gemeinsam genutzt von der Simulator-Freigabe und der Wiederherstellung
// aus dem Konfigurationsverlauf. Liefert bei Vorbedingungsfehlern (Geraet
// nicht erreichbar, laufende Kalibrierung) HTTP-Status + Fehler.
func (ws *WebServer) applySettings(settings []firmwareSetting) (applyResult, int, error) {
	// Waehrend CAL START darf nichts umgestellt werden (die Firmware sammelt
	// dann gerade Kalibrierschuesse mit den bisherigen Offsets).
	var cal CalTelegram
	if err := ws.queryTelegram("CAL STATUS", "cal", &cal); err != nil {
		return applyResult{}, http.StatusBadGateway, fmt.Errorf("Gerät nicht erreichbar: %w", err)
	}
	if cal.State == "waiting" || cal.State == "start" {
		return applyResult{}, http.StatusConflict, fmt.Errorf("Am Gerät läuft gerade eine Kalibrierung (CAL START) - erst abschließen oder abbrechen.")
	}
	var before ConfigTelegram
	if err := ws.queryTelegram("SHOW", "config", &before); err != nil {
		return applyResult{}, http.StatusBadGateway, fmt.Errorf("Gerätekonfiguration nicht lesbar: %w", err)
	}

	res := applyResult{Steps: []transferStep{}, Remaining: []transferItem{}}
	for _, cmd := range transferCommands(diffTransfer(&before, settings)) {
		st := transferStep{Cmd: cmd, OK: true}
		if err := ws.sendChecked(cmd); err != nil {
			st.OK, st.Error = false, err.Error()
		}
		res.Steps = append(res.Steps, st)
	}

	var afterCfg ConfigTelegram
	var after []transferItem
	if err := ws.queryTelegram("SHOW", "config", &afterCfg); err != nil {
		after = diffTransfer(nil, settings) // Kontrolle nicht moeglich -> alles als offen melden
	} else {
		after = diffTransfer(&afterCfg, settings)
	}
	for _, it := range after {
		if it.Changed {
			res.Remaining = append(res.Remaining, it)
		}
	}
	res.OK = len(res.Remaining) == 0
	for _, st := range res.Steps {
		res.OK = res.OK && st.OK
	}
	res.MAC = ws.deviceState.CurrentMAC()
	return res, http.StatusOK, nil
}

// handleAdminTransferApply: POST /admin/api/transfer/apply {created_at} -
// schreibt die Abweichungen genau der Freigabe, die der Admin angezeigt
// bekam (created_at), prueft per SHOW nach und meldet an den Server.
func (ws *WebServer) handleAdminTransferApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "ungueltiger Body", http.StatusBadRequest)
		return
	}
	t, status, err := fetchCalibrationTransfer(ws.cfg.ServerURL, ws.cfg.LaneNo)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if !t.CreatedAt.Equal(body.CreatedAt) {
		http.Error(w, "Die Freigabe wurde im Simulator inzwischen geändert - bitte erneut prüfen.", http.StatusConflict)
		return
	}
	res, status, err := ws.applySettings(t.Settings)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	resp := map[string]any{"ok": res.OK, "steps": res.Steps, "remaining": res.Remaining, "mac": res.MAC}
	if err := reportCalibrationTransferApplied(ws.cfg.ServerURL, ws.cfg.LaneNo, t.CreatedAt, res.MAC, res.OK,
		map[string]any{"steps": res.Steps, "remaining": res.Remaining}); err != nil {
		resp["report_error"] = "Rückmeldung an den Server fehlgeschlagen: " + err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}
