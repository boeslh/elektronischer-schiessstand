// ============================================================================
// api.go – REST-API der Standsteuerung
//
// Endpunkte:
//
//	Verwaltung (Browser-UI / Aufsicht):
//	  GET  /api/lanes                     Standuebersicht mit Belegung
//	  POST /api/lanes/init {"count":n}    Staende 1..n anlegen
//	  GET  /api/shooters?q=...            Schuetzensuche
//	  POST /api/shooters                  Schuetze anlegen
//	  GET  /api/disciplines               aktive Disziplinen
//	  POST /api/lanes/{no}/assign         Stand belegen {shooter_id?,discipline_id}
//	  POST /api/sessions/{id}/status      {"status":"sighting|match|paused|finished|aborted"}
//	  POST /api/sessions/{id}/shots/{no}/annul  {"actor":"...","reason":"..."}
//	  GET  /api/sessions/{id}/shots       Schussliste
//
//	Stand-PC-Schnittstelle:
//	  GET  /api/lanes/{no}/session        aktive Session + Kalibrierung
//	                                      (null wenn Stand frei)
//	Live:
//	  GET  /events                        SSE: jeder Schuss aller Staende
//
// ============================================================================
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed web
var webFS embed.FS

type APIServer struct {
	store      *Store
	live       *LiveHub
	listen     string
	dsn        string // fuer pg_dump/pg_restore (Import/Export-Kachel, siehe backup.go)
	backupDir  string
	liveStates sync.Map // key=laneNo(int) → LaneLiveState
}

func NewAPIServer(store *Store, live *LiveHub, listen, dsn, backupDir string) *APIServer {
	return &APIServer{store: store, live: live, listen: listen, dsn: dsn, backupDir: backupDir}
}

func serveHTML(fsys fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
}

