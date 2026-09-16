// ============================================================================
// preisschiessen_anzeige.go – Anzeigen-Slot vom Typ "preisschiessen"
// (server/anzeige_config.go, Tabelle anzeige_config): zeigt für einen
// zentral konfigurierbaren Anzeige-Slot (/anzeige/{id}) exakt dieselbe
// Kiosk-Anzeige wie die preisschiessen-eigenen Routen "/ps/{id}/kiosk" bzw.
// "/ps/{id}/kiosk2" (siehe display.go loadDisplay/renderPage) - anstatt
// einen eigenen, nur an eine feste Preisschiessen-ID gebundenen Pfad zu
// merken, kann so ein fest montierter Bildschirm zentral (ohne dort selbst
// etwas umzustellen) auf ein anderes Preisschiessen oder den anderen
// Kiosk-Slot umgeschaltet werden - Auswahl über slot.Params.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type preisschiessenAnzeigeParams struct {
	PreisschiessenID string `json:"preisschiessen_id"`
	Kiosk            int    `json:"kiosk"` // 1 oder 2, siehe loadDisplay itemsColumn
}

// resolvePreisschiessenID löst "aktuell" (siehe site.go handleAktuellRedirect)
// auf das aktuell markierte Preisschießen auf - Komfortoption analog zu
// "/ps/aktuell/kiosk", damit ein Anzeigen-Slot nicht bei jedem neuen
// Preisschießen manuell umgestellt werden muss.
func resolvePreisschiessenID(ctx context.Context, pool *pgxpool.Pool, raw string) (string, error) {
	if raw != "aktuell" {
		return raw, nil
	}
	var id string
	err := pool.QueryRow(ctx, `SELECT id::text FROM preisschiessen WHERE aktuell LIMIT 1`).Scan(&id)
	return id, err
}

func renderPreisschiessenAnzeigeSlot(w http.ResponseWriter, ctx context.Context, pool *pgxpool.Pool, slot anzeigeSlot) {
	var params preisschiessenAnzeigeParams
	_ = json.Unmarshal(slot.Params, &params)

	psID := ""
	if params.PreisschiessenID != "" {
		var err error
		psID, err = resolvePreisschiessenID(ctx, pool, params.PreisschiessenID)
		if err != nil {
			psID = ""
		}
	}
	if psID == "" {
		fmt.Fprintf(w, `<!DOCTYPE html><html lang="de"><head><meta charset="UTF-8">
<title>Anzeige %d</title></head><body style="background:#12161b;color:#75879a;
font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh">
Kein Preisschießen ausgewählt oder aktuell keines als "Aktuell" markiert.</body></html>`, slot.ID)
		return
	}

	itemsColumn := "anzeige_items"
	if params.Kiosk == 2 {
		itemsColumn = "anzeige_items_2"
	}
	cfg, sections, err := loadDisplay(ctx, pool, psID, itemsColumn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderPage(w, cfg, sections)
}
