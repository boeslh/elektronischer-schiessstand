// ============================================================================
// admin.go – Verwaltung (/admin): Vereine, Benutzer, API-Schluessel und
// Veroeffentlichungen.
//
// Plattform-Administrator: alle Vereine und Benutzer, alle Veroeffentlichungen.
// Vereinsbenutzer: Benutzer, API-Schluessel und Veroeffentlichungen des
// eigenen Vereins. Jeder Benutzer kann sein eigenes Passwort aendern.
// ============================================================================
package main

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}$`)

func (a *App) requireUser(w http.ResponseWriter, r *http.Request) *User {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, a.url("/admin/login"), http.StatusSeeOther)
		return nil
	}
	if r.Method == http.MethodPost && !a.checkCSRF(r, u) {
		http.Error(w, "Sicherheitsprüfung fehlgeschlagen – bitte Seite neu laden.", http.StatusForbidden)
		return nil
	}
	return u
}

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if r.Method == http.MethodPost {
		if err := a.login(r.Context(), w, r, strings.TrimSpace(r.FormValue("username")), r.FormValue("password")); err != nil {
			msg = err.Error()
		} else {
			http.Redirect(w, r, a.url("/admin/"), http.StatusSeeOther)
			return
		}
	}
	body := `<div class="card narrow"><h2>Anmeldung</h2>`
	if msg != "" {
		body += `<p class="err">` + html.EscapeString(msg) + `</p>`
	}
	body += `<form method="post" action="/admin/login">
<label>Benutzername<input name="username" autocomplete="username" required autofocus></label>
<label>Passwort<input name="password" type="password" autocomplete="current-password" required></label>
<button type="submit">Anmelden</button></form></div>`
	a.page(w, "Anmeldung", "", body, true)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if u := a.requireUser(w, r); u == nil {
		return
	}
	a.logout(w, r)
	http.Redirect(w, r, a.url("/admin/login"), http.StatusSeeOther)
}

func (a *App) handleAdmin(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	a.renderAdmin(w, r, u, r.URL.Query().Get("msg"), "")
}

// adminAction: alle POST-Aktionen der Verwaltung.
func (a *App) handleAdminAction(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	ctx := r.Context()
	action := r.PathValue("action")
	id := r.FormValue("id")
	msg, shownKey := "", ""
	var err error
	switch action {
	case "verein-neu":
		if !u.IsPlatformAdmin() {
			err = errForbidden
			break
		}
		name, slug := strings.TrimSpace(r.FormValue("name")), strings.ToLower(strings.TrimSpace(r.FormValue("slug")))
		if name == "" || !slugRe.MatchString(slug) {
			err = fmt.Errorf("Name und Kürzel angeben (Kürzel: Kleinbuchstaben, Ziffern, Bindestrich)")
			break
		}
		_, err = a.pool.Exec(ctx, `INSERT INTO vereine (name, slug) VALUES ($1, $2)`, name, slug)
		msg = "Verein angelegt."
	case "verein-loeschen":
		if !u.IsPlatformAdmin() {
			err = errForbidden
			break
		}
		_, err = a.pool.Exec(ctx, `DELETE FROM vereine WHERE id=$1::uuid`, id)
		msg = "Verein mit allen Benutzern, Schlüsseln und Veröffentlichungen gelöscht."
	case "benutzer-neu":
		name := strings.TrimSpace(r.FormValue("username"))
		vid := r.FormValue("verein_id")
		if !u.IsPlatformAdmin() {
			vid = u.VereinID
		}
		var h string
		if h, err = hashPassword(r.FormValue("password")); err != nil {
			break
		}
		if name == "" {
			err = fmt.Errorf("Benutzername fehlt")
			break
		}
		_, err = a.pool.Exec(ctx, `INSERT INTO users (username, password_hash, verein_id) VALUES ($1, $2, NULLIF($3,'')::uuid)`,
			name, h, vid)
		msg = "Benutzer angelegt."
	case "benutzer-loeschen":
		if id == u.ID {
			err = fmt.Errorf("Den eigenen Benutzer kann man nicht löschen")
			break
		}
		_, err = a.pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid AND ($2 = '' OR verein_id = NULLIF($2,'')::uuid)`,
			id, scopeVerein(u))
		msg = "Benutzer gelöscht."
	case "benutzer-passwort":
		var h string
		if h, err = hashPassword(r.FormValue("password")); err != nil {
			break
		}
		_, err = a.pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1::uuid AND ($3 = '' OR verein_id = NULLIF($3,'')::uuid)`,
			id, h, scopeVerein(u))
		if err == nil {
			a.pool.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1::uuid AND user_id <> $2::uuid`, id, u.ID)
		}
		msg = "Passwort gesetzt."
	case "eigenes-passwort":
		var h string
		if h, err = hashPassword(r.FormValue("password")); err != nil {
			break
		}
		_, err = a.pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1::uuid`, u.ID, h)
		msg = "Eigenes Passwort geändert."
	case "schluessel-neu":
		vid := u.VereinID
		if u.IsPlatformAdmin() {
			vid = r.FormValue("verein_id")
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if vid == "" || name == "" {
			err = fmt.Errorf("Bezeichnung (und Verein) angeben")
			break
		}
		shownKey, err = a.newAPIKey(ctx, vid, name)
		msg = "API-Schlüssel erzeugt."
	case "schluessel-sperren":
		_, err = a.pool.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE key_id=$1 AND ($2 = '' OR verein_id = NULLIF($2,'')::uuid)`,
			id, scopeVerein(u))
		msg = "API-Schlüssel gesperrt."
	case "veroeffentlichung-loeschen":
		var tag interface{ RowsAffected() int64 }
		tag, err = a.pool.Exec(ctx, `DELETE FROM publications WHERE id=$1::uuid AND ($2 = '' OR verein_id = NULLIF($2,'')::uuid)`,
			id, scopeVerein(u))
		if err == nil && tag.RowsAffected() == 1 {
			log.Printf("Veröffentlichung %s gelöscht von %s", id, u.Username)
		}
		msg = "Veröffentlichung gelöscht."
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		msg = "Fehler: " + friendlyErr(err)
		shownKey = ""
	}
	if shownKey != "" {
		a.renderAdmin(w, r, u, msg, shownKey) // Schluessel nur in dieser Antwort sichtbar
		return
	}
	http.Redirect(w, r, a.url("/admin/?msg=")+urlQueryEscape(msg), http.StatusSeeOther)
}

