// ============================================================================
// db.go – Asynchroner PostgreSQL-Schreiber mit Retry-Queue
//
// Designziel: Der Stand funktioniert auch bei Server-/Netzausfall weiter
// (Meyton-Prinzip SteuerPC<->Workstation). Schuesse landen in einer
// In-Memory-Queue und werden mit Backoff nachgeliefert, sobald die DB
// wieder erreichbar ist. Das lokale Schussprotokoll (shotlog.go) ist
// davon unabhaengig bereits geschrieben.
//
// Schreibt in die Tabelle "shots" des zentralen Datenmodells.
// ============================================================================
package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type dbEntry struct {
	shot      *Shot
	sessionID string
	shotNo    int
}

type DBWriter struct {
	ctx context.Context
	dsn string

	mu    sync.Mutex
	queue []dbEntry

	pool *pgxpool.Pool
	wake chan struct{}
}

// NewDBWriter startet den DB-Writer. Ausserdem werden beim Start alle
// Schuesse des HEUTIGEN lokalen Schussprotokolls in die Queue vorgemerkt
// ("Nachschreiben") - das faengt genau den Fall ab, dass der Stand-PC
// waehrend eines Server-/DB-Ausfalls neu gestartet wird: die In-Memory-Queue
// ist dann leer, obwohl bereits Schuesse gefallen sind, die noch nie in der
// DB ankamen. Das lokale JSONL-Log (shotlog.go, IMMER geschrieben, egal ob
// DB konfiguriert ist) ist die Quelle dafuer. Bewusst nur der aktuelle Tag
// (kein rueckwirkendes Nachschreiben aelterer Tage). ON CONFLICT (session_id,
// shot_no) DO NOTHING macht das erneute Einreihen bereits gespeicherter
// Schuesse ungefaehrlich.
func NewDBWriter(ctx context.Context, dsn, shotLogDir string, laneNo int) *DBWriter {
	w := &DBWriter{
		ctx:  ctx,
		dsn:  dsn,
		wake: make(chan struct{}, 1),
	}
	w.queue = ResyncShotsFromLog(shotLogDir, laneNo)
	if n := len(w.queue); n > 0 {
		log.Printf("DB: %d Schuss/Schuesse aus heutigem Log zum Nachschreiben vorgemerkt", n)
	}
	go w.run()
	return w
}

// Enqueue ist nicht-blockierend; die Pipeline wartet nie auf die DB.
// Session und shot_no werden PRO SCHUSS uebergeben, damit nach einem
// Sessionwechsel gepufferte Altschuesse noch zur richtigen Session gehen.
func (w *DBWriter) Enqueue(shot *Shot, sessionID string, shotNo int) {
	w.mu.Lock()
	w.queue = append(w.queue, dbEntry{shot, sessionID, shotNo})
	n := len(w.queue)
	w.mu.Unlock()

	if n > 1000 {
		log.Printf("WARNUNG: DB-Queue bei %d Schuessen – Server pruefen!", n)
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *DBWriter) run() {
	backoff := time.Second
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.wake:
		case <-time.After(10 * time.Second): // periodischer Flush-Versuch
		}

		if err := w.flush(); err != nil {
			log.Printf("DB: %v – Retry in %v", err, backoff)
			if !sleepCtx(w.ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		} else {
			backoff = time.Second
		}
	}
}

func (w *DBWriter) flush() error {
	w.mu.Lock()
	pending := make([]dbEntry, len(w.queue))
	copy(pending, w.queue)
	w.mu.Unlock()

	if len(pending) == 0 {
		return nil
	}
	if err := w.ensurePool(); err != nil {
		return err
	}

	written := 0
	for _, e := range pending {
		if err := w.insertShot(e); err != nil {
			break // Verbindung weg -> Rest bleibt in der Queue
		}
		written++
	}

	if written > 0 {
		w.mu.Lock()
		w.queue = w.queue[written:]
		w.mu.Unlock()
	}
	if written < len(pending) {
		return errQueueIncomplete
	}
	return nil
}

var errQueueIncomplete = &queueErr{}

type queueErr struct{}

func (*queueErr) Error() string { return "Queue nicht vollstaendig geschrieben" }

func (w *DBWriter) ensurePool() error {
	if w.pool != nil {
		return nil
	}
	cfg, err := pgxpool.ParseConfig(w.dsn)
	if err != nil {
		return err
	}
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(w.ctx, cfg)
	if err != nil {
		return err
	}
	w.pool = pool
	log.Printf("DB: verbunden")
	return nil
}

func (w *DBWriter) insertShot(e dbEntry) error {
	ctx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
	defer cancel()

	s := e.shot
	status := "valid"
	if s.Rejected {
		status = "rejected"
	}
	// shot_kind: "probe" -> sighting, "wertung" -> match (war bisher fest
	// auf 'match' verdrahtet, unabhaengig vom tatsaechlichen Modus)
	kind := "match"
	if s.Mode == ModeProbe {
		kind = "sighting"
	}

	// air_ns als jsonb (verschachteltes Array variabler Laenge je Mikrofon)
	var airNs []byte
	if len(s.AirNs) > 0 {
		airNs, _ = json.Marshal(s.AirNs)
	}

	// shot_no = PC-seitiger Sessionzaehler (1-basiert, inkl. reject); device_seq = ESP32
	_, err := w.pool.Exec(ctx, `
		INSERT INTO shots (
			session_id, shot_no, kind, status, fired_at, series_no,
			device_seq, sensor_hits,
			x_mm, y_mm, ring, decimal_value, is_inner_ten, center_distance,
			air_ns, x_um, y_um, pos_res_um, precision_um, cluster_hits,
			pos_valid, piezo_ns, piezo_ok, clean, reject_reason, device_ts_ms
		) VALUES (
			$1::uuid, $2, $3::shot_kind, $4::shot_status, $5, $6,
			$7, $8,
			$9::float8, $10::float8, $11::smallint, $12::numeric, $13, $14::numeric,
			$15::jsonb, $16::bigint, $17::bigint, $18::bigint, $19::bigint, $20::smallint,
			$21::boolean, $22::bigint, $23::boolean, $24::boolean, $25, $26::bigint
		)
		ON CONFLICT (session_id, shot_no) DO NOTHING`,
		e.sessionID, e.shotNo, kind, status, s.FiredAt, s.SeriesNo,
		s.Seq, s.Hits,
		s.XMM, s.YMM, s.Ring, s.Decimal, s.InnerTen, s.CenterDistance,
		airNs, s.XUm, s.YUm, s.PosResUm, s.PrecisionUm, s.ClusterHits,
		s.PosValid, s.PiezoNs, s.PiezoOk, s.Clean, s.RejectMsg, s.DeviceTsMs,
	)
	if err != nil {
		// Pool bei Verbindungsfehler verwerfen -> ensurePool baut neu auf
		w.pool.Close()
		w.pool = nil
	}
	return err
}

func (w *DBWriter) Close() {
	if w.pool != nil {
		w.pool.Close()
	}
}
