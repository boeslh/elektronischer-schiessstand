// ============================================================================
// devicestate.go – geparste ESP32-Telegramme "status"/"config"/"confignet"/
// "cal" (bisher nur geloggt, siehe transport.go dispatchLine) plus ein
// threadsicherer Cache des jeweils zuletzt bekannten Standes fuer die
// Admin-GUI (web/admin.html).
// ============================================================================
package main

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

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

	// Seit Firmware Rev 4.13.0: TATSAECHLICH aktive Zeitstempel-Erfassung
	// ("isr" | "mcpwm") - kann trotz SET CAPTURE=MCPWM "isr" sein, wenn
	// MCPWM beim Boot scheiterte (Vergleich mit config.capture, siehe
	// admin.html). cap_* nur bei "mcpwm" (Zeitbasis-Diagnose).
	Capture     string `json:"capture,omitempty"`
	CapSyncNs   *int   `json:"cap_sync_ns,omitempty"`
	CapCheckNs  *int   `json:"cap_check_ns,omitempty"`
	CapReanchor *uint  `json:"cap_reanchor,omitempty"`
	// Seit Rev 4.15.0: aktuell verwendete Schallgeschwindigkeit und ihre
	// Quelle ("dht" | "fixed"); temp_c/rh_pct/dht_* nur bei SET DHT=1
	// (temp_c/rh_pct null = noch keine gueltige Messung).
	SoundActMps *float64 `json:"sound_act_mps,omitempty"`
	SoundSrc    string   `json:"sound_src,omitempty"`
	TempC       *float64 `json:"temp_c,omitempty"`
	RhPct       *float64 `json:"rh_pct,omitempty"`
	DhtAgeS     *uint    `json:"dht_age_s,omitempty"`
	DhtErr      *uint    `json:"dht_err,omitempty"`

	Corr *int `json:"corr,omitempty"`
}

