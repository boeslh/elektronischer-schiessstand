// ============================================================================
// devicestate.go – geparste ESP32-Telegramme "status"/"config"/"confignet"/
// "cal" (bisher nur geloggt, siehe transport.go dispatchLine) plus ein
// threadsicherer Cache des jeweils zuletzt bekannten Standes fuer die
// Admin-GUI (web/admin.html).
// ============================================================================
package main

import "sync"

// StatusTelegram: siehe protokoll-referenz.md Abschnitt 4.2. "mac" seit
// Firmware Rev 4.9.0 (stabile Geraete-ID, unabhaengig von "lane").
type StatusTelegram struct {
	Type       string `json:"type"`
	Version    string `json:"version"`
	MAC        string `json:"mac"`
	Lane       int    `json:"lane"`
	UptimeS    uint64 `json:"uptime_s"`
	Shots      uint   `json:"shots"`
	WindowMs   uint   `json:"window_ms"`
	DebounceMs uint   `json:"debounce_ms"`
	Mics       int    `json:"mics"`
	Buffered   uint   `json:"buffered"`
	TestMode   int    `json:"test_mode"`
	Corr       *int   `json:"corr,omitempty"`
}

// ConfigTelegram: siehe Abschnitt 4.3 - Feldnamen 1:1 wie im Telegramm
// (snake_case ueber json-Tag), Bedeutung siehe SET-Tabelle Abschnitt 5.2.
type ConfigTelegram struct {
	Type              string  `json:"type"`
	MAC               string  `json:"mac"`
	Lane              int     `json:"lane"`
	DebounceMs        int     `json:"debounce_ms"`
	WindowMs          int     `json:"window_ms"`
	Debug             int     `json:"debug"`
	OutlierUm         int     `json:"outlier_um"`
	ClusterRadiusUm   int     `json:"cluster_radius_um"`
	MinClusterHits    int     `json:"min_cluster_hits"`
	MaxPrecisionUm    int     `json:"max_precision_um"`
	MinMics           int     `json:"min_mics"`
	TDOAUs            int     `json:"tdoa_us"`
	MicOffsetNs       [6]int  `json:"mic_offset_ns"`
	MicEnabled        [6]int  `json:"mic_enabled"`
	CalShots          int     `json:"cal_shots"`
	Target            string  `json:"target"`
	StandoffSteelMm   float64 `json:"standoff_steel_mm"`
	StandoffPaperMm   float64 `json:"standoff_paper_mm"`
	MicHalfXMm        float64 `json:"mic_half_x_mm"`
	BulletShiftPct    int     `json:"bullet_shift_pct"`
	BulletShiftCapMm  float64 `json:"bullet_shift_cap_mm"`
	UsePiezo          int     `json:"use_piezo"`
	PiezoMinUs        int     `json:"piezo_min_us"`
	PiezoMaxUs        int     `json:"piezo_max_us"`
	TestCooldownMs    int     `json:"test_cooldown_ms"`
	OffsetXUm         int     `json:"offset_x_um"`
	OffsetYUm         int     `json:"offset_y_um"`
	SoundMps          int     `json:"sound_mps"`
	PaperFeedMm       float64 `json:"paper_feed_mm"`
	PaperSpeedMmps    float64 `json:"paper_speed_mmps"`
	PaperAuto         int     `json:"paper_auto"`
	PaperTrigger      string  `json:"paper_trigger"`
	PaperDirInvert    int     `json:"paper_dir_invert"`
	PaperJogSpeedMmps float64 `json:"paper_jog_speed_mmps"`
	Corr              *int    `json:"corr,omitempty"`
}

