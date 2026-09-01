// ============================================================================
// testdata_seed.go – Entwicklung-Kachel: Testdaten für Preisschießen erzeugen
//
// Admin-only Werkzeug (wie Import/Export), um ein Preisschießen mit
// plausiblen Testteilnehmern durchzuspielen, ohne echte Mitglieder zu
// verwenden: legt Schützen mit Nachname "Testuser" an (verteilt auf
// bestehende Vereine), meldet einen Teil davon im gewählten Preisschießen
// an, kauft je Teilnehmer ein zur Altersklasse passendes Set plus einige
// zusätzliche Einzelscheiben desselben Scheiben-Typs und "beschießt" jede
// gekaufte Scheibe mit Zufallstreffern.
//
// Markierung/Aufräumen: alle erzeugten Schützen bekommen shooters.notes mit
// dem Präfix testdatenMarker - darüber findet CleanupTestdaten alles wieder,
// was dieses Werkzeug angelegt hat (unabhängig vom Preisschießen). Schüsse
// dürfen laut Schema nie gelöscht werden (trg_shots_no_delete) - Cleanup
// deaktiviert den Trigger dafür kurzzeitig innerhalb einer Transaktion,
// genau wie delete-selection.go es für die Import/Export-Kachel bereits tut.
//
// Die Zuordnung Zehntelwert -> Ring/Teiler/Innenzehner repliziert exakt die
// Formel aus standpc/score.go (Scorer.Score/decimalValue), damit die
// erzeugten Testschüsse in Auswertung/Preisanzeige genauso plausibel
// aussehen wie echte TDOA-Treffer.
// ============================================================================
package main

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"time"
)

const testdatenMarker = "TESTDATEN-GENERATOR"

var testdatenVornamenM = []string{
	"Hans", "Peter", "Klaus", "Josef", "Georg", "Michael", "Thomas", "Andreas",
	"Stefan", "Martin", "Wolfgang", "Werner", "Herbert", "Kurt", "Helmut",
	"Günther", "Manfred", "Rudolf", "Walter", "Franz", "Anton", "Karl",
	"Ludwig", "Otto", "Heinrich", "Alexander", "Florian", "Maximilian",
	"Sebastian", "Christian", "Daniel", "Tobias", "Felix", "Lukas", "Jonas",
	"Simon", "Julian", "Fabian", "Moritz", "Paul", "Leon", "Noah", "Elias",
	"Ben", "Tim", "Jakob", "Matthias", "Markus", "Bernd", "Dieter",
}

var testdatenVornamenW = []string{
	"Maria", "Anna", "Elisabeth", "Ursula", "Ingrid", "Helga", "Renate",
	"Christa", "Gisela", "Erika", "Brigitte", "Monika", "Petra", "Sabine",
	"Susanne", "Andrea", "Claudia", "Birgit", "Karin", "Angelika", "Martina",
	"Sandra", "Nicole", "Julia", "Laura", "Lisa", "Sarah", "Lena", "Hannah",
	"Emma", "Mia", "Sophie", "Marie", "Johanna", "Katharina", "Stefanie",
	"Melanie", "Christine", "Barbara", "Ruth", "Elfriede", "Waltraud",
	"Gerda", "Edith",
}

type TestdatenParams struct {
	PreisschiessenID string
	Members          int
	Anmelden         int
	MinScheiben      int
	MaxScheiben      int
}

type TestdatenResult struct {
	ShootersCreated   int      `json:"shooters_created"`
	TeilnehmerCreated int      `json:"teilnehmer_created"`
	SetsGekauft       int      `json:"sets_gekauft"`
	ScheibenGekauft   int      `json:"scheiben_gekauft"`
	SessionsCreated   int      `json:"sessions_created"`
	ShotsCreated      int      `json:"shots_created"`
	GesamtUmsatz      float64  `json:"gesamt_umsatz"`
	Warnings          []string `json:"warnings"`
}

