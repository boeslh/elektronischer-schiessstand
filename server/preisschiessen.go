// ============================================================================
// preisschiessen.go – Preisschießen: Anmeldung + Kasse
//
// Eigenständiges Modul (siehe migrations/021_preisschiessen.sql). Verkauf von
// "Scheiben" (Produkte, nicht zu verwechseln mit targets/sessions aus
// store.go) und Sets gegen ein Guthabenkonto je Teilnehmer. Der Saldo wird
// nie redundant gespeichert, sondern aus ps_guthaben_buchungen berechnet
// (view v_ps_guthaben) – gleiches Prinzip wie v_session_results.
// ============================================================================
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation erkennt eine Postgres UNIQUE-Verletzung (Fehlercode 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ----------------------------------------------------------------------------
// Typen
// ----------------------------------------------------------------------------

type Preisschiessen struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	StartsOn            string  `json:"starts_on"`
	EndsOn              string  `json:"ends_on"`
	ShootingType        int     `json:"shooting_type"`
	MaxNegativeGuthaben float64 `json:"max_negative_guthaben"`
	Active              bool    `json:"active"`
	TeilnehmerCount     int     `json:"teilnehmer_count"`
}

type PSScheibe struct {
	ID                string   `json:"id"`
	PreisschiessenID  string   `json:"preisschiessen_id"`
	Name              string   `json:"name"`
	DisciplineID      string   `json:"discipline_id"`
	DisciplineName    string   `json:"discipline_name"`
	Price             float64  `json:"price"`
	TargetColor       string   `json:"target_color"`
	StandaloneErlaubt bool     `json:"standalone_erlaubt"`
	Active            bool     `json:"active"`
	SortOrder         int      `json:"sort_order"`
	MaxProTeilnehmer  *int     `json:"max_pro_teilnehmer"`
	ClassIDs          []string `json:"class_ids"`
	RequiredSetIDs    []string `json:"required_set_ids"`
}

type PSSetItem struct {
	ScheibeID   string `json:"scheibe_id"`
	ScheibeName string `json:"scheibe_name"`
	Quantity    int    `json:"quantity"`
}

type PSSet struct {
	ID               string      `json:"id"`
	PreisschiessenID string      `json:"preisschiessen_id"`
	Name             string      `json:"name"`
	Price            float64     `json:"price"`
	Active           bool        `json:"active"`
	SortOrder        int         `json:"sort_order"`
	MaxProTeilnehmer *int        `json:"max_pro_teilnehmer"`
	Items            []PSSetItem `json:"items"`
	ClassIDs         []string    `json:"class_ids"`
}

type PSTeilnehmer struct {
	ID               string  `json:"id"`
	PreisschiessenID string  `json:"preisschiessen_id"`
	ShooterID        string  `json:"shooter_id"`
	ShooterName      string  `json:"shooter_name"`
	TeilnehmerNr     int     `json:"teilnehmer_nr"`
	ClassID          *string `json:"class_id"`
	ClassName        string  `json:"class_name"`
	Guthaben         float64 `json:"guthaben"`
	ScheibenCount    int     `json:"scheiben_count"`
	CreatedAt        string  `json:"created_at"`
}

// PSKaufScheibeEinheit ist eine einzelne gekaufte Scheibe (mit Seriennummer).
// Status wird aus der verknüpften Session/den Schüssen berechnet, nicht
// gespeichert (vgl. Kommentar in migrations/024_...).
type PSKaufScheibeEinheit struct {
	ID            string  `json:"id"`
	KaufID        string  `json:"kauf_id"`
	ScheibeID     string  `json:"scheibe_id"`
	ScheibeName   string  `json:"scheibe_name"`
	SerialNo      int     `json:"serial_no"`
	SessionID     *string `json:"session_id"`
	LaneNo        *int    `json:"lane_no"`
	ShotCount     int     `json:"shot_count"`
	RequiredShots int     `json:"required_shots"`
	Status        string  `json:"status"` // gekauft | begonnen | beendet
}

type PSKauf struct {
	ID           string  `json:"id"`
	TeilnehmerID string  `json:"teilnehmer_id"`
	Typ          string  `json:"typ"`
	ScheibeID    *string `json:"scheibe_id"`
	SetID        *string `json:"set_id"`
	Name         string  `json:"name"`
	Preis        float64 `json:"preis"`
	PurchasedAt  string  `json:"purchased_at"`
	ReturnedAt   *string `json:"returned_at"`
}

// CartItem referenziert eine Scheibe oder ein Set im (rein clientseitigen)
// Warenkorb - wird erst beim Buchen/Bezahlen an den Server geschickt und
// dort neu validiert (Preis/Verfügbarkeit werden nie vom Client übernommen).
type CartItem struct {
	Typ       string `json:"typ"` // scheibe | set
	ScheibeID string `json:"scheibe_id"`
	SetID     string `json:"set_id"`
}

type PSAuswertungTag struct {
	Datum       string  `json:"datum"`
	Aufladung   float64 `json:"aufladung"`
	Auszahlung  float64 `json:"auszahlung"`
	Bareinnahme float64 `json:"bareinnahme"`
}

type PSAuswertungPosition struct {
	Datum string  `json:"datum"`
	Typ   string  `json:"typ"`
	Name  string  `json:"name"`
	Menge int     `json:"menge"`
	Summe float64 `json:"summe"`
}

type PSAuswertung struct {
	Bareinnahmen []PSAuswertungTag      `json:"bareinnahmen"`
	Verkaeufe    []PSAuswertungPosition `json:"verkaeufe"`
}

// ----------------------------------------------------------------------------
// Store – Preisschießen (Stammdaten)
// ----------------------------------------------------------------------------

