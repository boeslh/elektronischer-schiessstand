// ============================================================================
// ESP32-Simulator – testet die komplette Kette ohne reale Hardware
//
// Bildet die Firmware Rev 3.1/3.2 nach:
//   - identisches JSON-Zeilenprotokoll (shot/status/pong/ok/reject)
//   - Befehle: PING, STATUS, RESET, SHOW, DEBOUNCE=, WINDOW= (Dummy)
//   - Transport TCP-Client (wie ESP32 -> Stand-PC) ODER virtueller
//     serieller Port (PTY) fuer den "serial"-Transport des Stand-PCs
//
// Physik-Simulation:
//   - Liest DIESELBE config.json wie die Stand-PC-Software -> Sensor-
//     positionen, Blechwinkel und Schallgeschwindigkeit sind konsistent
//   - Treffer wird in SCHEIBEN-Koordinaten gewuerfelt (Schuetzenmodell:
//     2D-Normalverteilung um den Haltepunkt, Streuung einstellbar),
//     dann INVERS auf das Blech projiziert und daraus die exakten
//     Sensorlaufzeiten berechnet
//   - Realismus: 1-µs-Quantisierung (wie esp_timer), optionales
//     Gauss-Rauschen auf den Zeiten, Sensorausfall-Wahrscheinlichkeit
//
// Einzelne Lane:
//   TCP:    ./simulator -config ../config.json -connect 127.0.0.1:9201
//   Seriell:./simulator -config ../config.json -pty
//           (gibt /dev/pts/N aus -> als serial_port eintragen)
//   Manuell:./simulator ... -mode manual    dann z.B. "shot 2.5 -1.0"
//
// Mehrere Lanes auf einmal (auto-Modus, nur TCP):
//   ./simulator -lanes 4 -config-dir ..
//   Lädt ../config.json (Lane 1) + ../config-lane02..04.json,
//   verbindet je zu 127.0.0.1:9201..9204
//
// Build:  go build -o simulator ./simulator
// ============================================================================
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unsafe"

	"syscall"
)

// ----------------------------------------------------------------------------
// Konfiguration: Teilmenge der Stand-PC config.json
// ----------------------------------------------------------------------------

type sensorPos struct {
	X float64 `json:"x_mm"`
	Y float64 `json:"y_mm"`
}

type airSensor struct {
	X float64 `json:"x_mm"`
	Y float64 `json:"y_mm"`
	Z float64 `json:"z_mm"`
}

type hybridCfg struct {
	Enabled          bool        `json:"enabled"`
	AirSensors       []airSensor `json:"air_sensors"`
	AirSoundSpeedMPS float64     `json:"air_sound_speed_mps"`
}

type simConfig struct {
	Sensors       []sensorPos `json:"sensors"`
	SoundSpeedMPS float64     `json:"sound_speed_mps"`
	PlateAngleDeg float64     `json:"plate_angle_deg"`
	PlateOffsetX  float64     `json:"plate_offset_x_mm"`
	PlateOffsetY  float64     `json:"plate_offset_y_mm"`
	Hybrid        hybridCfg   `json:"hybrid"`
}

func loadSimConfig(path string) (*simConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c simConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if len(c.Sensors) < 3 {
		return nil, fmt.Errorf("config braucht >=3 Sensoren")
	}
	if c.SoundSpeedMPS <= 0 {
		c.SoundSpeedMPS = 3000
	}
	return &c, nil
}

// ----------------------------------------------------------------------------
// Physik: Scheibe -> Blech -> Sensorzeiten
// ----------------------------------------------------------------------------

type simulator struct {
	cfg *simConfig

	plateCX, plateCY float64 // Blechmittelpunkt
	angleRad         float64
	soundMMperUS     float64

	noiseUS float64 // Gauss-Sigma auf den Zeiten
	dropout float64 // P(Sensor faellt aus)

	mu  sync.Mutex
	seq int
	out io.Writer
}

func newSimulator(cfg *simConfig, noiseUS, dropout float64) *simulator {
	s := &simulator{
		cfg:          cfg,
		angleRad:     cfg.PlateAngleDeg * math.Pi / 180,
		soundMMperUS: cfg.SoundSpeedMPS / 1000.0,
		noiseUS:      noiseUS,
		dropout:      dropout,
	}
	for _, p := range cfg.Sensors {
		s.plateCX += p.X
		s.plateCY += p.Y
	}
	n := float64(len(cfg.Sensors))
	s.plateCX /= n
	s.plateCY /= n
	return s
}