var errForbidden = fmt.Errorf("keine Berechtigung")

func scopeVerein(u *User) string {
	if u.IsPlatformAdmin() {
		return ""
	}
	return u.VereinID
}

func friendlyErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "duplicate key") && strings.Contains(s, "username"):
		return "Benutzername ist schon vergeben"
	case strings.Contains(s, "duplicate key") && strings.Contains(s, "slug"):
		return "Kürzel ist schon vergeben"
	case strings.Contains(s, "invalid input syntax"):
		return "ungültige Eingabe"
	}
	return s
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "–"
	}
	return t.In(berlin).Format("02.01.2006 15:04")
}

func (a *App) renderAdmin(w http.ResponseWriter, r *http.Request, u *User, notice, shownKey string) {
	ctx := r.Context()
	var b strings.Builder
	csrf := `<input type="hidden" name="csrf" value="` + u.csrf + `">`
	confirm := func(text string) string {
		return ` onsubmit="return confirm('` + html.EscapeString(text) + `')"`
	}
	if notice != "" {
		cls := "ok"
		if strings.HasPrefix(notice, "Fehler") {
			cls = "err"
		}
		fmt.Fprintf(&b, `<p class="%s">%s</p>`, cls, html.EscapeString(notice))
	}
	if shownKey != "" {
		fmt.Fprintf(&b, `<div class="card key"><h3>Neuer API-Schlüssel</h3>
<p>Diesen Schlüssel jetzt im Schießstand-Server unter <em>Einstellungen → Online-Veröffentlichung</em> eintragen.
Er wird <strong>nur dieses eine Mal</strong> angezeigt.</p><code class="secret">%s</code></div>`, html.EscapeString(shownKey))
	}
	who := "Plattform-Administrator"
	if !u.IsPlatformAdmin() {
		who = "Verein " + html.EscapeString(u.VereinName) + ` – öffentliche Seite: <a href="/v/` +
			html.EscapeString(u.VereinSlug) + `/">/v/` + html.EscapeString(u.VereinSlug) + `/</a>`
	}
	fmt.Fprintf(&b, `<div class="card"><p>Angemeldet als <strong>%s</strong> · %s</p>
<form method="post" action="/admin/logout" style="display:inline">%s<button class="sec">Abmelden</button></form></div>`,
		html.EscapeString(u.Username), who, csrf)

	scope := scopeVerein(u)
	// Vereine (nur Plattform-Administrator)
	type verein struct {
		id, name, slug string
		pubs           int
	}
	var vereine []verein
	if u.IsPlatformAdmin() {
		rows, _ := a.pool.Query(ctx, `SELECT v.id::text, v.name, v.slug, (SELECT count(*) FROM publications p WHERE p.verein_id=v.id)
			FROM vereine v ORDER BY v.name`)
		for rows != nil && rows.Next() {
			var v verein
			rows.Scan(&v.id, &v.name, &v.slug, &v.pubs)
			vereine = append(vereine, v)
		}
		if rows != nil {
			rows.Close()
		}
		b.WriteString(`<div class="card"><h2>Vereine</h2><table><tr><th>Name</th><th>Kürzel (Adresse)</th><th>Veröffentlichungen</th><th></th></tr>`)
		for _, v := range vereine {
			fmt.Fprintf(&b, `<tr><td>%s</td><td><a href="/v/%s/">/v/%s/</a></td><td>%d</td><td><form method="post" action="/admin/aktion/verein-loeschen"%s>%s<input type="hidden" name="id" value="%s"><button class="danger">Löschen</button></form></td></tr>`,
				html.EscapeString(v.name), v.slug, v.slug, v.pubs,
				confirm("Verein "+v.name+" mit ALLEN Benutzern, Schlüsseln und Veröffentlichungen löschen?"), csrf, v.id)
		}
		fmt.Fprintf(&b, `</table><form method="post" action="/admin/aktion/verein-neu" class="row">%s
<input name="name" placeholder="Name des Vereins" required><input name="slug" placeholder="kuerzel (für die Adresse)" required pattern="[a-z0-9][a-z0-9-]{1,40}">
<button>Verein anlegen</button></form></div>`, csrf)
	}

	// Benutzer
	b.WriteString(`<div class="card"><h2>Benutzer</h2><table><tr><th>Benutzer</th><th>Verein</th><th>Letzte Anmeldung</th><th>Passwort setzen</th><th></th></tr>`)
	rows, err := a.pool.Query(ctx, `SELECT u.id::text, u.username, COALESCE(v.name,'Plattform-Administrator'), u.last_login_at
		FROM users u LEFT JOIN vereine v ON v.id=u.verein_id
		WHERE ($1 = '' OR u.verein_id = NULLIF($1,'')::uuid) ORDER BY v.name NULLS FIRST, u.username`, scope)
	if err == nil {
		for rows.Next() {
			var id, name, vn string
			var last *time.Time
			rows.Scan(&id, &name, &vn, &last)
			fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td>
<td><form method="post" action="/admin/aktion/benutzer-passwort" class="row">%s<input type="hidden" name="id" value="%s"><input name="password" type="password" placeholder="neues Passwort" minlength="%d" required autocomplete="new-password"><button class="sec">Setzen</button></form></td>
<td>`, html.EscapeString(name), html.EscapeString(vn), fmtTime(last), csrf, id, minPasswordLen)
			if id != u.ID {
				fmt.Fprintf(&b, `<form method="post" action="/admin/aktion/benutzer-loeschen"%s>%s<input type="hidden" name="id" value="%s"><button class="danger">Löschen</button></form>`,
					confirm("Benutzer "+name+" löschen?"), csrf, id)
			}
			b.WriteString(`</td></tr>`)
		}
		rows.Close()
	}
	b.WriteString(`</table><form method="post" action="/admin/aktion/benutzer-neu" class="row">` + csrf +
		`<input name="username" placeholder="Benutzername" required autocomplete="off">` +
		fmt.Sprintf(`<input name="password" type="password" placeholder="Passwort (mind. %d Zeichen)" minlength="%d" required autocomplete="new-password">`, minPasswordLen, minPasswordLen))
	if u.IsPlatformAdmin() {
		b.WriteString(`<select name="verein_id"><option value="">Plattform-Administrator</option>`)
		for _, v := range vereine {
			fmt.Fprintf(&b, `<option value="%s">%s</option>`, v.id, html.EscapeString(v.name))
		}
		b.WriteString(`</select>`)
	}
	b.WriteString(`<button>Benutzer anlegen</button></form></div>`)

	// API-Schluessel
	b.WriteString(`<div class="card"><h2>API-Schlüssel der Schießstand-Server</h2>
<p class="hint">Jeder Schießstand-Server eines Vereins meldet sich mit einem Schlüssel an. Ein gesperrter Schlüssel kann keine Daten mehr übertragen; bereits veröffentlichte Daten bleiben erhalten.</p>
<table><tr><th>Bezeichnung</th><th>Verein</th><th>Kennung</th><th>Erzeugt</th><th>Zuletzt benutzt</th><th></th></tr>`)
	rows, err = a.pool.Query(ctx, `SELECT k.key_id, k.name, v.name, k.created_at, k.last_used_at, COALESCE(k.last_used_ip,''), k.revoked_at
		FROM api_keys k JOIN vereine v ON v.id=k.verein_id
		WHERE ($1 = '' OR k.verein_id = NULLIF($1,'')::uuid) ORDER BY v.name, k.created_at`, scope)
	if err == nil {
		for rows.Next() {
			var kid, name, vn, ip string
			var created time.Time
			var used, revoked *time.Time
			rows.Scan(&kid, &name, &vn, &created, &used, &ip, &revoked)
			action := `<span class="hint">gesperrt ` + fmtTime(revoked) + `</span>`
			if revoked == nil {
				action = fmt.Sprintf(`<form method="post" action="/admin/aktion/schluessel-sperren"%s>%s<input type="hidden" name="id" value="%s"><button class="danger">Sperren</button></form>`,
					confirm("Schlüssel "+name+" sperren?"), csrf, kid)
			}
			usedTxt := fmtTime(used)
			if ip != "" {
				usedTxt += " (" + html.EscapeString(ip) + ")"
			}
			fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td><code>ssk_%s_…</code></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				html.EscapeString(name), html.EscapeString(vn), kid, fmtTime(&created), usedTxt, action)
		}
		rows.Close()
	}
	b.WriteString(`</table><form method="post" action="/admin/aktion/schluessel-neu" class="row">` + csrf +
		`<input name="name" placeholder="Bezeichnung, z. B. Schießstand Vereinsheim" required>`)
	if u.IsPlatformAdmin() {
		b.WriteString(`<select name="verein_id" required><option value="">– Verein –</option>`)
		for _, v := range vereine {
			fmt.Fprintf(&b, `<option value="%s">%s</option>`, v.id, html.EscapeString(v.name))
		}
		b.WriteString(`</select>`)
	}
	b.WriteString(`<button>Schlüssel erzeugen</button></form></div>`)

	// Veroeffentlichungen
	b.WriteString(`<div class="card"><h2>Veröffentlichungen</h2>
<p class="hint">Löschen entfernt die Anzeige mit allen Daten vom Online-Server. Überträgt ein Schießstand-Server sie weiterhin, wird sie dabei neu angelegt – dort die Veröffentlichung vorher beenden.</p>
<table><tr><th>Titel</th><th>Art</th><th>Verein</th><th>Datum</th><th>Status</th><th>Aktualisiert</th><th></th></tr>`)
	rows, err = a.pool.Query(ctx, `SELECT p.id::text, p.title, p.kind, v.name, v.slug, COALESCE(to_char(p.starts_on,'DD.MM.YYYY'),''), p.status, p.updated_at
		FROM publications p JOIN vereine v ON v.id=p.verein_id
		WHERE ($1 = '' OR p.verein_id = NULLIF($1,'')::uuid) ORDER BY p.updated_at DESC`, scope)
	if err == nil {
		for rows.Next() {
			var id, title, kind, vn, slug, datum, status string
			var upd time.Time
			rows.Scan(&id, &title, &kind, &vn, &slug, &datum, &status, &upd)
			path, art := "ps", "Preisschießen"
			if kind == "rwk" {
				path, art = "rwk", "Rundenwettkampf"
			}
			fmt.Fprintf(&b, `<tr><td><a href="/v/%s/%s/%s/">%s</a></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td>
<td><form method="post" action="/admin/aktion/veroeffentlichung-loeschen"%s>%s<input type="hidden" name="id" value="%s"><button class="danger">Löschen</button></form></td></tr>`,
				slug, path, id, html.EscapeString(title), art, html.EscapeString(vn), datum, html.EscapeString(statusLabel(status)), fmtTime(&upd),
				confirm(art+" „"+title+"“ mit allen Daten endgültig vom Online-Server löschen?"), csrf, id)
		}
		rows.Close()
	}
	b.WriteString(`</table></div>`)

	fmt.Fprintf(&b, `<div class="card narrow"><h2>Eigenes Passwort ändern</h2><form method="post" action="/admin/aktion/eigenes-passwort">%s
<label>Neues Passwort<input name="password" type="password" minlength="%d" required autocomplete="new-password"></label><button>Ändern</button></form></div>`,
		csrf, minPasswordLen)
	a.page(w, "Verwaltung", "", b.String(), true)
}

func statusLabel(s string) string {
	switch s {
	case "running", "laufend":
		return "läuft"
	case "finished", "archived", "beendet":
		return "beendet"
	case "planned":
		return "geplant"
	}
	return s
}