func (a *APIServer) Run(ctx context.Context) error {
	webSub, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", serveHTML(webSub, "index.html"))
	mux.HandleFunc("GET /lanes", a.serveHTMLGated(webSub, "lanes.html", "lanes"))
	mux.HandleFunc("GET /disciplines", a.serveHTMLGated(webSub, "disciplines.html", "disciplines"))
	mux.HandleFunc("GET /settings", a.serveHTMLGated(webSub, "settings.html", "settings"))
	mux.Handle("GET /settings.js", http.FileServer(http.FS(webSub)))
	mux.Handle("GET /role.js", http.FileServer(http.FS(webSub)))
	mux.HandleFunc("GET /events", a.live.HandleSSE)

	mux.HandleFunc("GET /api/role", a.h(a.getRole))
	mux.HandleFunc("POST /api/role/switch", a.h(a.switchRole))
	mux.HandleFunc("GET /api/roles", a.h(a.listRoles))
	mux.HandleFunc("PUT /api/roles/{key}/tiles", a.h(a.setRoleTiles))
	mux.HandleFunc("PUT /api/roles/{key}/password", a.h(a.setRolePassword))
	mux.HandleFunc("PUT /api/roles/{key}/permissions", a.h(a.setRoleCorrection))
	mux.HandleFunc("PUT /api/roles/{key}/manage-preisschiessen", a.h(a.setRoleManagePreisschiessen))
	mux.HandleFunc("GET /benutzerverwaltung", func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.requireAdmin(w, r); err != nil {
			writeAccessDeniedPage(w, err)
			return
		}
		serveHTML(webSub, "benutzerverwaltung.html")(w, r)
	})

	mux.HandleFunc("GET /api/admin/backups", a.h(a.listBackupsHandler))
	mux.HandleFunc("POST /api/admin/backups", a.h(a.createBackupHandler))
	mux.HandleFunc("GET /api/admin/backups/{filename}/download", a.downloadBackupHandler)
	mux.HandleFunc("PUT /api/admin/backups/{filename}/rename", a.h(a.renameBackupHandler))
	mux.HandleFunc("POST /api/admin/backups/{filename}/restore", a.h(a.restoreBackupHandler))
	mux.HandleFunc("POST /api/admin/restore-upload", a.h(a.restoreUploadHandler))
	mux.HandleFunc("POST /api/admin/backups/upload", a.h(a.uploadBackupHandler))
	mux.HandleFunc("POST /api/admin/export-selection", a.h(a.exportSelectionHandler))
	mux.HandleFunc("POST /api/admin/import-selection", a.h(a.importSelectionHandler))
	mux.HandleFunc("POST /api/admin/delete-selection", a.h(a.deleteSelectionHandler))
	mux.HandleFunc("GET /import-export", func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.requireAdmin(w, r); err != nil {
			writeAccessDeniedPage(w, err)
			return
		}
		serveHTML(webSub, "import-export.html")(w, r)
	})

	mux.HandleFunc("GET /entwicklung", func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.requireAdmin(w, r); err != nil {
			writeAccessDeniedPage(w, err)
			return
		}
		serveHTML(webSub, "entwicklung.html")(w, r)
	})
	mux.HandleFunc("POST /api/admin/testdaten/generate", a.h(a.generateTestdatenHandler))
	mux.HandleFunc("POST /api/admin/testdaten/cleanup", a.h(a.cleanupTestdatenHandler))

	mux.HandleFunc("GET /api/lanes", a.h(a.listLanes))
	mux.HandleFunc("POST /api/lanes/init", a.h(a.initLanes))
	mux.HandleFunc("GET /api/lanes/{no}/session", a.h(a.laneSession))
	mux.HandleFunc("PUT /api/lanes/{no}/livestate", a.h(a.setLiveState))
	mux.HandleFunc("POST /api/lanes/{no}/assign", a.h(a.assignLane))
	mux.HandleFunc("GET /api/lanes/{no}/preisschiessen", a.h(a.getLanePreisschiessenInfo))
	mux.HandleFunc("POST /api/lanes/{no}/preisschiessen/scheibe", a.h(a.postChooseScheibeAtLane))
	mux.HandleFunc("POST /api/lanes/{no}/preisschiessen/buchen", a.h(a.postBuchenAtLane))
	mux.HandleFunc("POST /api/lanes/{no}/preisschiessen/freigeben", a.h(a.postFreeLane))
	mux.HandleFunc("POST /api/lanes/{no}/preisschiessen/scheibe-abschliessen", a.h(a.postScheibeAbschliessenAtLane))
	mux.HandleFunc("GET /stammdaten", a.serveHTMLGated(webSub, "stammdaten.html", "stammdaten"))

	mux.HandleFunc("GET /api/gaue", a.h(a.listGaue))
	mux.HandleFunc("POST /api/gaue", a.h(a.createGau))
	mux.HandleFunc("PUT /api/gaue/{id}", a.h(a.updateGau))
	mux.HandleFunc("DELETE /api/gaue/{id}", a.h(a.deleteGau))

	mux.HandleFunc("GET /api/clubs", a.h(a.listClubs))
	mux.HandleFunc("POST /api/clubs", a.h(a.createClub))
	mux.HandleFunc("PUT /api/clubs/{id}", a.h(a.updateClub))
	mux.HandleFunc("DELETE /api/clubs/{id}", a.h(a.deleteClub))
	mux.HandleFunc("POST /api/clubs/import", a.h(a.importClubs))

	mux.HandleFunc("GET /api/shooter-classes", a.h(a.listShooterClasses))
	mux.HandleFunc("POST /api/shooter-classes", a.h(a.createShooterClass))
	mux.HandleFunc("PUT /api/shooter-classes/{id}", a.h(a.updateShooterClass))
	mux.HandleFunc("DELETE /api/shooter-classes/{id}", a.h(a.deleteShooterClass))
	mux.HandleFunc("POST /api/shooter-classes/import", a.h(a.importShooterClasses))

	mux.HandleFunc("GET /api/shooters", a.h(a.listShooters))
	mux.HandleFunc("GET /api/shooters/list", a.h(a.listShootersFull))
	mux.HandleFunc("POST /api/shooters", a.h(a.createShooter))
	mux.HandleFunc("POST /api/shooters/full", a.h(a.createShooterFull))
	mux.HandleFunc("POST /api/shooters/import", a.h(a.importMembers))
	mux.HandleFunc("PUT /api/shooters/{id}", a.h(a.updateShooterFull))
	mux.HandleFunc("DELETE /api/shooters/{id}", a.h(a.deleteShooter))
	mux.HandleFunc("POST /api/shooters/recalculate-classes", a.h(a.recalculateSportsClasses))

	mux.HandleFunc("GET /api/teams", a.h(a.listTeams))
	mux.HandleFunc("POST /api/teams", a.h(a.createTeam))
	mux.HandleFunc("PUT /api/teams/{id}", a.h(a.updateTeam))
	mux.HandleFunc("DELETE /api/teams/{id}", a.h(a.deleteTeam))
	mux.HandleFunc("GET /api/teams/{id}/members", a.h(a.listTeamMembers))
	mux.HandleFunc("POST /api/teams/{id}/members", a.h(a.addTeamMember))
	mux.HandleFunc("DELETE /api/teams/{id}/members/{shooterID}", a.h(a.removeTeamMember))

	mux.HandleFunc("GET /wettkampf", a.serveHTMLGated(webSub, "wettkampf.html", "wettkampf"))
	mux.HandleFunc("GET /wettkampf-bearbeiten", a.serveHTMLGated(webSub, "wettkampf-bearbeiten.html", "wettkampf"))
	mux.HandleFunc("GET /ergebnisse", a.serveHTMLGated(webSub, "ergebnisse.html", "ergebnisse"))
	mux.HandleFunc("GET /ergebnis-ansicht", a.serveHTMLGated(webSub, "ergebnis-ansicht.html", "ergebnisse"))
	mux.HandleFunc("GET /auswertung", a.serveHTMLGated(webSub, "auswertung.html", "auswertung"))
	mux.HandleFunc("GET /api/auswertung/rundenwettkampf", a.h(a.listRundenwettkampfResults))
	mux.HandleFunc("GET /api/auswertung/gruppenwettkampf", a.h(a.listGruppenwettkampfData))
	mux.HandleFunc("GET /api/auswertungen", a.h(a.listSavedAuswertungen))
	mux.HandleFunc("POST /api/auswertungen", a.h(a.createSavedAuswertung))
	mux.HandleFunc("DELETE /api/auswertungen/{id}", a.h(a.deleteSavedAuswertung))
	mux.HandleFunc("GET /api/results", a.h(a.listResults))
	mux.HandleFunc("GET /api/competitions", a.h(a.listCompetitions))
	mux.HandleFunc("GET /api/competitions/{id}", a.h(a.getCompetition))
	mux.HandleFunc("POST /api/competitions", a.h(a.createCompetition))
	mux.HandleFunc("PUT /api/competitions/{id}", a.h(a.updateCompetition))
	mux.HandleFunc("DELETE /api/competitions/{id}", a.h(a.deleteCompetition))
	mux.HandleFunc("PUT /api/competitions/{id}/status", a.h(a.setCompetitionStatus))
	mux.HandleFunc("GET /api/competitions/{id}/participants", a.h(a.listParticipants))
	mux.HandleFunc("POST /api/competitions/{id}/participants", a.h(a.addParticipant))
	mux.HandleFunc("DELETE /api/competitions/{id}/participants/{pid}", a.h(a.removeParticipant))
	mux.HandleFunc("GET /api/competitions/{id}/starters", a.h(a.listStarters))
	mux.HandleFunc("POST /api/competitions/{id}/starters", a.h(a.addStarter))
	mux.HandleFunc("POST /api/competitions/{id}/starters/import-team", a.h(a.importTeamStarters))
	mux.HandleFunc("PUT /api/competitions/{id}/starters/{sid}/role", a.h(a.setStarterRole))
	mux.HandleFunc("DELETE /api/competitions/{id}/starters/{sid}", a.h(a.removeStarter))

	mux.HandleFunc("GET /preisschiessen", a.serveHTMLGated(webSub, "preisschiessen.html", "preisschiessen"))
	mux.HandleFunc("GET /preisschiessen-liste", a.serveHTMLGated(webSub, "preisschiessen-liste.html", "preisschiessen"))
	mux.HandleFunc("GET /preisschiessen-bearbeiten", a.serveHTMLGated(webSub, "preisschiessen-bearbeiten.html", "preisschiessen"))
	mux.HandleFunc("GET /api/preisschiessen", a.h(a.listPreisschiessen))
	mux.HandleFunc("POST /api/preisschiessen", a.h(a.createPreisschiessen))
	mux.HandleFunc("GET /api/preisschiessen/{id}", a.h(a.getPreisschiessen))
	mux.HandleFunc("PUT /api/preisschiessen/{id}", a.h(a.updatePreisschiessen))
	mux.HandleFunc("DELETE /api/preisschiessen/{id}", a.h(a.deletePreisschiessen))
	mux.HandleFunc("POST /api/preisschiessen/{id}/clone", a.h(a.clonePreisschiessen))
	mux.HandleFunc("GET /api/preisschiessen/{id}/scheiben", a.h(a.listScheiben))
	mux.HandleFunc("POST /api/preisschiessen/{id}/scheiben", a.h(a.createScheibe))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/scheiben/{sid}", a.h(a.updateScheibe))
	mux.HandleFunc("DELETE /api/preisschiessen/{id}/scheiben/{sid}", a.h(a.deleteScheibe))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/scheiben/{sid}/classes", a.h(a.setScheibeClasses))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/scheiben/{sid}/requires", a.h(a.setScheibeRequiredSets))
	mux.HandleFunc("GET /api/preisschiessen/{id}/sets", a.h(a.listSets))
	mux.HandleFunc("POST /api/preisschiessen/{id}/sets", a.h(a.createSet))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/sets/{setid}", a.h(a.updateSet))
	mux.HandleFunc("DELETE /api/preisschiessen/{id}/sets/{setid}", a.h(a.deleteSet))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/sets/{setid}/items", a.h(a.setSetItems))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/sets/{setid}/classes", a.h(a.setSetClasses))
	mux.HandleFunc("GET /api/preisschiessen/{id}/teilnehmer", a.h(a.listPSTeilnehmer))
	mux.HandleFunc("POST /api/preisschiessen/{id}/teilnehmer", a.h(a.createPSTeilnehmer))
	mux.HandleFunc("POST /api/preisschiessen/{id}/teilnehmer/recalculate-classes", a.h(a.recalcPSTeilnehmerClasses))
	mux.HandleFunc("GET /api/preisschiessen/{id}/teilnehmer/{tid}", a.h(a.getPSTeilnehmer))
	mux.HandleFunc("GET /api/preisschiessen/{id}/teilnehmer/{tid}/angebot", a.h(a.listAngebot))
	mux.HandleFunc("GET /api/preisschiessen/{id}/teilnehmer/{tid}/konto", a.h(a.listKonto))
	mux.HandleFunc("GET /api/preisschiessen/{id}/teilnehmer/{tid}/scheiben-einheiten", a.h(a.listKaufScheibenEinheiten))
	mux.HandleFunc("POST /api/preisschiessen/{id}/teilnehmer/{tid}/aufladung", a.h(a.buchAufladung))
	mux.HandleFunc("POST /api/preisschiessen/{id}/teilnehmer/{tid}/auszahlung", a.h(a.postAuszahlung))
	mux.HandleFunc("POST /api/preisschiessen/{id}/teilnehmer/{tid}/bezahlen", a.h(a.postBezahlen))
	mux.HandleFunc("POST /api/preisschiessen/{id}/kaeufe/{kid}/ruckgabe", a.h(a.postRuckgabe))
	mux.HandleFunc("POST /api/preisschiessen/{id}/teilnehmer/{tid}/stand", a.h(a.postAssignTeilnehmerLanePending))
	mux.HandleFunc("DELETE /api/preisschiessen/{id}/teilnehmer/{tid}/stand", a.h(a.deleteTeilnehmerLanePending))
	mux.HandleFunc("GET /api/preisschiessen/pending-lanes", a.h(a.listPendingLanes))
	mux.HandleFunc("GET /api/preisschiessen/{id}/lanes-overview", a.h(a.listPSLaneOverview))
	mux.HandleFunc("GET /api/preisschiessen/{id}/auswertung", a.h(a.psAuswertung))

	// Preisschiessen-Auswertung (Meister/Punkt/Adler-Wertungen, siehe
	// preisschiessen_wertungen.go) - eigener Tab "Auswertung" auf der
	// Preisschiessen-Seite, nicht zu verwechseln mit der Kasse-Auswertung
	// oben oder der globalen Auswertung-Kachel (auswertung.html).
	mux.HandleFunc("GET /api/preisschiessen/{id}/wertungen", a.h(a.listWertungen))
	mux.HandleFunc("POST /api/preisschiessen/{id}/wertungen", a.h(a.createWertung))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/wertungen/{wid}", a.h(a.updateWertung))
	mux.HandleFunc("DELETE /api/preisschiessen/{id}/wertungen/{wid}", a.h(a.deleteWertung))
	mux.HandleFunc("GET /api/preisschiessen/{id}/wertungen/{wid}/ergebnis", a.h(a.getWertungErgebnis))
	mux.HandleFunc("GET /api/preisschiessen/{id}/auswertung-status", a.h(a.getAuswertungStatus))
	mux.HandleFunc("POST /api/preisschiessen/{id}/auswertung-recompute", a.h(a.postRecomputeAuswertung))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/auswertung-settings", a.h(a.putAuswertungSettings))
	mux.HandleFunc("GET /api/preisschiessen/{id}/anzeige-config", a.h(a.getAnzeigeConfig))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/anzeige-config", a.h(a.putAnzeigeConfig))

	// Vereins-Auswertungen (Anzahl/Prozent/Punkte je Verein, siehe
	// preisschiessen_vereine.go) - eigener Bereich, unabhängig von den
	// Teilnehmer-Wertungen oben.
	mux.HandleFunc("GET /api/preisschiessen/{id}/vereine/teilnahme", a.h(a.listVereinTeilnahme))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/vereine/teilnahme", a.h(a.setVereinTeilnahme))
	mux.HandleFunc("GET /api/preisschiessen/{id}/vereine/zeitraeume", a.h(a.listVereinZeitraeume))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/vereine/zeitraeume", a.h(a.setVereinZeitraeume))
	mux.HandleFunc("GET /api/preisschiessen/{id}/vereine/auswertung", a.h(a.getVereinsAuswertung))

	// Gewinne (Geldbeträge/Sachpreise) je Auswertungsliste und Platz, siehe
	// preisschiessen_gewinne.go.
	mux.HandleFunc("GET /api/preisschiessen/{id}/gewinne", a.h(a.listGewinne))
	mux.HandleFunc("PUT /api/preisschiessen/{id}/gewinne", a.h(a.setGewinne))

	mux.HandleFunc("GET /api/targets", a.h(a.listTargets))
	mux.HandleFunc("GET /api/disciplines", a.h(a.listDisciplines))
	mux.HandleFunc("GET /api/disciplines/full", a.h(a.listDisciplinesFull))
	mux.HandleFunc("POST /api/disciplines", a.h(a.createDiscipline))
	mux.HandleFunc("PUT /api/disciplines/{id}", a.h(a.updateDiscipline))
	mux.HandleFunc("DELETE /api/disciplines/{id}", a.h(a.deleteDiscipline))
	mux.HandleFunc("POST /api/sessions/{id}/status", a.h(a.setSessionStatus))
	mux.HandleFunc("GET /api/sessions/{id}/shots", a.h(a.sessionShots))
	mux.HandleFunc("POST /api/sessions/{id}/shots/{no}/annul", a.h(a.annulShot))
	mux.HandleFunc("POST /api/sessions/{id}/shots/{no}/correct", a.h(a.correctShot))
	mux.HandleFunc("DELETE /api/sessions/{id}/shots/{no}/correct", a.h(a.revertShotCorrection))
	mux.HandleFunc("POST /api/sessions/{id}/apply-simulation", a.h(a.applySessionSimulation))

	mux.HandleFunc("GET /simulator", a.serveHTMLGated(webSub, "simulator.html", "simulator"))
	mux.HandleFunc("GET /api/sessions/{id}/simulator-shots", a.h(a.simulatorShots))
	mux.HandleFunc("GET /api/sessions/{id}/target-geometry", a.h(a.sessionTargetGeometry))
	mux.HandleFunc("POST /api/sessions/{id}/simulate", a.h(a.simulateSession))
	mux.HandleFunc("POST /api/sessions/{id}/calibrate", a.h(a.calibrateSession))
	mux.HandleFunc("POST /api/sessions/{id}/shots/{no}/solve-detail", a.h(a.solveShotDetail))
	mux.HandleFunc("GET /api/simulator-configs", a.h(a.listSimulatorConfigs))
	mux.HandleFunc("POST /api/simulator-configs", a.h(a.saveSimulatorConfig))
	mux.HandleFunc("DELETE /api/simulator-configs/{id}", a.h(a.deleteSimulatorConfig))

	mux.HandleFunc("GET /archiv", a.serveHTMLGated(webSub, "archiv.html", "archiv"))
	mux.HandleFunc("GET /api/archive/events", a.h(a.listArchivedEvents))
	mux.HandleFunc("POST /api/archive/export", a.archiveExport)
	mux.HandleFunc("POST /api/archive/export-delete", a.archiveExportDelete)
	mux.HandleFunc("POST /api/archive/import", a.archiveImport)

	mux.HandleFunc("GET /standaktion", a.serveHTMLGated(webSub, "standaktion.html", "standaktion"))
	mux.HandleFunc("PUT /api/lanes/{no}/standpc-url", a.h(a.setLaneStandpcURL))
	mux.HandleFunc("GET /api/settings/standpc-dev-mode", a.h(a.getStandpcDevMode))
	mux.HandleFunc("PUT /api/settings/standpc-dev-mode", a.h(a.setStandpcDevMode))
	mux.HandleFunc("GET /api/settings/font-sizes", a.h(a.getFontSizes))
	mux.HandleFunc("PUT /api/settings/font-sizes", a.h(a.setFontSizes))
	mux.HandleFunc("POST /api/settings/font-sizes/push", a.h(a.pushFontSizes))
	mux.HandleFunc("GET /api/lanes/{no}/local-sessions", a.h(a.proxyLocalSessions))
	mux.HandleFunc("GET /api/lanes/{no}/local-sessions/{sid}/shots", a.h(a.proxyLocalSessionShots))
	mux.HandleFunc("POST /api/transfer", a.h(a.transferSession))
	mux.HandleFunc("POST /api/import-session", a.h(a.importSession))
	mux.HandleFunc("POST /api/lanes/{no}/push-disciplines", a.h(a.pushDisciplinesToStandPC))

	srv := &http.Server{Addr: a.listen, Handler: mux}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// h: einheitlicher Handler-Wrapper (JSON-Fehler, Logging)
