// ============================================================================
// web.go – Schuetzenanzeige im Browser
//
// Bewusste Designentscheidung: Server-Sent Events (SSE) statt WebSocket.
//   - Datenfluss ist rein unidirektional (Server -> Browser)
//   - SSE ist Standard-HTTP: keine Zusatzbibliothek, kein Upgrade-Handshake
//   - Browser reconnectet automatisch (EventSource)
//
// Endpunkte:
//   GET /          eingebettete Anzeige-Seite (HTML/JS, kein Build noetig)
//   GET /events    SSE-Stream: named events "shot" und "status"
//   GET /shots     bisherige Schuesse der Sitzung (JSON-Array, fuer Reload)
//   GET /status    aktueller Modus + Disziplin (JSON)
//   POST /mode     Modus umschalten {"mode":"wertung"|"probe"}
// ============================================================================
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

//go:embed web/index.html
var indexHTML []byte

// sseMsg kapselt ein benanntes SSE-Event.
type sseMsg struct {
	event string
	data  []byte
}

type WebServer struct {
	cfg         *Config
	sessions    *SessionManager          // gesetzt nach Konstruktion
	disciplines []DisciplineDef          // unveraenderlich nach Start
	targets     map[string]TargetGeometry // unveraenderlich nach Start
	announceURL string                   // eigene HTTP-URL fuer Server-Discovery

	mu             sync.Mutex
	clients        map[chan sseMsg]struct{}
	history        []*Shot // Sitzungsverlauf fuer neu verbundene Clients
	historyGen     int     // wird bei ResetHistory inkrementiert
	decimalScoring bool    // gecacht aus letztem BroadcastStatus; unter mu
}

func NewWebServer(cfg *Config, disciplines []DisciplineDef, targets map[string]TargetGeometry) *WebServer {
	ws := &WebServer{
		cfg:         cfg,
		disciplines: disciplines,
		targets:     targets,
		clients:     make(map[chan sseMsg]struct{}),
	}
	ws.announceURL = resolveAnnounceURL(cfg)
	if ws.announceURL != "" {
		log.Printf("Discovery: meldet sich als %s", ws.announceURL)
	}
	return ws
}

// resolveAnnounceURL ermittelt die eigene HTTP-URL für die Server-Discovery.
// Priorität: 1. announce_url aus Config  2. Auto-Detect via UDP-Route zum Server
func resolveAnnounceURL(cfg *Config) string {
	if cfg.AnnounceURL != "" {
		return cfg.AnnounceURL
	}
	if cfg.ServerURL == "" || cfg.HTTPListen == "" {
		return ""
	}
	// Port aus HTTPListen extrahieren (z.B. ":8080" oder "0.0.0.0:8080")
	_, port, err := net.SplitHostPort(cfg.HTTPListen)
	if err != nil || port == "" {
		return ""
	}
	// Eigene IP anhand der Route zum Server bestimmen
	u, err := url.Parse(cfg.ServerURL)
	if err != nil {
		return ""
	}
	serverHost := u.Hostname()
	serverPort := u.Port()
	if serverPort == "" {
		serverPort = "80"
	}
	// UDP-Verbindung (sendet keine Pakete) zeigt die ausgehende lokale IP
	conn, err := net.DialTimeout("udp4", net.JoinHostPort(serverHost, serverPort), 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	localIP := conn.LocalAddr().(*net.UDPAddr).IP.String()
	return "http://" + net.JoinHostPort(localIP, port)
}

func (ws *WebServer) Run(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", ws.handleIndex)
	mux.HandleFunc("GET /events", ws.handleSSE)
	mux.HandleFunc("GET /shots", ws.handleShots)
	mux.HandleFunc("GET /status", ws.handleStatus)
	mux.HandleFunc("POST /mode", ws.handleSetMode)
	mux.HandleFunc("GET /disciplines", ws.handleDisciplines)
	mux.HandleFunc("POST /discipline", ws.handleSetDiscipline)
	mux.HandleFunc("GET /targets", ws.handleTargets)
	mux.HandleFunc("GET /display", ws.handleDisplay)
	mux.HandleFunc("GET /api/local-sessions", ws.handleLocalSessions)
	mux.HandleFunc("GET /api/local-sessions/{id}/shots", ws.handleLocalSessionShots)
	mux.HandleFunc("PUT /api/disciplines/config", ws.handlePutDisciplinesConfig)

	srv := &http.Server{Addr: ws.cfg.HTTPListen, Handler: mux}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// Heartbeat: alle 5 s den Live-Zustand an den Server senden,
	// auch wenn kein Schuss faellt und kein Modus gewechselt wird.
	if ws.cfg.ServerURL != "" {
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// CurrentMode ausserhalb ws.mu lesen (vermeidet Lock-Inversion).
					mode := ""
					if ws.sessions != nil {
						mode = ws.sessions.CurrentMode()
					}
					ws.mu.Lock()
					ws.pushLiveState(mode)
					ws.mu.Unlock()
				}
			}
		}()
	}

	log.Printf("Anzeige: http://localhost%s", ws.cfg.HTTPListen)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("Webserver: %v", err)
	}
}