// targetToPlate: INVERSE der PlateToTarget-Transformation des Stand-PCs.
// Stand-PC:  tx = (px-cx)+offX ;  ty = (py-cy)*cos(a)+offY
// Invers:    px = tx-offX+cx   ;  py = (ty-offY)/cos(a)+cy
func (s *simulator) targetToPlate(tx, ty float64) (px, py float64) {
	px = tx - s.cfg.PlateOffsetX + s.plateCX
	py = (ty-s.cfg.PlateOffsetY)/math.Cos(s.angleRad) + s.plateCY
	return
}

// fire erzeugt das Schuss-Telegramm fuer eine Trefferposition auf der SCHEIBE
func (s *simulator) fire(targetX, targetY float64) {
	px, py := s.targetToPlate(targetX, targetY)

	// Exakte Laufzeiten je Sensor
	n := len(s.cfg.Sensors)
	dist := make([]float64, n)
	minD := math.Inf(1)
	for i, sp := range s.cfg.Sensors {
		dist[i] = math.Hypot(px-sp.X, py-sp.Y)
		if dist[i] < minD {
			minD = dist[i]
		}
	}

	times := make([]int64, n) // Nanosekunden
	hits := 0
	firstSet := false
	const ccResNs = 1000.0 / 240.0 // ~4,17ns @ 240MHz (wie Firmware 3.3)
	for i := range dist {
		// Sensorausfall?
		if rand.Float64() < s.dropout {
			times[i] = -1
			continue
		}
		t := (dist[i] - minD) / s.soundMMperUS * 1000.0 // ns, exakt
		if s.noiseUS > 0 {
			t += rand.NormFloat64() * s.noiseUS * 1000.0
		}
		if t < 0 {
			t = 0
		}
		// Zykluszaehler-Quantisierung wie Firmware Rev 3.3
		times[i] = int64(math.Round(t/ccResNs) * ccResNs)
		hits++
		if times[i] == 0 {
			firstSet = true
		}
	}
	// Referenzsensor sicherstellen (Firmware: erster Hit hat immer t=0)
	if hits > 0 && !firstSet {
		minT := int64(math.MaxInt64)
		for _, t := range times {
			if t >= 0 && t < minT {
				minT = t
			}
		}
		for i := range times {
			if times[i] >= 0 {
				times[i] -= minT
			}
		}
	}

	// ─── Hybrid: Luftflanken je Mikrofon erzeugen ───────────────────
	// Physik: t=0 ist der erste Stahl-Hit; der Aufprall lag davor bei
	//   t0 = −minD / v_stahl
	// Je Mikrofon entstehen:
	//   1. VORLAEUFER: Stahlwelle laeuft lateral bis unter das Mikrofon
	//      und strahlt ab: t0 + d_lateral/v_stahl + z/v_luft
	//   2. DIREKTER SCHALL: t0 + dist3D/v_luft   ← der gesuchte
	//   3. NACHKLINGELN: 1–2 Stoerflanken nach dem Vorlaeufer
	var airOut [][]int64
	if s.cfg.Hybrid.Enabled {
		vAir := s.cfg.Hybrid.AirSoundSpeedMPS
		if vAir <= 0 {
			vAir = 343
		}
		vAirMMNS := vAir / 1e6
		vSteelMMNS := s.soundMMperUS / 1000.0 // mm/µs -> mm/ns? s.soundMMperUS ist mm/µs
		_ = vSteelMMNS
		t0 := -(minD / s.soundMMperUS) * 1000.0 // ns (negativ)
		const ccResNs = 1000.0 / 240.0
		for _, m := range s.cfg.Hybrid.AirSensors {
			var edges []int64
			lat := math.Hypot(px-m.X, py-m.Y)
			d3 := math.Sqrt(lat*lat + m.Z*m.Z)
			// 1. Vorlaeufer
			pre := t0 + lat/s.soundMMperUS*1000.0 + m.Z/vAirMMNS
			// 2. direkter Schall
			direct := t0 + d3/vAirMMNS
			for _, t := range []float64{pre, direct} {
				t += rand.NormFloat64() * s.noiseUS * 1000.0
				if t < 0 {
					t = 0
				}
				edges = append(edges, int64(math.Round(t/ccResNs)*ccResNs))
			}
			// 3. Nachklingeln: Stoerflanken zwischen Vorlaeufer und +1ms
			for k := 0; k < 1+rand.Intn(2); k++ {
				t := pre + 80_000 + rand.Float64()*400_000
				edges = append(edges, int64(t))
			}
			airOut = append(airOut, edges)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if hits < 3 {
		fmt.Fprintf(s.out,
			"{\"type\":\"reject\",\"reason\":\"only %d sensor(s)\",\"hits\":%d}\n",
			hits, hits)
		log.Printf("REJECT (%d Sensoren) – Ziel war x=%.1f y=%.1f",
			hits, targetX, targetY)
		return
	}

	s.seq++
	nsParts := make([]string, n)
	usParts := make([]string, n)
	for i, t := range times {
		nsParts[i] = fmt.Sprintf("%d", t)
		if t < 0 {
			usParts[i] = "-1"
		} else {
			usParts[i] = fmt.Sprintf("%d", (t+500)/1000)
		}
	}
	airJSON := ""
	if s.cfg.Hybrid.Enabled {
		var mics []string
		for _, edges := range airOut {
			var es []string
			for _, e := range edges {
				es = append(es, fmt.Sprintf("%d", e))
			}
			mics = append(mics, "["+strings.Join(es, ",")+"]")
		}
		airJSON = ",\"air_ns\":[" + strings.Join(mics, ",") + "]"
	}
	fmt.Fprintf(s.out,
		"{\"type\":\"shot\",\"seq\":%d,\"t_ns\":[%s],\"t_us\":[%s]%s,"+
			"\"hits\":%d,\"ts\":%d}\n",
		s.seq, strings.Join(nsParts, ","), strings.Join(usParts, ","),
		airJSON, hits, time.Now().UnixMilli())
	log.Printf("Schuss #%d  Ziel x=%+.2f y=%+.2f mm  t_ns=%v",
		s.seq, targetX, targetY, times)
}

// ----------------------------------------------------------------------------
// Befehle (PING/STATUS/...) – Firmware-kompatibel
// ----------------------------------------------------------------------------

func (s *simulator) handleCommand(cmd string) {
	c := strings.ToUpper(strings.TrimSpace(cmd))
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case c == "":
	case c == "PING":
		fmt.Fprint(s.out, "{\"type\":\"pong\"}\n")
	case c == "STATUS", c == "SHOW":
		fmt.Fprintf(s.out, "{\"type\":\"status\",\"version\":\"SIM-1.0\","+
			"\"shots\":%d,\"sensors\":%d,\"simulator\":true}\n",
			s.seq, len(s.cfg.Sensors))
	case c == "RESET":
		s.seq = 0
		fmt.Fprint(s.out, "{\"type\":\"ok\",\"cmd\":\"reset\"}\n")
	case strings.HasPrefix(c, "DEBOUNCE="), strings.HasPrefix(c, "WINDOW="),
		strings.HasPrefix(c, "SET "):
		fmt.Fprint(s.out, "{\"type\":\"ok\",\"simulated\":true}\n")
	default:
		fmt.Fprint(s.out, "{\"type\":\"error\",\"msg\":\"unknown command\"}\n")
	}
}

func (s *simulator) readCommands(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		s.handleCommand(sc.Text())
	}
}

