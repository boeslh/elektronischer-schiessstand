// ============================================================================
// auth.go – Anmeldung der Verwaltung und API-Schluessel der Schiessstaende.
//
// Verwaltung: Benutzername + Passwort (bcrypt). Nach der Anmeldung ein
// zufaelliges Sitzungs-Cookie (HttpOnly, Secure, SameSite=Strict); in der
// Datenbank liegt nur dessen SHA-256. Sitzungen enden nach 2 Stunden ohne
// Nutzung bzw. spaetestens nach 12 Stunden. Jedes Formular traegt ein aus der
// Sitzung abgeleitetes CSRF-Token. Fehlversuche werden je Benutzer und
// Adresse gebremst (nach 5 Fehlversuchen 5 Minuten Sperre).
//
// Schiessstand-Server: "Authorization: Bearer ssk_<key_id>_<geheim>" - der
// Geheimteil (32 Zufallsbytes) wird nur einmal bei der Erzeugung angezeigt,
// gespeichert ist nur sein SHA-256. Der Schluessel gehoert zu genau einem
// Verein und darf nur dessen Veroeffentlichungen schreiben.
// ============================================================================
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie  = "so_session"
	sessionIdle    = 2 * time.Hour
	sessionMaxLife = 12 * time.Hour
	minPasswordLen = 10
)

type App struct {
	cfg      Config
	pool     *pgxpool.Pool
	throttle *loginThrottle
}

// User: angemeldeter Benutzer der Verwaltung.
type User struct {
	ID         string
	Username   string
	VereinID   string // "" = Plattform-Administrator
	VereinName string
	VereinSlug string
	csrf       string
}

func (u *User) IsPlatformAdmin() bool { return u.VereinID == "" }

// dummyHash: fuer unbekannte Benutzernamen (gleiche Rechenzeit wie ein
// echter Passwortvergleich, verraet nicht, ob der Name existiert).
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("kein-benutzer-vorhanden"), 12)

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func hashPassword(pw string) (string, error) {
	if len(pw) < minPasswordLen {
		return "", fmt.Errorf("Passwort zu kurz (mindestens %d Zeichen)", minPasswordLen)
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

func setPlatformAdmin(ctx context.Context, pool *pgxpool.Pool, username, pw string) error {
	h, err := hashPassword(pw)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO users (username, password_hash, verein_id) VALUES ($1, $2, NULL)
		ON CONFLICT (username) DO UPDATE SET password_hash = EXCLUDED.password_hash, verein_id = NULL`,
		strings.TrimSpace(username), h)
	return err
}

// ── Sitzungen ────────────────────────────────────────────────────────────

func (a *App) login(ctx context.Context, w http.ResponseWriter, r *http.Request, username, pw string) error {
	key := strings.ToLower(username) + "|" + clientIP(r)
	if !a.throttle.allowed(key) {
		return errors.New("Zu viele Fehlversuche – bitte einige Minuten warten.")
	}
	var id, hash string
	err := a.pool.QueryRow(ctx, `SELECT id::text, password_hash FROM users WHERE username=$1`, username).Scan(&id, &hash)
	if err != nil {
		// gleiche Rechenzeit wie bei vorhandenem Benutzer
		bcrypt.CompareHashAndPassword(dummyHash, []byte(pw))
		a.throttle.fail(key)
		return errors.New("Benutzername oder Passwort falsch.")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		a.throttle.fail(key)
		return errors.New("Benutzername oder Passwort falsch.")
	}
	a.throttle.reset(key)
	token := randomToken(32)
	if _, err := a.pool.Exec(ctx, `INSERT INTO user_sessions (token_hash, user_id) VALUES ($1, $2::uuid)`,
		sha256hex(token), id); err != nil {
		return err
	}
	a.pool.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1::uuid`, id)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: a.url("/"), HttpOnly: true,
		Secure: a.secureCookies(), SameSite: http.SameSiteStrictMode})
	log.Printf("Anmeldung: %s (%s)", username, clientIP(r))
	return nil
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.pool.Exec(r.Context(), `DELETE FROM user_sessions WHERE token_hash=$1`, sha256hex(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: a.url("/"), MaxAge: -1, HttpOnly: true,
		Secure: a.secureCookies(), SameSite: http.SameSiteStrictMode})
}

func (a *App) secureCookies() bool { return true }

