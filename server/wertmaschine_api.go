// ============================================================================
// wertmaschine_api.go – HTTP-Handler fuer Wertmaschinen- und manuelle
// Ergebniserfassung, siehe Konzept .claude/plans/wise-scribbling-abelson.md
// Abschnitt 3. Registrierung der Routen in api.go Run().
// ============================================================================
package main

import (
	"net/http"
)

// GET /api/wertmaschine/config?discipline_id=...&protocol=rmiv|rmiii
// wertmaschineConfig loest die Disziplin auf drei moeglichen Wegen auf
// (direkt per discipline_id, ueber einen Rundenwettkampf-Starter, oder ueber
// die physische Seriennummer einer Preisschiessen-Scheibe) - der
// wertmaschine-Dienst hat keinen eigenen Datenbankzugriff (siehe Konzept
// .claude/plans/wise-scribbling-abelson.md Abschnitt 4) und muss die
// Zuordnung daher komplett dem Server ueberlassen.
func (a *APIServer) wertmaschineConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	protocol := q.Get("protocol")
	if protocol == "" {
		return nil, errBadRequest("protocol erforderlich")
	}
	disciplineID := q.Get("discipline_id")
	if disciplineID == "" {
		if starterID := q.Get("starter_id"); starterID != "" {
			id, err := a.store.DisciplineIDForStarter(r.Context(), starterID)
			if err != nil {
				return nil, err
			}
			disciplineID = id
		} else if serial := q.Get("physical_serial_no"); serial != "" {
			psID := q.Get("preisschiessen_id")
			if psID == "" {
				return nil, errBadRequest("preisschiessen_id erforderlich")
			}
			unitID, err := a.store.FindKaufScheibeByPhysicalSerial(r.Context(), psID, serial)
			if err != nil {
				return nil, errBadRequest(err.Error())
			}
			id, err := a.store.DisciplineIDForKaufScheibe(r.Context(), unitID)
			if err != nil {
				return nil, err
			}
			disciplineID = id
		} else {
			return nil, errBadRequest("discipline_id, starter_id oder physical_serial_no erforderlich")
		}
	}
	cfg, err := a.store.BuildDisagConfig(r.Context(), disciplineID, protocol)
	if err != nil {
		return nil, err
	}
	return map[string]string{"config_string": cfg, "discipline_id": disciplineID}, nil
}

// GET /api/wertmaschine/preisschiessen-liste
// Liefert die aktiven Preisschiessen (nur id+name) fuer die
// Preisschiessen-Auswahl in der Wertmaschinen-Bedienoberflaeche - dort soll
// nach Namen statt nach roher ID ausgewaehlt werden koennen (der
// wertmaschine-Dienst hat keinen eigenen Datenbankzugriff).
func (a *APIServer) wertmaschinePreisschiessenListe(w http.ResponseWriter, r *http.Request) (any, error) {
	all, err := a.store.ListPreisschiessen(r.Context())
	if err != nil {
		return nil, err
	}
	type option struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := make([]option, 0, len(all))
	for _, p := range all {
		if !p.Active {
			continue
		}
		out = append(out, option{ID: p.ID, Name: p.Name})
	}
	return out, nil
}

// GET /api/wertmaschine/scheibe-lookup?preisschiessen_id=...&physical_serial_no=...
// Liefert Scheiben-/Schuetzen-Name zu einer physischen Seriennummer - fuer
// die Live-Anzeige neben dem Seriennummer-Feld in der
// Wertmaschinen-Bedienoberflaeche (wertmaschine/web/index.html).
func (a *APIServer) wertmaschineScheibeLookup(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	psID := q.Get("preisschiessen_id")
	serial := q.Get("physical_serial_no")
	if psID == "" || serial == "" {
		return nil, errBadRequest("preisschiessen_id und physical_serial_no erforderlich")
	}
	res, err := a.store.LookupScheibeByPhysicalSerial(r.Context(), psID, serial)
	if err != nil {
		return nil, errBadRequest(err.Error())
	}
	return res, nil
}

type manualResultBody struct {
	Granularity      string            `json:"granularity"` // shot | series | total
	Shots            []ManualShotInput `json:"shots"`
	ConfirmOverwrite bool              `json:"confirm_overwrite"`
}

func (b manualResultBody) validate() error {
	switch b.Granularity {
	case "shot", "series", "total":
	default:
		return errBadRequest(`granularity muss "shot", "series" oder "total" sein`)
	}
	if len(b.Shots) == 0 {
		return errBadRequest("shots darf nicht leer sein")
	}
	return nil
}