// ConfigTelegram: siehe Abschnitt 4.3 - Feldnamen 1:1 wie im Telegramm
// (snake_case ueber json-Tag), Bedeutung siehe SET-Tabelle Abschnitt 5.2.
// Seit Firmware Rev 4.11.2: "target"/"standoff_steel_mm" entfallen (TARGET_STEEL
// komplett entfernt, Papier ist die einzige Geometrie) - seit Rev 4.11.0 neu:
// RejectLockoutMs/Algo/PelletRMm/MaxSigmaUm (SET REJECTLOCK/ALGO/PELLETR/MAXSIGMA).
type ConfigTelegram struct {
	Type             string  `json:"type"`
	MAC              string  `json:"mac"`
	Lane             int     `json:"lane"`
	DebounceMs       int     `json:"debounce_ms"`
	RejectLockoutMs  int     `json:"reject_lockout_ms"`
	WindowMs         int     `json:"window_ms"`
	Debug            int     `json:"debug"`
	OutlierUm        int     `json:"outlier_um"`
	ClusterRadiusUm  int     `json:"cluster_radius_um"`
	MinClusterHits   int     `json:"min_cluster_hits"`
	MaxPrecisionUm   int     `json:"max_precision_um"`
	MinMics          int     `json:"min_mics"`
	TDOAUs           int     `json:"tdoa_us"`
	MicOffsetNs      [6]int  `json:"mic_offset_ns"`
	MicEnabled       [6]int  `json:"mic_enabled"`
	CalShots         int     `json:"cal_shots"`
	StandoffPaperMm  float64 `json:"standoff_paper_mm"`
	MicHalfXMm       float64 `json:"mic_half_x_mm"`
	BulletShiftPct   int     `json:"bullet_shift_pct"`
	BulletShiftCapMm float64 `json:"bullet_shift_cap_mm"`
	// Endpositions-Verfahren (Firmware Rev 4.12.0, SET CLUSTERCORR/
	// CLUSTERITER/LSQ/LSQPELLETR) - nil bei aelterer Firmware.
	ClusterCorr       *int     `json:"cluster_corr,omitempty"`
	ClusterIter       *int     `json:"cluster_iter,omitempty"`
	LSQ               *int     `json:"lsq,omitempty"`
	LSQPelletRMm      *float64 `json:"lsq_pellet_r_mm,omitempty"`
	Algo              string   `json:"algo"`
	PelletRMm         float64  `json:"pellet_r_mm"`
	MaxSigmaUm        int64    `json:"max_sigma_um"`
	UsePiezo          int      `json:"use_piezo"`
	PiezoMinUs        int      `json:"piezo_min_us"`
	PiezoMaxUs        int      `json:"piezo_max_us"`
	TestCooldownMs    int      `json:"test_cooldown_ms"`
	OffsetXUm         int      `json:"offset_x_um"`
	OffsetYUm         int      `json:"offset_y_um"`
	SoundMps          int      `json:"sound_mps"` // seit Rev 4.15.0 gerundet, genau: SoundSetMps
	PaperFeedMm       float64  `json:"paper_feed_mm"`
	PaperSpeedMmps    float64  `json:"paper_speed_mmps"`
	PaperAuto         int      `json:"paper_auto"`
	PaperTrigger      string   `json:"paper_trigger"`
	PaperDirInvert    int      `json:"paper_dir_invert"`
	PaperJogSpeedMmps float64  `json:"paper_jog_speed_mmps"`
	// Seit Firmware Rev 4.13-4.15 (nil bei aelterer Firmware): eingestellte
	// Erfassung (SET CAPTURE, wirkt erst nach Neustart - aktiv: status.capture),
	// Pulsbreiten-Korrektur (SET WALKK/WALKCAL/WALKMAX), genaue
	// Schallgeschwindigkeit (SET SOUNDSPEED mit Nachkommastelle) und DHT22.
	Capture     string   `json:"capture,omitempty"`
	WalkK       *float64 `json:"walk_k,omitempty"`
	WalkCal     *int     `json:"walk_cal,omitempty"`
	WalkMaxUs   *int     `json:"walk_max_us,omitempty"`
	SoundSetMps *float64 `json:"sound_set_mps,omitempty"`
	Dht         *int     `json:"dht,omitempty"`
	// Seit Rev 4.16.0: SET RAWLOG=1 -> zu jedem gueltigen Schuss folgt ein
	// "raw"-Telegramm (alle Flanken + Parameter, siehe rawpair.go).
	RawLog *int `json:"raw_log,omitempty"`
	// Seit Rev 4.17.0: Zonen-Kalibrierung (SET ZONES/ZHOLE, Saetze per
	// SHOWZONES -> ZoneSets). nil = Firmware ohne Zonen.
	Zones   *int `json:"zones,omitempty"`
	ZHoleUm *int `json:"z_hole_um,omitempty"`
	// ZoneSets: Antwort auf SHOWZONES (nicht Teil von config) - nur bei
	// Bedarf gelesen, siehe WebServer.readDeviceConfig.
	ZoneSets *ZonesTelegram `json:"-"`
	// Truncated: Telegramm kam abgeschnitten an (Firmware-Puffergrenze), nur
	// die vollstaendigen Felder sind gueltig - nicht vom Geraet gesendet.
	Truncated bool `json:"truncated,omitempty"`
	Corr      *int `json:"corr,omitempty"`
}

// ZonesTelegram: Antwort auf SHOWZONES (Firmware Rev 4.17.0, protokoll-
// referenz.md 4.3b).
type ZonesTelegram struct {
	Type     string        `json:"type"`
	Zones    int           `json:"zones"`
	Active   int           `json:"active"`
	ZHoleUm  int           `json:"z_hole_um"`
	ZBandUm  int           `json:"z_band_um"`
	ZBlendUm int           `json:"z_blend_um"`
	A        *ZoneSetValue `json:"a"`
	B        *ZoneSetValue `json:"b"`
	C        *ZoneSetValue `json:"c"`
}

// ZoneSetValue: Satz einer Zone (SET ZCAL).
type ZoneSetValue struct {
	SoundMps    float64 `json:"sound_mps"`
	MicOffsetNs [6]int  `json:"mic_offset_ns"`
	OffsetXUm   int     `json:"offset_x_um"`
	OffsetYUm   int     `json:"offset_y_um"`
}