func (s *Store) ListPreisschiessen(ctx context.Context) ([]Preisschiessen, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.name, COALESCE(p.starts_on::text,''), COALESCE(p.ends_on::text,''),
		       p.shooting_type, p.max_negative_guthaben, p.active,
		       (SELECT COUNT(*) FROM ps_teilnehmer t WHERE t.preisschiessen_id = p.id)
		FROM preisschiessen p
		ORDER BY p.starts_on DESC NULLS LAST, p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Preisschiessen
	for rows.Next() {
		var p Preisschiessen
		if err := rows.Scan(&p.ID, &p.Name, &p.StartsOn, &p.EndsOn,
			&p.ShootingType, &p.MaxNegativeGuthaben, &p.Active, &p.TeilnehmerCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPreisschiessen(ctx context.Context, id string) (Preisschiessen, error) {
	var p Preisschiessen
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, COALESCE(starts_on::text,''), COALESCE(ends_on::text,''),
		       shooting_type, max_negative_guthaben, active
		FROM preisschiessen WHERE id=$1`, id,
	).Scan(&p.ID, &p.Name, &p.StartsOn, &p.EndsOn, &p.ShootingType, &p.MaxNegativeGuthaben, &p.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, &httpError{code: 404, msg: "Preisschießen nicht gefunden"}
	}
	return p, err
}

func (s *Store) CreatePreisschiessen(ctx context.Context, p Preisschiessen) (string, error) {
	startsOn, _ := time.Parse("2006-01-02", p.StartsOn)
	endsOn, _ := time.Parse("2006-01-02", p.EndsOn)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO preisschiessen (name, starts_on, ends_on, shooting_type, max_negative_guthaben)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		p.Name, nullTime(startsOn), nullTime(endsOn), p.ShootingType, p.MaxNegativeGuthaben,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdatePreisschiessen(ctx context.Context, p Preisschiessen) error {
	startsOn, _ := time.Parse("2006-01-02", p.StartsOn)
	endsOn, _ := time.Parse("2006-01-02", p.EndsOn)
	_, err := s.pool.Exec(ctx, `
		UPDATE preisschiessen SET
		  name = $1, starts_on = $2, ends_on = $3, shooting_type = $4,
		  max_negative_guthaben = $5, active = $6, updated_at = now()
		WHERE id = $7`,
		p.Name, nullTime(startsOn), nullTime(endsOn), p.ShootingType,
		p.MaxNegativeGuthaben, p.Active, p.ID,
	)
	return err
}

// DeletePreisschiessen entfernt ein Preisschießen mit allem Zubehör. Die
// Teilnehmer (und damit deren Käufe/Buchungen, per ON DELETE CASCADE über
// teilnehmer_id) werden VOR den Scheiben/Sets gelöscht: ps_kaeufe verweist
// zusätzlich per RESTRICT auf scheibe_id/set_id (damit ein einzelnes
// DeleteScheibe/DeleteSet bei bestehenden Käufen fehlschlägt) – bei
// automatischem Kaskadieren aus preisschiessen in einer einzigen Anweisung
// wäre die Reihenfolge nicht garantiert und könnte genau daran scheitern.
func (s *Store) DeletePreisschiessen(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ps_teilnehmer WHERE preisschiessen_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM preisschiessen WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ----------------------------------------------------------------------------
// Store – Scheiben
// ----------------------------------------------------------------------------

func (s *Store) ListScheiben(ctx context.Context, preisschiessenID string) ([]PSScheibe, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sc.id, sc.preisschiessen_id, sc.name, sc.discipline_id, d.name,
		       sc.price, COALESCE(sc.target_color,''), sc.standalone_erlaubt,
		       sc.active, sc.sort_order, sc.max_pro_teilnehmer,
		       COALESCE((SELECT array_agg(class_id::text) FROM ps_scheibe_classes WHERE scheibe_id = sc.id), '{}'),
		       COALESCE((SELECT array_agg(required_set_id::text) FROM ps_scheibe_requires_set WHERE scheibe_id = sc.id), '{}')
		FROM ps_scheiben sc
		JOIN disciplines d ON d.id = sc.discipline_id
		WHERE sc.preisschiessen_id = $1
		ORDER BY sc.sort_order, sc.name`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PSScheibe
	for rows.Next() {
		var x PSScheibe
		if err := rows.Scan(&x.ID, &x.PreisschiessenID, &x.Name, &x.DisciplineID, &x.DisciplineName,
			&x.Price, &x.TargetColor, &x.StandaloneErlaubt, &x.Active, &x.SortOrder, &x.MaxProTeilnehmer,
			&x.ClassIDs, &x.RequiredSetIDs); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) CreateScheibe(ctx context.Context, x PSScheibe) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ps_scheiben
		  (preisschiessen_id, name, discipline_id, price, target_color, standalone_erlaubt, active, sort_order, max_pro_teilnehmer)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9)
		RETURNING id`,
		x.PreisschiessenID, x.Name, x.DisciplineID, x.Price, x.TargetColor,
		x.StandaloneErlaubt, x.Active, x.SortOrder, x.MaxProTeilnehmer,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdateScheibe(ctx context.Context, x PSScheibe) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE ps_scheiben SET
		  name = $1, discipline_id = $2, price = $3, target_color = NULLIF($4,''),
		  standalone_erlaubt = $5, active = $6, sort_order = $7, max_pro_teilnehmer = $8
		WHERE id = $9`,
		x.Name, x.DisciplineID, x.Price, x.TargetColor,
		x.StandaloneErlaubt, x.Active, x.SortOrder, x.MaxProTeilnehmer, x.ID,
	)
	return err
}

func (s *Store) DeleteScheibe(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM ps_scheiben WHERE id=$1`, id)
	return err
}

func (s *Store) SetScheibeClasses(ctx context.Context, scheibeID string, classIDs []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ps_scheibe_classes WHERE scheibe_id=$1`, scheibeID); err != nil {
		return err
	}
	for _, cid := range classIDs {
		if cid == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_scheibe_classes (scheibe_id, class_id) VALUES ($1,$2)`, scheibeID, cid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SetScheibeRequiredSets(ctx context.Context, scheibeID string, setIDs []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ps_scheibe_requires_set WHERE scheibe_id=$1`, scheibeID); err != nil {
		return err
	}
	for _, sid := range setIDs {
		if sid == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_scheibe_requires_set (scheibe_id, required_set_id) VALUES ($1,$2)`, scheibeID, sid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ----------------------------------------------------------------------------
// Store – Sets
// ----------------------------------------------------------------------------

func (s *Store) ListSets(ctx context.Context, preisschiessenID string) ([]PSSet, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, preisschiessen_id, name, price, active, sort_order, max_pro_teilnehmer
		FROM ps_sets WHERE preisschiessen_id=$1 ORDER BY sort_order, name`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	var out []PSSet
	var ids []string
	for rows.Next() {
		var x PSSet
		if err := rows.Scan(&x.ID, &x.PreisschiessenID, &x.Name, &x.Price, &x.Active, &x.SortOrder, &x.MaxProTeilnehmer); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
		ids = append(ids, x.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}

	itemsBySet, err := s.loadSetItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	classesBySet, err := s.loadSetClasses(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Items = itemsBySet[out[i].ID]
		out[i].ClassIDs = classesBySet[out[i].ID]
	}
	return out, nil
}

func (s *Store) loadSetItems(ctx context.Context, setIDs []string) (map[string][]PSSetItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT si.set_id, si.scheibe_id, sc.name, si.quantity
		FROM ps_set_items si
		JOIN ps_scheiben sc ON sc.id = si.scheibe_id
		WHERE si.set_id = ANY($1::uuid[])
		ORDER BY sc.sort_order, sc.name`, setIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]PSSetItem{}
	for rows.Next() {
		var setID string
		var it PSSetItem
		if err := rows.Scan(&setID, &it.ScheibeID, &it.ScheibeName, &it.Quantity); err != nil {
			return nil, err
		}
		out[setID] = append(out[setID], it)
	}
	return out, rows.Err()
}

func (s *Store) loadSetClasses(ctx context.Context, setIDs []string) (map[string][]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT set_id, class_id::text FROM ps_set_classes WHERE set_id = ANY($1::uuid[])`, setIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var setID, classID string
		if err := rows.Scan(&setID, &classID); err != nil {
			return nil, err
		}
		out[setID] = append(out[setID], classID)
	}
	return out, rows.Err()
}

func (s *Store) CreateSet(ctx context.Context, x PSSet) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ps_sets (preisschiessen_id, name, price, active, sort_order, max_pro_teilnehmer)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		x.PreisschiessenID, x.Name, x.Price, x.Active, x.SortOrder, x.MaxProTeilnehmer,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdateSet(ctx context.Context, x PSSet) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE ps_sets SET name=$1, price=$2, active=$3, sort_order=$4, max_pro_teilnehmer=$5 WHERE id=$6`,
		x.Name, x.Price, x.Active, x.SortOrder, x.MaxProTeilnehmer, x.ID)
	return err
}

func (s *Store) DeleteSet(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM ps_sets WHERE id=$1`, id)
	return err
}