// ----------------------------------------------------------------------------
// Transporte
// ----------------------------------------------------------------------------

// TCP-Client wie die echte Firmware: verbindet sich zum Stand-PC
func connectTCP(addr string) (io.ReadWriter, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	log.Printf("TCP verbunden mit %s", addr)
	return conn, nil
}

// PTY: virtueller serieller Port. Wir halten die Master-Seite, der
// Stand-PC oeffnet den Slave (/dev/pts/N) wie einen echten USB-Port.
func openPTY() (io.ReadWriter, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, "", err
	}
	// unlockpt
	var unlock int32 = 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(),
		0x40045431 /*TIOCSPTLCK*/, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		return nil, "", errno
	}
	// ptsname
	var ptn uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(),
		0x80045430 /*TIOCGPTN*/, uintptr(unsafe.Pointer(&ptn))); errno != 0 {
		return nil, "", errno
	}
	slave := fmt.Sprintf("/dev/pts/%d", ptn)

	// Slave auf raw/-echo stellen, sonst spiegelt das Terminal unsere
	// eigenen Telegramme als "Befehle" zurueck
	if err := exec.Command("stty", "-F", slave, "raw", "-echo").Run(); err != nil {
		log.Printf("WARNUNG: stty auf %s fehlgeschlagen: %v", slave, err)
	}
	return master, slave, nil
}

// ----------------------------------------------------------------------------
// Schuetzenmodell fuer den Automatikmodus
// ----------------------------------------------------------------------------
//
// 2D-Normalverteilung um den Haltepunkt. Faustwerte fuer LG 10m
// (Streukreis-Sigma der Trefferablage):
//   spread 0.5 mm  ~ Weltklasse (~10.5er Schnitt)
//   spread 1.5 mm  ~ guter Vereinsschuetze
//   spread 3.0 mm  ~ Anfaenger
// ----------------------------------------------------------------------------

