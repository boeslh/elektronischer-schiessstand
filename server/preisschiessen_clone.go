// ============================================================================
// preisschiessen_clone.go – Preisschießen klonen
//
// Kopiert die komplette Konfiguration eines Preisschießens (Scheiben, Sets,
// Wertungen inkl. Adler-Verknüpfungen und Scheiben-Faktoren, Vereins-
// Auswertung-Konfiguration, Gewinne, Anzeige-Konfiguration) unter einem neuen
// Namen - bewusst NICHT mitkopiert werden Teilnehmer, Käufe/Buchungen,
// Sessions/Schüsse und der Ergebnis-Cache (ps_wertung_ergebnisse): das sind
// Verlaufsdaten eines konkreten Durchlaufs, kein wiederverwendbares Template.
// Typischer Anwendungsfall: nächstjähriges Preisschießen aus dem Vorjahr
// vorbereiten, ohne die komplette Scheiben-/Set-/Wertungskonfiguration neu
// eintippen zu müssen.
//
// Fremdschlüssel, die auf andere geklonte Zeilen verweisen (Set-Items auf
// Scheiben, Wertungen auf Scheiben, Adler-Wertungen auf zwei andere
// Wertungen, Anzeige-Konfiguration auf Wertungen, Gewinne auf Wertungen),
// werden über id-Umschlüsselungstabellen (alte ID -> neue ID) nachgeführt,
// die beim Klonen der jeweils referenzierten Tabelle entstehen - deshalb ist
// die Reihenfolge unten wichtig (z.B. Scheiben vor Sets vor Wertungen).
// ============================================================================
package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type scheibeSrc struct {
	id, name, disciplineID  string
	price                   float64
	targetColor             *string
	standaloneErlaubt, actv bool
	sortOrder               int
	maxProTeilnehmer        *int
}

type setSrc struct {
	id, name         string
	price            float64
	actv             bool
	sortOrder        int
	maxProTeilnehmer *int
}

type wertungSrc struct {
	id, disziplinKey, typ, shortDesc string
	longDesc, wertungsfeld           *string
	klassenIDs                       []string
	anzSumme                         int
	adlerTeilerID, adlerMeisterID    *string
	sortOrder                        int
	visible                          bool
}