type TestdatenCleanupResult struct {
	ShootersDeleted   int `json:"shooters_deleted"`
	TeilnehmerDeleted int `json:"teilnehmer_deleted"`
	SessionsDeleted   int `json:"sessions_deleted"`
	ShotsDeleted      int `json:"shots_deleted"`
}

func (p TestdatenParams) validate() error {
	if p.PreisschiessenID == "" {
		return errBadRequest("Preisschießen fehlt")
	}
	if p.Members < 1 || p.Members > 1000 {
		return errBadRequest("Anzahl Mitglieder muss zwischen 1 und 1000 liegen")
	}
	if p.Anmelden < 0 || p.Anmelden > p.Members {
		return errBadRequest("Anzahl Anmeldungen muss zwischen 0 und der Mitgliederzahl liegen")
	}
	if p.MinScheiben < 0 || p.MaxScheiben < p.MinScheiben || p.MaxScheiben > 50 {
		return errBadRequest("ungültiger Scheiben-Bereich")
	}
	return nil
}

// ----------------------------------------------------------------------------
// Trefferwertung – Umkehrung von standpc/score.go: aus einem gewünschten
// Zehntelwert (statt aus x/y wie beim echten Stand) werden Teiler, Ring und
// Innenzehner-Flag konsistent zur DB-Scheibengeometrie berechnet.
// ----------------------------------------------------------------------------

type ringGeom struct {
	value int
	dMM   float64
}

type targetGeom struct {
	caliberMM float64
	edgeScore bool
	innerTenD float64
	r10MM     float64
	denom     float64    // effektiver Ring10-Aussenradius in 1/100mm
	rings     []ringGeom // aufsteigend nach Durchmesser (Ring 10 zuerst)
}