type handlerFunc func(w http.ResponseWriter, r *http.Request) (any, error)

func (a *APIServer) h(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := fn(w, r)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrLaneBusy) {
				status = http.StatusConflict
			}
			if errors.Is(err, ErrWrongPassword) {
				status = http.StatusUnauthorized
			}
			if errors.Is(err, ErrUnknownRole) {
				status = http.StatusBadRequest
			}
			var he *httpError
			if errors.As(err, &he) {
				status = he.code
			}
			log.Printf("%s %s -> %v", r.Method, r.URL.Path, err)
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(result)
	}
}

func decodeBody[T any](r *http.Request) (T, error) {
	var v T
	err := json.NewDecoder(r.Body).Decode(&v)
	return v, err
}

// ----------------------------------------------------------------------------
// Handler
// ----------------------------------------------------------------------------

func (a *APIServer) listLanes(w http.ResponseWriter, r *http.Request) (any, error) {
	lanes, err := a.store.ListLanes(r.Context())
	if err != nil {
		return nil, err
	}
	for i := range lanes {
		if v, ok := a.liveStates.Load(lanes[i].LaneNo); ok {
			ls := v.(LaneLiveState)
			lanes[i].LiveMode = ls.Mode
			lanes[i].LiveWertungCount = ls.WertungCount
			lanes[i].LiveTotalRings = ls.TotalRings
			lanes[i].LiveTotalDecimal = ls.TotalDecimal
			lanes[i].LiveLastSeen = ls.LastSeenAt.Unix()
		}
	}
	return lanes, nil
}