// POST /api/competitions/{id}/starters/{sid}/manual-result
func (a *APIServer) manualResultStarter(w http.ResponseWriter, r *http.Request) (any, error) {
	starterID := r.PathValue("sid")
	body, err := decodeBody[manualResultBody](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	if err := body.validate(); err != nil {
		return nil, err
	}
	sessionID, err := a.store.RecordRundenwettkampfResult(r.Context(), starterID, body.Granularity,
		body.Shots, "manual", "office", body.ConfirmOverwrite)
	if err != nil {
		return nil, err
	}
	return map[string]string{"session_id": sessionID}, nil
}

// POST /api/preisschiessen/{id}/scheiben-einheiten/{unit}/manual-result
func (a *APIServer) manualResultScheibeEinheit(w http.ResponseWriter, r *http.Request) (any, error) {
	unitID := r.PathValue("unit")
	body, err := decodeBody[manualResultBody](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	if err := body.validate(); err != nil {
		return nil, err
	}
	sessionID, err := a.store.RecordPreisschiessenResult(r.Context(), unitID, body.Granularity,
		body.Shots, "manual", "office", body.ConfirmOverwrite)
	if err != nil {
		return nil, err
	}
	return map[string]string{"session_id": sessionID}, nil
}

// GET /api/preisschiessen/{id}/scheiben-einheiten/by-physical-serial?serial=...
// Live-Dublettencheck beim Scheibenverkauf (server/web/preisschiessen.html) -
// die eigentliche, verbindliche Pruefung ist der Unique Index aus
// migrations/056, das hier ist nur eine schnelle Vorab-Rueckmeldung.
func (a *APIServer) scheibeEinheitByPhysicalSerial(w http.ResponseWriter, r *http.Request) (any, error) {
	preisschiessenID := r.PathValue("id")
	serial := r.URL.Query().Get("serial")
	if serial == "" {
		return map[string]bool{"taken": false}, nil
	}
	_, err := a.store.FindKaufScheibeByPhysicalSerial(r.Context(), preisschiessenID, serial)
	return map[string]bool{"taken": err == nil}, nil
}

type wertmaschineRundenwettkampfBody struct {
	StarterID        string            `json:"starter_id"`
	Granularity      string            `json:"granularity"`
	Shots            []ManualShotInput `json:"shots"`
	ConfirmOverwrite bool              `json:"confirm_overwrite"`
}

// POST /api/wertmaschine/rundenwettkampf
func (a *APIServer) wertmaschineRundenwettkampf(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[wertmaschineRundenwettkampfBody](r)
	if err != nil || body.StarterID == "" {
		return nil, errBadRequest("starter_id erforderlich")
	}
	if err := (manualResultBody{Granularity: body.Granularity, Shots: body.Shots}).validate(); err != nil {
		return nil, err
	}
	sessionID, err := a.store.RecordRundenwettkampfResult(r.Context(), body.StarterID, body.Granularity,
		body.Shots, "wertmaschine", "wertmaschine", body.ConfirmOverwrite)
	if err != nil {
		return nil, err
	}
	return map[string]string{"session_id": sessionID}, nil
}

type wertmaschinePreisschiessenBody struct {
	PreisschiessenID string            `json:"preisschiessen_id"`
	PhysicalSerialNo string            `json:"physical_serial_no"`
	Granularity      string            `json:"granularity"`
	Shots            []ManualShotInput `json:"shots"`
	ConfirmOverwrite bool              `json:"confirm_overwrite"`
}

// POST /api/wertmaschine/preisschiessen-scheibe
func (a *APIServer) wertmaschinePreisschiessenScheibe(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[wertmaschinePreisschiessenBody](r)
	if err != nil || body.PreisschiessenID == "" || body.PhysicalSerialNo == "" {
		return nil, errBadRequest("preisschiessen_id und physical_serial_no erforderlich")
	}
	if err := (manualResultBody{Granularity: body.Granularity, Shots: body.Shots}).validate(); err != nil {
		return nil, err
	}
	unitID, err := a.store.FindKaufScheibeByPhysicalSerial(r.Context(), body.PreisschiessenID, body.PhysicalSerialNo)
	if err != nil {
		return nil, errBadRequest(err.Error())
	}
	sessionID, err := a.store.RecordPreisschiessenResult(r.Context(), unitID, body.Granularity,
		body.Shots, "wertmaschine", "wertmaschine", body.ConfirmOverwrite)
	if err != nil {
		return nil, err
	}
	return map[string]string{"session_id": sessionID}, nil
}
