// ============================================================================
// Stand-PC Software – Elektronischer Schießstand
// main.go: Konfiguration laden, Komponenten verdrahten, Lebenszyklus
//
// Datenfluss:
//   ESP32 --USB-Serial--> serialReader --> Shot-Pipeline:
//     1. TDOA-Solver  (tdoa.go)   Rohzeiten -> Position auf Blech
//     2. Geometrie    (tdoa.go)   Blech -> Scheibenkoordinaten
//     3. Wertung      (score.go)  x/y -> Ring, Zehntel, Teiler
//     4. Schussprotokoll (shotlog.go)  lokales append-only Log  [IMMER]
//     5. Datenbank    (db.go)     zentrale PostgreSQL          [optional]
//     6. Live-Anzeige (web.go)    Browser via SSE
//
// Build:   go build -o standpc .
// Start:   ./standpc -config config.json
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

// ----------------------------------------------------------------------------
// Konfiguration (config.json)
// ----------------------------------------------------------------------------

type SensorPos struct {
	X float64 `json:"x_mm"`
	Y float64 `json:"y_mm"`
}

type Config struct {
	LaneNo int `json:"lane_no"`

	// Transport: "serial", "tcp" oder "both"
	//   serial: ESP32 per USB-Kabel an diesem PC
	//   tcp:    ESP32 verbindet sich per WLAN/LAN zu diesem PC
	//   both:   beide Kanaele parallel aktiv (z.B. Umruestphase)
	Transport  string `json:"transport"`
	SerialPort string `json:"serial_port"` // z.B. "/dev/ttyUSB0"
	BaudRate   int    `json:"baud_rate"`   // 115200
	TCPListen  string `json:"tcp_listen"`  // z.B. ":9000"

	// Kalibrierung: Positionen der Sensoren auf dem Blech (Blech-Koordinaten,
	// mm). Reihenfolge = Sensorindex im ESP32-Telegramm!
	Sensors []SensorPos `json:"sensors"`

	// Schallgeschwindigkeit IM BLECH (Körperschall, nicht Luft!).
	// Stahl: ~5000 m/s fuer Longitudinalwellen, effektiv (Biegewellen,
	// frequenzabhaengig) meist 2000-3200 m/s -> per Kalibrierung ermitteln.
	SoundSpeedMPS float64 `json:"sound_speed_mps"`

	// Geometrie schraeges Abprallblech -> senkrechte Papierscheibe
	PlateAngleDeg float64 `json:"plate_angle_deg"` // Neigung des Blechs
	PlateOffsetX  float64 `json:"plate_offset_x_mm"`
	PlateOffsetY  float64 `json:"plate_offset_y_mm"`

	// Waffen-/Kalibereigenschaften – gelten fuer alle Disziplinen an diesem Stand
	CaliberMM   float64 `json:"caliber_mm"`   // Geschossdurchmesser mm (Standard: 4.5)
	EdgeScoring bool    `json:"edge_scoring"` // Rand zaehlt zum naechsten Ring

	// Lokales Schussprotokoll
	ShotLogDir string `json:"shot_log_dir"` // z.B. "./shotlog"

	// Zentrale Datenbank (leer lassen = nur lokal arbeiten)
	PostgresDSN string `json:"postgres_dsn"`
	// Zentraler Server: Stand-PC holt Session + Kalibrierung automatisch.
	// Leer lassen -> statische session_id aus dieser Datei (Altverhalten).
	ServerURL string `json:"server_url"`
	SessionID string `json:"session_id"`

	// Webserver fuer die Schuetzenanzeige
	HTTPListen  string `json:"http_listen"`   // z.B. ":8080"
	// Eigene URL fuer Server-Discovery (leer = automatisch aus ServerURL und HTTPListen ableiten)
	AnnounceURL string `json:"announce_url"` // z.B. "http://192.168.1.10:8080"

	// Disziplinen-Konfiguration (leer = disciplines.json im Arbeitsverzeichnis)
	DisciplinesFile string `json:"disciplines_file"`
	// Scheiben-Geometrie (leer = targets.json im Arbeitsverzeichnis)
	TargetsFile string `json:"targets_file"`

	// Hybrid: Luft-TOA-Feinmessung (Firmware: SET HYBRID=1)
	Hybrid HybridConfig `json:"hybrid"`

	// Darstellung der Schuesse in der Anzeige
	ShotDisplay ShotDisplayConfig `json:"shot_display"`
}