// autoFire schiesst maxShots Schuesse; maxShots=0 bedeutet unbegrenzt.
func autoFire(s *simulator, interval time.Duration, spread, aimX, aimY float64, maxShots int) {
	if maxShots > 0 {
		log.Printf("Automatik: %d Schuesse, alle %v, Streuung sigma=%.1f mm, "+
			"Haltepunkt (%.1f/%.1f)", maxShots, interval, spread, aimX, aimY)
	} else {
		log.Printf("Automatik: ∞ Schuesse, alle %v, Streuung sigma=%.1f mm, "+
			"Haltepunkt (%.1f/%.1f)", interval, spread, aimX, aimY)
	}
	for i := 0; maxShots == 0 || i < maxShots; i++ {
		// Zufaelliger Abzugszeitpunkt (+-20%)
		jitter := time.Duration((rand.Float64()*0.4 - 0.2) * float64(interval))
		time.Sleep(interval + jitter)
		x := aimX + rand.NormFloat64()*spread
		y := aimY + rand.NormFloat64()*spread
		s.fire(x, y)
	}
	log.Printf("Alle %d Schuesse abgegeben.", maxShots)
}

// ----------------------------------------------------------------------------
// main
// ----------------------------------------------------------------------------

// laneConfigPath gibt den Pfad zur config.json einer Lane zurück.
// Lane 1 → <dir>/config.json, Lane N → <dir>/config-lane0N.json
func laneConfigPath(dir string, laneNo int) string {
	if laneNo == 1 {
		return fmt.Sprintf("%s/config.json", dir)
	}
	return fmt.Sprintf("%s/config-lane%02d.json", dir, laneNo)
}

// laneAddr berechnet die TCP-Adresse nach dem Portschema:
// Anzeige 9001..9099, ESP32-TCP 9201..9299
func laneAddr(host string, laneNo int) string {
	return fmt.Sprintf("%s:%d", host, 9200+laneNo)
}

func runLane(laneNo int, cfgPath, addr string,
	interval time.Duration, spread, aimX, aimY, noise, dropout float64,
	maxShots int, wg *sync.WaitGroup) {
	defer wg.Done()

	cfg, err := loadSimConfig(cfgPath)
	if err != nil {
		log.Printf("[Lane %d] FEHLER config %s: %v", laneNo, cfgPath, err)
		return
	}
	sim := newSimulator(cfg, noise, dropout)
	// Jeder Simulator startet mit leicht unterschiedlichem Haltepunkt,
	// damit die Schussmuster sich nicht ueberlagern.
	jitterX := (rand.Float64()*2 - 1) * spread * 0.5
	jitterY := (rand.Float64()*2 - 1) * spread * 0.5

	// Retry-Schleife: Stand-PC startet evtl. spaeter
	var rw io.ReadWriter
	for {
		rw, err = connectTCP(addr)
		if err != nil {
			log.Printf("[Lane %d] Verbindung zu %s fehlgeschlagen, retry in 5s...", laneNo, addr)
			time.Sleep(5 * time.Second)
			continue
		}
		break
	}
	sim.out = rw
	sim.handleCommand("STATUS")
	fmt.Fprint(rw, "{\"type\":\"ready\"}\n")
	go sim.readCommands(rw)
	log.Printf("[Lane %d] verbunden mit %s", laneNo, addr)
	autoFire(sim, interval, spread, aimX+jitterX, aimY+jitterY, maxShots)
}