func (s *Store) loadTargetGeom(ctx context.Context, targetID string) (targetGeom, error) {
	var g targetGeom
	if err := s.pool.QueryRow(ctx, `
		SELECT caliber_mm, edge_scoring, COALESCE(inner_ten_d_mm,0)
		FROM targets WHERE id=$1`, targetID,
	).Scan(&g.caliberMM, &g.edgeScore, &g.innerTenD); err != nil {
		return g, fmt.Errorf("Scheibengeometrie: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT ring_value, diameter_mm FROM target_rings
		WHERE target_id=$1 ORDER BY diameter_mm ASC`, targetID)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var rg ringGeom
		if err := rows.Scan(&rg.value, &rg.dMM); err != nil {
			return g, err
		}
		g.rings = append(g.rings, rg)
	}
	if err := rows.Err(); err != nil {
		return g, err
	}
	if len(g.rings) == 0 || g.caliberMM <= 0 {
		return g, fmt.Errorf("Scheibe %s: keine Ringdefinition", targetID)
	}
	g.r10MM = g.rings[0].dMM / 2.0
	g.denom = (g.r10MM + g.caliberMM/2.0) * 100
	return g, nil
}

// scoreFromDecimal liefert zu einem vorgegebenen Zehntelwert (6.0..10.9)
// den passenden Teiler/Ring/Innenzehner - exakte Umkehrung von
// Scorer.decimalValue in standpc/score.go (Formel: Zehntelwert = 11 -
// Teiler/Nenner). rawDec liegt in der Mitte des Zehntel-"Buckets", damit die
// Vorwärtsformel (floor) wieder exakt auf dec zurückrundet.
func scoreFromDecimal(g targetGeom, dec float64) (ring int, centerDistance float64, innerTen bool, xMM, yMM float64) {
	rawDec := dec + 0.05
	teiler := (11.0 - rawDec) * g.denom // 1/100mm
	if teiler < 0 {
		teiler = 0
	}
	centerDistance = math.Round(teiler*10) / 10
	r := teiler / 100.0 // mm

	rEff := r
	if g.edgeScore {
		rEff = math.Max(0, r-g.caliberMM/2.0)
	}
	for _, rg := range g.rings {
		if rEff <= rg.dMM/2.0 {
			ring = rg.value
			break
		}
	}
	innerTen = g.innerTenD > 0 && rEff <= g.innerTenD/2.0

	theta := rand.Float64() * 2 * math.Pi
	xMM = math.Round(r*math.Cos(theta)*1000) / 1000
	yMM = math.Round(r*math.Sin(theta)*1000) / 1000
	return
}

func randomDecimalScore() float64 {
	// gleichverteilt über die 50 Zehntelwerte 6.0..10.9
	return math.Floor((6.0+rand.Float64()*4.9)*10) / 10
}

// ----------------------------------------------------------------------------
// Testdaten-Erzeugung
// ----------------------------------------------------------------------------

type tdClassRow struct {
	id             string
	minAge, maxAge *int
	sex            *int
}

type tdLane struct {
	laneID, calibrationID string
	laneNo                int
}

type tdScheibeMeta struct {
	disciplineID   string
	matchShotCount int
	shotsPerSeries int
	geom           targetGeom
}

func (s *Store) GenerateTestdaten(ctx context.Context, p TestdatenParams) (TestdatenResult, error) {
	var res TestdatenResult
	if err := p.validate(); err != nil {
		return res, err
	}

	var psName string
	var shootingType int
	var endsOn *time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT name, shooting_type, ends_on FROM preisschiessen WHERE id=$1`, p.PreisschiessenID,
	).Scan(&psName, &shootingType, &endsOn); err != nil {
		return res, errBadRequest("Preisschießen nicht gefunden")
	}
	refYear := time.Now().Year()
	if endsOn != nil {
		refYear = endsOn.Year()
	}

	// Vereine
	var clubIDs []string
	rows, err := s.pool.Query(ctx, `SELECT id FROM clubs`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		clubIDs = append(clubIDs, id)
	}
	rows.Close()
	if len(clubIDs) == 0 {
		return res, errBadRequest("keine Vereine vorhanden – erst unter Stammdaten anlegen")
	}

	// Altersklassen-Pool: nur Klassen, die auch an mindestens ein Set dieses
	// Preisschießens gekoppelt sind (sonst könnte ein erzeugter Teilnehmer
	// später kein passendes Set kaufen) - Fallback auf alle Klassen der
	// passenden Schießart, falls keine Sets klassenspezifisch sind.
	classPool, err := s.loadTestdatenClassPool(ctx, p.PreisschiessenID, shootingType)
	if err != nil {
		return res, err
	}
	if len(classPool) == 0 {
		return res, errBadRequest("keine Sportklassen für dieses Preisschießen/diese Schießart gefunden")
	}

	// ---- Schützen erzeugen ----
	shooterIDs := make([]string, 0, p.Members)
	marker := fmt.Sprintf("%s (%s)", testdatenMarker, time.Now().Format("2006-01-02 15:04"))
	for i := 0; i < p.Members; i++ {
		cls := classPool[rand.IntN(len(classPool))]
		ageLo, ageHi := 6, 90
		if cls.minAge != nil && *cls.minAge > ageLo {
			ageLo = *cls.minAge
		}
		if cls.maxAge != nil && *cls.maxAge < ageHi {
			ageHi = *cls.maxAge
		}
		if ageHi < ageLo {
			ageHi = ageLo
		}
		age := ageLo + rand.IntN(ageHi-ageLo+1)
		birth := time.Date(refYear-age, time.Month(1+rand.IntN(12)), 1+rand.IntN(28), 0, 0, 0, 0, time.UTC)

		sexCode := rand.IntN(2)
		if cls.sex != nil {
			sexCode = *cls.sex
		}
		gender, firstNames := "W", testdatenVornamenW
		if sexCode == 1 {
			gender, firstNames = "M", testdatenVornamenM
		}
		firstName := firstNames[rand.IntN(len(firstNames))]
		clubID := clubIDs[rand.IntN(len(clubIDs))]

		var shooterID string
		if err := s.pool.QueryRow(ctx, `
			INSERT INTO shooters (last_name, first_name, birth_date, gender, club_id, notes)
			VALUES ('Testuser', $1, $2, $3, $4, $5) RETURNING id`,
			firstName, birth, gender, clubID, marker,
		).Scan(&shooterID); err != nil {
			return res, fmt.Errorf("Schütze anlegen: %w", err)
		}
		shooterIDs = append(shooterIDs, shooterID)
		res.ShootersCreated++
	}

	if p.Anmelden == 0 {
		return res, nil
	}

	// Zufällige Teilmenge zur Anmeldung auswählen.
	perm := rand.Perm(len(shooterIDs))[:p.Anmelden]

	// Lane/Kalibrierungs-Pool für die simulierten Sessions: bevorzugt Stände,
	// die nicht an physisch aktive Stand-PCs (1-3) gebunden sind, damit
	// keine Live-Anzeige/SSE bei laufendem Betrieb gestört wird.
	lanes, err := s.loadTestdatenLanes(ctx)
	if err != nil {
		return res, err
	}
	if len(lanes) == 0 {
		return res, errBadRequest("kein kalibrierter Stand für Testschüsse verfügbar")
	}
	laneCursor := 0
	nextLane := func() tdLane {
		l := lanes[laneCursor%len(lanes)]
		laneCursor++
		return l
	}

	scheibeMetaCache := map[string]tdScheibeMeta{}
	targetGeomCache := map[string]targetGeom{}

	for _, idx := range perm {
		shooterID := shooterIDs[idx]

		teilnehmer, err := s.CreateTeilnehmer(ctx, p.PreisschiessenID, shooterID)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Anmeldung fehlgeschlagen: %v", err))
			continue
		}
		res.TeilnehmerCreated++
		if teilnehmer.ClassID == nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"Teilnehmer %d: keine Sportklasse ermittelt, kein Einkauf möglich", teilnehmer.TeilnehmerNr))
			continue
		}

		setID, err := s.pickEligibleSet(ctx, p.PreisschiessenID, *teilnehmer.ClassID)
		if err != nil {
			return res, err
		}
		if setID == "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"Teilnehmer %d: kein passendes Set für die Klasse gefunden", teilnehmer.TeilnehmerNr))
			continue
		}

		extraScheibeID, err := s.pickStandaloneScheibeOfSet(ctx, setID)
		if err != nil {
			return res, err
		}

		items := []CartItem{{Typ: "set", SetID: setID}}
		nExtra := 0
		if extraScheibeID != "" {
			nExtra = p.MinScheiben
			if p.MaxScheiben > p.MinScheiben {
				nExtra += rand.IntN(p.MaxScheiben - p.MinScheiben + 1)
			}
			for i := 0; i < nExtra; i++ {
				items = append(items, CartItem{Typ: "scheibe", ScheibeID: extraScheibeID})
			}
		}

		kaufIDs, total, err := s.purchaseTestdatenItems(ctx, teilnehmer.ID, p.PreisschiessenID, items)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"Teilnehmer %d: Einkauf fehlgeschlagen (%v)", teilnehmer.TeilnehmerNr, err))
			continue
		}
		res.SetsGekauft++
		res.ScheibenGekauft += nExtra
		res.GesamtUmsatz += total

		einheiten, err := s.listKaufScheibenForKaeufe(ctx, kaufIDs)
		if err != nil {
			return res, err
		}

		for _, e := range einheiten {
			meta, ok := scheibeMetaCache[e.scheibeID]
			if !ok {
				meta, err = s.loadScheibeMeta(ctx, e.scheibeID, targetGeomCache)
				if err != nil {
					return res, err
				}
				scheibeMetaCache[e.scheibeID] = meta
			}

			lane := nextLane()
			shotCount, sessionID, err := s.shootTestdatenScheibe(
				ctx, lane, meta, shooterID, psName, e.id)
			if err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"Teilnehmer %d: Beschuss fehlgeschlagen (%v)", teilnehmer.TeilnehmerNr, err))
				continue
			}
			_ = sessionID
			res.SessionsCreated++
			res.ShotsCreated += shotCount
		}
	}

	return res, nil
}