// ShotDisplayConfig: visuelle Darstellung der Schussloecher im Browser.
type ShotDisplayConfig struct {
	ColorRing10     string  `json:"color_ring10"`     // Farbe letzter Schuss Ring 10
	ColorRing9      string  `json:"color_ring9"`      // Farbe letzter Schuss Ring 9
	ColorOther      string  `json:"color_other"`      // Farbe letzter Schuss Ringe 0-8
	ColorPrevious   string  `json:"color_previous"`   // Farbe vorherige Schuesse
	OpacityPrevious float64 `json:"opacity_previous"` // Deckkraft vorherige Schuesse (0..1)
	FillColor       string  `json:"fill_color"`       // Hintergrundfarbe (Lochmitte)
}

// DisciplineDef beschreibt eine Schiessdisziplin.
type DisciplineDef struct {
	Name           string `json:"name"`
	TargetNo       int    `json:"target_no"`
	TrialShots     int    `json:"trial_shots"`      // Probeschuesse max.
	ScoringShots   int    `json:"scoring_shots"`    // Wertungsschuesse
	ShotsPerSeries int    `json:"shots_per_series"` // Schuesse je Serie
	DecimalScoring bool   `json:"decimal_scoring"`  // Zehntelwertung aktiv
}

// DisciplinesConfig: Inhalt der disciplines.json
type DisciplinesConfig struct {
	Default     string         `json:"default"`
	Disciplines []DisciplineDef `json:"disciplines"`
}

// builtinDisciplines: eingebettete Standardwerte, falls keine Datei vorhanden.
var builtinDisciplines = DisciplinesConfig{
	Default: "LG-40",
	Disciplines: []DisciplineDef{
		{Name: "LG-40",  TargetNo: 1, TrialShots: 100, ScoringShots: 40, ShotsPerSeries: 10, DecimalScoring: false},
		{Name: "LGA-30", TargetNo: 1, TrialShots: 100, ScoringShots: 30, ShotsPerSeries: 10, DecimalScoring: true},
		{Name: "LP-40",  TargetNo: 7, TrialShots: 100, ScoringShots: 40, ShotsPerSeries: 10, DecimalScoring: false},
		{Name: "LPA-30", TargetNo: 7, TrialShots: 100, ScoringShots: 30, ShotsPerSeries: 10, DecimalScoring: true},
	},
}

func loadDisciplines(path string) (*DisciplinesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var dc DisciplinesConfig
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("disciplines.json parsen: %w", err)
	}
	if len(dc.Disciplines) == 0 {
		return nil, fmt.Errorf("disciplines.json enthaelt keine Disziplinen")
	}
	return &dc, nil
}

// TargetDef: interne Scorer-Konfiguration, zusammengesetzt aus TargetGeometry
// (targets.json) und den Waffen-Parametern (config.json: caliber_mm, edge_scoring).
type TargetDef struct {
	Name        string
	CaliberMM   float64
	EdgeScoring bool
	InnerTenDMM float64
	Rings       []RingDef
}

type RingDef struct {
	Value      int
	DiameterMM float64
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config lesen: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config parsen: %w", err)
	}
	if len(cfg.Sensors) < 3 {
		return nil, fmt.Errorf("mindestens 3 Sensoren noetig, %d konfiguriert",
			len(cfg.Sensors))
	}
	if cfg.BaudRate == 0 {
		cfg.BaudRate = 115200
	}
	switch cfg.Transport {
	case "":
		cfg.Transport = "serial" // rueckwaertskompatibel
	case "serial", "tcp", "both":
	default:
		return nil, fmt.Errorf("transport muss serial, tcp oder both sein, "+
			"nicht %q", cfg.Transport)
	}
	if (cfg.Transport == "tcp" || cfg.Transport == "both") && cfg.TCPListen == "" {
		cfg.TCPListen = ":9000"
	}
	if (cfg.Transport == "serial" || cfg.Transport == "both") &&
		cfg.SerialPort == "" {
		return nil, fmt.Errorf("transport %q braucht serial_port", cfg.Transport)
	}
	if cfg.HTTPListen == "" {
		cfg.HTTPListen = ":8080"
	}
	if cfg.ShotLogDir == "" {
		cfg.ShotLogDir = "./shotlog"
	}
	if cfg.CaliberMM == 0 {
		cfg.CaliberMM = 4.5 // Luftgewehr / Luftpistole ISSF
	}
	// Standardwerte Schussdarstellung
	if cfg.ShotDisplay.ColorRing10 == "" {
		cfg.ShotDisplay.ColorRing10 = "#FF0000"
	}
	if cfg.ShotDisplay.ColorRing9 == "" {
		cfg.ShotDisplay.ColorRing9 = "#F3FB06"
	}
	if cfg.ShotDisplay.ColorOther == "" {
		cfg.ShotDisplay.ColorOther = "#0209F9"
	}
	if cfg.ShotDisplay.ColorPrevious == "" {
		cfg.ShotDisplay.ColorPrevious = "#AAAAAA"
	}
	if cfg.ShotDisplay.OpacityPrevious == 0 {
		cfg.ShotDisplay.OpacityPrevious = 0.85
	}
	if cfg.ShotDisplay.FillColor == "" {
		cfg.ShotDisplay.FillColor = "#000000"
	}
	return &cfg, nil
}