func (a *APIServer) setLiveState(w http.ResponseWriter, r *http.Request) (any, error) {
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errors.New("ungueltige Standnummer")
	}
	// Erweiterter Body: enthält ggf. standpc_url für Discovery
	var body struct {
		LaneLiveState
		StandPCURL string `json:"standpc_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, err
	}
	body.LastSeenAt = time.Now()

	// Auto-Discovery: URL in DB schreiben wenn vorhanden und geändert
	if body.StandPCURL != "" {
		prevURL := ""
		if prev, ok := a.liveStates.Load(no); ok {
			prevURL = prev.(LaneLiveState).StandPCURL
		}
		if body.StandPCURL != prevURL {
			log.Printf("Discovery Stand %d: URL %s", no, body.StandPCURL)
			go func(u string) {
				_ = a.store.SetLaneStandpcURL(context.Background(), no, u)
			}(body.StandPCURL)
		}
		body.LaneLiveState.StandPCURL = body.StandPCURL
	}
	a.liveStates.Store(no, body.LaneLiveState)
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) initLanes(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Count int `json:"count"`
	}](r)
	if err != nil || body.Count < 1 || body.Count > 100 {
		return nil, errors.New("count 1-100 erforderlich")
	}
	if err := a.store.EnsureLanes(r.Context(), body.Count); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "count": body.Count}, nil
}

func (a *APIServer) laneSession(w http.ResponseWriter, r *http.Request) (any, error) {
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errors.New("ungueltige Standnummer")
	}
	return a.store.ActiveSessionForLane(r.Context(), no)
}

func (a *APIServer) assignLane(w http.ResponseWriter, r *http.Request) (any, error) {
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errors.New("ungueltige Standnummer")
	}
	body, err := decodeBody[struct {
		ShooterID    string `json:"shooter_id"`
		DisciplineID string `json:"discipline_id"`
		EventID      string `json:"event_id"`
	}](r)
	if err != nil || body.DisciplineID == "" {
		return nil, errors.New("discipline_id erforderlich")
	}
	id, err := a.store.AssignLane(r.Context(), no, body.ShooterID, body.DisciplineID, body.EventID)
	if err != nil {
		return nil, err
	}
	return map[string]string{"session_id": id}, nil
}

func (a *APIServer) listShooters(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListShooters(r.Context(), r.URL.Query().Get("q"))
}

func (a *APIServer) createShooter(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		LastName  string `json:"last_name"`
		FirstName string `json:"first_name"`
		PassNo    string `json:"pass_no"`
	}](r)
	if err != nil || body.LastName == "" {
		return nil, errors.New("last_name erforderlich")
	}
	id, err := a.store.CreateShooter(r.Context(),
		body.LastName, body.FirstName, body.PassNo)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": id}, nil
}

func (a *APIServer) listDisciplines(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListDisciplines(r.Context())
}

func (a *APIServer) listTargets(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListTargets(r.Context())
}

func (a *APIServer) listDisciplinesFull(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListDisciplinesFull(r.Context())
}

func (a *APIServer) createDiscipline(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[DisciplineFull](r)
	if err != nil || body.Name == "" || body.TargetID == "" {
		return nil, errors.New("name und target_id sind erforderlich")
	}
	if body.DistanceM == 0 {
		body.DistanceM = 10
	}
	if body.ShotsPerSeries == 0 {
		body.ShotsPerSeries = 10
	}
	body.Active = true
	id, err := a.store.CreateDiscipline(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateDiscipline(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[DisciplineFull](r)
	if err != nil || body.Name == "" || body.TargetID == "" {
		return nil, errors.New("name und target_id sind erforderlich")
	}
	body.ID = r.PathValue("id")
	if err := a.store.UpdateDiscipline(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteDiscipline(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.DeleteDiscipline(r.Context(), r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) setSessionStatus(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Status string `json:"status"`
	}](r)
	if err != nil {
		return nil, errors.New("status erforderlich")
	}
	switch body.Status {
	case "sighting", "match", "paused", "finished", "aborted":
	default:
		return nil, errors.New("ungueltiger Status")
	}
	if err := a.store.SetSessionStatus(r.Context(),
		r.PathValue("id"), body.Status); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) sessionShots(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.SessionShots(r.Context(), r.PathValue("id"))
}

func (a *APIServer) annulShot(w http.ResponseWriter, r *http.Request) (any, error) {
	role, err := a.requireCorrection(w, r)
	if err != nil {
		return nil, err
	}
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errors.New("ungueltige Schussnummer")
	}
	body, _ := decodeBody[struct {
		Reason string `json:"reason"`
	}](r)
	if err := a.store.AnnulShot(r.Context(),
		r.PathValue("id"), no, role.RoleKey, body.Reason); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ----------------------------------------------------------------------------
// Simulator (Kalibrier-Neuberechnung, siehe simulator.go)
// ----------------------------------------------------------------------------

func (a *APIServer) simulatorShots(w http.ResponseWriter, r *http.Request) (any, error) {
	shots, err := a.store.SessionShotsRaw(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if shots == nil {
		shots = []SimShotRaw{}
	}
	return shots, nil
}

// sessionTargetGeometry liefert die visuelle Referenzscheibe (Ringe/gefuellt)
// fuer die SVG-Darstellung im Simulator. Nutzt die StandPC-Scheibennummer
// (disciplines.standpc_target_no), falls bekannt - sonst einen Fallback aus
// den rohen DB-Ringdurchmessern (siehe target_geometry.go).
func (a *APIServer) sessionTargetGeometry(w http.ResponseWriter, r *http.Request) (any, error) {
	sessionID := r.PathValue("id")
	targetID, _, _, standpcTargetNo, err := a.store.SessionTargets(r.Context(), sessionID)
	if err != nil {
		return nil, err
	}
	if geo, ok := targetGeometries[standpcTargetNo]; ok {
		return geo, nil
	}
	target, err := a.store.LoadTargetDef(r.Context(), targetID)
	if err != nil {
		return nil, err
	}
	// standpc_target_no oft nicht gepflegt (siehe Migration 010) - anhand des
	// Scheibennamens (z.B. "LP 10m ISSF") auf eine bekannte Geometrie mit
	// korrekter "gefuellt"-Zeichnung schliessen, bevor auf den ungenauen
	// Ringwert-Heuristik-Fallback zurueckgegriffen wird.
	if geo, ok := matchTargetGeometryByName(target.Name); ok {
		return geo, nil
	}
	return targetGeometryFromRings(target), nil
}

type simSideResult struct {
	XMM         float64 `json:"x_mm"`
	YMM         float64 `json:"y_mm"`
	Ring        int     `json:"ring"`
	Decimal     float64 `json:"decimal"`
	PosValid    bool    `json:"pos_valid,omitempty"`
	ClusterHits int     `json:"cluster_hits,omitempty"`
}

type simShotResult struct {
	ShotNo int           `json:"shot_no"`
	Kind   string        `json:"kind"`
	HasRaw bool          `json:"has_raw"`
	Orig   simSideResult `json:"orig"`
	Sim    simSideResult `json:"sim"`
}

// simulateSession berechnet alle Schuesse einer Session unter den
// gegebenen Kalibrierparametern neu (aus den gespeicherten air_ns-
// Rohdaten) und liefert Original- neben Neuberechnungs-Werten. Probe-
// Schuesse (kind=sighting) werden - wie beim Stand-PC - ggf. gegen eine
// abweichende Probescheibe (sighting_target_id) gewertet.
func (a *APIServer) simulateSession(w http.ResponseWriter, r *http.Request) (any, error) {
	sessionID := r.PathValue("id")
	body, err := decodeBody[struct {
		Params SimParams `json:"params"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}

	targetID, sightingTargetID, shotsPerSeries, _, err := a.store.SessionTargets(r.Context(), sessionID)
	if err != nil {
		return nil, err
	}
	matchTarget, err := a.store.LoadTargetDef(r.Context(), targetID)
	if err != nil {
		return nil, err
	}
	scorerMatch := NewScorer(matchTarget)
	scorerSighting := scorerMatch
	if sightingTargetID != "" {
		sightingTarget, err := a.store.LoadTargetDef(r.Context(), sightingTargetID)
		if err != nil {
			return nil, err
		}
		scorerSighting = NewScorer(sightingTarget)
	}

	shots, err := a.store.SessionShotsRaw(r.Context(), sessionID)
	if err != nil {
		return nil, err
	}

	out := make([]simShotResult, 0, len(shots))
	for _, sh := range shots {
		sc := scorerMatch
		if sh.Kind == "sighting" {
			sc = scorerSighting
		}
		res := simShotResult{
			ShotNo: sh.ShotNo,
			Kind:   sh.Kind,
			HasRaw: sh.HasRaw,
			Orig: simSideResult{
				XMM: sh.XMM, YMM: sh.YMM, Ring: sh.Ring, Decimal: sh.Decimal,
			},
		}
		if sh.HasRaw {
			sim := SolveShot(sh.AirNs, body.Params)
			res.Sim.PosValid = sim.PosValid
			res.Sim.ClusterHits = sim.ClusterHits
			if sim.PosValid {
				xmm := float64(sim.XUm) / 1000.0
				ymm := float64(sim.YUm) / 1000.0
				score := sc.Score(xmm, ymm)
				res.Sim.XMM = xmm
				res.Sim.YMM = ymm
				res.Sim.Ring = score.Ring
				res.Sim.Decimal = score.Decimal
			}
		}
		out = append(out, res)
	}

	return map[string]any{
		"shots_per_series": shotsPerSeries,
		"shots":            out,
	}, nil
}