func (s *Store) loadTestdatenClassPool(ctx context.Context, preisschiessenID string, shootingType int) ([]tdClassRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT sc.id, sc.min_age, sc.max_age, sc.sex
		FROM ps_set_classes psc
		JOIN ps_sets st ON st.id = psc.set_id
		JOIN shooter_classes sc ON sc.id = psc.class_id
		WHERE st.preisschiessen_id = $1 AND st.active`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	var out []tdClassRow
	for rows.Next() {
		var c tdClassRow
		if err := rows.Scan(&c.id, &c.minAge, &c.maxAge, &c.sex); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(out) > 0 {
		return out, nil
	}

	// Fallback: keine klassenspezifischen Sets - alle Klassen der Schießart.
	rows, err = s.pool.Query(ctx,
		`SELECT id, min_age, max_age, sex FROM shooter_classes WHERE type=$1`, shootingType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c tdClassRow
		if err := rows.Scan(&c.id, &c.minAge, &c.maxAge, &c.sex); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) pickEligibleSet(ctx context.Context, preisschiessenID, classID string) (string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT st.id FROM ps_sets st
		WHERE st.preisschiessen_id=$1 AND st.active
		  AND (
		        NOT EXISTS (SELECT 1 FROM ps_set_classes psc WHERE psc.set_id = st.id)
		        OR EXISTS (SELECT 1 FROM ps_set_classes psc WHERE psc.set_id = st.id AND psc.class_id = $2)
		      )`, preisschiessenID, classID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[rand.IntN(len(ids))], nil
}