// ----------------------------------------------------------------------------
// Zentrale Datentypen
// ----------------------------------------------------------------------------

// RawShot: dekodiertes ESP32-Telegramm (Firmware ab Rev 4.6.1). Die
// Positionsberechnung (inkl. Kalibrierung) findet nicht mehr auf dem
// Stand-PC statt, sondern direkt auf dem ESP32 - das Telegramm liefert
// x_um/y_um bereits als fertige Scheibenkoordinaten. air_ns bleibt als
// Rohdaten fuer spaetere Analysen/Simulationen erhalten.
//
//	{"type":"shot","seq":8,"air_ns":[[...],...],"x_um":-61627,"y_um":32685,
//	 "pos_res_um":4021,"precision_um":1691,"cluster_hits":2,"pos_valid":1,
//	 "piezo_ns":671125,"piezo_ok":1,"clean":1,"hits":4,"ts":1004046}
//	{"type":"reject","seq":9,"reason":"only 2 mic(s)","hits":2,"piezo_ns":null}
type RawShot struct {
	Type        string    `json:"type"` // "shot" | "reject"
	Seq         int       `json:"seq"`
	AirNs       [][]int64 `json:"air_ns"` // alle erfassten Flanken je Mikrofon, ns
	XUm         int64     `json:"x_um"`
	YUm         int64     `json:"y_um"`
	PosResUm    int64     `json:"pos_res_um"`
	PrecisionUm int64     `json:"precision_um"`
	ClusterHits int       `json:"cluster_hits"`
	PosValid    int       `json:"pos_valid"` // 0/1
	PiezoNs     *int64    `json:"piezo_ns"`  // null wenn Piezo nicht ausgeloest
	PiezoOk     *int      `json:"piezo_ok"`  // 0/1, nur wenn SET PIEZO=1
	Clean       int       `json:"clean"`     // 0/1, nur bei type=shot
	Hits        int       `json:"hits"`
	TsMs        uint64    `json:"ts"`
	Reason      string    `json:"reason"` // nur bei type=reject
}

// Shot: vollstaendig ausgewerteter Schuss (geht an Log, DB und Anzeige).
// Enthaelt sowohl die kompletten Rohwerte aus dem ESP32-Telegramm (fuer
// spaetere Analysen/Simulationen) als auch die daraus abgeleiteten
// Anzeige-/Wertungswerte (nur gueltig, wenn !Rejected).
type Shot struct {
	SessionID string `json:"session_id,omitempty"` // fuer Wiederherstellung nach Neustart
	Seq       int    `json:"seq"`
	FiredAt   string `json:"fired_at"` // RFC3339

	// ---- Rohdaten aus dem ESP32-Telegramm, vollstaendig ----
	AirNs       [][]int64 `json:"air_ns,omitempty"`
	Hits        int       `json:"hits"`
	XUm         int64     `json:"x_um,omitempty"`
	YUm         int64     `json:"y_um,omitempty"`
	PosResUm    int64     `json:"pos_res_um,omitempty"`
	PrecisionUm int64     `json:"precision_um,omitempty"`
	ClusterHits int       `json:"cluster_hits,omitempty"`
	PosValid    bool      `json:"pos_valid"`
	PiezoNs     *int64    `json:"piezo_ns,omitempty"`
	PiezoOk     *bool     `json:"piezo_ok,omitempty"`
	Clean       bool      `json:"clean"`
	DeviceTsMs  uint64    `json:"device_ts_ms"`

	// ---- Fuer die Anzeige/Wertung (nur x_um/y_um sind dafuer relevant) ----
	XMM            float64 `json:"x_mm"` // Scheibenkoordinaten, 0/0 = Mitte
	YMM            float64 `json:"y_mm"`
	Ring           int     `json:"ring"`
	Decimal        float64 `json:"decimal"`
	InnerTen       bool    `json:"inner_ten"`
	CenterDistance float64 `json:"center_distance"` // Abstand Mitte in 1/100 mm

	Mode      string `json:"mode"` // "probe" | "wertung"
	Rejected  bool   `json:"rejected"`
	RejectMsg string `json:"reject_msg,omitempty"`
	Overcount bool   `json:"overcount,omitempty"` // Schuss nach Disziplinabschluss
}