// calibrateSession berechnet neue Mikrofon-Timing-Offsets (OFS0-OFS5) aus
// den vom Nutzer ausgewaehlten Kalibrier-Schuessen (siehe CalibrateMicOffsets,
// simulator.go) - Server-Pendant zu "CAL START" der Firmware, nur mit frei
// waehlbaren statt automatisch "naechste N" gesammelten Schuessen.
func (a *APIServer) calibrateSession(w http.ResponseWriter, r *http.Request) (any, error) {
	sessionID := r.PathValue("id")
	body, err := decodeBody[struct {
		Params  SimParams `json:"params"`
		ShotNos []int     `json:"shot_nos"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	if len(body.ShotNos) < 3 {
		return nil, errBadRequest("mindestens 3 Kalibrier-Schuesse noetig")
	}

	shots, err := a.store.SessionShotsRaw(r.Context(), sessionID)
	if err != nil {
		return nil, err
	}
	wanted := make(map[int]bool, len(body.ShotNos))
	for _, no := range body.ShotNos {
		wanted[no] = true
	}
	var airNs [][6][]int64
	for _, sh := range shots {
		if wanted[sh.ShotNo] && sh.HasRaw {
			airNs = append(airNs, sh.AirNs)
		}
	}
	if len(airNs) < 3 {
		return nil, errBadRequest("zu wenige der ausgewaehlten Schuesse haben Rohdaten (mind. 3 noetig)")
	}

	offsets, cost := CalibrateMicOffsets(airNs, body.Params)
	return map[string]any{
		"mic_offset_ns": offsets,
		"cost_mm":       cost,
		"shots_used":    len(airNs),
	}, nil
}

// candidateOut/bulletShiftOut: API-Form von airCandidate/bulletShiftMic
// (simulator.go) - x/y der Kandidaten werden hier (statt in simulator.go)
// nach Scheibenkoordinaten (0.001mm inkl. OffsetXUm/OffsetYUm) umgerechnet,
// die Kugelkorrektur-Messwerte auf 2 Nachkommastellen (mm) gerundet, wie
// fuer die Detailanzeige gewuenscht.
type candidateOut struct {
	Ref        int     `json:"ref"`
	A          int     `json:"a"`
	B          int     `json:"b"`
	XUm        int64   `json:"x_um"`
	YUm        int64   `json:"y_um"`
	ResidualMM float64 `json:"residual_mm"`
	Best       bool    `json:"best"`
}

type bulletShiftOut struct {
	Mic              int     `json:"mic"`
	SignedResidualMM float64 `json:"signed_residual_mm"`
	ShiftMM          float64 `json:"shift_mm"`
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// solveShotDetail liefert die vollstaendige Neuberechnung EINES Schusses
// (alle Werte aus SimResult: x_um/y_um/pos_res_um/precision_um/cluster_hits)
// plus alle geloesten 3er-Mikrofon-Kombinationen ("Schnittpunkte" inkl.
// beteiligter Mikrofone und Rest-Fehler) und die Kugeldurchmesser-Korrektur-
// Messwerte je verbleibendem Mikrofon (siehe SolveShotDetail() in
// simulator.go) - fuer die optionale Detailansicht, bewusst als eigener
// Endpoint statt Teil von /simulate, da diese Daten nur fuer jeweils EINEN
// angeklickten Schuss gebraucht werden.
func (a *APIServer) solveShotDetail(w http.ResponseWriter, r *http.Request) (any, error) {
	sessionID := r.PathValue("id")
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errBadRequest("ungueltige Schussnummer")
	}
	body, err := decodeBody[struct {
		Params SimParams `json:"params"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}

	shots, err := a.store.SessionShotsRaw(r.Context(), sessionID)
	if err != nil {
		return nil, err
	}
	var target *SimShotRaw
	for i := range shots {
		if shots[i].ShotNo == no {
			target = &shots[i]
			break
		}
	}
	if target == nil {
		return nil, &httpError{code: 404, msg: "Schuss nicht gefunden"}
	}
	if !target.HasRaw {
		return nil, errBadRequest("keine Rohdaten fuer diesen Schuss vorhanden")
	}

	res, candidates, bulletShift := SolveShotDetail(target.AirNs, body.Params)

	cands := make([]candidateOut, len(candidates))
	for i, c := range candidates {
		cands[i] = candidateOut{
			Ref:        c.Ref,
			A:          c.A,
			B:          c.B,
			XUm:        int64(math.Round(float64(c.X)*1000)) + body.Params.OffsetXUm,
			YUm:        int64(math.Round(float64(c.Y)*1000)) + body.Params.OffsetYUm,
			ResidualMM: round2(float64(c.ResidualMM)),
			Best:       c.Best,
		}
	}
	bshifts := make([]bulletShiftOut, len(bulletShift))
	for i, b := range bulletShift {
		bshifts[i] = bulletShiftOut{
			Mic:              b.Mic,
			SignedResidualMM: round2(float64(b.SignedResidualMM)),
			ShiftMM:          round2(float64(b.ShiftMM)),
		}
	}

	return map[string]any{
		"sim":          res,
		"candidates":   cands,
		"bullet_shift": bshifts,
	}, nil
}

func (a *APIServer) listSimulatorConfigs(w http.ResponseWriter, r *http.Request) (any, error) {
	list, err := a.store.ListSimulatorConfigs(r.Context())
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []SimulatorConfig{}
	}
	return list, nil
}