// broadcast sendet ein benanntes SSE-Event an alle verbundenen Browser.
func (ws *WebServer) broadcast(event string, data []byte) {
	msg := sseMsg{event: event, data: data}
	for ch := range ws.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Broadcast sendet einen Schuss an alle verbundenen Browser.
// gen muss vor dem letzten ResetHistory-Aufruf abgefragt worden sein;
// stimmt es nicht ueberein, wird der Schuss nicht in die History aufgenommen
// (verhindert dass der Probe-Ausloeser eines Auto-Switch in die Wertungs-History gelangt).
func (ws *WebServer) Broadcast(shot *Shot, gen int) {
	data, err := json.Marshal(shot)
	if err != nil {
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if !shot.Rejected && ws.historyGen == gen {
		ws.history = append(ws.history, shot)
	}
	ws.broadcast("shot", data)
	ws.pushLiveState(shot.Mode)
}

// BroadcastStatus sendet eine Statusaenderung (Modus/Disziplin) an alle Clients.
func (ws *WebServer) BroadcastStatus(si StatusInfo) {
	data, err := json.Marshal(si)
	if err != nil {
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	// Zehntelwertung cachen, damit pushLiveState keinen SessionManager-Lock braucht.
	if si.Discipline != nil {
		ws.decimalScoring = si.Discipline.DecimalScoring
	} else {
		ws.decimalScoring = false
	}
	ws.broadcast("status", data)
	ws.pushLiveState(si.Mode)
}

// pushLiveState schickt den aktuellen Zustand an den zentralen Server.
// Muss unter ws.mu aufgerufen werden. Startet HTTP-Aufruf als Goroutine.
func (ws *WebServer) pushLiveState(mode string) {
	if ws.cfg.ServerURL == "" {
		return
	}
	// Zehntelwertung nur senden wenn die aktuelle Disziplin sie verwendet.
	// ws.decimalScoring wird in BroadcastStatus gesetzt (unter ws.mu) –
	// kein externer Lock noetig, da pushLiveState immer unter ws.mu laeuft.
	var rings int
	var decimal float64
	var wertungCount int
	for _, s := range ws.history {
		if s.Mode == ModeWertung && !s.Overcount && !s.Rejected {
			wertungCount++
			rings += s.Ring
			if ws.decimalScoring {
				decimal += s.Decimal
			}
		}
	}
	type payload struct {
		Mode         string  `json:"mode"`
		WertungCount int     `json:"wertung_count"`
		TotalRings   int     `json:"total_rings"`
		TotalDecimal float64 `json:"total_decimal"`
		StandPCURL   string  `json:"standpc_url,omitempty"`
	}
	data, _ := json.Marshal(payload{mode, wertungCount, rings, decimal, ws.announceURL})
	url := fmt.Sprintf("%s/api/lanes/%d/livestate",
		ws.cfg.ServerURL, ws.cfg.LaneNo)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
}

func (ws *WebServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (ws *WebServer) handleShots(w http.ResponseWriter, r *http.Request) {
	ws.mu.Lock()
	data, _ := json.Marshal(ws.history)
	ws.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (ws *WebServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE nicht unterstuetzt", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan sseMsg, 16)
	ws.mu.Lock()
	ws.clients[ch] = struct{}{}
	ws.mu.Unlock()
	defer func() {
		ws.mu.Lock()
		delete(ws.clients, ch)
		ws.mu.Unlock()
	}()

	// Initialen Status sofort senden, damit der Browser nach (Re-)Connect den
	// aktuellen Modus kennt, ohne auf das naechste Event warten zu muessen.
	if ws.sessions != nil {
		if data, err := json.Marshal(ws.sessions.Status()); err == nil {
			fmt.Fprintf(w, "event: status\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}

	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case msg := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.event, msg.data)
			flusher.Flush()
		}
	}
}

func (ws *WebServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if ws.sessions == nil {
		http.Error(w, "nicht bereit", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.sessions.Status())
}

func (ws *WebServer) handleSetMode(w http.ResponseWriter, r *http.Request) {
	if ws.sessions == nil {
		http.Error(w, "nicht bereit", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "ungueltige Anfrage", http.StatusBadRequest)
		return
	}
	if body.Mode != ModeProbe && body.Mode != ModeWertung {
		http.Error(w, "mode muss 'probe' oder 'wertung' sein", http.StatusBadRequest)
		return
	}
	if !ws.sessions.SetMode(body.Mode) {
		http.Error(w, "Zurueckschalten nicht moeglich: Wertungsschuesse bereits abgegeben",
			http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.sessions.Status())
}

// RestoreHistory setzt den Sitzungsverlauf nach einem Neustart wieder ein.
// Muss OHNE ws.mu aufgerufen werden.
func (ws *WebServer) RestoreHistory(shots []*Shot) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.history = shots
	ws.historyGen++ // neuer gen-Wert, damit kein "alter" Broadcast die History überschreibt
}

// ResetHistory leert den Sitzungsverlauf (Sessionwechsel oder Moduswechsel).
// Inkrementiert historyGen damit nachfolgende Broadcast-Aufrufe den Schuss
// der den Reset ausgeloest hat nicht mehr in die neue History schreiben.
func (ws *WebServer) ResetHistory() {
	ws.mu.Lock()
	ws.history = nil
	ws.historyGen++
	ws.mu.Unlock()
}

// HistoryGen liefert die aktuelle History-Generation (vor dem naechsten ResetHistory-Aufruf).
func (ws *WebServer) HistoryGen() int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.historyGen
}

func (ws *WebServer) handleDisciplines(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.disciplines)
}

func (ws *WebServer) handleTargets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.targets)
}

func (ws *WebServer) handleDisplay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.cfg.ShotDisplay)
}

