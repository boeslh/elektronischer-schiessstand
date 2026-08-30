// ============================================================================
// settings.go – globale, app-weite Einstellungen (Singleton-Zeile
// app_settings, siehe migrations/027_standpc_dev_mode.sql). Enthaelt den
// Entwicklermodus fuer Stand-PCs (Schuesse per Mausklick) und die
// konfigurierbaren Schriftgroessen der Stand-PC-Anzeige (028_standpc_font_sizes.sql).
// ============================================================================
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Store) GetStandpcDevMode(ctx context.Context) (bool, error) {
	var v bool
	err := s.pool.QueryRow(ctx,
		`SELECT standpc_dev_mode FROM app_settings WHERE id=1`).Scan(&v)
	return v, err
}

func (s *Store) SetStandpcDevMode(ctx context.Context, enabled bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE app_settings SET standpc_dev_mode=$1 WHERE id=1`, enabled)
	return err
}

func (a *APIServer) getStandpcDevMode(w http.ResponseWriter, r *http.Request) (any, error) {
	enabled, err := a.store.GetStandpcDevMode(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]bool{"enabled": enabled}, nil
}

func (a *APIServer) setStandpcDevMode(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Enabled bool `json:"enabled"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetStandpcDevMode(r.Context(), body.Enabled); err != nil {
		return nil, err
	}
	return map[string]bool{"enabled": body.Enabled}, nil
}

// ----------------------------------------------------------------------------
// Schriftgroessen der Stand-PC-Anzeige (Name/Scheibe/Status/Menue/
// Menue-Preisschiessen/Ergebnisse). Werden hier nur gespeichert - der Push an
// die Staende (mit lokaler Persistenz dort fuer den Offline-Betrieb) passiert
// separat ueber pushFontSizes/PUT {standpc_url}/api/font-sizes.
// ----------------------------------------------------------------------------

type FontSizes struct {
	Name       int `json:"name"`
	Scheibe    int `json:"scheibe"`
	Status     int `json:"status"`
	Menu       int `json:"menu"`
	MenuPS     int `json:"menu_ps"`
	Ergebnisse int `json:"ergebnisse"`
}

func (s *Store) GetFontSizes(ctx context.Context) (FontSizes, error) {
	var f FontSizes
	err := s.pool.QueryRow(ctx, `
		SELECT font_name, font_scheibe, font_status, font_menu, font_menu_ps, font_ergebnisse
		FROM app_settings WHERE id=1`,
	).Scan(&f.Name, &f.Scheibe, &f.Status, &f.Menu, &f.MenuPS, &f.Ergebnisse)
	return f, err
}

func (s *Store) SetFontSizes(ctx context.Context, f FontSizes) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE app_settings SET
		  font_name=$1, font_scheibe=$2, font_status=$3,
		  font_menu=$4, font_menu_ps=$5, font_ergebnisse=$6
		WHERE id=1`,
		f.Name, f.Scheibe, f.Status, f.Menu, f.MenuPS, f.Ergebnisse)
	return err
}

func (a *APIServer) getFontSizes(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.GetFontSizes(r.Context())
}

func (a *APIServer) setFontSizes(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[FontSizes](r)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetFontSizes(r.Context(), body); err != nil {
		return nil, err
	}
	return body, nil
}

// pushFontSizes sendet die gespeicherten Schriftgroessen an alle Staende mit
// konfigurierter standpc_url (gilt global, anders als push-disciplines das
// bewusst pro Stand unterschiedliche Disziplinlisten erlaubt). Der Stand-PC
// persistiert den Wert lokal (font_sizes.json), damit er auch ohne
// Serververbindung nach einem Neustart erhalten bleibt.
type fontSizesPushResult struct {
	LaneNo int    `json:"lane_no"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

func (a *APIServer) pushFontSizes(w http.ResponseWriter, r *http.Request) (any, error) {
	fs, err := a.store.GetFontSizes(r.Context())
	if err != nil {
		return nil, err
	}
	lanes, err := a.store.ListLanes(r.Context())
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(fs)
	client := &http.Client{Timeout: 5 * time.Second}

	var results []fontSizesPushResult
	for _, l := range lanes {
		if !l.Active || l.StandPCURL == "" {
			continue
		}
		res := fontSizesPushResult{LaneNo: l.LaneNo}
		req, err := http.NewRequest(http.MethodPut, l.StandPCURL+"/api/font-sizes", bytes.NewReader(data))
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			res.Error = fmt.Sprintf("nicht erreichbar: %v", err)
			results = append(results, res)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		} else {
			res.OK = true
		}
		results = append(results, res)
	}
	return results, nil
}
