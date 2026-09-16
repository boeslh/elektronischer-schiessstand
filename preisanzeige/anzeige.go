// ============================================================================
// anzeige.go – zentraler Einstiegspunkt fuer die konfigurierbaren
// Anzeigen-Slots (siehe server/anzeige_config.go, Tabelle anzeige_config).
// EINE Route /anzeige/{id} fuer ALLE Anzeige-Typen - welcher Inhalt
// tatsaechlich gerendert wird, entscheidet das "type"-Feld der geladenen
// Konfiguration. Eine ungueltige oder unbekannte ID liefert immer Slot 1
// aus (nie 404) - ein an eine feste URL gebundener Bildschirm zeigt so
// immer etwas Sinnvolles, auch wenn er (noch) nicht konfiguriert wurde.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

type anzeigeSlot struct {
	ID     int
	Type   string
	Name   string
	Params json.RawMessage
}

func registerAnzeigeRoutes(mux *http.ServeMux, pool *pgxpool.Pool) {
	h := &anzeigeHandler{pool: pool}
	mux.HandleFunc("GET /anzeige/{id}", h.handleAnzeige)
}

type anzeigeHandler struct {
	pool *pgxpool.Pool
}

// loadAnzeigeSlot laedt die Konfiguration zur ID - bei ungueltiger ID oder
// fehlender Zeile wird Slot 1 geladen (siehe Dateikopf). Slot 1 existiert
// serverseitig immer (server/anzeige_config.go DeleteAnzeigeSlot legt ihn
// beim Loeschen sofort wieder an); existiert er hier trotzdem ausnahmsweise
// nicht (DB direkt manipuliert o.ae.), liefert ein eingebauter Fallback-Wert
// dieselbe Auto-Standanzeige.
func loadAnzeigeSlot(ctx context.Context, pool *pgxpool.Pool, idStr string) anzeigeSlot {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		id = 1
	}
	slot, ok := queryAnzeigeSlot(ctx, pool, id)
	if ok {
		return slot
	}
	if id != 1 {
		if slot, ok := queryAnzeigeSlot(ctx, pool, 1); ok {
			return slot
		}
	}
	return anzeigeSlot{ID: 1, Type: "stand", Name: "Standard", Params: json.RawMessage(`{"grid_size":"auto","lane_nos":[]}`)}
}

func queryAnzeigeSlot(ctx context.Context, pool *pgxpool.Pool, id int) (anzeigeSlot, bool) {
	var s anzeigeSlot
	err := pool.QueryRow(ctx, `SELECT id, type, name, params FROM anzeige_config WHERE id=$1`, id).Scan(&s.ID, &s.Type, &s.Name, &s.Params)
	return s, err == nil
}

func (h *anzeigeHandler) handleAnzeige(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slot := loadAnzeigeSlot(ctx, h.pool, r.PathValue("id"))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	switch slot.Type {
	case "stand":
		renderStandanzeige(w, ctx, h.pool, slot)
	case "runde":
		renderRundenanzeige(w, ctx, h.pool, slot)
	case "preisschiessen":
		renderPreisschiessenAnzeigeSlot(w, ctx, h.pool, slot)
	default:
		fmt.Fprintf(w, `<!DOCTYPE html><html lang="de"><head><meta charset="UTF-8">
<title>Anzeige %d</title></head><body style="background:#12161b;color:#75879a;
font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh">
Anzeige-Typ "%s" ist noch nicht verfügbar.</body></html>`, slot.ID, html.EscapeString(slot.Type))
	}
}