func (a *APIServer) saveSimulatorConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Name   string    `json:"name"`
		Params SimParams `json:"params"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	if body.Name == "" {
		return nil, errBadRequest("name erforderlich")
	}
	id, err := a.store.SaveSimulatorConfig(r.Context(), body.Name, body.Params)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": id}, nil
}

func (a *APIServer) deleteSimulatorConfig(w http.ResponseWriter, r *http.Request) (any, error) {
	return nil, a.store.DeleteSimulatorConfig(r.Context(), r.PathValue("id"))
}

// ----------------------------------------------------------------------------
// Stammdaten – Gaue
// ----------------------------------------------------------------------------

func (a *APIServer) listGaue(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListGaue(r.Context())
}

func (a *APIServer) createGau(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		GauNo string `json:"gau_no"`
		Name  string `json:"name"`
	}](r)
	if err != nil || body.GauNo == "" || body.Name == "" {
		return nil, errors.New("gau_no und name erforderlich")
	}
	id, err := a.store.CreateGau(r.Context(), body.GauNo, body.Name)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateGau(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		GauNo string `json:"gau_no"`
		Name  string `json:"name"`
	}](r)
	if err != nil || body.GauNo == "" || body.Name == "" {
		return nil, errors.New("gau_no und name erforderlich")
	}
	if err := a.store.UpdateGau(r.Context(), r.PathValue("id"), body.GauNo, body.Name); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteGau(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.DeleteGau(r.Context(), r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ----------------------------------------------------------------------------
// Stammdaten – Vereine
// ----------------------------------------------------------------------------

func (a *APIServer) listClubs(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListClubsFull(r.Context())
}

func (a *APIServer) createClub(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[ClubFull](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	id, err := a.store.CreateClub(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateClub(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[ClubFull](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	body.ID = r.PathValue("id")
	if err := a.store.UpdateClub(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteClub(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.DeleteClub(r.Context(), r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) importClubs(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Clubs      []ImportClubEntry `json:"clubs"`
		CreateGaue bool              `json:"create_gaue"`
	}](r)
	if err != nil || len(body.Clubs) == 0 {
		return nil, errors.New("clubs erforderlich")
	}
	created, updated, err := a.store.ImportClubs(r.Context(), body.Clubs, body.CreateGaue)
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": created, "updated": updated}, nil
}

// ----------------------------------------------------------------------------
// Stammdaten – Schützen (vollständig)
// ----------------------------------------------------------------------------

func (a *APIServer) listShootersFull(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query().Get("q")
	clubID := r.URL.Query().Get("club_id")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 10 || pageSize > 500 {
		pageSize = 50
	}
	list, total, err := a.store.ListShootersFull(r.Context(), q, clubID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	return map[string]any{"shooters": list, "total": total, "page": page, "page_size": pageSize}, nil
}

func (a *APIServer) createShooterFull(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[ShooterFull](r)
	if err != nil || body.LastName == "" {
		return nil, errors.New("last_name erforderlich")
	}
	id, err := a.store.CreateShooterFull(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateShooterFull(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[ShooterFull](r)
	if err != nil || body.LastName == "" {
		return nil, errors.New("last_name erforderlich")
	}
	body.ID = r.PathValue("id")
	if err := a.store.UpdateShooterFull(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteShooter(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.DeleteShooterFull(r.Context(), r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) recalculateSportsClasses(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Year int `json:"year"`
	}](r)
	if err != nil || body.Year < 1900 || body.Year > 2200 {
		return nil, errors.New("gueltiges Sportjahr erforderlich")
	}
	return a.store.RecalculateSportsClasses(r.Context(), body.Year)
}

func (a *APIServer) importMembers(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Rows []MemberRow `json:"rows"`
	}](r)
	if err != nil || len(body.Rows) == 0 {
		return nil, errors.New("rows erforderlich")
	}
	created, updated, skipped, err := a.store.ImportMembers(r.Context(), body.Rows)
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": created, "updated": updated, "skipped": skipped}, nil
}

// ----------------------------------------------------------------------------
// Mannschaften
// ----------------------------------------------------------------------------

func (a *APIServer) listTeams(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	list, err := a.store.ListTeams(r.Context(),
		q.Get("club_id"), q.Get("gau_id"),
		q.Get("active") == "1")
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (a *APIServer) createTeam(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[Team](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	id, err := a.store.CreateTeam(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateTeam(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[Team](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	body.ID = r.PathValue("id")
	if err := a.store.UpdateTeam(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteTeam(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.DeleteTeam(r.Context(), r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) listTeamMembers(w http.ResponseWriter, r *http.Request) (any, error) {
	list, err := a.store.ListTeamMembers(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (a *APIServer) addTeamMember(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		ShooterID string `json:"shooter_id"`
		Position  *int   `json:"position"`
		JoinedAt  string `json:"joined_at"`
		Notes     string `json:"notes"`
	}](r)
	if err != nil || body.ShooterID == "" {
		return nil, errors.New("shooter_id erforderlich")
	}
	if err := a.store.AddTeamMember(r.Context(),
		r.PathValue("id"), body.ShooterID, body.Position, body.JoinedAt, body.Notes,
	); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) removeTeamMember(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.RemoveTeamMember(r.Context(),
		r.PathValue("id"), r.PathValue("shooterID"),
	); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ----------------------------------------------------------------------------
// Sportklassen
// ----------------------------------------------------------------------------

func (a *APIServer) listShooterClasses(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListShooterClasses(r.Context())
}

func (a *APIServer) createShooterClass(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[ShooterClass](r)
	if err != nil || body.Code == "" || body.Name == "" {
		return nil, errors.New("code und name erforderlich")
	}
	id, err := a.store.CreateShooterClass(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateShooterClass(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[ShooterClass](r)
	if err != nil || body.Code == "" || body.Name == "" {
		return nil, errors.New("code und name erforderlich")
	}
	body.ID = r.PathValue("id")
	if err := a.store.UpdateShooterClass(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteShooterClass(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.DeleteShooterClass(r.Context(), r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) importShooterClasses(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Classes []ShooterClass `json:"classes"`
	}](r)
	if err != nil || len(body.Classes) == 0 {
		return nil, errors.New("classes erforderlich")
	}
	created, updated, err := a.store.ImportShooterClasses(r.Context(), body.Classes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": created, "updated": updated}, nil
}

// ----------------------------------------------------------------------------
// Wettkämpfe
// ----------------------------------------------------------------------------

func (a *APIServer) listCompetitions(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	status := q.Get("status")
	// Archivierte Wettkämpfe werden standardmäßig ausgeblendet;
	// include_archived=1 oder status=archived schaltet sie ein.
	includeArchived := q.Get("include_archived") == "1" || status == "archived"
	list, err := a.store.ListCompetitions(r.Context(),
		q.Get("type"), status, q.Get("active") == "1", includeArchived)
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (a *APIServer) getCompetition(w http.ResponseWriter, r *http.Request) (any, error) {
	c, err := a.store.GetCompetition(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &httpError{code: http.StatusNotFound, msg: "Wettkampf nicht gefunden"}
		}
		return nil, err
	}
	return c, nil
}

func (a *APIServer) createCompetition(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[Competition](r)
	if err != nil || body.Name == "" || body.Type == "" {
		return nil, errors.New("name und type erforderlich")
	}
	if body.Status == "" {
		body.Status = "planned"
	}
	id, err := a.store.CreateCompetition(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateCompetition(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[Competition](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	body.ID = r.PathValue("id")
	if body.Status == "" {
		body.Status = "planned"
	}
	if err := a.store.UpdateCompetition(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteCompetition(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.DeleteCompetition(r.Context(), r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) setCompetitionStatus(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Status string `json:"status"`
	}](r)
	if err != nil || body.Status == "" {
		return nil, errors.New("status erforderlich")
	}
	if err := a.store.SetCompetitionStatus(r.Context(), r.PathValue("id"), body.Status); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) listParticipants(w http.ResponseWriter, r *http.Request) (any, error) {
	list, err := a.store.ListParticipants(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (a *APIServer) addParticipant(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[CompetitionParticipant](r)
	if err != nil {
		return nil, err
	}
	if body.TeamID == "" && body.ClubID == "" && body.GauID == "" {
		return nil, errors.New("team_id, club_id oder gau_id erforderlich")
	}
	id, err := a.store.AddParticipant(r.Context(), r.PathValue("id"), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) removeParticipant(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.RemoveParticipant(r.Context(), r.PathValue("pid")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) listStarters(w http.ResponseWriter, r *http.Request) (any, error) {
	list, err := a.store.ListStarters(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (a *APIServer) addStarter(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		ShooterID    string `json:"shooter_id"`
		DisciplineID string `json:"discipline_id"`
		TeamID       string `json:"team_id"`
		StartNo      string `json:"start_no"`
		Role         string `json:"role"`
	}](r)
	if err != nil || body.ShooterID == "" {
		return nil, errors.New("shooter_id erforderlich")
	}
	id, err := a.store.AddStarter(r.Context(),
		r.PathValue("id"), body.ShooterID, body.DisciplineID, body.TeamID, body.StartNo, body.Role)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) importTeamStarters(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		TeamID string `json:"team_id"`
	}](r)
	if err != nil || body.TeamID == "" {
		return nil, errors.New("team_id erforderlich")
	}
	n, err := a.store.ImportTeamStarters(r.Context(), r.PathValue("id"), body.TeamID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"added": n}, nil
}

func (a *APIServer) setStarterRole(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Role string `json:"role"`
	}](r)
	if err != nil || body.Role == "" {
		return nil, errors.New("role erforderlich (S, E oder AK)")
	}
	if body.Role != "S" && body.Role != "E" && body.Role != "AK" {
		return nil, errors.New("ungültige Rolle – erlaubt: S, E, AK")
	}
	if err := a.store.SetStarterRole(r.Context(), r.PathValue("sid"), body.Role); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) removeStarter(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.RemoveStarter(r.Context(), r.PathValue("sid")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) listResults(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	results, err := a.store.ListResults(r.Context(),
		q.Get("date"), q.Get("name"), q.Get("event_id"))
	if err != nil {
		return nil, err
	}
	// Live-Daten einmergen: Schüsse vom StandPC die noch nicht in der DB sind
	for i := range results {
		if results[i].ShotCount > 0 {
			continue
		}
		v, ok := a.liveStates.Load(results[i].LaneNo)
		if !ok {
			continue
		}
		ls := v.(LaneLiveState)
		if ls.WertungCount == 0 {
			continue
		}
		results[i].ShotCount = ls.WertungCount
		results[i].TotalRings = ls.TotalRings
		results[i].TotalDecimal = ls.TotalDecimal
		results[i].LiveData = true
	}
	return results, nil
}

// ── Standaktion: StandPC-URL ─────────────────────────────────────────────────

func (a *APIServer) setLaneStandpcURL(w http.ResponseWriter, r *http.Request) (any, error) {
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errBadRequest("ungültige Lane-Nummer")
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errBadRequest("ungültiger Body")
	}
	if err := a.store.SetLaneStandpcURL(r.Context(), no, body.URL); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

// ── Standaktion: Proxy zu StandPC ───────────────────────────────────────────

func standpcURL(lanes []Lane, laneNo int) (string, error) {
	for _, l := range lanes {
		if l.LaneNo == laneNo {
			if l.StandPCURL == "" {
				return "", fmt.Errorf("keine StandPC-URL für Stand %d konfiguriert", laneNo)
			}
			return l.StandPCURL, nil
		}
	}
	return "", fmt.Errorf("Stand %d nicht gefunden", laneNo)
}

func (a *APIServer) proxyLocalSessions(w http.ResponseWriter, r *http.Request) (any, error) {
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errBadRequest("ungültige Lane-Nummer")
	}
	lanes, err := a.store.ListLanes(r.Context())
	if err != nil {
		return nil, err
	}
	base, err := standpcURL(lanes, no)
	if err != nil {
		return nil, err
	}
	return proxyGet(base + "/api/local-sessions")
}

func (a *APIServer) proxyLocalSessionShots(w http.ResponseWriter, r *http.Request) (any, error) {
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errBadRequest("ungültige Lane-Nummer")
	}
	sid := r.PathValue("sid")
	lanes, err := a.store.ListLanes(r.Context())
	if err != nil {
		return nil, err
	}
	base, err := standpcURL(lanes, no)
	if err != nil {
		return nil, err
	}
	return proxyGet(base + "/api/local-sessions/" + sid + "/shots")
}

func proxyGet(url string) (any, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("StandPC nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	var result any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("StandPC-Antwort ungültig: %w", err)
	}
	return result, nil
}

// ── Standaktion: Transfer ───────────────────────────────────────────────────

func (a *APIServer) transferSession(w http.ResponseWriter, r *http.Request) (any, error) {
	var body struct {
		SessionID string `json:"session_id"`
		ToLaneNo  int    `json:"to_lane_no"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errBadRequest("ungültiger Body")
	}
	if body.SessionID == "" || body.ToLaneNo == 0 {
		return nil, errBadRequest("session_id und to_lane_no erforderlich")
	}
	if err := a.store.TransferSession(r.Context(), body.SessionID, body.ToLaneNo); err != nil {
		return nil, err
	}
	// Live-State der alten Lane löschen damit der Dot sofort grau wird
	a.liveStates.Range(func(k, v any) bool {
		ls := v.(LaneLiveState)
		if ls.WertungCount > 0 {
			return true
		}
		return true
	})
	return map[string]string{"status": "ok"}, nil
}

