// ============================================================================
// preisschiessen_gewinne.go – Gewinne (Geldbeträge/Sachpreise) je
// Auswertungsliste und Platz
//
// Eine "Auswertungsliste" ist entweder eine Teilnehmer-Wertung
// (ps_wertungen, siehe preisschiessen_wertungen.go) oder eine der drei
// festen Vereins-Auswertungen (siehe preisschiessen_vereine.go) - beim
// Speichern wird immer die komplette Platz-Liste EINER Auswertungsliste auf
// einmal ersetzt (Store.SetGewinneForListe), das Frontend zeigt/bearbeitet
// jeweils eine Liste zur Zeit (Tab "Gewinne").
// ============================================================================
package main

import (
	"context"
	"fmt"
	"net/http"
)

// vereinTypLabels: Anzeige-Namen der drei festen Vereins-Auswertungen für
// den Gewinne-Tab (dieselben Schlüssel wie ps_verein_teilnahme-Auswertung).
var vereinTypLabels = map[string]string{
	"anzahl":  "Vereine – Teilnehmer Anzahl",
	"prozent": "Vereine – Teilnehmer Prozent",
	"punkte":  "Vereine – Teilnehmer Punkte",
}

func validVereinTyp(t string) bool {
	_, ok := vereinTypLabels[t]
	return ok
}

type PSGewinnZeile struct {
	ID        string   `json:"id"`
	WertungID *string  `json:"wertung_id"`
	VereinTyp *string  `json:"verein_typ"`
	Platz     int      `json:"platz"`
	Betrag    *float64 `json:"betrag"`
	Sachpreis *string  `json:"sachpreis"`
}

// PSGewinnZeileInput: Eingabe beim Speichern - Liste (wertung_id/verein_typ)
// wird für den ganzen Aufruf einmal angegeben, nicht je Zeile.
type PSGewinnZeileInput struct {
	Platz     int      `json:"platz"`
	Betrag    *float64 `json:"betrag"`
	Sachpreis *string  `json:"sachpreis"`
}

func (s *Store) ListGewinne(ctx context.Context, preisschiessenID string) ([]PSGewinnZeile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, wertung_id::text, verein_typ, platz, betrag, sachpreis
		FROM ps_gewinne WHERE preisschiessen_id=$1
		ORDER BY COALESCE(wertung_id::text, verein_typ), platz`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PSGewinnZeile{}
	for rows.Next() {
		var z PSGewinnZeile
		if err := rows.Scan(&z.ID, &z.WertungID, &z.VereinTyp, &z.Platz, &z.Betrag, &z.Sachpreis); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// SetGewinneForListe ersetzt komplett die Platz-Gewinne EINER Auswertungsliste
// (DELETE+INSERT, wie z.B. Store.SetVereinZeitraeume in preisschiessen_vereine.go).
// Genau eines von wertungID/vereinTyp muss gesetzt sein (leerer String = nicht
// gesetzt), sonst Fehler.
func (s *Store) SetGewinneForListe(ctx context.Context, preisschiessenID, wertungID, vereinTyp string, zeilen []PSGewinnZeileInput) error {
	if (wertungID == "") == (vereinTyp == "") {
		return errBadRequest("genau eine Auswertungsliste (Wertung oder Vereins-Auswertung) muss angegeben sein")
	}
	if vereinTyp != "" && !validVereinTyp(vereinTyp) {
		return errBadRequest("unbekannte Vereins-Auswertung")
	}

	seen := map[int]bool{}
	for _, z := range zeilen {
		if z.Platz < 1 {
			return errBadRequest("Platz muss mindestens 1 sein")
		}
		if seen[z.Platz] {
			return errBadRequest(fmt.Sprintf("Platz %d ist doppelt", z.Platz))
		}
		seen[z.Platz] = true
		hasSachpreis := z.Sachpreis != nil && *z.Sachpreis != ""
		if z.Betrag == nil && !hasSachpreis {
			return errBadRequest(fmt.Sprintf("Platz %d: Betrag oder Sachpreis angeben", z.Platz))
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		DELETE FROM ps_gewinne WHERE preisschiessen_id=$1
		  AND wertung_id IS NOT DISTINCT FROM NULLIF($2,'')::uuid
		  AND verein_typ IS NOT DISTINCT FROM NULLIF($3,'')`,
		preisschiessenID, wertungID, vereinTyp); err != nil {
		return err
	}
	for _, z := range zeilen {
		var sachpreis *string
		if z.Sachpreis != nil && *z.Sachpreis != "" {
			sachpreis = z.Sachpreis
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_gewinne (preisschiessen_id, wertung_id, verein_typ, platz, betrag, sachpreis)
			VALUES ($1, NULLIF($2,'')::uuid, NULLIF($3,''), $4, $5, $6)`,
			preisschiessenID, wertungID, vereinTyp, z.Platz, z.Betrag, sachpreis); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ----------------------------------------------------------------------------
// HTTP-Handler
// ----------------------------------------------------------------------------

func (a *APIServer) listGewinne(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListGewinne(r.Context(), r.PathValue("id"))
}

func (a *APIServer) setGewinne(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		WertungID string               `json:"wertung_id"`
		VereinTyp string               `json:"verein_typ"`
		Zeilen    []PSGewinnZeileInput `json:"zeilen"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body")
	}
	if err := a.store.SetGewinneForListe(r.Context(), r.PathValue("id"), body.WertungID, body.VereinTyp, body.Zeilen); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