// ----------------------------------------------------------------------------
// main
// ----------------------------------------------------------------------------

func main() {
	cfgPath := flag.String("config", "config.json", "Pfad zur Konfiguration")
	flag.Parse()

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	log.Printf("Stand %d | Transport %s | Kaliber %.1fmm | %d Sensoren",
		cfg.LaneNo, cfg.Transport, cfg.CaliberMM, len(cfg.Sensors))

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shotLog, err := NewShotLog(cfg.ShotLogDir, cfg.LaneNo)
	if err != nil {
		log.Fatalf("FATAL Schussprotokoll: %v", err)
	}
	defer shotLog.Close()

	var db *DBWriter
	if cfg.PostgresDSN != "" {
		db = NewDBWriter(ctx, cfg.PostgresDSN, cfg.ShotLogDir, cfg.LaneNo)
		defer db.Close()
	} else {
		log.Printf("Keine Datenbank konfiguriert – nur lokales Protokoll")
	}

	// --- Disziplinen laden ---
	discFile := cfg.DisciplinesFile
	if discFile == "" {
		discFile = "disciplines.json"
	}
	dc, err := loadDisciplines(discFile)
	if err != nil {
		log.Printf("Disziplinen: %v – verwende eingebettete Standardwerte", err)
		dc = &builtinDisciplines
	} else {
		log.Printf("Disziplinen: %d aus %s geladen (Standard: %s)",
			len(dc.Disciplines), discFile, dc.Default)
	}

	// --- Scheibengeometrie laden ---
	tgtFile := cfg.TargetsFile
	if tgtFile == "" {
		tgtFile = "targets.json"
	}
	targets, err := loadTargets(tgtFile)
	if err != nil {
		log.Printf("Scheiben: %v – verwende eingebettete Standardwerte", err)
		targets = builtinTargets
	}

	// Scorer-Map: je Scheibe einen eigenen Scorer
	scorers := make(map[int]*Scorer)
	for key, tg := range targets {
		no, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		td := TargetGeomToTargetDef(tg, cfg.CaliberMM, cfg.EdgeScoring)
		scorers[no] = NewScorer(&td)
		log.Printf("Scorer Scheibe %d (%s): Nenner %.0f", no, tg.Name,
			(td.Rings[0].DiameterMM/2+td.CaliberMM/2)*100)
	}

	// Fallback-Scorer: Scheibe 1 (LG) oder erste verfuegbare.
	// Greift nur wenn Disziplin noch nicht gesetzt (Startup-Moment).
	var fallbackScorer *Scorer
	if s, ok := scorers[1]; ok {
		fallbackScorer = s
	} else {
		for _, s := range scorers {
			fallbackScorer = s
			break
		}
	}
	if fallbackScorer == nil {
		log.Fatalf("FATAL: Keine Scheibendefinition verfuegbar")
	}

	web := NewWebServer(cfg, dc.Disciplines, targets)

	// Session + Kalibrierung vom zentralen Server (oder statisch/lokal)
	sessions := NewSessionManager(cfg, web, dc.Disciplines)
	web.sessions = sessions // Rueckreferenz fuer /status und /mode

	// Startdisziplin setzen
	if def := sessions.LookupDiscipline(dc.Default); def != nil {
		sessions.SetDiscipline(def)
		log.Printf("Startdisziplin: %s", def.Name)
	}

	go web.Run(ctx)
	go sessions.Run(ctx)

	// --- Shot-Pipeline: verarbeitet RawShots zu Shots ---
	rawCh := make(chan RawShot, 32)

	go func() {
		for raw := range rawCh {
			// Scorer zur aktuellen Disziplin waehlen; Fallback = config.json-Target
			sc := fallbackScorer
			if disc := sessions.CurrentDiscipline(); disc != nil {
				if s, ok := scorers[disc.TargetNo]; ok {
					sc = s
				}
			}
			shot := processShot(raw, sc)

			// histGen VOR NextShot merken: NextShot kann ResetHistory ausloesen
			// (Auto-Switch Probe->Wertung). Stimmt gen nicht mehr ueberein,
			// landet der ausloesende Probe-Schuss nicht in der Wertungs-History.
			histGen := web.HistoryGen()

			// 1. Mode + SessionID setzen (benoetigt fuer Log und Wiederherstellung).
			// Reject-Telegramme (und shot-Telegramme mit pos_valid=0) zaehlen NICHT
			// als Probe-/Wertungsschuss, bekommen aber trotzdem eine shot_no und
			// werden mit status='rejected' in Log/DB gespeichert.
			sid, no, mode, overcount := sessions.NextShot(!shot.Rejected)
			shot.Mode = mode
			shot.SessionID = sid
			shot.Overcount = overcount
			if overcount {
				shot.Ring = 0
				shot.Decimal = 0.0
				shot.CenterDistance = 99999.0
			}
			if db != nil && sid != "" {
				db.Enqueue(&shot, sid, no)
			}

			// 2. Lokal protokollieren – NACH Mode-Zuweisung, VOR DB-Commit
			if err := shotLog.Append(&shot); err != nil {
				log.Printf("FEHLER Schussprotokoll: %v", err)
			}
			sessions.SaveState()

			// 3. Live-Anzeige (Anzeige/index.html filtert rejected selbst heraus)
			web.Broadcast(&shot, histGen)

			if shot.Rejected {
				log.Printf("Schuss #%d VERWORFEN: %s", shot.Seq, shot.RejectMsg)
			} else {
				log.Printf("Schuss #%d  x=%+.2f y=%+.2f  Ring %d (%.1f)  Teiler %.1f  clean=%v",
					shot.Seq, shot.XMM, shot.YMM, shot.Ring, shot.Decimal,
					shot.CenterDistance, shot.Clean)
			}
		}
	}()

	// --- Transport(e) starten: liefern RawShots in rawCh ---
	switch cfg.Transport {
	case "serial":
		runSerialReader(ctx, cfg, rawCh) // blockiert bis ctx-Ende
	case "tcp":
		runTCPReader(ctx, cfg, rawCh) // blockiert bis ctx-Ende
	case "both":
		go runTCPReader(ctx, cfg, rawCh)
		runSerialReader(ctx, cfg, rawCh)
	}

	close(rawCh)
	log.Printf("Beendet.")
}