// zcalValue: Wert fuer SET ZCAL=<Zone>,<Wert> - Format wie der Server
// (server/calibration_transfer.go zcalValue), damit der Vergleich greift.
func (z *ZoneSetValue) zcalValue() string {
	if z == nil {
		return "OFF"
	}
	parts := []string{strconv.FormatFloat(z.SoundMps, 'f', 1, 64)}
	for _, o := range z.MicOffsetNs {
		parts = append(parts, strconv.Itoa(o))
	}
	parts = append(parts, strconv.Itoa(z.OffsetXUm), strconv.Itoa(z.OffsetYUm))
	return strings.Join(parts, ",")
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
	SoundMps  int    `json:"sound_mps"` // gerundet - genau: SoundSetMps (Rev 4.15.0)
	Shots     int    `json:"shots"`
	// Seit Rev 4.14/4.15 (nil bei aelterer Firmware), siehe Abschnitt 4.6.
	SoundSetMps *float64 `json:"sound_set_mps,omitempty"`
	SoundActMps *float64 `json:"sound_act_mps,omitempty"`
	WalkK       *float64 `json:"walk_k,omitempty"`
	WalkFitted  *int     `json:"walk_fitted,omitempty"`
	Corr        *int     `json:"corr,omitempty"`

	// capture: nur intern (Sicherung einer uebernommenen Kalibrierung unter
	// neuer MAC), nicht Teil des Telegramms.
	capture string
}

// soundSet liefert die genaue eingestellte Schallgeschwindigkeit (seit
// Rev 4.15.0 sound_set_mps, vorher nur die Ganzzahl sound_mps).
func (c CalTelegram) soundSet() float64 {
	if c.SoundSetMps != nil {
		return *c.SoundSetMps
	}
	return float64(c.SoundMps)
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
	zones     *ZonesTelegram // letzte SHOWZONES-Antwort (Firmware 4.17.0)

	// configSeq/configCh: Zaehler bzw. Benachrichtigung fuer jedes empfangene
	// config-Telegramm (configCh wird bei SetConfig geschlossen und ersetzt) -
	// fuer queryTelegram("SHOW"), siehe ConfigWatch.
	configSeq uint64
	configCh  chan struct{}

	// onCalDone: einmalig in main() gesetzt (vor Start der Transport-Goroutinen,
	// daher ohne eigenes Locking) - benachrichtigt WebServer.uploadCalibrationBackup
	// ueber transport.go, ohne dass dispatchLine() den WebServer kennen muesste.
	onCalDone func(CalTelegram)

	// onConfig: wie onCalDone, fuer jedes empfangene config-Telegramm
	// (Konfigurationssicherung, siehe config_backup.go).
	onConfig func(ConfigTelegram)
}

func NewDeviceState() *DeviceState { return &DeviceState{} }

func (d *DeviceState) SetStatus(s StatusTelegram) { d.mu.Lock(); d.status = &s; d.mu.Unlock() }
func (d *DeviceState) SetConfig(c ConfigTelegram) {
	d.mu.Lock()
	d.config = &c
	d.configSeq++
	if d.configCh != nil {
		close(d.configCh)
	}
	d.configCh = make(chan struct{})
	d.mu.Unlock()
}
func (d *DeviceState) SetConfignet(c ConfignetTelegram) { d.mu.Lock(); d.confignet = &c; d.mu.Unlock() }
func (d *DeviceState) SetZones(z ZonesTelegram)         { d.mu.Lock(); d.zones = &z; d.mu.Unlock() }
func (d *DeviceState) SetCal(c CalTelegram)             { d.mu.Lock(); d.cal = &c; d.mu.Unlock() }

// ConfigWatch liefert den aktuellen config-Zaehler und einen Kanal, der beim
// naechsten empfangenen config-Telegramm geschlossen wird.
func (d *DeviceState) ConfigWatch() (uint64, <-chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.configCh == nil {
		d.configCh = make(chan struct{})
	}
	return d.configSeq, d.configCh
}

// ConfigAfter liefert das zuletzt empfangene config-Telegramm, sofern es
// nach dem per ConfigWatch gemerkten Zaehlerstand seq eingetroffen ist.
func (d *DeviceState) ConfigAfter(seq uint64) *ConfigTelegram {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.configSeq > seq && d.config != nil {
		c := *d.config
		return &c
	}
	return nil
}