func (s *Store) pickStandaloneScheibeOfSet(ctx context.Context, setID string) (string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sc.id FROM ps_set_items si
		JOIN ps_scheiben sc ON sc.id = si.scheibe_id
		WHERE si.set_id=$1 AND sc.standalone_erlaubt AND sc.active`, setID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[rand.IntN(len(ids))], nil
}

// purchaseTestdatenItems kauft Set + Zusatzscheiben in einer Transaktion
// (reuse von Store.purchaseItems, derselben Logik wie die Kasse) und deckt
// das entstandene Guthaben-Minus sofort mit einer Aufladungsbuchung -
// stellvertretend für die Bar-Einzahlung an der Kasse.
func (s *Store) purchaseTestdatenItems(ctx context.Context, teilnehmerID, preisschiessenID string, items []CartItem) ([]string, float64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)

	kaufIDs, total, err := s.purchaseItems(ctx, tx, teilnehmerID, preisschiessenID, items)
	if err != nil {
		return nil, 0, err
	}
	if total > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_guthaben_buchungen (teilnehmer_id, typ, betrag, notiz)
			VALUES ($1,'aufladung',$2,$3)`,
			teilnehmerID, total, testdatenMarker+": Kasseneinzahlung"); err != nil {
			return nil, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return kaufIDs, total, nil
}

type tdKaufScheibe struct {
	id, scheibeID string
}