func (ws *WebServer) handleSetDiscipline(w http.ResponseWriter, r *http.Request) {
	if ws.sessions == nil {
		http.Error(w, "nicht bereit", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "ungueltige Anfrage", http.StatusBadRequest)
		return
	}
	def := ws.sessions.LookupDiscipline(body.Name)
	if def == nil {
		http.Error(w, "Disziplin nicht gefunden", http.StatusNotFound)
		return
	}
	ws.sessions.SetDiscipline(def)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.sessions.Status())
}

func (ws *WebServer) handleLocalSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := ListLocalSessions(ws.cfg.ShotLogDir, ws.cfg.LaneNo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if sessions == nil {
		sessions = []LocalSession{}
	}
	json.NewEncoder(w).Encode(sessions)
}

func (ws *WebServer) handleLocalSessionShots(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	shots := LoadShotsForSession(ws.cfg.ShotLogDir, ws.cfg.LaneNo, id)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if shots == nil {
		shots = []*Shot{}
	}
	json.NewEncoder(w).Encode(shots)
}

func (ws *WebServer) handlePutDisciplinesConfig(w http.ResponseWriter, r *http.Request) {
	var cfg DisciplinesConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "ungültiger Body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(cfg.Disciplines) == 0 {
		http.Error(w, "mindestens eine Disziplin erforderlich", http.StatusBadRequest)
		return
	}

	// Datei schreiben (wenn konfiguriert)
	if ws.cfg.DisciplinesFile != "" {
		data, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(ws.cfg.DisciplinesFile, data, 0o644); err != nil {
			http.Error(w, "Datei schreiben: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Laufende Disziplinliste neu laden
	if ws.sessions != nil {
		ws.sessions.ReloadDisciplines(cfg.Disciplines)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]any{
		"written":  ws.cfg.DisciplinesFile != "",
		"reloaded": ws.sessions != nil,
		"count":    len(cfg.Disciplines),
		"default":  cfg.Default,
	})
}
