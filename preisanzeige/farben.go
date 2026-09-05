// ============================================================================
// farben.go – Hintergrund-/Schrift-/Tabellenfarben aus ps_anzeige_config
// (siehe server/migrations/041_preisschiessen_anzeige_farben.sql), gemeinsam
// genutzt von der browsbaren Ergebnis-Website (site.go, renderLayout) und
// dem Kiosk-Modus (display.go, renderPage) - über dieselben Werte lassen
// sich beide Ansichten aneinander angleichen.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
)

type farbenConfig struct {
	Bg      string
	Text    string
	RowEven string
	RowOdd  string
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// sanitizeHexColor lässt nur ein striktes #rrggbb durch - die Werte landen
// unescaped in einem <style>-Block, ein ungültiger Wert aus der DB (sollte
// wegen der Validierung beim Speichern im Hauptserver nicht vorkommen, siehe
// server/preisschiessen_wertungen.go SetAnzeigeConfig) darf trotzdem nie zu
// injizierbarem CSS führen.
func sanitizeHexColor(c, fallback string) string {
	if hexColorRE.MatchString(c) {
		return c
	}
	return fallback
}

// loadFarbenConfig liest die Farbkonfiguration eines Preisschießens, mit den
// Defaults aus der Migration (Cyan-Theme der bisherigen Ergebnis-Website),
// falls noch keine ps_anzeige_config-Zeile existiert.
func loadFarbenConfig(ctx context.Context, pool *pgxpool.Pool, preisschiessenID string) farbenConfig {
	f := farbenConfig{Bg: "#80ffff", Text: "#000000", RowEven: "#eafeff", RowOdd: "#cbf5f8"}
	var bg, text, rowEven, rowOdd string
	err := pool.QueryRow(ctx, `
		SELECT bg_color, text_color, row_even_color, row_odd_color
		FROM ps_anzeige_config WHERE preisschiessen_id=$1`, preisschiessenID,
	).Scan(&bg, &text, &rowEven, &rowOdd)
	if err != nil {
		return f
	}
	f.Bg = sanitizeHexColor(bg, f.Bg)
	f.Text = sanitizeHexColor(text, f.Text)
	f.RowEven = sanitizeHexColor(rowEven, f.RowEven)
	f.RowOdd = sanitizeHexColor(rowOdd, f.RowOdd)
	return f
}

// colorOverrideCSS: Override-Block für die browsbare Ergebnis-Website
// (site.go) - wird NACH siteCSS im selben <style> ausgegeben, damit die
// gleichspezifischen Regeln greifen. Betrifft Seitenhintergrund/-schrift
// sowie alle Tabellen (table.result: Ergebnislisten; table.liste: die
// Listen-Navigation in der Sidebar) - die Hervorhebung der aktiven Zeile
// (tr.active) wird am Ende erneut gesetzt, damit sie trotz gleicher
// Selektor-Spezifität nicht von der Zeilenfarbe überschrieben wird.
func colorOverrideCSS(f farbenConfig) string {
	return fmt.Sprintf(`
body{background-color:%s;color:%s}
table.result tr:nth-child(odd) td{background:%s}
table.result tr:nth-child(even) td{background:%s}
table.liste tr:nth-child(odd) td{background:%s}
table.liste tr:nth-child(even) td{background:%s}
table.liste tr.active td{background:#fff59d}
`, f.Bg, f.Text, f.RowOdd, f.RowEven, f.RowOdd, f.RowEven)
}