func main() {
	cfgPath := flag.String("config", "config.json",
		"config.json der Stand-PC-Software (Geometrie!)")
	connect := flag.String("connect", "",
		"TCP-Modus: Adresse des Stand-PCs, z.B. 127.0.0.1:9201")
	usePTY := flag.Bool("pty", false,
		"Seriell-Modus: virtuellen Port (/dev/pts/N) erzeugen")
	mode := flag.String("mode", "auto", "auto | manual")
	interval := flag.Duration("interval", 8*time.Second,
		"Schussintervall im auto-Modus")
	spread := flag.Float64("spread", 1.5,
		"Streuung sigma in mm (0.5=Profi 1.5=gut 3.0=Anfaenger)")
	aimX   := flag.Float64("aim-x", 0, "Haltepunkt-Ablage x in mm")
	aimY   := flag.Float64("aim-y", 0, "Haltepunkt-Ablage y in mm")
	noise   := flag.Float64("noise", 0.05, "Zeitrauschen sigma in µs (0.05µs=50ns realistisch)")
	dropout := flag.Float64("dropout", 0.02,
		"Wahrscheinlichkeit Sensorausfall je Sensor (0..1)")
	shots := flag.Int("shots", 0,
		"Anzahl Schuesse pro Lane, dann beenden (0 = unbegrenzt)")

	// Multi-Lane-Flags
	numLanes  := flag.Int("lanes", 0,
		"Anzahl Lanes gleichzeitig simulieren (1..99); überschreibt -config und -connect")
	configDir := flag.String("config-dir", "..",
		"Verzeichnis mit config.json / config-lane0N.json (nur mit -lanes oder -lane)")
	host      := flag.String("host", "127.0.0.1",
		"Hostname/IP des Stand-PCs (nur mit -lanes oder -lane)")
	laneNo := flag.Int("lane", 0,
		"Lane-Nummer (setzt -config und -connect automatisch; überschreibt -config/-connect)")

	flag.Parse()

	// ── Multi-Lane-Modus ──────────────────────────────────────────────────────
	if *numLanes > 0 {
		if *mode == "manual" {
			log.Fatalf("-lanes und -mode manual sind nicht kombinierbar")
		}
		log.Printf("Starte %d Lane-Simulatoren (auto-Modus)...", *numLanes)
		var wg sync.WaitGroup
		for i := 1; i <= *numLanes; i++ {
			path := laneConfigPath(*configDir, i)
			addr := laneAddr(*host, i)
			if _, err := os.Stat(path); err != nil {
				log.Printf("[Lane %d] Keine config gefunden (%s) – übersprungen", i, path)
				continue
			}
			wg.Add(1)
			// Versetzter Start: Schüsse nicht synchron
			go func(laneNo int, p, a string) {
				time.Sleep(time.Duration(laneNo-1) * (*interval / time.Duration(*numLanes)))
				runLane(laneNo, p, a, *interval, *spread, *aimX, *aimY, *noise, *dropout, *shots, &wg)
			}(i, path, addr)
		}
		wg.Wait()
		return
	}

	// ── Einzelne Lane mit -lane N ─────────────────────────────────────────────
	if *laneNo > 0 {
		*cfgPath = laneConfigPath(*configDir, *laneNo)
		*connect = laneAddr(*host, *laneNo)
	}

	// ── Einzelne Lane (bisheriges Verhalten) ─────────────────────────────────
	cfg, err := loadSimConfig(*cfgPath)
	if err != nil {
		log.Fatalf("FATAL config: %v", err)
	}
	sim := newSimulator(cfg, *noise, *dropout)

	var rw io.ReadWriter
	switch {
	case *connect != "":
		rw, err = connectTCP(*connect)
		if err != nil {
			log.Fatalf("FATAL TCP: %v", err)
		}
	case *usePTY:
		var slave string
		rw, slave, err = openPTY()
		if err != nil {
			log.Fatalf("FATAL PTY: %v", err)
		}
		log.Printf("Virtueller serieller Port bereit: %s", slave)
		log.Printf("-> in config.json eintragen: \"serial_port\": \"%s\"", slave)
	default:
		log.Fatalf("Entweder -connect host:port oder -pty oder -lanes N angeben")
	}
	sim.out = rw

	sim.handleCommand("STATUS")
	fmt.Fprint(rw, "{\"type\":\"ready\"}\n")
	go sim.readCommands(rw)

	switch *mode {
	case "auto":
		if *shots > 0 {
			autoFire(sim, *interval, *spread, *aimX, *aimY, *shots)
		} else {
			go autoFire(sim, *interval, *spread, *aimX, *aimY, 0)
			select {}
		}
	case "manual":
		log.Printf("Manueller Modus. Befehle:")
		log.Printf("  shot <x> <y>   Treffer auf Scheibenkoordinate (mm)")
		log.Printf("  r              Zufallsschuss (spread=%.1f)", *spread)
		log.Printf("  q              beenden")
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			switch {
			case len(f) == 0:
			case f[0] == "q":
				return
			case f[0] == "r":
				sim.fire(*aimX+rand.NormFloat64()**spread,
					*aimY+rand.NormFloat64()**spread)
			case f[0] == "shot" && len(f) == 3:
				var x, y float64
				if _, err := fmt.Sscanf(f[1]+" "+f[2], "%f %f", &x, &y); err == nil {
					sim.fire(x, y)
				} else {
					log.Printf("Format: shot 2.5 -1.0")
				}
			default:
				log.Printf("Unbekannt. shot <x> <y> | r | q")
			}
		}
	default:
		log.Fatalf("mode muss auto oder manual sein")
	}
}