func (s *Store) listKaufScheibenForKaeufe(ctx context.Context, kaufIDs []string) ([]tdKaufScheibe, error) {
	if len(kaufIDs) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, scheibe_id FROM ps_kauf_scheiben WHERE kauf_id = ANY($1::uuid[])`, kaufIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tdKaufScheibe
	for rows.Next() {
		var k tdKaufScheibe
		if err := rows.Scan(&k.id, &k.scheibeID); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) loadScheibeMeta(ctx context.Context, scheibeID string, geomCache map[string]targetGeom) (tdScheibeMeta, error) {
	var meta tdScheibeMeta
	var targetID string
	if err := s.pool.QueryRow(ctx, `
		SELECT d.id, d.match_shot_count, d.shots_per_series, d.target_id
		FROM ps_scheiben sc JOIN disciplines d ON d.id = sc.discipline_id
		WHERE sc.id=$1`, scheibeID,
	).Scan(&meta.disciplineID, &meta.matchShotCount, &meta.shotsPerSeries, &targetID); err != nil {
		return meta, err
	}
	geom, ok := geomCache[targetID]
	if !ok {
		var err error
		geom, err = s.loadTargetGeom(ctx, targetID)
		if err != nil {
			return meta, err
		}
		geomCache[targetID] = geom
	}
	meta.geom = geom
	return meta, nil
}

// loadTestdatenLanes liefert kalibrierte Stände, bevorzugt ohne physisch
// aktive Stand-PCs (1-3) - fällt auf alle kalibrierten Stände zurück, falls
// keine "freien" Stände existieren.
func (s *Store) loadTestdatenLanes(ctx context.Context) ([]tdLane, error) {
	load := func(excludePhysical bool) ([]tdLane, error) {
		q := `
			SELECT l.id, l.lane_no, c.id
			FROM lanes l
			JOIN calibrations c ON c.lane_id = l.id AND c.valid_to IS NULL
			WHERE l.active`
		if excludePhysical {
			q += ` AND l.lane_no NOT IN (1,2,3)`
		}
		q += ` ORDER BY l.lane_no`
		rows, err := s.pool.Query(ctx, q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []tdLane
		for rows.Next() {
			var l tdLane
			if err := rows.Scan(&l.laneID, &l.laneNo, &l.calibrationID); err != nil {
				return nil, err
			}
			out = append(out, l)
		}
		return out, rows.Err()
	}
	lanes, err := load(true)
	if err != nil {
		return nil, err
	}
	if len(lanes) > 0 {
		return lanes, nil
	}
	return load(false)
}

// shootTestdatenScheibe legt für eine gekaufte Scheiben-Einheit eine fertige
// Session mit vollständiger Schusszahl an und verknüpft sie mit der
// Einheit - fachlich das, was sonst AssignTeilnehmerLane + reale Treffer am
// Stand-PC über die Zeit tun, hier synchron im Batch.
func (s *Store) shootTestdatenScheibe(ctx context.Context, lane tdLane, meta tdScheibeMeta,
	shooterID, listName, kaufScheibeID string) (int, string, error) {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback(ctx)

	shotCount := meta.matchShotCount
	if shotCount <= 0 {
		shotCount = 1
	}
	finishedAt := time.Now().Add(-time.Duration(rand.IntN(14)) * 24 * time.Hour)
	startedAt := finishedAt.Add(-time.Duration(shotCount) * 8 * time.Second)

	var sessionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO sessions (lane_id, calibration_id, discipline_id, shooter_id,
		                       status, started_at, finished_at, list_name)
		VALUES ($1,$2,$3,$4,'finished',$5,$6,$7) RETURNING id`,
		lane.laneID, lane.calibrationID, meta.disciplineID, shooterID,
		startedAt, finishedAt, listName,
	).Scan(&sessionID); err != nil {
		return 0, "", fmt.Errorf("Session anlegen: %w", err)
	}

	shotsPerSeries := meta.shotsPerSeries
	if shotsPerSeries <= 0 {
		shotsPerSeries = shotCount
	}
	for shotNo := 1; shotNo <= shotCount; shotNo++ {
		dec := randomDecimalScore()
		ring, centerDistance, innerTen, x, y := scoreFromDecimal(meta.geom, dec)
		firedAt := startedAt.Add(time.Duration(shotNo) * 8 * time.Second)
		seriesNo := (shotNo-1)/shotsPerSeries + 1
		if _, err := tx.Exec(ctx, `
			INSERT INTO shots (session_id, shot_no, kind, status, series_no, fired_at,
			                    device_seq, raw_t_ns, sensor_hits, confidence,
			                    x_mm, y_mm, ring, decimal_value, is_inner_ten, center_distance)
			VALUES ($1,$2,'match','valid',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			sessionID, shotNo, seriesNo, firedAt,
			shotNo, []int64{}, 4, 0.95,
			x, y, ring, dec, innerTen, centerDistance,
		); err != nil {
			return 0, "", fmt.Errorf("Schuss anlegen: %w", err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE ps_kauf_scheiben SET session_id=$1 WHERE id=$2`, sessionID, kaufScheibeID,
	); err != nil {
		return 0, "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, "", err
	}
	return shotCount, sessionID, nil
}

// ----------------------------------------------------------------------------
// Aufräumen
// ----------------------------------------------------------------------------