// currentUser: angemeldeter Benutzer oder nil.
func (a *App) currentUser(r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	th := sha256hex(c.Value)
	var u User
	var created, seen time.Time
	err = a.pool.QueryRow(r.Context(), `
		SELECT u.id::text, u.username, COALESCE(u.verein_id::text,''), COALESCE(v.name,''), COALESCE(v.slug,''),
		       s.created_at, s.last_seen
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		LEFT JOIN vereine v ON v.id = u.verein_id
		WHERE s.token_hash = $1`, th).Scan(&u.ID, &u.Username, &u.VereinID, &u.VereinName, &u.VereinSlug, &created, &seen)
	if err != nil {
		return nil
	}
	if time.Since(seen) > sessionIdle || time.Since(created) > sessionMaxLife {
		a.pool.Exec(r.Context(), `DELETE FROM user_sessions WHERE token_hash=$1`, th)
		return nil
	}
	a.pool.Exec(r.Context(), `UPDATE user_sessions SET last_seen=now() WHERE token_hash=$1`, th)
	u.csrf = sha256hex("csrf|" + c.Value)[:32]
	return &u
}

// checkCSRF: Formular-Token und Herkunft pruefen (zusaetzlich zu SameSite=Strict).
func (a *App) checkCSRF(r *http.Request, u *User) bool {
	if o := r.Header.Get("Origin"); o != "" && !strings.HasSuffix(o, "://"+r.Host) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(u.csrf)) == 1
}

func (a *App) cleanupLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.pool.Exec(ctx, `DELETE FROM user_sessions WHERE last_seen < now() - interval '2 hours'
				OR created_at < now() - interval '12 hours'`)
			a.throttle.prune()
		}
	}
}

// ── Bremse fuer Fehlversuche ─────────────────────────────────────────────

type loginThrottle struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginThrottle() *loginThrottle { return &loginThrottle{fails: map[string][]time.Time{}} }

const (
	throttleMax    = 5
	throttleWindow = 5 * time.Minute
)

func (t *loginThrottle) recent(key string) []time.Time {
	var out []time.Time
	for _, f := range t.fails[key] {
		if time.Since(f) < throttleWindow {
			out = append(out, f)
		}
	}
	t.fails[key] = out
	return out
}

func (t *loginThrottle) allowed(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.recent(key)) < throttleMax
}

func (t *loginThrottle) fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fails[key] = append(t.recent(key), time.Now())
}

func (t *loginThrottle) reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, key)
}

func (t *loginThrottle) prune() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.fails {
		if len(t.recent(k)) == 0 {
			delete(t.fails, k)
		}
	}
}

// clientIP: Adresse des Aufrufers (hinter einem Reverse-Proxy aus
// X-Forwarded-For, sonst die Verbindungsadresse).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if (host == "127.0.0.1" || host == "::1") && r.Header.Get("X-Forwarded-For") != "" {
		return strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	}
	return host
}

// ── API-Schluessel ───────────────────────────────────────────────────────

// newAPIKey: neuer Schluessel fuer einen Verein; liefert den vollstaendigen
// Schluessel (nur jetzt sichtbar).
func (a *App) newAPIKey(ctx context.Context, vereinID, name string) (string, error) {
	keyID := hex.EncodeToString(randomBytes(6))
	secret := randomToken(32)
	if _, err := a.pool.Exec(ctx, `INSERT INTO api_keys (key_id, verein_id, name, secret_hash) VALUES ($1, $2::uuid, $3, $4)`,
		keyID, vereinID, name, sha256hex(secret)); err != nil {
		return "", err
	}
	return "ssk_" + keyID + "_" + secret, nil
}

// authAPIKey: Verein zum Bearer-Schluessel (oder Fehler).
func (a *App) authAPIKey(r *http.Request) (vereinID string, err error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ssk_") {
		return "", errors.New("kein API-Schlüssel")
	}
	parts := strings.SplitN(strings.TrimPrefix(h, "Bearer ssk_"), "_", 2)
	if len(parts) != 2 {
		return "", errors.New("API-Schlüssel ungültig")
	}
	var hash string
	var revoked *time.Time
	err = a.pool.QueryRow(r.Context(), `SELECT verein_id::text, secret_hash, revoked_at FROM api_keys WHERE key_id=$1`,
		parts[0]).Scan(&vereinID, &hash, &revoked)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && revoked != nil) ||
		(err == nil && subtle.ConstantTimeCompare([]byte(hash), []byte(sha256hex(parts[1]))) != 1) {
		return "", errors.New("API-Schlüssel ungültig")
	}
	if err != nil {
		return "", err
	}
	a.pool.Exec(r.Context(), `UPDATE api_keys SET last_used_at=now(), last_used_ip=$2 WHERE key_id=$1`, parts[0], clientIP(r))
	return vereinID, nil
}