// ── Standaktion: Import ─────────────────────────────────────────────────────

func (a *APIServer) importSession(w http.ResponseWriter, r *http.Request) (any, error) {
	var body struct {
		SessionID string       `json:"session_id"` // Ziel-Session in DB
		LaneNo    int          `json:"lane_no"`    // StandPC für Schüsse
		LocalSID  string       `json:"local_sid"`  // Session-ID im lokalen Log
		Shots     []ImportShot `json:"shots"`      // direkt mitgeliefert ODER
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errBadRequest("ungültiger Body")
	}
	if body.SessionID == "" {
		return nil, errBadRequest("session_id erforderlich")
	}

	shots := body.Shots

	// Wenn keine Schüsse direkt mitgeliefert wurden, vom StandPC nachladen
	if len(shots) == 0 && body.LaneNo > 0 && body.LocalSID != "" {
		lanes, err := a.store.ListLanes(r.Context())
		if err != nil {
			return nil, err
		}
		base, err := standpcURL(lanes, body.LaneNo)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(base + "/api/local-sessions/" + body.LocalSID + "/shots")
		if err != nil {
			return nil, fmt.Errorf("StandPC nicht erreichbar: %w", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		// StandPC liefert []Shot (standpc-Format), in ImportShot umwandeln
		var rawShots []struct {
			ShotNo         int     `json:"shot_no"`
			Mode           string  `json:"mode"`
			FiredAt        string  `json:"fired_at"`
			XMM            float64 `json:"x_mm"`
			YMM            float64 `json:"y_mm"`
			Ring           int     `json:"ring"`
			Decimal        float64 `json:"decimal"`
			InnerTen       bool    `json:"inner_ten"`
			CenterDistance float64 `json:"center_distance"`
			Seq            int     `json:"seq"`
			RawTNs         []int64 `json:"raw_t_ns"`
			SensorHits     int     `json:"sensor_hits"`
			Confidence     float64 `json:"confidence"`
			Rejected       bool    `json:"rejected"`
		}
		if err := json.Unmarshal(raw, &rawShots); err != nil {
			return nil, fmt.Errorf("StandPC-Antwort ungültig: %w", err)
		}
		shotNo := 1
		for _, s := range rawShots {
			if s.Rejected {
				continue
			}
			shots = append(shots, ImportShot{
				ShotNo:         shotNo,
				Kind:           s.Mode,
				FiredAt:        s.FiredAt,
				XMM:            s.XMM,
				YMM:            s.YMM,
				Ring:           s.Ring,
				Decimal:        s.Decimal,
				InnerTen:       s.InnerTen,
				CenterDistance: s.CenterDistance,
				Seq:            s.Seq,
				RawTNs:         s.RawTNs,
				SensorHits:     s.SensorHits,
				Confidence:     s.Confidence,
			})
			shotNo++
		}
	}

	if err := a.store.ImportShots(r.Context(), body.SessionID, shots); err != nil {
		return nil, err
	}
	return map[string]any{"imported": len(shots)}, nil
}

func errBadRequest(msg string) error {
	return &httpError{code: http.StatusBadRequest, msg: msg}
}

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

// ── Standaktion: Disziplinen übertragen ─────────────────────────────────────

func (a *APIServer) pushDisciplinesToStandPC(w http.ResponseWriter, r *http.Request) (any, error) {
	no, err := strconv.Atoi(r.PathValue("no"))
	if err != nil {
		return nil, errBadRequest("ungültige Lane-Nummer")
	}

	// Body: { "default": "LG-40", "discipline_ids": ["uuid1","uuid2",...] }
	var body struct {
		Default       string   `json:"default"`
		DisciplineIDs []string `json:"discipline_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errBadRequest("ungültiger Body")
	}

	// Alle aktiven Disziplinen laden
	all, err := a.store.ListDisciplinesFull(r.Context())
	if err != nil {
		return nil, err
	}

	// Filtern nach übergebenen IDs; wenn keine IDs übergeben → alle aktiven
	type standpcDef struct {
		Name           string `json:"name"`
		TargetNo       int    `json:"target_no"`
		TrialShots     int    `json:"trial_shots"`
		ScoringShots   int    `json:"scoring_shots"`
		ShotsPerSeries int    `json:"shots_per_series"`
		DecimalScoring bool   `json:"decimal_scoring"`
		Anzeige        string `json:"anzeige"`
	}

	idSet := map[string]bool{}
	for _, id := range body.DisciplineIDs {
		idSet[id] = true
	}

	var defs []standpcDef
	for _, d := range all {
		if !d.Active {
			continue
		}
		if len(idSet) > 0 && !idSet[d.ID] {
			continue
		}
		if d.StandPCTargetNo == 0 {
			continue // ohne target_no nicht übertragbar
		}
		trialShots := 100
		if d.MaxSightingShots != nil {
			trialShots = *d.MaxSightingShots
		}
		defs = append(defs, standpcDef{
			Name:           d.Name,
			TargetNo:       d.StandPCTargetNo,
			TrialShots:     trialShots,
			ScoringShots:   d.MatchShotCount,
			ShotsPerSeries: d.ShotsPerSeries,
			DecimalScoring: d.DecimalScoring,
			Anzeige:        d.Anzeige,
		})
	}

	if len(defs) == 0 {
		return nil, &httpError{code: 400, msg: "keine übertragbaren Disziplinen (standpc_target_no nicht gesetzt?)"}
	}

	if body.Default == "" && len(defs) > 0 {
		body.Default = defs[0].Name
	}

	payload := map[string]any{
		"default":     body.Default,
		"disciplines": defs,
	}

	lanes, err := a.store.ListLanes(r.Context())
	if err != nil {
		return nil, err
	}
	base, err := standpcURL(lanes, no)
	if err != nil {
		return nil, err
	}

	data, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodPut, base+"/api/disciplines/config",
		io.NopCloser(io.Reader(bytes.NewReader(data))))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("StandPC nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	var result any
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

// ── Auswertung ──────────────────────────────────────────────────────────────

func (a *APIServer) listRundenwettkampfResults(w http.ResponseWriter, r *http.Request) (any, error) {
	eventID := r.URL.Query().Get("event_id")
	if eventID == "" {
		return nil, errBadRequest("event_id erforderlich")
	}
	return a.store.ListRundenwettkampfResults(r.Context(), eventID)
}

func (a *APIServer) listGruppenwettkampfData(w http.ResponseWriter, r *http.Request) (any, error) {
	eventID := r.URL.Query().Get("event_id")
	if eventID == "" {
		return nil, errBadRequest("event_id erforderlich")
	}
	return a.store.ListGruppenwettkampfData(r.Context(), eventID)
}

func (a *APIServer) listSavedAuswertungen(w http.ResponseWriter, r *http.Request) (any, error) {
	eventID := r.URL.Query().Get("event_id")
	list, err := a.store.ListSavedAuswertungen(r.Context(), eventID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []SavedAuswertung{}
	}
	return list, nil
}

func (a *APIServer) createSavedAuswertung(w http.ResponseWriter, r *http.Request) (any, error) {
	var body struct {
		Name    string         `json:"name"`
		Type    string         `json:"type"`
		EventID string         `json:"event_id"`
		Params  map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Name == "" {
		return nil, errBadRequest("name erforderlich")
	}
	if body.Type == "" {
		body.Type = "runde"
	}
	if body.Params == nil {
		body.Params = map[string]any{}
	}
	id, err := a.store.CreateSavedAuswertung(r.Context(), body.Name, body.Type, body.EventID, body.Params)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": id}, nil
}

func (a *APIServer) deleteSavedAuswertung(w http.ResponseWriter, r *http.Request) (any, error) {
	id := r.PathValue("id")
	return nil, a.store.DeleteSavedAuswertung(r.Context(), id)
}

// ─────────────────────────────────────────────────────────────────────────────
// Archiv
// ─────────────────────────────────────────────────────────────────────────────

func (a *APIServer) listArchivedEvents(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListArchivedEventsWithStats(r.Context())
}

type archiveRequest struct {
	EventIDs       []string `json:"event_ids"`
	IncludeOrphans bool     `json:"include_orphans"`
}

// archiveExport exportiert die angegebenen Events als JSON-Datei (kein Löschen).
func (a *APIServer) archiveExport(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.EventIDs) == 0 {
		http.Error(w, `{"error":"event_ids erforderlich"}`, http.StatusBadRequest)
		return
	}
	bundle, err := a.store.ExportArchivedEvents(r.Context(), req.EventIDs, req.IncludeOrphans)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	sendBundleDownload(w, bundle)
}

// archiveExportDelete exportiert UND löscht die angegebenen Events.
func (a *APIServer) archiveExportDelete(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.EventIDs) == 0 {
		http.Error(w, `{"error":"event_ids erforderlich"}`, http.StatusBadRequest)
		return
	}
	bundle, err := a.store.ExportArchivedEvents(r.Context(), req.EventIDs, req.IncludeOrphans)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if err := a.store.DeleteArchivedEvents(r.Context(), req.EventIDs, req.IncludeOrphans); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	sendBundleDownload(w, bundle)
}

// archiveImport importiert eine zuvor exportierte Archivdatei.
func (a *APIServer) archiveImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20) // 500 MB Limit
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, `{"error":"Multipart-Formular konnte nicht gelesen werden"}`, http.StatusBadRequest)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"Kein Datei-Feld 'file'"}`, http.StatusBadRequest)
		return
	}
	defer f.Close()

	var bundle ArchiveBundle
	if err := json.NewDecoder(f).Decode(&bundle); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Ungültige Archivdatei: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	if bundle.Version != archiveBundleVersion {
		http.Error(w, fmt.Sprintf(`{"error":"Unbekannte Bundle-Version %d"}`, bundle.Version), http.StatusBadRequest)
		return
	}

	result, err := a.store.ImportArchiveBundle(r.Context(), &bundle)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func sendBundleDownload(w http.ResponseWriter, bundle *ArchiveBundle) {
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		http.Error(w, `{"error":"JSON-Serialisierung fehlgeschlagen"}`, http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("schiessstand_archiv_%s.json", time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}
