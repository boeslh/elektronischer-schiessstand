// ============================================================================
// vereine.go – Vereins-Auswertungen (Anzahl/Prozent/Punkte) für die Anzeige.
//
// Bewusste Duplizierung der Berechnungslogik aus server/preisschiessen_vereine.go
// (Store.ComputeVereinsAuswertung/assignVereinsPlatz) statt eines Imports: die
// beiden Go-Module sind unabhängig (preisanzeige läuft ggf. auf einem anderen
// Rechner, siehe main.go-Kommentar), und die Berechnung ist – anders als die
// Meister/Punkt/Adler-Wertungen – bewusst billig genug, um live pro Anfrage zu
// laufen, ohne den Batch-Cache-Mechanismus (ps_wertung_ergebnisse) zu brauchen.
// ============================================================================
package main

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

var vereinTypLabels = map[string]string{
	"anzahl":  "Vereine – Teilnehmer Anzahl",
	"prozent": "Vereine – Teilnehmer Prozent",
	"punkte":  "Vereine – Teilnehmer Punkte",
}

var vereinTypOrder = []string{"anzahl", "prozent", "punkte"}

type vereinErgebnisZeile struct {
	ClubID     string
	ClubName   string
	Gastgeber  bool
	Anzahl     int
	Mitglieder *int
	Prozent    *float64
	Punkte     float64
	Platz      int // nur für den jeweils angefragten Typ gesetzt (siehe computeVereinsAuswertung)
}

// computeVereinsAuswertung berechnet alle drei Metriken für alle
// teilnehmenden Vereine dieses Preisschiessens - typ steuert nur, welches
// Feld in .Platz landet (für die jeweilige Anzeige-Seite).
func computeVereinsAuswertung(ctx context.Context, pool *pgxpool.Pool, preisschiessenID, typ string) ([]vereinErgebnisZeile, error) {
	clubRows, err := pool.Query(ctx, `
		SELECT vt.club_id, c.name, vt.gastgeber, c.member_count
		FROM ps_verein_teilnahme vt JOIN clubs c ON c.id = vt.club_id
		WHERE vt.preisschiessen_id=$1`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	var rows []vereinErgebnisZeile
	var clubIDs []string
	for clubRows.Next() {
		var r vereinErgebnisZeile
		if err := clubRows.Scan(&r.ClubID, &r.ClubName, &r.Gastgeber, &r.Mitglieder); err != nil {
			clubRows.Close()
			return nil, err
		}
		rows = append(rows, r)
		clubIDs = append(clubIDs, r.ClubID)
	}
	if err := clubRows.Err(); err != nil {
		return nil, err
	}
	clubRows.Close()
	if len(rows) == 0 {
		return []vereinErgebnisZeile{}, nil
	}

	countByClub := map[string]int{}
	cRows, err := pool.Query(ctx, `
		SELECT sh.club_id, COUNT(*) FROM ps_teilnehmer t
		JOIN shooters sh ON sh.id = t.shooter_id
		WHERE t.preisschiessen_id=$1 AND sh.club_id = ANY($2::uuid[])
		GROUP BY sh.club_id`, preisschiessenID, clubIDs)
	if err != nil {
		return nil, err
	}
	for cRows.Next() {
		var cid string
		var n int
		if err := cRows.Scan(&cid, &n); err != nil {
			cRows.Close()
			return nil, err
		}
		countByClub[cid] = n
	}
	if err := cRows.Err(); err != nil {
		return nil, err
	}
	cRows.Close()

	type zSpan struct {
		von, bis string
		punkte   float64
	}
	var spans []zSpan
	zRows, err := pool.Query(ctx, `
		SELECT von::text, bis::text, punkte FROM ps_verein_punkte_zeitraum
		WHERE preisschiessen_id=$1 ORDER BY sort_order, von`, preisschiessenID)
	if err != nil {
		return nil, err
	}
	for zRows.Next() {
		var z zSpan
		if err := zRows.Scan(&z.von, &z.bis, &z.punkte); err != nil {
			zRows.Close()
			return nil, err
		}
		spans = append(spans, z)
	}
	if err := zRows.Err(); err != nil {
		return nil, err
	}
	zRows.Close()

	punkteByClub := map[string]float64{}
	fRows, err := pool.Query(ctx, `
		SELECT sh.club_id, MIN((s.fired_at AT TIME ZONE 'Europe/Berlin')::date)::text
		FROM ps_teilnehmer t
		JOIN shooters sh ON sh.id = t.shooter_id
		JOIN ps_kaeufe k ON k.teilnehmer_id = t.id
		JOIN ps_kauf_scheiben ks ON ks.kauf_id = k.id
		JOIN shots s ON s.session_id = ks.session_id AND s.status <> 'annulled'
		WHERE t.preisschiessen_id=$1 AND sh.club_id = ANY($2::uuid[])
		GROUP BY t.id, sh.club_id`, preisschiessenID, clubIDs)
	if err != nil {
		return nil, err
	}
	for fRows.Next() {
		var cid, d string
		if err := fRows.Scan(&cid, &d); err != nil {
			fRows.Close()
			return nil, err
		}
		for _, sp := range spans {
			if d >= sp.von && d <= sp.bis { // ISO-Datumsstrings, lexikalisch vergleichbar
				punkteByClub[cid] += sp.punkte
				break
			}
		}
	}
	if err := fRows.Err(); err != nil {
		return nil, err
	}
	fRows.Close()

	for i := range rows {
		rows[i].Anzahl = countByClub[rows[i].ClubID]
		if rows[i].Mitglieder != nil && *rows[i].Mitglieder > 0 {
			p := float64(rows[i].Anzahl) / float64(*rows[i].Mitglieder) * 100
			rows[i].Prozent = &p
		}
		rows[i].Punkte = punkteByClub[rows[i].ClubID]
	}

	var get func(vereinErgebnisZeile) *float64
	switch typ {
	case "prozent":
		get = func(r vereinErgebnisZeile) *float64 { return r.Prozent }
	case "punkte":
		get = func(r vereinErgebnisZeile) *float64 { v := r.Punkte; return &v }
	default:
		get = func(r vereinErgebnisZeile) *float64 { v := float64(r.Anzahl); return &v }
	}
	platz := assignVereinsPlatz(rows, get)
	for i := range rows {
		rows[i].Platz = platz[rows[i].ClubID]
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Platz < rows[j].Platz })
	return rows, nil
}

// assignVereinsPlatz - siehe server/preisschiessen_vereine.go für die
// ausführlich kommentierte Fassung: Gastgeber-Verein landet immer auf dem
// letzten Platz, unabhängig vom Wert.
func assignVereinsPlatz(rows []vereinErgebnisZeile, get func(vereinErgebnisZeile) *float64) map[string]int {
	type item struct {
		clubID    string
		name      string
		val       *float64
		gastgeber bool
	}
	items := make([]item, len(rows))
	for i, r := range rows {
		items[i] = item{r.ClubID, r.ClubName, get(r), r.Gastgeber}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.gastgeber != b.gastgeber {
			return !a.gastgeber
		}
		if a.gastgeber && b.gastgeber {
			return a.name < b.name
		}
		if (a.val == nil) != (b.val == nil) {
			return a.val != nil
		}
		if a.val != nil && b.val != nil && *a.val != *b.val {
			return *a.val > *b.val
		}
		return a.name < b.name
	})
	out := make(map[string]int, len(items))
	for i, it := range items {
		out[it.clubID] = i + 1
	}
	return out
}