// SetCalDoneHandler registriert den Callback fuer abgeschlossene Kalibrierungen
// (siehe transport.go dispatchLine, Fall "cal"/"done").
func (d *DeviceState) SetCalDoneHandler(f func(CalTelegram)) { d.onCalDone = f }

// SetConfigHandler registriert den Callback fuer empfangene config-Telegramme
// (einmalig in main(), vor Start der Transport-Goroutinen).
func (d *DeviceState) SetConfigHandler(f func(ConfigTelegram)) { d.onConfig = f }

// ShotContext: Geraetezustand, der einem Schuss fuer das spaetere
// Nachrechnen im Simulator mitgegeben wird - aktive Schallgeschwindigkeit
// und Erfassung (letztes status-Telegramm, bei DHT=1 regelmaessig per
// runStatusPoll erneuert) sowie das eingestellte WALKK (letztes config).
type ShotContext struct {
	SoundActMps *float64
	Capture     string
	WalkK       *float64
}

func (d *DeviceState) ShotContext() ShotContext {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var c ShotContext
	if d.status != nil {
		c.SoundActMps, c.Capture = d.status.SoundActMps, d.status.Capture
	}
	if d.config != nil {
		c.WalkK = d.config.WalkK
		if c.SoundActMps == nil && d.config.SoundSetMps != nil && (d.config.Dht == nil || *d.config.Dht == 0) {
			c.SoundActMps = d.config.SoundSetMps
		}
	}
	return c
}

// DHTActive: ob laut letztem config-Telegramm die DHT22-Messung aktiv ist
// (dann aendert sich die Schallgeschwindigkeit laufend, siehe runStatusPoll).
func (d *DeviceState) DHTActive() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.config != nil && d.config.Dht != nil && *d.config.Dht == 1
}

// RawLogActive: ob laut letztem config-Telegramm "raw"-Telegramme folgen
// (SET RAWLOG=1, Firmware Rev 4.16.0).
func (d *DeviceState) RawLogActive() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.config != nil && d.config.RawLog != nil && *d.config.RawLog == 1
}

// ActiveCapture: aktiver Erfassungsmodus laut letztem status ("" = unbekannt).
func (d *DeviceState) ActiveCapture() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.status == nil {
		return ""
	}
	return d.status.Capture
}

// Snapshot: konsistente Kopie aller vier Felder fuer die Admin-GUI-Antwort.
type DeviceStateSnapshot struct {
	Status    *StatusTelegram    `json:"status"`
	Config    *ConfigTelegram    `json:"config"`
	Confignet *ConfignetTelegram `json:"confignet"`
	Cal       *CalTelegram       `json:"cal"`
	Zones     *ZonesTelegram     `json:"zones,omitempty"`
}

func (d *DeviceState) Snapshot() DeviceStateSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return DeviceStateSnapshot{Status: d.status, Config: d.config, Confignet: d.confignet, Cal: d.cal, Zones: d.zones}
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

// statusPollInterval: Abstand der STATUS-Abfragen bei aktiver DHT22-Messung
// (Firmware misst alle 30 s).
const statusPollInterval = 30 * time.Second

// runStatusPoll fragt bei aktiver DHT22-Messung (SET DHT=1, Firmware Rev
// 4.15.0) regelmaessig STATUS ab. Die Firmware sendet status nur beim
// Verbindungsaufbau und auf Anfrage - ohne Abfrage bliebe sound_act_mps
// (Schallgeschwindigkeit aus Temperatur/Luftfeuchte), das jedem Schuss fuer
// das Nachrechnen im Simulator mitgegeben wird, auf dem Stand des
// Verbindungsaufbaus stehen. Bei DHT=0 ist die Schallgeschwindigkeit fest,
// dann wird nichts abgefragt (kein unnoetiger Verkehr zum ESP32).
func runStatusPoll(ctx context.Context, cmds *CommandManager, state *DeviceState) {
	t := time.NewTicker(statusPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if state.DHTActive() {
				cmds.SendCommand("STATUS", 0) // Antwort aktualisiert den DeviceState ueber dispatchLine
			}
		}
	}
}