// processShot: dekodiertes ESP32-Telegramm -> vollstaendiger Shot.
// Die Positionsberechnung findet nicht mehr hier statt (siehe RawShot) -
// x_um/y_um kommen bereits fertig kalibriert vom ESP32. reject-Telegramme
// sowie shot-Telegramme mit pos_valid=0 werden fuer Log/DB durchgereicht,
// aber als Rejected markiert (siehe Aufrufer: Anzeige/Wertung ignoriert sie).
func processShot(raw RawShot, scorer *Scorer) Shot {
	shot := Shot{
		Seq:         raw.Seq,
		FiredAt:     nowRFC3339(),
		AirNs:       raw.AirNs,
		Hits:        raw.Hits,
		XUm:         raw.XUm,
		YUm:         raw.YUm,
		PosResUm:    raw.PosResUm,
		PrecisionUm: raw.PrecisionUm,
		ClusterHits: raw.ClusterHits,
		PosValid:    raw.PosValid != 0,
		Clean:       raw.Clean != 0,
		DeviceTsMs:  raw.TsMs,
		RejectMsg:   raw.Reason,
	}
	if raw.PiezoNs != nil {
		ns := *raw.PiezoNs
		shot.PiezoNs = &ns
	}
	if raw.PiezoOk != nil {
		ok := *raw.PiezoOk != 0
		shot.PiezoOk = &ok
	}

	if raw.Type == "reject" {
		shot.Rejected = true
		return shot
	}
	if !shot.PosValid {
		shot.Rejected = true
		shot.RejectMsg = "keine gueltige Position (pos_valid=0)"
		return shot
	}

	// Fuer die Anzeige/Wertung sind ausschliesslich x_um/y_um relevant.
	shot.XMM = float64(raw.XUm) / 1000.0
	shot.YMM = float64(raw.YUm) / 1000.0

	res := scorer.Score(shot.XMM, shot.YMM)
	shot.Ring = res.Ring
	shot.Decimal = res.Decimal
	shot.InnerTen = res.InnerTen
	shot.CenterDistance = res.CenterDistance
	return shot
}