func (s *Store) ClonePreisschiessen(ctx context.Context, sourceID, newName string) (string, error) {
	if newName == "" {
		return "", errBadRequest("Name darf nicht leer sein")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var newPSID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO preisschiessen (name, starts_on, ends_on, shooting_type,
		                             max_negative_guthaben, active, sets_at_standpc)
		SELECT $2, starts_on, ends_on, shooting_type,
		       max_negative_guthaben, active, sets_at_standpc
		FROM preisschiessen WHERE id=$1
		RETURNING id`, sourceID, newName,
	).Scan(&newPSID); err != nil {
		if isUniqueViolation(err) {
			return "", &httpError{code: 404, msg: "Quell-Preisschießen nicht gefunden"}
		}
		return "", err
	}

	// ---- Scheiben ----
	scheibeIDMap := map[string]string{}
	scRows, err := tx.Query(ctx, `
		SELECT id, name, discipline_id, price, target_color, standalone_erlaubt,
		       active, sort_order, max_pro_teilnehmer
		FROM ps_scheiben WHERE preisschiessen_id=$1`, sourceID)
	if err != nil {
		return "", err
	}
	var scheiben []scheibeSrc
	for scRows.Next() {
		var x scheibeSrc
		if err := scRows.Scan(&x.id, &x.name, &x.disciplineID, &x.price, &x.targetColor,
			&x.standaloneErlaubt, &x.actv, &x.sortOrder, &x.maxProTeilnehmer); err != nil {
			scRows.Close()
			return "", err
		}
		scheiben = append(scheiben, x)
	}
	scRows.Close()
	if err := scRows.Err(); err != nil {
		return "", err
	}
	for _, x := range scheiben {
		var newID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO ps_scheiben (preisschiessen_id, name, discipline_id, price, target_color,
			                          standalone_erlaubt, active, sort_order, max_pro_teilnehmer)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			newPSID, x.name, x.disciplineID, x.price, x.targetColor,
			x.standaloneErlaubt, x.actv, x.sortOrder, x.maxProTeilnehmer,
		).Scan(&newID); err != nil {
			return "", err
		}
		scheibeIDMap[x.id] = newID
	}

	// ---- Scheiben-Klassen-Restriktion ----
	if err := cloneJoinRows(ctx, tx,
		`SELECT scheibe_id, class_id FROM ps_scheibe_classes WHERE scheibe_id = ANY($1::uuid[])`,
		scheibeIDs(scheiben),
		`INSERT INTO ps_scheibe_classes (scheibe_id, class_id) VALUES ($1,$2)`,
		scheibeIDMap,
	); err != nil {
		return "", err
	}

	// ---- Sets ----
	setIDMap := map[string]string{}
	stRows, err := tx.Query(ctx, `
		SELECT id, name, price, active, sort_order, max_pro_teilnehmer
		FROM ps_sets WHERE preisschiessen_id=$1`, sourceID)
	if err != nil {
		return "", err
	}
	var sets []setSrc
	for stRows.Next() {
		var x setSrc
		if err := stRows.Scan(&x.id, &x.name, &x.price, &x.actv, &x.sortOrder, &x.maxProTeilnehmer); err != nil {
			stRows.Close()
			return "", err
		}
		sets = append(sets, x)
	}
	stRows.Close()
	if err := stRows.Err(); err != nil {
		return "", err
	}
	for _, x := range sets {
		var newID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO ps_sets (preisschiessen_id, name, price, active, sort_order, max_pro_teilnehmer)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			newPSID, x.name, x.price, x.actv, x.sortOrder, x.maxProTeilnehmer,
		).Scan(&newID); err != nil {
			return "", err
		}
		setIDMap[x.id] = newID
	}

	// ---- Set-Items (Set -> enthaltene Scheiben) ----
	siRows, err := tx.Query(ctx, `
		SELECT set_id, scheibe_id, quantity FROM ps_set_items WHERE set_id = ANY($1::uuid[])`, setIDs(sets))
	if err != nil {
		return "", err
	}
	type setItemSrc struct {
		setID, scheibeID string
		quantity         int
	}
	var setItems []setItemSrc
	for siRows.Next() {
		var x setItemSrc
		if err := siRows.Scan(&x.setID, &x.scheibeID, &x.quantity); err != nil {
			siRows.Close()
			return "", err
		}
		setItems = append(setItems, x)
	}
	siRows.Close()
	if err := siRows.Err(); err != nil {
		return "", err
	}
	for _, x := range setItems {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_set_items (set_id, scheibe_id, quantity) VALUES ($1,$2,$3)`,
			setIDMap[x.setID], scheibeIDMap[x.scheibeID], x.quantity); err != nil {
			return "", err
		}
	}

	// ---- Set-Klassen-Restriktion ----
	if err := cloneJoinRows(ctx, tx,
		`SELECT set_id, class_id FROM ps_set_classes WHERE set_id = ANY($1::uuid[])`,
		setIDs(sets),
		`INSERT INTO ps_set_classes (set_id, class_id) VALUES ($1,$2)`,
		setIDMap,
	); err != nil {
		return "", err
	}

	// ---- Scheibe-erfordert-Set (Gating) ----
	rsRows, err := tx.Query(ctx, `
		SELECT scheibe_id, required_set_id FROM ps_scheibe_requires_set WHERE scheibe_id = ANY($1::uuid[])`,
		scheibeIDs(scheiben))
	if err != nil {
		return "", err
	}
	type requiresSrc struct{ scheibeID, requiredSetID string }
	var requiresRows []requiresSrc
	for rsRows.Next() {
		var x requiresSrc
		if err := rsRows.Scan(&x.scheibeID, &x.requiredSetID); err != nil {
			rsRows.Close()
			return "", err
		}
		requiresRows = append(requiresRows, x)
	}
	rsRows.Close()
	if err := rsRows.Err(); err != nil {
		return "", err
	}
	for _, x := range requiresRows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_scheibe_requires_set (scheibe_id, required_set_id) VALUES ($1,$2)`,
			scheibeIDMap[x.scheibeID], setIDMap[x.requiredSetID]); err != nil {
			return "", err
		}
	}

	// ---- Wertungen (erst ohne Adler-Verknüpfung, die verweist auf andere
	// Wertungen dieser selben Charge und wird erst danach nachgetragen) ----
	wertungIDMap := map[string]string{}
	wRows, err := tx.Query(ctx, `
		SELECT id, disziplin_key, typ, short_desc, long_desc, wertungsfeld,
		       klassen_ids, anz_summe, adler_teiler_id, adler_meister_id, sort_order, visible
		FROM ps_wertungen WHERE preisschiessen_id=$1`, sourceID)
	if err != nil {
		return "", err
	}
	var wertungen []wertungSrc
	for wRows.Next() {
		var x wertungSrc
		if err := wRows.Scan(&x.id, &x.disziplinKey, &x.typ, &x.shortDesc, &x.longDesc, &x.wertungsfeld,
			&x.klassenIDs, &x.anzSumme, &x.adlerTeilerID, &x.adlerMeisterID, &x.sortOrder, &x.visible); err != nil {
			wRows.Close()
			return "", err
		}
		wertungen = append(wertungen, x)
	}
	wRows.Close()
	if err := wRows.Err(); err != nil {
		return "", err
	}
	for _, x := range wertungen {
		var newID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO ps_wertungen (preisschiessen_id, disziplin_key, typ, short_desc, long_desc,
			                           wertungsfeld, klassen_ids, anz_summe, sort_order, visible)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
			newPSID, x.disziplinKey, x.typ, x.shortDesc, x.longDesc,
			x.wertungsfeld, x.klassenIDs, x.anzSumme, x.sortOrder, x.visible,
		).Scan(&newID); err != nil {
			return "", err
		}
		wertungIDMap[x.id] = newID
	}
	for _, x := range wertungen {
		if x.adlerTeilerID == nil && x.adlerMeisterID == nil {
			continue
		}
		var newTeiler, newMeister *string
		if x.adlerTeilerID != nil {
			if v, ok := wertungIDMap[*x.adlerTeilerID]; ok {
				newTeiler = &v
			}
		}
		if x.adlerMeisterID != nil {
			if v, ok := wertungIDMap[*x.adlerMeisterID]; ok {
				newMeister = &v
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE ps_wertungen SET adler_teiler_id=$1, adler_meister_id=$2 WHERE id=$3`,
			newTeiler, newMeister, wertungIDMap[x.id]); err != nil {
			return "", err
		}
	}

	// ---- Wertung-Scheiben-Faktoren ----
	wsRows, err := tx.Query(ctx, `
		SELECT wertung_id, scheibe_id, faktor FROM ps_wertung_scheiben WHERE wertung_id = ANY($1::uuid[])`,
		wertungIDs(wertungen))
	if err != nil {
		return "", err
	}
	type wsSrc struct {
		wertungID, scheibeID string
		faktor               float64
	}
	var wsRowsSrc []wsSrc
	for wsRows.Next() {
		var x wsSrc
		if err := wsRows.Scan(&x.wertungID, &x.scheibeID, &x.faktor); err != nil {
			wsRows.Close()
			return "", err
		}
		wsRowsSrc = append(wsRowsSrc, x)
	}
	wsRows.Close()
	if err := wsRows.Err(); err != nil {
		return "", err
	}
	for _, x := range wsRowsSrc {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_wertung_scheiben (wertung_id, scheibe_id, faktor) VALUES ($1,$2,$3)`,
			wertungIDMap[x.wertungID], scheibeIDMap[x.scheibeID], x.faktor); err != nil {
			return "", err
		}
	}

	// ---- Auswertung-Intervall (nur die Konfiguration, kein Lauf-/
	// Ergebnisstatus - last_started_at/last_finished_at/status bleiben leer,
	// es wurde für das neue Preisschießen ja noch nie gerechnet) ----
	var intervalSeconds *int
	hasStatusRow := false
	if err := tx.QueryRow(ctx,
		`SELECT interval_seconds FROM ps_auswertung_status WHERE preisschiessen_id=$1`, sourceID,
	).Scan(&intervalSeconds); err == nil {
		hasStatusRow = true
	} else if err != pgx.ErrNoRows {
		return "", err
	}
	if hasStatusRow {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ps_auswertung_status (preisschiessen_id, interval_seconds) VALUES ($1,$2)`,
			newPSID, intervalSeconds); err != nil {
			return "", err
		}
	}

	// ---- Anzeige-Konfiguration (anzeige_items/anzeige_items_2 auf die neuen
	// Wertungen ummappen - "verein:..."-Einträge bleiben unverändert, sie
	// referenzieren keine Wertung) ----
	var ac PSAnzeigeConfig
	var oldItems, oldItems2 []string
	hasAnzeigeConfig := false
	if err := tx.QueryRow(ctx, `
		SELECT reload_seconds, title_font_size, list_font_size, anzeige_items, anzeige_items_2, werbung_intervall,
		       bg_color, text_color, row_even_color, row_odd_color,
		       kiosk_show_verein, kiosk_show_klasse, kiosk_anzahl_einzelergebnisse, show_scheibe
		FROM ps_anzeige_config WHERE preisschiessen_id=$1`, sourceID,
	).Scan(&ac.ReloadSeconds, &ac.TitleFontSize, &ac.ListFontSize, &oldItems, &oldItems2, &ac.WerbungIntervall,
		&ac.BgColor, &ac.TextColor, &ac.RowEvenColor, &ac.RowOddColor,
		&ac.KioskShowVerein, &ac.KioskShowKlasse, &ac.KioskAnzahlEinzelergebnisse, &ac.ShowScheibe,
	); err == nil {
		hasAnzeigeConfig = true
	} else if err != pgx.ErrNoRows {
		return "", err
	}
	if hasAnzeigeConfig {
		remapItems := func(items []string) []string {
			out := make([]string, 0, len(items))
			for _, item := range items {
				if oid, ok := strings.CutPrefix(item, "wertung:"); ok {
					if nid, ok := wertungIDMap[oid]; ok {
						out = append(out, "wertung:"+nid)
					}
					continue
				}
				out = append(out, item) // "verein:..." unverändert
			}
			return out
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_anzeige_config (preisschiessen_id, reload_seconds, title_font_size, list_font_size, anzeige_items, anzeige_items_2, werbung_intervall,
			                                bg_color, text_color, row_even_color, row_odd_color,
			                                kiosk_show_verein, kiosk_show_klasse, kiosk_anzahl_einzelergebnisse, show_scheibe)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			newPSID, ac.ReloadSeconds, ac.TitleFontSize, ac.ListFontSize, remapItems(oldItems), remapItems(oldItems2), ac.WerbungIntervall,
			ac.BgColor, ac.TextColor, ac.RowEvenColor, ac.RowOddColor,
			ac.KioskShowVerein, ac.KioskShowKlasse, ac.KioskAnzahlEinzelergebnisse, ac.ShowScheibe); err != nil {
			return "", err
		}
	}

	// ---- Vereins-Auswertung: teilnehmende Vereine + Gastgeber ----
	if _, err := tx.Exec(ctx, `
		INSERT INTO ps_verein_teilnahme (preisschiessen_id, club_id, gastgeber)
		SELECT $2, club_id, gastgeber FROM ps_verein_teilnahme WHERE preisschiessen_id=$1`,
		sourceID, newPSID); err != nil {
		return "", err
	}

	// ---- Vereins-Auswertung: Punkte-Zeiträume ----
	if _, err := tx.Exec(ctx, `
		INSERT INTO ps_verein_punkte_zeitraum (preisschiessen_id, von, bis, punkte, sort_order)
		SELECT $2, von, bis, punkte, sort_order FROM ps_verein_punkte_zeitraum WHERE preisschiessen_id=$1`,
		sourceID, newPSID); err != nil {
		return "", err
	}

	// ---- Gewinne (wertung_id auf neue Wertungen ummappen, verein_typ-Zeilen
	// unverändert übernehmen) ----
	gRows, err := tx.Query(ctx, `
		SELECT wertung_id::text, verein_typ, platz, betrag, sachpreis
		FROM ps_gewinne WHERE preisschiessen_id=$1`, sourceID)
	if err != nil {
		return "", err
	}
	type gewinnSrc struct {
		wertungID *string
		vereinTyp *string
		platz     int
		betrag    *float64
		sachpreis *string
	}
	var gewinne []gewinnSrc
	for gRows.Next() {
		var x gewinnSrc
		if err := gRows.Scan(&x.wertungID, &x.vereinTyp, &x.platz, &x.betrag, &x.sachpreis); err != nil {
			gRows.Close()
			return "", err
		}
		gewinne = append(gewinne, x)
	}
	gRows.Close()
	if err := gRows.Err(); err != nil {
		return "", err
	}
	for _, x := range gewinne {
		var newWertungID *string
		if x.wertungID != nil {
			if v, ok := wertungIDMap[*x.wertungID]; ok {
				newWertungID = &v
			} else {
				continue // referenzierte Wertung wurde nicht mitkopiert - inkonsistent, überspringen
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ps_gewinne (preisschiessen_id, wertung_id, verein_typ, platz, betrag, sachpreis)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			newPSID, newWertungID, x.vereinTyp, x.platz, x.betrag, x.sachpreis); err != nil {
			return "", err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return newPSID, nil
}

// cloneJoinRows kopiert eine einfache 2-Spalten-Zuordnungstabelle
// (z.B. ps_scheibe_classes: scheibe_id -> class_id), wobei nur die erste
// Spalte über idMap umgeschlüsselt wird (die zweite referenziert eine
// globale, nicht geklonte Tabelle wie shooter_classes).
func cloneJoinRows(ctx context.Context, tx pgx.Tx, selectSQL string, ids []string,
	insertSQL string, idMap map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, selectSQL, ids)
	if err != nil {
		return err
	}
	type pair struct{ a, b string }
	var out []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.a, &p.b); err != nil {
			rows.Close()
			return err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range out {
		if _, err := tx.Exec(ctx, insertSQL, idMap[p.a], p.b); err != nil {
			return err
		}
	}
	return nil
}

func scheibeIDs(s []scheibeSrc) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.id
	}
	return out
}
func setIDs(s []setSrc) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.id
	}
	return out
}
func wertungIDs(s []wertungSrc) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.id
	}
	return out
}

// ----------------------------------------------------------------------------
// HTTP-Handler
// ----------------------------------------------------------------------------

func (a *APIServer) clonePreisschiessen(w http.ResponseWriter, r *http.Request) (any, error) {
	if _, err := a.requireManagePreisschiessen(w, r); err != nil {
		return nil, err
	}
	body, err := decodeBody[struct {
		Name string `json:"name"`
	}](r)
	if err != nil {
		return nil, errBadRequest("ungültiger Body")
	}
	newID, err := a.store.ClonePreisschiessen(r.Context(), r.PathValue("id"), body.Name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": newID}, nil
}
