// ============================================================================
// web.go – lokale Bedienoberflaeche (Browser am Rechner mit der
// Wertmaschine). SSE statt WebSocket, gleiches Muster wie standpc/web.go.
// ============================================================================
package main

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"
)

//go:embed web/index.html
var indexHTML embed.FS

type sseMsg struct {
	event string
	data  []byte
}

type WebServer struct {
	session *Session

	mu      sync.Mutex
	clients map[chan sseMsg]struct{}
}

func NewWebServer() *WebServer {
	return &WebServer{clients: make(map[chan sseMsg]struct{})}
}

func (ws *WebServer) broadcastState() {
	data, err := json.Marshal(ws.session.Status())
	if err != nil {
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for ch := range ws.clients {
		select {
		case ch <- sseMsg{event: "state", data: data}:
		default:
		}
	}
}

// rawMsg: ein einzelnes gesendetes/empfangenes Byte-Paket auf der seriellen
// Schnittstelle - fuer den Entwickler-Debug-Mitschnitt in der
// Bedienoberflaeche (nur aktiv, wenn session.devMode gesetzt ist, siehe
// session.go newMachine/pollDevMode).
type rawMsg struct {
	Dir   string `json:"dir"` // "TX" | "RX"
	Hex   string `json:"hex"`
	ASCII string `json:"ascii"`
	Time  string `json:"time"`
}

// broadcastRaw implementiert disag.RawSink - wird direkt aus der
// Lese-/Schreib-Goroutine des jeweiligen Machine-Treibers aufgerufen (siehe
// disag/serial.go loggingPort), sendet also NICHT synchron mit dem
// Hauptzustand (anders als broadcastState), damit ein langsamer/blockierter
// Browser-Client nie den seriellen Datenverkehr selbst verzoegert (Channel
// mit Puffer + nicht-blockierendem Send, wie broadcastState).
func (ws *WebServer) broadcastRaw(dir string, b []byte) {
	data, err := json.Marshal(rawMsg{
		Dir: dir, Hex: hex.EncodeToString(b), ASCII: fmt.Sprintf("%q", b),
		Time: time.Now().Format("15:04:05.000"),
	})
	if err != nil {
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for ch := range ws.clients {
		select {
		case ch <- sseMsg{event: "raw", data: data}:
		default:
		}
	}
}

func (ws *WebServer) Run(listen string) error {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(indexHTML, "web")
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, _ := fs.ReadFile(sub, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.HandleFunc("GET /events", ws.handleSSE)
	mux.HandleFunc("GET /status", ws.handleStatus)
	mux.HandleFunc("GET /preisschiessen-liste", ws.handlePreisschiessenListe)
	mux.HandleFunc("GET /scheibe-lookup", ws.handleScheibeLookup)
	mux.HandleFunc("POST /start/preisschiessen", ws.handleStartPreisschiessen)
	mux.HandleFunc("POST /start/rundenwettkampf", ws.handleStartRundenwettkampf)
	mux.HandleFunc("POST /correct", ws.handleCorrect)
	mux.HandleFunc("POST /submit", ws.handleSubmit)
	mux.HandleFunc("POST /abort", ws.handleAbort)
	mux.HandleFunc("POST /recover", ws.handleRecover)

	log.Printf("wertmaschine: http://localhost%s", listen)
	return http.ListenAndServe(listen, mux)
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

	if data, err := json.Marshal(ws.session.Status()); err == nil {
		fmt.Fprintf(w, "event: state\ndata: %s\n\n", data)
		flusher.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.event, msg.data)
			flusher.Flush()
		}
	}
}

func (ws *WebServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.session.Status())
}

// handlePreisschiessenListe proxied die aktiven Preisschiessen vom Server
// (per PSK authentifiziert) fuer die Namens-Auswahl im Browser - der
// Browser selbst spricht nie direkt mit dem zentralen Server.
func (ws *WebServer) handlePreisschiessenListe(w http.ResponseWriter, r *http.Request) {
	list, err := ws.session.client.ListPreisschiessen()
	if err != nil {
		writeJSONError(w, err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// handleScheibeLookup proxied die Scheiben-/Schuetzen-Suche nach physischer
// Seriennummer vom Server (per PSK authentifiziert) - fuer die Live-Anzeige
// neben dem Seriennummer-Feld beim Preisschiessen-Modus.
func (ws *WebServer) handleScheibeLookup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := ws.session.client.LookupScheibe(q.Get("preisschiessen_id"), q.Get("physical_serial_no"))
	if err != nil {
		writeJSONError(w, err, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func writeJSONError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (ws *WebServer) handleStartPreisschiessen(w http.ResponseWriter, r *http.Request) {
	var body struct{ PreisschiessenID, PhysicalSerial string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	if err := ws.session.StartPreisschiessen(body.PreisschiessenID, body.PhysicalSerial); err != nil {
		writeJSONError(w, err, http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (ws *WebServer) handleStartRundenwettkampf(w http.ResponseWriter, r *http.Request) {
	var body struct{ StarterID string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	if err := ws.session.StartRundenwettkampf(body.StarterID); err != nil {
		writeJSONError(w, err, http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (ws *WebServer) handleCorrect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index   int
		Ring    int
		Decimal float64
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	if err := ws.session.CorrectShot(body.Index, body.Ring, body.Decimal); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	ws.broadcastState()
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (ws *WebServer) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var body struct{ ConfirmOverwrite bool }
	json.NewDecoder(r.Body).Decode(&body)
	if !ws.session.AllResolved() {
		writeJSONError(w, fmt.Errorf("es gibt noch unbestaetigte/unkorrigierte Schuesse"), http.StatusBadRequest)
		return
	}
	sessionID, err := ws.session.Submit(body.ConfirmOverwrite)
	if err != nil {
		writeJSONError(w, err, http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"session_id": sessionID})
}

func (ws *WebServer) handleAbort(w http.ResponseWriter, r *http.Request) {
	ws.session.Abort()
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleRecover setzt eine blockierte Wertmaschine per Software zurueck
// (siehe session.go Recover-Kommentar) - fuer den Fall, dass das Geraet nach
// einer unklaren Karte nicht mehr reagiert, ohne die laufende Erfassung
// abzubrechen oder das Geraet aus-/einschalten zu muessen.
func (ws *WebServer) handleRecover(w http.ResponseWriter, r *http.Request) {
	if err := ws.session.Recover(); err != nil {
		writeJSONError(w, err, http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
