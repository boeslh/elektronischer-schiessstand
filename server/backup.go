// ============================================================================
// backup.go – Komplettes DB-Backup/-Restore (Import/Export-Kachel, admin-only).
//
// Backups liegen in a.backupDir (Default: ../db-backups relativ zum
// WorkingDirectory des Service, siehe main.go) im pg_dump-Custom-Format
// (-F c). Bewusst AUSSERHALB von server/ bzw. dem Git-Repo, damit
// personenbezogene Daten nicht versehentlich eingecheckt werden koennen.
//
// Restore laeuft gegen die Live-DB, waehrend der Server-Prozess selbst per
// Connection-Pool verbunden bleibt - pg_restore --clean braucht kurzzeitig
// exklusive Sperren, parallele Anfragen koennen währenddessen kurz warten/
// fehlschlagen. Deshalb: vor jedem Restore automatisch ein frisches
// Sicherheits-Backup, und der Hinweis im Frontend, Restores nur bei
// ruhendem Schiessbetrieb durchzufuehren.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// isSafeBackupFilename akzeptiert jeden einfachen Dateinamen, der auf .dump
// endet - kein Pfadanteil (schuetzt vor Path-Traversal), damit auch frei
// umbenannte Backups (siehe renameBackupHandler) erkannt werden.
func isSafeBackupFilename(name string) bool {
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		return false
	}
	return strings.HasSuffix(name, ".dump")
}

type BackupInfo struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at"`
}

func (a *APIServer) resolvePgTool(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s nicht gefunden (PostgreSQL-Client-Tools installiert?): %w", name, err)
	}
	return path, nil
}

// uniqueBackupPath liefert einen freien Dateinamen fuer ein neues Backup
// (haengt bei Kollision _2/_3/... an).
func uniqueBackupPath(dir string) (filename, path string) {
	base := fmt.Sprintf("schiessstand_%s.dump", time.Now().Format("20060102_150405"))
	return uniquePathFor(dir, base)
}

// uniquePathFor liefert einen freien Dateinamen fuer den gewuenschten
// Basisnamen (haengt bei Kollision _2/_3/... vor der .dump-Endung an).
func uniquePathFor(dir, base string) (filename, path string) {
	filename = base
	path = filepath.Join(dir, filename)
	stem := strings.TrimSuffix(base, ".dump")
	for n := 2; ; n++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return filename, path
		}
		filename = fmt.Sprintf("%s_%d.dump", stem, n)
		path = filepath.Join(dir, filename)
	}
}

func (a *APIServer) createBackup(ctx context.Context) (string, error) {
	pgDump, err := a.resolvePgTool("pg_dump")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(a.backupDir, 0o755); err != nil {
		return "", fmt.Errorf("Backup-Verzeichnis: %w", err)
	}
	filename, path := uniqueBackupPath(a.backupDir)
	cmd := exec.CommandContext(ctx, pgDump, a.dsn, "-F", "c", "-f", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(path) // unvollstaendige Datei nicht liegen lassen
		return "", fmt.Errorf("pg_dump fehlgeschlagen: %s: %w", out, err)
	}
	return filename, nil
}

func listBackups(dir string) ([]BackupInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupInfo{}, nil
		}
		return nil, err
	}
	out := []BackupInfo{}
	for _, e := range entries {
		if e.IsDir() || !isSafeBackupFilename(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{
			Filename: e.Name(), SizeBytes: info.Size(),
			CreatedAt: info.ModTime().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename > out[j].Filename })
	return out, nil
}

// safeBackupPath validiert filename gegen isSafeBackupFilename (schuetzt vor
// Path-Traversal/fremden Dateien) und liefert den vollen Pfad.
func safeBackupPath(dir, filename string) (string, error) {
	if !isSafeBackupFilename(filename) {
		return "", errBadRequest("ungueltiger Backup-Dateiname")
	}
	return filepath.Join(dir, filename), nil
}

func (a *APIServer) restoreFromFile(ctx context.Context, path string) error {
	pgRestore, err := a.resolvePgTool("pg_restore")
	if err != nil {
		return err
	}
	// Sicherheits-Backup vor jedem Restore - Restore ueberschreibt sonst
	// unwiderruflich den aktuellen Stand.
	if _, err := a.createBackup(ctx); err != nil {
		return fmt.Errorf("Sicherheits-Backup vor Restore fehlgeschlagen, Restore abgebrochen: %w", err)
	}
	cmd := exec.CommandContext(ctx, pgRestore, "--clean", "--if-exists", "-d", a.dsn, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_restore fehlgeschlagen: %s: %w", out, err)
	}
	return nil
}

