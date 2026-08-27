// ============================================================================
// shotlog.go – Lokales Schussprotokoll (Meyton-Logdatei-Prinzip)
//
// Append-only JSON-Lines-Datei. Wird VOR der Datenbank geschrieben und
// dient als Nachweis bei DB-/Netzwerkausfall. Eine Datei pro Tag und Stand:
//
//	shotlog/lane03_2025-06-10.jsonl
//
// Jede Zeile = ein vollstaendiger Schuss inkl. Rohdaten. fsync nach jedem
// Schuss – bei Schussraten am Schiessstand (<1/s) voellig unkritisch und
// garantiert Persistenz auch bei Stromausfall.
// ============================================================================
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ShotLog struct {
	dir    string
	laneNo int

	mu      sync.Mutex
	file    *os.File
	curDate string
}

func NewShotLog(dir string, laneNo int) (*ShotLog, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("Logverzeichnis anlegen: %w", err)
	}
	return &ShotLog{dir: dir, laneNo: laneNo}, nil
}

func (l *ShotLog) Append(shot *Shot) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.ensureFile(); err != nil {
		return err
	}

	line, err := json.Marshal(shot)
	if err != nil {
		return fmt.Errorf("Schuss serialisieren: %w", err)
	}
	if _, err := l.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("Schreiben: %w", err)
	}
	// Persistenz erzwingen – das Protokoll ist die letzte Verteidigungslinie
	return l.file.Sync()
}

// ensureFile oeffnet die Tagesdatei, rolliert bei Datumswechsel
func (l *ShotLog) ensureFile() error {
	today := time.Now().Format("2006-01-02")
	if l.file != nil && l.curDate == today {
		return nil
	}
	if l.file != nil {
		l.file.Close()
	}
	name := fmt.Sprintf("lane%02d_%s.jsonl", l.laneNo, today)
	f, err := os.OpenFile(filepath.Join(l.dir, name),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("Logdatei oeffnen: %w", err)
	}
	l.file = f
	l.curDate = today
	return nil
}

func (l *ShotLog) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}
