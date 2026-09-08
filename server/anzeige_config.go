// ============================================================================
// anzeige_config.go – zentral verwaltete Anzeigen-Slots fuer den
// Display-Server (preisanzeige, /anzeige/{id}). Ein physischer Bildschirm
// wird im Browser einmalig fest auf eine ID eingestellt; WAS dort gezeigt
// wird (Standanzeige, Rundenwettkampf, spaeter Werbung/Info/Tabelle), wird
// hier zentral konfiguriert, ohne dass jemand zur Anzeige gehen muss.
//
// Bewusst "AnzeigeSlot" genannt (nicht "AnzeigeConfig") - die Preisschießen-
// Kiosk-Konfiguration heisst bereits PSAnzeigeConfig/GetAnzeigeConfig
// (preisschiessen_wertungen.go), ein anderes, unabhaengiges System.
//
// Typoffen (params JSONB) statt fester Spalten je Typ, damit spaetere
// Anzeige-Typen ohne neue Migration ergaenzt werden koennen (siehe
// migrations/051_anzeige_config.sql). IDs werden automatisch vergeben
// (kleinste freie Zahl, Luecken werden aufgefuellt, siehe
// NextAnzeigeSlotID) - Slot 1 existiert immer (Fallback-Ziel fuer
// ungueltige/fehlende IDs auf Seiten von preisanzeige), DeleteAnzeigeSlot
// legt ihn beim Loeschen sofort wieder mit Default-Werten an.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

type AnzeigeSlot struct {
	ID        int             `json:"id"`
	Type      string          `json:"type"` // "stand" | "runde" (spaeter weitere)
	Name      string          `json:"name"`
	Params    json.RawMessage `json:"params"`
	UpdatedAt string          `json:"updated_at,omitempty"`
}

var defaultAnzeigeSlot1 = AnzeigeSlot{
	ID: 1, Type: "stand", Name: "Standard",
	Params: json.RawMessage(`{"grid_size":"auto","lane_nos":[]}`),
}

func (s *Store) ListAnzeigeSlots(ctx context.Context) ([]AnzeigeSlot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, type, name, params, updated_at::text
		FROM anzeige_config ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnzeigeSlot{}
	for rows.Next() {
		var c AnzeigeSlot
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.Params, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetAnzeigeSlot(ctx context.Context, id int) (AnzeigeSlot, error) {
	var c AnzeigeSlot
	err := s.pool.QueryRow(ctx, `
		SELECT id, type, name, params, updated_at::text
		FROM anzeige_config WHERE id=$1`, id,
	).Scan(&c.ID, &c.Type, &c.Name, &c.Params, &c.UpdatedAt)
	if err != nil && id == 1 {
		// Slot 1 muss immer existieren (Fallback-Ziel bei ungueltigen IDs) -
		// defensiv nochmal anlegen, falls er aus irgendeinem Grund fehlt
		// (z.B. direkte DB-Manipulation), statt hier einen Fehler zu melden.
		if _, insErr := s.pool.Exec(ctx, `
			INSERT INTO anzeige_config (id, type, name, params) VALUES (1, $1, $2, $3)
			ON CONFLICT (id) DO NOTHING`,
			defaultAnzeigeSlot1.Type, defaultAnzeigeSlot1.Name, defaultAnzeigeSlot1.Params); insErr == nil {
			return defaultAnzeigeSlot1, nil
		}
	}
	return c, err
}

// NextAnzeigeSlotID liefert die kleinste positive Ganzzahl, die noch
// nicht als ID vergeben ist (Luecken durch geloeschte Konfigurationen
// werden zuerst wieder aufgefuellt).
func (s *Store) NextAnzeigeSlotID(ctx context.Context) (int, error) {
	var next int
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MIN(t.id), 1) FROM generate_series(1,
			(SELECT COALESCE(MAX(id), 0) + 1 FROM anzeige_config)) AS t(id)
		WHERE NOT EXISTS (SELECT 1 FROM anzeige_config a WHERE a.id = t.id)`,
	).Scan(&next)
	return next, err
}

func (s *Store) CreateAnzeigeSlot(ctx context.Context, c AnzeigeSlot) (int, error) {
	id, err := s.NextAnzeigeSlotID(ctx)
	if err != nil {
		return 0, err
	}
	params := c.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO anzeige_config (id, type, name, params) VALUES ($1, $2, $3, $4)`,
		id, c.Type, c.Name, params)
	return id, err
}

func (s *Store) UpdateAnzeigeSlot(ctx context.Context, c AnzeigeSlot) error {
	params := c.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE anzeige_config SET type=$1, name=$2, params=$3, updated_at=now()
		WHERE id=$4`, c.Type, c.Name, params, c.ID)
	return err
}

// DeleteAnzeigeSlot loescht eine Konfiguration - wird Slot 1 geloescht,
// legt es ihn sofort wieder mit den Default-Werten an (Slot 1 darf nie
// dauerhaft fehlen, siehe Dateikopf).
func (s *Store) DeleteAnzeigeSlot(ctx context.Context, id int) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM anzeige_config WHERE id=$1`, id); err != nil {
		return err
	}
	if id == 1 {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO anzeige_config (id, type, name, params) VALUES (1, $1, $2, $3)`,
			defaultAnzeigeSlot1.Type, defaultAnzeigeSlot1.Name, defaultAnzeigeSlot1.Params)
		return err
	}
	return nil
}

// ----------------------------------------------------------------------------
// HTTP-Handler
// ----------------------------------------------------------------------------

func (a *APIServer) listAnzeigeConfigs(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListAnzeigeSlots(r.Context())
}

func (a *APIServer) createAnzeigeConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[AnzeigeSlot](r)
	if err != nil || body.Type == "" {
		return nil, errBadRequest("type erforderlich")
	}
	id, err := a.store.CreateAnzeigeSlot(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]int{"id": id}, nil
}

func (a *APIServer) updateAnzeigeConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[AnzeigeSlot](r)
	if err != nil || body.Type == "" {
		return nil, errBadRequest("type erforderlich")
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return nil, errBadRequest("ungueltige ID")
	}
	body.ID = id
	if err := a.store.UpdateAnzeigeSlot(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func (a *APIServer) deleteAnzeigeConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return nil, errBadRequest("ungueltige ID")
	}
	if err := a.store.DeleteAnzeigeSlot(r.Context(), id); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