// ----------------------------------------------------------------------------
// HTTP-Handler
// ----------------------------------------------------------------------------

func (a *APIServer) listBackupsHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	backups, err := listBackups(a.backupDir)
	if err != nil {
		return nil, err
	}
	return map[string]any{"backups": backups}, nil
}

func (a *APIServer) createBackupHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	filename, err := a.createBackup(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{"filename": filename}, nil
}

func (a *APIServer) renameBackupHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	oldPath, err := safeBackupPath(a.backupDir, r.PathValue("filename"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(oldPath); err != nil {
		return nil, &httpError{code: http.StatusNotFound, msg: "Backup nicht gefunden"}
	}
	body, err := decodeBody[struct {
		NewName string `json:"new_name"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungueltiger Body: " + err.Error())
	}
	newName := strings.TrimSpace(body.NewName)
	if newName == "" {
		return nil, errBadRequest("neuer Name erforderlich")
	}
	if !strings.HasSuffix(newName, ".dump") {
		newName += ".dump"
	}
	newPath, err := safeBackupPath(a.backupDir, newName)
	if err != nil {
		return nil, err
	}
	if newPath == oldPath {
		return map[string]any{"ok": true, "filename": newName}, nil
	}
	if _, err := os.Stat(newPath); err == nil {
		return nil, errBadRequest("es existiert bereits ein Backup mit diesem Namen")
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return nil, fmt.Errorf("umbenennen fehlgeschlagen: %w", err)
	}
	return map[string]any{"ok": true, "filename": newName}, nil
}

func (a *APIServer) downloadBackupHandler(w http.ResponseWriter, r *http.Request) {
	if _, err := a.requireAdmin(w, r); err != nil {
		writeAccessDeniedPage(w, err)
		return
	}
	path, err := safeBackupPath(a.backupDir, r.PathValue("filename"))
	if err != nil {
		http.Error(w, "ungueltiger Dateiname", http.StatusBadRequest)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "Backup nicht gefunden", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "Backup nicht lesbar", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	io.Copy(w, f)
}

func (a *APIServer) restoreBackupHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	path, err := safeBackupPath(a.backupDir, r.PathValue("filename"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, &httpError{code: http.StatusNotFound, msg: "Backup nicht gefunden"}
	}
	if err := a.restoreFromFile(r.Context(), path); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// uploadBackupHandler speichert eine hochgeladene Datei als neues Backup,
// OHNE sie wiederherzustellen (im Gegensatz zu restoreUploadHandler) - fuer
// den selektiven Import als Quelle.
func (a *APIServer) uploadBackupHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30) // 1 GB Limit
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, errBadRequest("Multipart-Formular konnte nicht gelesen werden (evtl. zu groß)")
	}
	src, _, err := r.FormFile("file")
	if err != nil {
		return nil, errBadRequest("Kein Datei-Feld 'file'")
	}
	defer src.Close()

	if err := os.MkdirAll(a.backupDir, 0o755); err != nil {
		return nil, err
	}
	filename, path := uniqueBackupPath(a.backupDir)
	dst, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(path)
		return nil, fmt.Errorf("Hochladen fehlgeschlagen: %w", err)
	}
	dst.Close()
	return map[string]any{"ok": true, "filename": filename}, nil
}

func (a *APIServer) restoreUploadHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30) // 1 GB Limit
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, errBadRequest("Multipart-Formular konnte nicht gelesen werden (evtl. zu groß)")
	}
	src, _, err := r.FormFile("file")
	if err != nil {
		return nil, errBadRequest("Kein Datei-Feld 'file'")
	}
	defer src.Close()

	if err := os.MkdirAll(a.backupDir, 0o755); err != nil {
		return nil, err
	}
	filename, path := uniqueBackupPath(a.backupDir)
	dst, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(path)
		return nil, fmt.Errorf("Hochladen fehlgeschlagen: %w", err)
	}
	dst.Close()

	// Die hochgeladene Datei bleibt als Backup erhalten (zaehlt selbst als
	// Sicherung), restoreFromFile legt zusaetzlich ein Vorher-Backup an.
	if err := a.restoreFromFile(r.Context(), path); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "filename": filename}, nil
}