// CleanupTestdaten entfernt alle jemals mit diesem Werkzeug erzeugten
// Schützen samt Anmeldungen/Käufen/Sessions - bis auf die Schuss-Zeilen
// selbst kann laut Schema (trg_shots_no_delete) grundsätzlich nichts
// gelöscht werden; hier wird der Trigger dafür - wie schon in
// delete-selection.go für die Import/Export-Kachel - innerhalb derselben
// Transaktion kurz deaktiviert und danach garantiert wieder aktiviert.
func (s *Store) CleanupTestdaten(ctx context.Context) (TestdatenCleanupResult, error) {
	var res TestdatenCleanupResult

	var shooterIDs []string
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM shooters WHERE notes LIKE $1`, testdatenMarker+"%")
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		shooterIDs = append(shooterIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	if len(shooterIDs) == 0 {
		return res, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `ALTER TABLE shots DISABLE TRIGGER trg_shots_no_delete`); err != nil {
		return res, err
	}

	if tag, err := tx.Exec(ctx, `
		DELETE FROM shots WHERE session_id IN (
			SELECT id FROM sessions WHERE shooter_id = ANY($1::uuid[]))`, shooterIDs,
	); err != nil {
		return res, err
	} else {
		res.ShotsDeleted = int(tag.RowsAffected())
	}

	// ps_kaeufe/ps_guthaben_buchungen/ps_kauf_scheiben hängen per ON DELETE
	// CASCADE an ps_teilnehmer (siehe migrations/021_preisschiessen.sql) - muss
	// VOR dem Löschen der Sessions passieren, da ps_kauf_scheiben.session_id
	// ohne CASCADE auf sessions(id) verweist (sonst Fremdschlüssel-Verletzung).
	if tag, err := tx.Exec(ctx,
		`DELETE FROM ps_teilnehmer WHERE shooter_id = ANY($1::uuid[])`, shooterIDs,
	); err != nil {
		return res, err
	} else {
		res.TeilnehmerDeleted = int(tag.RowsAffected())
	}

	if tag, err := tx.Exec(ctx,
		`DELETE FROM sessions WHERE shooter_id = ANY($1::uuid[])`, shooterIDs,
	); err != nil {
		return res, err
	} else {
		res.SessionsDeleted = int(tag.RowsAffected())
	}

	if tag, err := tx.Exec(ctx,
		`DELETE FROM shooters WHERE id = ANY($1::uuid[])`, shooterIDs,
	); err != nil {
		return res, err
	} else {
		res.ShootersDeleted = int(tag.RowsAffected())
	}

	if _, err := tx.Exec(ctx, `ALTER TABLE shots ENABLE TRIGGER trg_shots_no_delete`); err != nil {
		return res, err
	}

	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	return res, nil
}

// ----------------------------------------------------------------------------
// HTTP-Handler (Entwicklung-Kachel, admin-only)
// ----------------------------------------------------------------------------

func (a *APIServer) generateTestdatenHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		PreisschiessenID string `json:"preisschiessen_id"`
		Members          int    `json:"members"`
		Anmelden         int    `json:"anmelden"`
		MinScheiben      int    `json:"min_scheiben"`
		MaxScheiben      int    `json:"max_scheiben"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body: " + err.Error())
	}
	return a.store.GenerateTestdaten(r.Context(), TestdatenParams{
		PreisschiessenID: body.PreisschiessenID,
		Members:          body.Members,
		Anmelden:         body.Anmelden,
		MinScheiben:      body.MinScheiben,
		MaxScheiben:      body.MaxScheiben,
	})
}

func (a *APIServer) cleanupTestdatenHandler(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireAdmin(w, r); err != nil {
		return nil, err
	}
	// Sicherheits-Backup vor dem Löschen, wie bei jeder destruktiven Aktion
	// der Import/Export-Kachel (siehe delete-selection.go).
	if _, err := a.createBackup(r.Context()); err != nil {
		return nil, fmt.Errorf("Sicherheits-Backup vor Aufräumen fehlgeschlagen, abgebrochen: %w", err)
	}
	return a.store.CleanupTestdaten(r.Context())
}