func (s *Store) SetSetItems(ctx context.Context, setID string, items []PSSetItem) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ps_set_items WHERE set_id=$1`, setID); err != nil {
		return err
	}
	for _, it := range items {
		if it.ScheibeID == "" {
			continue
		}
		qty := it.Quantity
		if qty < 1 {
			qty = 1
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_set_items (set_id, scheibe_id, quantity) VALUES ($1,$2,$3)`,
			setID, it.ScheibeID, qty); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SetSetClasses(ctx context.Context, setID string, classIDs []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ps_set_classes WHERE set_id=$1`, setID); err != nil {
		return err
	}
	for _, cid := range classIDs {
		if cid == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_set_classes (set_id, class_id) VALUES ($1,$2)`, setID, cid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ----------------------------------------------------------------------------
// Store – Teilnehmer
// ----------------------------------------------------------------------------

// computeShooterClass ordnet einen Schützen anhand Geburtsjahr + Referenzjahr
// + Schießart einer Sportklasse zu (engster Altersbereich gewinnt). Gleiche
// Logik wie Store.RecalculateSportsClasses (store.go), hier parametrisiert
// über ein beliebiges Referenzjahr (Preisschießen-Ende) statt fest über das
// aktuelle Kalenderjahr, und über shooting_type statt fest type=0.
func (s *Store) computeShooterClass(ctx context.Context, tx pgx.Tx, shooterID string, referenceYear, shootingType int) (*string, error) {
	var gender string
	var byear *int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(gender,''), EXTRACT(YEAR FROM birth_date)::int FROM shooters WHERE id=$1`,
		shooterID,
	).Scan(&gender, &byear); err != nil {
		return nil, err
	}
	if byear == nil {
		return nil, nil
	}
	var sexCode int
	switch gender {
	case "M":
		sexCode = 1
	case "W":
		sexCode = 0
	default:
		return nil, nil
	}
	age := referenceYear - *byear

	rows, err := tx.Query(ctx,
		`SELECT id, min_age, max_age, sex FROM shooter_classes WHERE type=$1`, shootingType)
	if err != nil {
		return nil, err
	}
	type classRow struct {
		id             string
		minAge, maxAge *int
		sex            *int
	}
	var classes []classRow
	for rows.Next() {
		var c classRow
		if err := rows.Scan(&c.id, &c.minAge, &c.maxAge, &c.sex); err != nil {
			rows.Close()
			return nil, err
		}
		classes = append(classes, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	const unboundedLow, unboundedHigh = -32768, 32767
	rangeWidth := func(c classRow) int {
		lo, hi := unboundedLow, unboundedHigh
		if c.minAge != nil {
			lo = *c.minAge
		}
		if c.maxAge != nil {
			hi = *c.maxAge
		}
		return hi - lo
	}

	var best *classRow
	for i := range classes {
		c := &classes[i]
		if c.sex != nil && *c.sex != sexCode {
			continue
		}
		if c.minAge != nil && age < *c.minAge {
			continue
		}
		if c.maxAge != nil && age > *c.maxAge {
			continue
		}
		if best == nil || rangeWidth(*c) < rangeWidth(*best) {
			best = c
		}
	}
	if best == nil {
		return nil, nil
	}
	return &best.id, nil
}

func (s *Store) ListTeilnehmer(ctx context.Context, preisschiessenID, search string) ([]PSTeilnehmer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.preisschiessen_id, t.shooter_id,
		       sh.last_name || ', ' || sh.first_name,
		       t.teilnehmer_nr, t.class_id::text, COALESCE(sc.name,''),
		       COALESCE(g.guthaben,0),
		       (SELECT COUNT(*) FROM ps_kauf_scheiben ks
		          JOIN ps_kaeufe k2 ON k2.id = ks.kauf_id
		         WHERE k2.teilnehmer_id = t.id AND k2.returned_at IS NULL),
		       t.created_at::text
		FROM ps_teilnehmer t
		JOIN shooters sh ON sh.id = t.shooter_id
		LEFT JOIN shooter_classes sc ON sc.id = t.class_id
		LEFT JOIN v_ps_guthaben g ON g.teilnehmer_id = t.id
		WHERE t.preisschiessen_id = $1
		  AND ($2 = '' OR sh.last_name ILIKE '%'||$2||'%' OR sh.first_name ILIKE '%'||$2||'%'
		               OR t.teilnehmer_nr::text = $2)
		ORDER BY t.teilnehmer_nr`, preisschiessenID, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PSTeilnehmer
	for rows.Next() {
		var t PSTeilnehmer
		if err := rows.Scan(&t.ID, &t.PreisschiessenID, &t.ShooterID, &t.ShooterName,
			&t.TeilnehmerNr, &t.ClassID, &t.ClassName, &t.Guthaben, &t.ScheibenCount, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTeilnehmer(ctx context.Context, id string) (PSTeilnehmer, error) {
	var t PSTeilnehmer
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.preisschiessen_id, t.shooter_id,
		       sh.last_name || ', ' || sh.first_name,
		       t.teilnehmer_nr, t.class_id::text, COALESCE(sc.name,''),
		       COALESCE(g.guthaben,0),
		       (SELECT COUNT(*) FROM ps_kauf_scheiben ks
		          JOIN ps_kaeufe k2 ON k2.id = ks.kauf_id
		         WHERE k2.teilnehmer_id = t.id AND k2.returned_at IS NULL),
		       t.created_at::text
		FROM ps_teilnehmer t
		JOIN shooters sh ON sh.id = t.shooter_id
		LEFT JOIN shooter_classes sc ON sc.id = t.class_id
		LEFT JOIN v_ps_guthaben g ON g.teilnehmer_id = t.id
		WHERE t.id = $1`, id,
	).Scan(&t.ID, &t.PreisschiessenID, &t.ShooterID, &t.ShooterName,
		&t.TeilnehmerNr, &t.ClassID, &t.ClassName, &t.Guthaben, &t.ScheibenCount, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, &httpError{code: 404, msg: "Teilnehmer nicht gefunden"}
	}
	return t, err
}

// ListKaufScheibenEinheiten liefert alle einzelnen gekauften Scheiben eines
// Teilnehmers (aus nicht zurückgegebenen Käufen) mit berechnetem Status.
func (s *Store) ListKaufScheibenEinheiten(ctx context.Context, teilnehmerID string) ([]PSKaufScheibeEinheit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ks.id, ks.kauf_id, ks.scheibe_id, sc.name, ks.serial_no, ks.session_id::text,
		       l.lane_no,
		       (SELECT COUNT(*) FROM shots sh WHERE sh.session_id = ks.session_id AND sh.status <> 'rejected'),
		       COALESCE(sr.shot_count, 0), d.match_shot_count
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k ON k.id = ks.kauf_id
		JOIN ps_scheiben sc ON sc.id = ks.scheibe_id
		JOIN disciplines d ON d.id = sc.discipline_id
		LEFT JOIN sessions se ON se.id = ks.session_id
		LEFT JOIN lanes l ON l.id = se.lane_id
		LEFT JOIN v_session_results sr ON sr.session_id = ks.session_id
		WHERE k.teilnehmer_id = $1 AND k.returned_at IS NULL
		ORDER BY ks.serial_no`, teilnehmerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PSKaufScheibeEinheit
	for rows.Next() {
		var x PSKaufScheibeEinheit
		var anyShots, matchShots, required int
		if err := rows.Scan(&x.ID, &x.KaufID, &x.ScheibeID, &x.ScheibeName, &x.SerialNo, &x.SessionID,
			&x.LaneNo, &anyShots, &matchShots, &required); err != nil {
			return nil, err
		}
		x.ShotCount = matchShots
		x.RequiredShots = required
		x.Status = "gekauft"
		if anyShots > 0 {
			x.Status = "begonnen"
		}
		if required > 0 && matchShots >= required {
			x.Status = "beendet"
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// createKaufEinheiten legt für einen Kauf die einzelnen Scheiben-Einheiten
// (mit fortlaufender Seriennummer je Preisschießen) an - eine Zeile je
// gekaufter Scheibe, bei Sets entsprechend der Stückzahl je enthaltener
// Scheibe. Muss innerhalb derselben Transaktion wie der ps_kaeufe-Insert
// laufen (atomarer Seriennummern-Zähler auf preisschiessen).
func (s *Store) createKaufEinheiten(ctx context.Context, tx pgx.Tx, preisschiessenID, kaufID string, scheibeIDs []string) error {
	n := len(scheibeIDs)
	if n == 0 {
		return nil
	}
	var startSerial int
	if err := tx.QueryRow(ctx, `
		UPDATE preisschiessen SET next_scheibe_serial = next_scheibe_serial + $2
		WHERE id = $1 RETURNING next_scheibe_serial - $2`,
		preisschiessenID, n,
	).Scan(&startSerial); err != nil {
		return err
	}
	for i, scheibeID := range scheibeIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_kauf_scheiben (preisschiessen_id, kauf_id, scheibe_id, serial_no)
			VALUES ($1,$2,$3,$4)`,
			preisschiessenID, kaufID, scheibeID, startSerial+i); err != nil {
			return err
		}
	}
	return nil
}

// CreateTeilnehmer meldet einen Schützen für ein Preisschießen an: berechnet
// die Sportklasse anhand des Preisschießen-Endes und vergibt atomar die
// nächste Teilnehmernummer (Zähler auf der preisschiessen-Zeile).
func (s *Store) CreateTeilnehmer(ctx context.Context, preisschiessenID, shooterID string) (PSTeilnehmer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PSTeilnehmer{}, err
	}
	defer tx.Rollback(ctx)

	var endsOn *time.Time
	var shootingType int
	if err := tx.QueryRow(ctx,
		`SELECT ends_on, shooting_type FROM preisschiessen WHERE id=$1`, preisschiessenID,
	).Scan(&endsOn, &shootingType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PSTeilnehmer{}, &httpError{code: 404, msg: "Preisschießen nicht gefunden"}
		}
		return PSTeilnehmer{}, err
	}
	refYear := time.Now().Year()
	if endsOn != nil {
		refYear = endsOn.Year()
	}

	classIDPtr, err := s.computeShooterClass(ctx, tx, shooterID, refYear, shootingType)
	if err != nil {
		return PSTeilnehmer{}, err
	}
	classID := ""
	if classIDPtr != nil {
		classID = *classIDPtr
	}

	var nr int
	if err := tx.QueryRow(ctx, `
		UPDATE preisschiessen SET next_teilnehmer_nr = next_teilnehmer_nr + 1
		WHERE id=$1 RETURNING next_teilnehmer_nr - 1`, preisschiessenID,
	).Scan(&nr); err != nil {
		return PSTeilnehmer{}, err
	}

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO ps_teilnehmer (preisschiessen_id, shooter_id, teilnehmer_nr, class_id)
		VALUES ($1,$2,$3,NULLIF($4,'')::uuid) RETURNING id`,
		preisschiessenID, shooterID, nr, classID,
	).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			return PSTeilnehmer{}, &httpError{code: 409, msg: "Schütze ist für dieses Preisschießen bereits angemeldet"}
		}
		return PSTeilnehmer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PSTeilnehmer{}, err
	}
	return s.GetTeilnehmer(ctx, id)
}

// ListAngebot liefert die für einen Teilnehmer aktuell kaufbaren Scheiben
// und Sets: aktiv, Klassen-Restriktion beachtet (keine Zeile = offen für
// alle), bei Scheiben zusätzlich standalone_erlaubt und Set-Gating
// (ps_scheibe_requires_set – mindestens eines der Sets muss aktuell,
// also nicht zurückgegeben, gekauft sein).
func (s *Store) ListAngebot(ctx context.Context, teilnehmerID string) ([]PSScheibe, []PSSet, error) {
	var preisschiessenID string
	var classID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT preisschiessen_id, class_id::text FROM ps_teilnehmer WHERE id=$1`, teilnehmerID,
	).Scan(&preisschiessenID, &classID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, &httpError{code: 404, msg: "Teilnehmer nicht gefunden"}
		}
		return nil, nil, err
	}

	scheibenRows, err := s.pool.Query(ctx, `
		SELECT sc.id, sc.preisschiessen_id, sc.name, sc.discipline_id, d.name, sc.price,
		       COALESCE(sc.target_color,''), sc.standalone_erlaubt, sc.active, sc.sort_order,
		       sc.max_pro_teilnehmer
		FROM ps_scheiben sc
		JOIN disciplines d ON d.id = sc.discipline_id
		WHERE sc.preisschiessen_id = $1
		  AND sc.active AND sc.standalone_erlaubt
		  AND (
		        NOT EXISTS (SELECT 1 FROM ps_scheibe_classes psc WHERE psc.scheibe_id = sc.id)
		        OR EXISTS (SELECT 1 FROM ps_scheibe_classes psc WHERE psc.scheibe_id = sc.id AND psc.class_id = $2)
		      )
		  AND (
		        NOT EXISTS (SELECT 1 FROM ps_scheibe_requires_set r WHERE r.scheibe_id = sc.id)
		        OR EXISTS (
		             SELECT 1 FROM ps_scheibe_requires_set r
		             JOIN ps_kaeufe k ON k.set_id = r.required_set_id
		                              AND k.teilnehmer_id = $3 AND k.returned_at IS NULL
		             WHERE r.scheibe_id = sc.id
		           )
		      )
		  AND (
		        sc.max_pro_teilnehmer IS NULL
		        OR (SELECT COUNT(*) FROM ps_kaeufe k WHERE k.teilnehmer_id = $3
		              AND k.typ = 'scheibe' AND k.scheibe_id = sc.id AND k.returned_at IS NULL)
		            < sc.max_pro_teilnehmer
		      )
		ORDER BY sc.sort_order, sc.name`, preisschiessenID, classID, teilnehmerID)
	if err != nil {
		return nil, nil, err
	}
	var scheiben []PSScheibe
	for scheibenRows.Next() {
		var x PSScheibe
		if err := scheibenRows.Scan(&x.ID, &x.PreisschiessenID, &x.Name, &x.DisciplineID, &x.DisciplineName,
			&x.Price, &x.TargetColor, &x.StandaloneErlaubt, &x.Active, &x.SortOrder, &x.MaxProTeilnehmer); err != nil {
			scheibenRows.Close()
			return nil, nil, err
		}
		scheiben = append(scheiben, x)
	}
	if err := scheibenRows.Err(); err != nil {
		scheibenRows.Close()
		return nil, nil, err
	}
	scheibenRows.Close()

	setRows, err := s.pool.Query(ctx, `
		SELECT st.id, st.preisschiessen_id, st.name, st.price, st.active, st.sort_order,
		       st.max_pro_teilnehmer
		FROM ps_sets st
		WHERE st.preisschiessen_id = $1 AND st.active
		  AND (
		        NOT EXISTS (SELECT 1 FROM ps_set_classes psc WHERE psc.set_id = st.id)
		        OR EXISTS (SELECT 1 FROM ps_set_classes psc WHERE psc.set_id = st.id AND psc.class_id = $2)
		      )
		  AND (
		        st.max_pro_teilnehmer IS NULL
		        OR (SELECT COUNT(*) FROM ps_kaeufe k WHERE k.teilnehmer_id = $3
		              AND k.typ = 'set' AND k.set_id = st.id AND k.returned_at IS NULL)
		            < st.max_pro_teilnehmer
		      )
		ORDER BY st.sort_order, st.name`, preisschiessenID, classID, teilnehmerID)
	if err != nil {
		return nil, nil, err
	}
	var sets []PSSet
	var setIDs []string
	for setRows.Next() {
		var x PSSet
		if err := setRows.Scan(&x.ID, &x.PreisschiessenID, &x.Name, &x.Price, &x.Active, &x.SortOrder, &x.MaxProTeilnehmer); err != nil {
			setRows.Close()
			return nil, nil, err
		}
		sets = append(sets, x)
		setIDs = append(setIDs, x.ID)
	}
	if err := setRows.Err(); err != nil {
		setRows.Close()
		return nil, nil, err
	}
	setRows.Close()

	if len(setIDs) > 0 {
		itemsBySet, err := s.loadSetItems(ctx, setIDs)
		if err != nil {
			return nil, nil, err
		}
		for i := range sets {
			sets[i].Items = itemsBySet[sets[i].ID]
		}
	}

	return scheiben, sets, nil
}

func (s *Store) ListKonto(ctx context.Context, teilnehmerID string) ([]PSKauf, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT k.id, k.teilnehmer_id, k.typ, k.scheibe_id::text, k.set_id::text,
		       COALESCE(sc.name, st.name), k.preis,
		       k.purchased_at::text, k.returned_at::text
		FROM ps_kaeufe k
		LEFT JOIN ps_scheiben sc ON sc.id = k.scheibe_id
		LEFT JOIN ps_sets st ON st.id = k.set_id
		WHERE k.teilnehmer_id = $1
		ORDER BY k.purchased_at DESC`, teilnehmerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PSKauf
	for rows.Next() {
		var k PSKauf
		if err := rows.Scan(&k.ID, &k.TeilnehmerID, &k.Typ, &k.ScheibeID, &k.SetID,
			&k.Name, &k.Preis, &k.PurchasedAt, &k.ReturnedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) BuchAufladung(ctx context.Context, teilnehmerID string, betrag float64, notiz string) error {
	if betrag <= 0 {
		return &httpError{code: 400, msg: "Betrag muss positiv sein"}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ps_guthaben_buchungen (teilnehmer_id, typ, betrag, notiz)
		VALUES ($1,'aufladung',$2,NULLIF($3,''))`, teilnehmerID, betrag, notiz)
	return err
}

// purchaseItem prüft Verfügbarkeit (aktiv, Klasse, standalone/Gating,
// Kauflimit) einer einzelnen Warenkorb-Position, legt den ps_kaeufe-Eintrag
// samt Scheiben-Einheiten an und gibt dessen Preis zurück - bucht aber NOCH
// KEINE Guthaben-Belastung (das übernimmt der Aufrufer für den ganzen
// Warenkorb auf einmal, siehe purchaseItems). Muss innerhalb einer
// Transaktion laufen, da Preis, Verfügbarkeit und Limit-Zählung sich sonst
// zwischen mehreren Positionen desselben Warenkorbs widersprechen könnten.
func (s *Store) purchaseItem(ctx context.Context, tx pgx.Tx, teilnehmerID, preisschiessenID string, item CartItem) (string, float64, error) {
	var preis float64
	var err error
	if item.Typ == "scheibe" {
		err = tx.QueryRow(ctx, `
			SELECT sc.price FROM ps_scheiben sc
			WHERE sc.id = $1 AND sc.active AND sc.standalone_erlaubt
			  AND (
			        NOT EXISTS (SELECT 1 FROM ps_scheibe_classes psc WHERE psc.scheibe_id = sc.id)
			        OR EXISTS (
			             SELECT 1 FROM ps_scheibe_classes psc
			             JOIN ps_teilnehmer t ON t.id = $2
			             WHERE psc.scheibe_id = sc.id AND psc.class_id = t.class_id
			           )
			      )
			  AND (
			        NOT EXISTS (SELECT 1 FROM ps_scheibe_requires_set r WHERE r.scheibe_id = sc.id)
			        OR EXISTS (
			             SELECT 1 FROM ps_scheibe_requires_set r
			             JOIN ps_kaeufe k ON k.set_id = r.required_set_id
			                              AND k.teilnehmer_id = $2 AND k.returned_at IS NULL
			             WHERE r.scheibe_id = sc.id
			           )
			      )
			  AND (
			        sc.max_pro_teilnehmer IS NULL
			        OR (SELECT COUNT(*) FROM ps_kaeufe k WHERE k.teilnehmer_id = $2
			              AND k.typ = 'scheibe' AND k.scheibe_id = sc.id AND k.returned_at IS NULL)
			            < sc.max_pro_teilnehmer
			      )`, item.ScheibeID, teilnehmerID).Scan(&preis)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT st.price FROM ps_sets st
			WHERE st.id = $1 AND st.active
			  AND (
			        NOT EXISTS (SELECT 1 FROM ps_set_classes psc WHERE psc.set_id = st.id)
			        OR EXISTS (
			             SELECT 1 FROM ps_set_classes psc
			             JOIN ps_teilnehmer t ON t.id = $2
			             WHERE psc.set_id = st.id AND psc.class_id = t.class_id
			           )
			      )
			  AND (
			        st.max_pro_teilnehmer IS NULL
			        OR (SELECT COUNT(*) FROM ps_kaeufe k WHERE k.teilnehmer_id = $2
			              AND k.typ = 'set' AND k.set_id = st.id AND k.returned_at IS NULL)
			            < st.max_pro_teilnehmer
			      )`, item.SetID, teilnehmerID).Scan(&preis)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, &httpError{code: 400, msg: "Nicht verfügbar (Klasse, Set-Voraussetzung, Kauflimit erreicht oder inaktiv)"}
		}
		return "", 0, err
	}

	var kaufID string
	var scheibeIDs []string
	if item.Typ == "scheibe" {
		err = tx.QueryRow(ctx, `
			INSERT INTO ps_kaeufe (teilnehmer_id, typ, scheibe_id, preis)
			VALUES ($1,'scheibe',$2,$3) RETURNING id`, teilnehmerID, item.ScheibeID, preis).Scan(&kaufID)
		scheibeIDs = []string{item.ScheibeID}
	} else {
		err = tx.QueryRow(ctx, `
			INSERT INTO ps_kaeufe (teilnehmer_id, typ, set_id, preis)
			VALUES ($1,'set',$2,$3) RETURNING id`, teilnehmerID, item.SetID, preis).Scan(&kaufID)
		if err == nil {
			itemRows, iErr := tx.Query(ctx,
				`SELECT scheibe_id, quantity FROM ps_set_items WHERE set_id=$1`, item.SetID)
			if iErr != nil {
				return "", 0, iErr
			}
			for itemRows.Next() {
				var scID string
				var qty int
				if sErr := itemRows.Scan(&scID, &qty); sErr != nil {
					itemRows.Close()
					return "", 0, sErr
				}
				for i := 0; i < qty; i++ {
					scheibeIDs = append(scheibeIDs, scID)
				}
			}
			if iErr := itemRows.Err(); iErr != nil {
				itemRows.Close()
				return "", 0, iErr
			}
			itemRows.Close()
		}
	}
	if err != nil {
		return "", 0, err
	}

	if err := s.createKaufEinheiten(ctx, tx, preisschiessenID, kaufID, scheibeIDs); err != nil {
		return "", 0, err
	}
	return kaufID, preis, nil
}

// purchaseItems bucht mehrere Warenkorb-Positionen nacheinander (jede prüft
// Verfügbarkeit/Limit erneut gegen den zwischenzeitlichen Stand innerhalb
// derselben Transaktion) und schreibt je Position eine 'kauf'-Buchung ins
// Guthaben-Ledger. Gibt die entstandenen Kauf-IDs und die Gesamtsumme
// zurück; der Guthaben-Check (reicht es, darf es ins Minus) obliegt dem
// jeweiligen Aufrufer (BuchWarenkorb bzw. Bezahlen).
func (s *Store) purchaseItems(ctx context.Context, tx pgx.Tx, teilnehmerID, preisschiessenID string, items []CartItem) ([]string, float64, error) {
	var kaufIDs []string
	var total float64
	for _, item := range items {
		kaufID, preis, err := s.purchaseItem(ctx, tx, teilnehmerID, preisschiessenID, item)
		if err != nil {
			return nil, 0, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_guthaben_buchungen (teilnehmer_id, typ, betrag, kauf_id)
			VALUES ($1,'kauf',$2,$3)`, teilnehmerID, -preis, kaufID); err != nil {
			return nil, 0, err
		}
		kaufIDs = append(kaufIDs, kaufID)
		total += preis
	}
	return kaufIDs, total, nil
}

// BuchWarenkorb ist die schnelle Variante des Bezahlens: bucht den ganzen
// Warenkorb auf einmal direkt gegen das vorhandene Guthaben - aber nur,
// wenn dieses den Gesamtpreis bereits ohne weitere Bareinzahlung deckt
// (strikt, kein Minus erlaubt). Reicht das Guthaben nicht, muss stattdessen
// Store.Bezahlen (mit Bareinzahlung) verwendet werden.
func (s *Store) BuchWarenkorb(ctx context.Context, teilnehmerID string, items []CartItem) ([]string, error) {
	if len(items) == 0 {
		return nil, &httpError{code: 400, msg: "Warenkorb ist leer"}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var preisschiessenID string
	var guthaben float64
	if err := tx.QueryRow(ctx, `
		SELECT t.preisschiessen_id, COALESCE(g.guthaben,0)
		FROM ps_teilnehmer t
		LEFT JOIN v_ps_guthaben g ON g.teilnehmer_id = t.id
		WHERE t.id = $1`, teilnehmerID,
	).Scan(&preisschiessenID, &guthaben); err != nil {
		return nil, err
	}

	kaufIDs, total, err := s.purchaseItems(ctx, tx, teilnehmerID, preisschiessenID, items)
	if err != nil {
		return nil, err
	}
	if guthaben-total < 0 {
		return nil, &httpError{code: 400, msg: fmt.Sprintf(
			"Guthaben reicht nicht aus (%.2f € benötigt, %.2f € vorhanden)", total, guthaben)}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return kaufIDs, nil
}

// Ruckgabe storniert einen Kauf vollständig (nur ganze Sets bzw. einzeln
// gekaufte Scheiben – keine Teilrückgabe aus einem Set) und erstattet den
// Kaufpreis dem Guthaben.
func (s *Store) Ruckgabe(ctx context.Context, kaufID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var teilnehmerID string
	var preis float64
	var returnedAt *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT teilnehmer_id, preis, returned_at FROM ps_kaeufe WHERE id=$1`, kaufID,
	).Scan(&teilnehmerID, &preis, &returnedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &httpError{code: 404, msg: "Kauf nicht gefunden"}
		}
		return err
	}
	if returnedAt != nil {
		return &httpError{code: 400, msg: "Bereits zurückgegeben"}
	}

	var angeschossen bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM ps_kauf_scheiben ks
		  WHERE ks.kauf_id = $1 AND ks.session_id IS NOT NULL
		    AND EXISTS (SELECT 1 FROM shots sh WHERE sh.session_id = ks.session_id AND sh.status <> 'rejected')
		)`, kaufID).Scan(&angeschossen); err != nil {
		return err
	}
	if angeschossen {
		return &httpError{code: 400, msg: "Bereits beschossen – keine Rückgabe mehr möglich"}
	}

	if _, err := tx.Exec(ctx, `UPDATE ps_kaeufe SET returned_at = now() WHERE id=$1`, kaufID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ps_kauf_scheiben WHERE kauf_id=$1`, kaufID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ps_guthaben_buchungen (teilnehmer_id, typ, betrag, kauf_id)
		VALUES ($1,'rueckgabe',$2,$3)`, teilnehmerID, preis, kaufID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AssignPreisschiessenLane weist eine gekaufte Scheiben-Einheit einem Stand
// zu. Nutzt die bestehende Standbelegung (Store.AssignLane) wieder - legt
// also eine ganz normale Session an, die am Stand-PC sofort erscheint; hier
// wird nur zusätzlich die entstandene Session mit der Scheiben-Einheit
// verknüpft, damit ListKaufScheibenEinheiten den Beschossen-Status ableiten
// kann. Eine bereits zugewiesene Einheit kann nicht erneut zugewiesen werden
// (Standwechsel läuft weiterhin über die bestehende Standaktion-Seite).
func (s *Store) AssignPreisschiessenLane(ctx context.Context, kaufScheibeID string, laneNo int) (string, error) {
	var existingSession *string
	var scheibeID, shooterID string
	if err := s.pool.QueryRow(ctx, `
		SELECT ks.session_id::text, ks.scheibe_id, sh.id
		FROM ps_kauf_scheiben ks
		JOIN ps_kaeufe k ON k.id = ks.kauf_id
		JOIN ps_teilnehmer t ON t.id = k.teilnehmer_id
		JOIN shooters sh ON sh.id = t.shooter_id
		WHERE ks.id = $1`, kaufScheibeID,
	).Scan(&existingSession, &scheibeID, &shooterID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", &httpError{code: 404, msg: "Scheiben-Einheit nicht gefunden"}
		}
		return "", err
	}
	if existingSession != nil {
		return "", &httpError{code: 400, msg: "Bereits einem Stand zugewiesen"}
	}

	var disciplineID string
	if err := s.pool.QueryRow(ctx,
		`SELECT discipline_id FROM ps_scheiben WHERE id=$1`, scheibeID,
	).Scan(&disciplineID); err != nil {
		return "", err
	}

	sessionID, err := s.AssignLane(ctx, laneNo, shooterID, disciplineID, "")
	if err != nil {
		return "", err
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE ps_kauf_scheiben SET session_id=$1 WHERE id=$2`, sessionID, kaufScheibeID,
	); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (s *Store) Auszahlung(ctx context.Context, teilnehmerID string, betrag float64) error {
	if betrag <= 0 {
		return &httpError{code: 400, msg: "Betrag muss positiv sein"}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var guthaben float64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(guthaben,0) FROM v_ps_guthaben WHERE teilnehmer_id=$1`, teilnehmerID,
	).Scan(&guthaben); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if betrag > guthaben {
		return &httpError{code: 400, msg: "Betrag übersteigt Guthaben"}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ps_guthaben_buchungen (teilnehmer_id, typ, betrag)
		VALUES ($1,'auszahlung',$2)`, teilnehmerID, -betrag); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Bezahlen ist der bewusste, vom Kaufvorgang getrennte Bezahlschritt an der
// Kasse: der erhaltene Betrag (z.B. bar) wird als Aufladung gebucht, danach
// wird der übergebene Warenkorb gebucht (leer = reine Ein-/Auszahlung ohne
// neuen Kauf, z.B. um nur ein zuvor entstandenes Minus auszugleichen oder
// Restguthaben auszuzahlen). Das Ergebnis darf bis zum je Preisschießen
// konfigurierten Limit ins Minus rutschen. Bleibt danach ein positiver Rest
// (mehr bezahlt als geschuldet), entscheidet restAuszahlen, ob dieser Rest
// als Guthaben stehen bleibt oder sofort als Rückgeld ausgezahlt wird.
// Gibt die entstandenen Kauf-IDs und das ausgezahlte Rückgeld zurück (0,
// wenn nichts auszuzahlen war).
func (s *Store) Bezahlen(ctx context.Context, teilnehmerID string, items []CartItem, erhalten float64, restAuszahlen bool) ([]string, float64, error) {
	if erhalten < 0 {
		return nil, 0, &httpError{code: 400, msg: "Betrag darf nicht negativ sein"}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)

	var preisschiessenID string
	var maxNegative float64
	if err := tx.QueryRow(ctx, `
		SELECT t.preisschiessen_id, p.max_negative_guthaben
		FROM ps_teilnehmer t
		JOIN preisschiessen p ON p.id = t.preisschiessen_id
		WHERE t.id = $1`, teilnehmerID,
	).Scan(&preisschiessenID, &maxNegative); err != nil {
		return nil, 0, err
	}

	if erhalten > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_guthaben_buchungen (teilnehmer_id, typ, betrag, notiz)
			VALUES ($1,'aufladung',$2,'Bezahlung an der Kasse')`, teilnehmerID, erhalten); err != nil {
			return nil, 0, err
		}
	}

	kaufIDs, _, err := s.purchaseItems(ctx, tx, teilnehmerID, preisschiessenID, items)
	if err != nil {
		return nil, 0, err
	}

	var guthaben float64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(guthaben,0) FROM v_ps_guthaben WHERE teilnehmer_id=$1`, teilnehmerID,
	).Scan(&guthaben); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, err
	}
	if guthaben < -maxNegative {
		return nil, 0, &httpError{code: 400, msg: fmt.Sprintf(
			"Guthaben reicht auch nach der Zahlung nicht aus (max. %.2f € Minus erlaubt)", maxNegative)}
	}

	var rueckgeld float64
	if restAuszahlen && guthaben > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_guthaben_buchungen (teilnehmer_id, typ, betrag, notiz)
			VALUES ($1,'auszahlung',$2,'Rückgeld')`, teilnehmerID, -guthaben); err != nil {
			return nil, 0, err
		}
		rueckgeld = guthaben
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return kaufIDs, rueckgeld, nil
}

// KasseAuswertung liefert zwei getrennte Kennzahlen: Bareinnahmen
// (Aufladung./. Auszahlung, echter Bargeldfluss) und Verkäufe nach
// Scheiben-/Set-Typ (verkaufte Ware, ohne zurückgegebene Käufe) – jeweils
// pro Tag, optional gefiltert auf einen bestimmten Tag.
func (s *Store) KasseAuswertung(ctx context.Context, preisschiessenID, datum string) (PSAuswertung, error) {
	var out PSAuswertung

	bRows, err := s.pool.Query(ctx, `
		SELECT b.created_at::date::text,
		       COALESCE(SUM(b.betrag) FILTER (WHERE b.typ='aufladung'), 0),
		       COALESCE(SUM(-b.betrag) FILTER (WHERE b.typ='auszahlung'), 0)
		FROM ps_guthaben_buchungen b
		JOIN ps_teilnehmer t ON t.id = b.teilnehmer_id
		WHERE t.preisschiessen_id = $1
		  AND b.typ IN ('aufladung','auszahlung')
		  AND ($2 = '' OR b.created_at::date::text = $2)
		GROUP BY b.created_at::date
		ORDER BY b.created_at::date`, preisschiessenID, datum)
	if err != nil {
		return out, err
	}
	for bRows.Next() {
		var row PSAuswertungTag
		if err := bRows.Scan(&row.Datum, &row.Aufladung, &row.Auszahlung); err != nil {
			bRows.Close()
			return out, err
		}
		row.Bareinnahme = row.Aufladung - row.Auszahlung
		out.Bareinnahmen = append(out.Bareinnahmen, row)
	}
	if err := bRows.Err(); err != nil {
		bRows.Close()
		return out, err
	}
	bRows.Close()

	kRows, err := s.pool.Query(ctx, `
		SELECT k.purchased_at::date::text, k.typ, COALESCE(sc.name, st.name), COUNT(*), SUM(k.preis)
		FROM ps_kaeufe k
		JOIN ps_teilnehmer t ON t.id = k.teilnehmer_id
		LEFT JOIN ps_scheiben sc ON sc.id = k.scheibe_id
		LEFT JOIN ps_sets st ON st.id = k.set_id
		WHERE t.preisschiessen_id = $1
		  AND k.returned_at IS NULL
		  AND ($2 = '' OR k.purchased_at::date::text = $2)
		GROUP BY k.purchased_at::date, k.typ, COALESCE(sc.name, st.name)
		ORDER BY k.purchased_at::date, COALESCE(sc.name, st.name)`, preisschiessenID, datum)
	if err != nil {
		return out, err
	}
	defer kRows.Close()
	for kRows.Next() {
		var row PSAuswertungPosition
		if err := kRows.Scan(&row.Datum, &row.Typ, &row.Name, &row.Menge, &row.Summe); err != nil {
			return out, err
		}
		out.Verkaeufe = append(out.Verkaeufe, row)
	}
	return out, kRows.Err()
}

// ----------------------------------------------------------------------------
// API-Handler
// ----------------------------------------------------------------------------

func (a *APIServer) listPreisschiessen(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListPreisschiessen(r.Context())
}

func (a *APIServer) getPreisschiessen(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.GetPreisschiessen(r.Context(), r.PathValue("id"))
}

func (a *APIServer) createPreisschiessen(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[Preisschiessen](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	id, err := a.store.CreatePreisschiessen(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updatePreisschiessen(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[Preisschiessen](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	body.ID = r.PathValue("id")
	if err := a.store.UpdatePreisschiessen(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deletePreisschiessen(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	if err := a.store.DeletePreisschiessen(r.Context(), r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) listScheiben(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListScheiben(r.Context(), r.PathValue("id"))
}

func (a *APIServer) createScheibe(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[PSScheibe](r)
	if err != nil || body.Name == "" || body.DisciplineID == "" {
		return nil, errors.New("name und discipline_id erforderlich")
	}
	body.PreisschiessenID = r.PathValue("id")
	body.Active = true
	id, err := a.store.CreateScheibe(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateScheibe(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[PSScheibe](r)
	if err != nil || body.Name == "" || body.DisciplineID == "" {
		return nil, errors.New("name und discipline_id erforderlich")
	}
	body.ID = r.PathValue("sid")
	if err := a.store.UpdateScheibe(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteScheibe(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	if err := a.store.DeleteScheibe(r.Context(), r.PathValue("sid")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) setScheibeClasses(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		ClassIDs []string `json:"class_ids"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetScheibeClasses(r.Context(), r.PathValue("sid"), body.ClassIDs); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) setScheibeRequiredSets(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		SetIDs []string `json:"set_ids"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetScheibeRequiredSets(r.Context(), r.PathValue("sid"), body.SetIDs); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) listSets(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListSets(r.Context(), r.PathValue("id"))
}

func (a *APIServer) createSet(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[PSSet](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	body.PreisschiessenID = r.PathValue("id")
	body.Active = true
	id, err := a.store.CreateSet(r.Context(), body)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]string{"id": id}, nil
}

func (a *APIServer) updateSet(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[PSSet](r)
	if err != nil || body.Name == "" {
		return nil, errors.New("name erforderlich")
	}
	body.ID = r.PathValue("setid")
	if err := a.store.UpdateSet(r.Context(), body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) deleteSet(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	if err := a.store.DeleteSet(r.Context(), r.PathValue("setid")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) setSetItems(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Items []PSSetItem `json:"items"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetSetItems(r.Context(), r.PathValue("setid"), body.Items); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) setSetClasses(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		ClassIDs []string `json:"class_ids"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetSetClasses(r.Context(), r.PathValue("setid"), body.ClassIDs); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) listPSTeilnehmer(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListTeilnehmer(r.Context(), r.PathValue("id"), r.URL.Query().Get("q"))
}

func (a *APIServer) createPSTeilnehmer(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		ShooterID string `json:"shooter_id"`
	}](r)
	if err != nil || body.ShooterID == "" {
		return nil, errors.New("shooter_id erforderlich")
	}
	t, err := a.store.CreateTeilnehmer(r.Context(), r.PathValue("id"), body.ShooterID)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return t, nil
}

func (a *APIServer) getPSTeilnehmer(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.GetTeilnehmer(r.Context(), r.PathValue("tid"))
}

func (a *APIServer) listAngebot(w http.ResponseWriter, r *http.Request) (any, error) {
	scheiben, sets, err := a.store.ListAngebot(r.Context(), r.PathValue("tid"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"scheiben": scheiben, "sets": sets}, nil
}

func (a *APIServer) listKonto(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListKonto(r.Context(), r.PathValue("tid"))
}

func (a *APIServer) listKaufScheibenEinheiten(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.ListKaufScheibenEinheiten(r.Context(), r.PathValue("tid"))
}

func (a *APIServer) postAssignPreisschiessenLane(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		LaneNo int `json:"lane_no"`
	}](r)
	if err != nil || body.LaneNo < 1 {
		return nil, errors.New("lane_no erforderlich")
	}
	sessionID, err := a.store.AssignPreisschiessenLane(r.Context(), r.PathValue("ksid"), body.LaneNo)
	if err != nil {
		return nil, err
	}
	return map[string]string{"session_id": sessionID}, nil
}

func (a *APIServer) buchAufladung(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Betrag float64 `json:"betrag"`
		Notiz  string  `json:"notiz"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := a.store.BuchAufladung(r.Context(), r.PathValue("tid"), body.Betrag, body.Notiz); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// validateCartItems prüft nur die Formalien (typ gesetzt, passende ID
// vorhanden) - Preis/Verfügbarkeit/Limit werden serverseitig beim
// tatsächlichen Buchen neu geprüft (siehe Store.purchaseItem).
func validateCartItems(items []CartItem) error {
	for _, it := range items {
		switch it.Typ {
		case "scheibe":
			if it.ScheibeID == "" {
				return errors.New("scheibe_id erforderlich")
			}
		case "set":
			if it.SetID == "" {
				return errors.New("set_id erforderlich")
			}
		default:
			return errors.New("typ muss 'scheibe' oder 'set' sein")
		}
	}
	return nil
}

func (a *APIServer) postBuchWarenkorb(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Items []CartItem `json:"items"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := validateCartItems(body.Items); err != nil {
		return nil, err
	}
	kaufIDs, err := a.store.BuchWarenkorb(r.Context(), r.PathValue("tid"), body.Items)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusCreated)
	return map[string]any{"kauf_ids": kaufIDs}, nil
}

func (a *APIServer) postAuszahlung(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Betrag float64 `json:"betrag"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := a.store.Auszahlung(r.Context(), r.PathValue("tid"), body.Betrag); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) postBezahlen(w http.ResponseWriter, r *http.Request) (any, error) {
	body, err := decodeBody[struct {
		Items          []CartItem `json:"items"`
		BetragErhalten float64    `json:"betrag_erhalten"`
		RestAuszahlen  bool       `json:"rest_auszahlen"`
	}](r)
	if err != nil {
		return nil, err
	}
	if err := validateCartItems(body.Items); err != nil {
		return nil, err
	}
	kaufIDs, rueckgeld, err := a.store.Bezahlen(r.Context(), r.PathValue("tid"), body.Items, body.BetragErhalten, body.RestAuszahlen)
	if err != nil {
		return nil, err
	}
	return map[string]any{"kauf_ids": kaufIDs, "rueckgeld": rueckgeld}, nil
}

func (a *APIServer) postRuckgabe(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.store.Ruckgabe(r.Context(), r.PathValue("kid")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *APIServer) psAuswertung(w http.ResponseWriter, r *http.Request) (any, error) {
	return a.store.KasseAuswertung(r.Context(), r.PathValue("id"), r.URL.Query().Get("date"))
}