// ConfignetTelegram: siehe Abschnitt 4.4. "net_pending"/
// "net_confirm_deadline_s" seit Rev 4.9.0 (Netzwerk-Sicherheitsnetz,
// Abschnitt 7.6).
type ConfignetTelegram struct {
	Type                string `json:"type"`
	MAC                 string `json:"mac"`
	SSID                string `json:"ssid"`
	Pass                string `json:"pass"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	StaticIP            int    `json:"static_ip"`
	IP                  string `json:"ip"`
	Gateway             string `json:"gateway"`
	Subnet              string `json:"subnet"`
	DNS                 string `json:"dns"`
	WifiIP              string `json:"wifi_ip"`
	TCPConnected        bool   `json:"tcp_connected"`
	NetPending          bool   `json:"net_pending"`
	NetConfirmDeadlineS *int   `json:"net_confirm_deadline_s,omitempty"`
	Corr                *int   `json:"corr,omitempty"`
}

// CalTelegram: siehe Abschnitt 4.6. "mac" bei state="done" seit Rev 4.9.0 -
// zusammen mit offsets_ns/sound_mps Grundlage fuer das Kalibrierungs-Backup
// (siehe uploadCalibrationBackup in calibration_backup.go).
type CalTelegram struct {
	Type      string `json:"type"`
	State     string `json:"state"`
	Need      int    `json:"need"`
	Progress  int    `json:"progress"`
	Reason    string `json:"reason"`
	MAC       string `json:"mac"`
	OffsetsNs [6]int `json:"offsets_ns"`
	SoundMps  int    `json:"sound_mps"`
	Shots     int    `json:"shots"`
	Corr      *int   `json:"corr,omitempty"`
}

// DeviceState haelt den jeweils zuletzt bekannten Stand der vier Telegramme
// - gefuellt passiv durch dispatchLine() (unabhaengig davon, ob sie als
// Antwort auf einen expliziten Befehl mit corr kamen oder z.B. automatisch
// bei Connect gesendet wurden), gelesen von der Admin-GUI.
type DeviceState struct {
	mu        sync.RWMutex
	status    *StatusTelegram
	config    *ConfigTelegram
	confignet *ConfignetTelegram
	cal       *CalTelegram

	// onCalDone: einmalig in main() gesetzt (vor Start der Transport-Goroutinen,
	// daher ohne eigenes Locking) - benachrichtigt WebServer.uploadCalibrationBackup
	// ueber transport.go, ohne dass dispatchLine() den WebServer kennen muesste.
	onCalDone func(CalTelegram)
}

func NewDeviceState() *DeviceState { return &DeviceState{} }

func (d *DeviceState) SetStatus(s StatusTelegram)       { d.mu.Lock(); d.status = &s; d.mu.Unlock() }
func (d *DeviceState) SetConfig(c ConfigTelegram)       { d.mu.Lock(); d.config = &c; d.mu.Unlock() }
func (d *DeviceState) SetConfignet(c ConfignetTelegram) { d.mu.Lock(); d.confignet = &c; d.mu.Unlock() }
func (d *DeviceState) SetCal(c CalTelegram)             { d.mu.Lock(); d.cal = &c; d.mu.Unlock() }

// SetCalDoneHandler registriert den Callback fuer abgeschlossene Kalibrierungen
// (siehe transport.go dispatchLine, Fall "cal"/"done").
func (d *DeviceState) SetCalDoneHandler(f func(CalTelegram)) { d.onCalDone = f }

// Snapshot: konsistente Kopie aller vier Felder fuer die Admin-GUI-Antwort.
type DeviceStateSnapshot struct {
	Status    *StatusTelegram    `json:"status"`
	Config    *ConfigTelegram    `json:"config"`
	Confignet *ConfignetTelegram `json:"confignet"`
	Cal       *CalTelegram       `json:"cal"`
}

func (d *DeviceState) Snapshot() DeviceStateSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return DeviceStateSnapshot{Status: d.status, Config: d.config, Confignet: d.confignet, Cal: d.cal}
}

// CurrentMAC: bequemer Zugriff auf die zuletzt bekannte Geraete-ID (fuer den
// Kalibrierungs-Restore, der die MAC braucht, um den richtigen
// Server-Datensatz zu laden) - "" wenn noch kein status/config/confignet
// empfangen wurde.
func (d *DeviceState) CurrentMAC() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.status != nil && d.status.MAC != "" {
		return d.status.MAC
	}
	if d.config != nil && d.config.MAC != "" {
		return d.config.MAC
	}
	if d.confignet != nil && d.confignet.MAC != "" {
		return d.confignet.MAC
	}
	return ""
}
