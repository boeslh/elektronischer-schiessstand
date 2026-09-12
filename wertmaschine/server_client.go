// ============================================================================
// server_client.go – HTTP-Client fuer die zentrale server-API. Signiert jede
// Anfrage per HMAC (siehe server/wertmaschine_auth.go) - gleiches Schema wie
// ESP32<->StandPC (standpc/devicelink.go), hier als Header statt als
// Zeilen-Praefix (siehe Konzept .claude/plans/wise-scribbling-abelson.md
// Abschnitt 3.1).
// ============================================================================
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ManualShotInput spiegelt server/manual_result.go ManualShotInput 1:1
// (JSON-Vertrag zwischen wertmaschine und server).
type ManualShotInput struct {
	Ring     *int     `json:"ring,omitempty"`
	Decimal  *float64 `json:"decimal,omitempty"`
	Teiler   *float64 `json:"teiler,omitempty"`
	Winkel   *float64 `json:"winkel,omitempty"`
	Count    int      `json:"count"`
	SeriesNo *int     `json:"series_no,omitempty"`
}

type ServerClient struct {
	baseURL string
	psk     []byte
	http    *http.Client
}

func NewServerClient(baseURL, pskHex string) (*ServerClient, error) {
	psk, err := hex.DecodeString(pskHex)
	if err != nil {
		return nil, fmt.Errorf("server_psk_hex ungueltig (muss Hex sein): %w", err)
	}
	return &ServerClient{baseURL: baseURL, psk: psk, http: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (c *ServerClient) authTag(body []byte) string {
	mac := hmac.New(sha256.New, c.psk)
	mac.Write(body)
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

func (c *ServerClient) do(method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wertmaschine-Auth", c.authTag(body))
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Server nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("Server-Fehler (%d): %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// FetchConfig loest ueber genau EINEN der drei Query-Parameter die Disziplin
// auf (discipline_id direkt, starter_id, oder preisschiessen_id+serial) und
// liefert den fertigen Disag-Konfigurationsstring.
func (c *ServerClient) FetchConfig(protocol string, params url.Values) (configString, disciplineID string, err error) {
	params.Set("protocol", protocol)
	respBody, err := c.do(http.MethodGet, "/api/wertmaschine/config?"+params.Encode(), nil)
	if err != nil {
		return "", "", err
	}
	var res struct {
		ConfigString string `json:"config_string"`
		DisciplineID string `json:"discipline_id"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return "", "", fmt.Errorf("Antwort nicht lesbar: %w", err)
	}
	return res.ConfigString, res.DisciplineID, nil
}

// ScheibeLookupResult spiegelt server/manual_result.go ScheibeLookupResult.
type ScheibeLookupResult struct {
	ScheibeName string `json:"scheibe_name"`
	ShooterName string `json:"shooter_name"`
}

// LookupScheibe loest eine physische Seriennummer auf Scheiben-/Schuetzen-
// Name auf - fuer die Live-Anzeige neben dem Seriennummer-Feld.
func (c *ServerClient) LookupScheibe(preisschiessenID, physicalSerial string) (ScheibeLookupResult, error) {
	var out ScheibeLookupResult
	params := url.Values{"preisschiessen_id": {preisschiessenID}, "physical_serial_no": {physicalSerial}}
	respBody, err := c.do(http.MethodGet, "/api/wertmaschine/scheibe-lookup?"+params.Encode(), nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return out, fmt.Errorf("Antwort nicht lesbar: %w", err)
	}
	return out, nil
}

// GetDevMode fragt den globalen Entwicklermodus-Schalter ab (siehe
// server/settings.go - trotz des Namens "standpc-dev-mode" ein
// anlagenweiter Schalter, hier fuer den Rohdaten-Debug-Mitschnitt genutzt).
// Unauthentifizierter Aufruf (wie bei standpc), die HMAC-Signatur wird vom
// Server fuer diesen Endpunkt nicht geprueft, schadet aber auch nicht.
func (c *ServerClient) GetDevMode() (bool, error) {
	respBody, err := c.do(http.MethodGet, "/api/settings/standpc-dev-mode", nil)
	if err != nil {
		return false, err
	}
	var res struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return false, fmt.Errorf("Antwort nicht lesbar: %w", err)
	}
	return res.Enabled, nil
}

// PreisschiessenOption: ein Eintrag der Preisschiessen-Auswahl in der
// Bedienoberflaeche (Auswahl nach Namen statt roher ID).
type PreisschiessenOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListPreisschiessen liefert die aktiven Preisschiessen fuer die Auswahl in
// der Bedienoberflaeche (web/index.html).
func (c *ServerClient) ListPreisschiessen() ([]PreisschiessenOption, error) {
	respBody, err := c.do(http.MethodGet, "/api/wertmaschine/preisschiessen-liste", nil)
	if err != nil {
		return nil, err
	}
	var out []PreisschiessenOption
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("Antwort nicht lesbar: %w", err)
	}
	return out, nil
}

type submitResult struct {
	SessionID string `json:"session_id"`
}

func (c *ServerClient) SubmitPreisschiessenScheibe(preisschiessenID, physicalSerialNo, granularity string,
	shots []ManualShotInput, confirmOverwrite bool) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"preisschiessen_id":  preisschiessenID,
		"physical_serial_no": physicalSerialNo,
		"granularity":        granularity,
		"shots":              shots,
		"confirm_overwrite":  confirmOverwrite,
	})
	respBody, err := c.do(http.MethodPost, "/api/wertmaschine/preisschiessen-scheibe", body)
	if err != nil {
		return "", err
	}
	var res submitResult
	json.Unmarshal(respBody, &res)
	return res.SessionID, nil
}

func (c *ServerClient) SubmitRundenwettkampf(starterID, granularity string,
	shots []ManualShotInput, confirmOverwrite bool) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"starter_id":        starterID,
		"granularity":       granularity,
		"shots":             shots,
		"confirm_overwrite": confirmOverwrite,
	})
	respBody, err := c.do(http.MethodPost, "/api/wertmaschine/rundenwettkampf", body)
	if err != nil {
		return "", err
	}
	var res submitResult
	json.Unmarshal(respBody, &res)
	return res.SessionID, nil
}
