package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/lava"
	"github.com/gotsta1/tomas_blank_bot/backend/internal/telegram"
	"golang.org/x/crypto/bcrypt"
)

func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if e := a.Healthy(r.Context()); e != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /webhooks/telegram", a.telegramWebhook)
	m.HandleFunc("POST /webhooks/lava", a.lavaWebhook)
	m.HandleFunc("POST /api/login", a.login)
	m.Handle("POST /api/logout", a.auth(http.HandlerFunc(a.logout)))
	m.Handle("GET /api/session", a.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]bool{"authenticated": true}) })))
	m.Handle("/api/", a.auth(http.HandlerFunc(a.admin)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		bodyLimit := int64(1 << 20)
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/messages/") && strings.HasSuffix(r.URL.Path, "/media") {
			bodyLimit = maxMessageMediaSize + (1 << 20)
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		m.ServeHTTP(w, r)
	})
}
func respond(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func bad(w http.ResponseWriter, code int, msg string) {
	respond(w, code, map[string]string{"error": msg})
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}
func equal(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (a *App) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == a.Config.BaseURL {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host == r.Host
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.originOK(r) {
		bad(w, 403, "Invalid origin")
		return
	}
	// Global account limit cannot be bypassed with spoofed proxy headers.
	var attempts int
	e := a.DB.QueryRow(r.Context(), `INSERT INTO login_attempts(bucket,attempts,window_start) VALUES('admin',1,now()) ON CONFLICT(bucket) DO UPDATE SET attempts=CASE WHEN login_attempts.window_start<now()-interval '5 minutes' THEN 1 ELSE login_attempts.attempts+1 END,window_start=CASE WHEN login_attempts.window_start<now()-interval '5 minutes' THEN now() ELSE login_attempts.window_start END RETURNING attempts`).Scan(&attempts)
	if e != nil {
		bad(w, 503, "Database unavailable")
		return
	}
	if attempts > 20 {
		w.Header().Set("Retry-After", "300")
		bad(w, 429, "Too many attempts")
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if decode(r, &in) != nil || len(in.Password) > 72 {
		bad(w, 400, "Invalid request")
		return
	}
	if a.Config.AdminPasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(a.Config.AdminPasswordHash), []byte(in.Password)) != nil {
		bad(w, 401, "Invalid credentials")
		return
	}
	token := make([]byte, 32)
	if _, e = rand.Read(token); e != nil {
		bad(w, 500, "Session error")
		return
	}
	value := hex.EncodeToString(token)
	expires := time.Now().Add(12 * time.Hour)
	if _, e = a.DB.Exec(r.Context(), "INSERT INTO admin_sessions(token_hash,expires_at) VALUES($1,$2)", digest(value), expires); e != nil {
		bad(w, 503, "Session error")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Value: value, Path: "/api", Expires: expires, HttpOnly: true, Secure: strings.HasPrefix(a.Config.BaseURL, "https://"), SameSite: http.SameSiteStrictMode})
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && !a.originOK(r) {
			bad(w, 403, "Invalid origin")
			return
		}
		cookie, e := r.Cookie("admin_session")
		if e != nil {
			bad(w, 401, "Authentication required")
			return
		}
		var ok bool
		if e = a.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM admin_sessions WHERE token_hash=$1 AND expires_at>now())", digest(cookie.Value)).Scan(&ok); e != nil {
			bad(w, 503, "Database unavailable")
			return
		}
		if !ok {
			bad(w, 401, "Authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie("admin_session")
	if _, e := a.DB.Exec(r.Context(), "DELETE FROM admin_sessions WHERE token_hash=$1", digest(cookie.Value)); e != nil {
		bad(w, 503, "Database unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Path: "/api", Value: "", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(a.Config.BaseURL, "https://"), SameSite: http.SameSiteStrictMode})
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	if !equal(a.Config.TelegramSecret, r.Header.Get("X-Telegram-Bot-Api-Secret-Token")) {
		bad(w, 401, "Unauthorized")
		return
	}
	b, e := io.ReadAll(r.Body)
	if e != nil {
		bad(w, 400, "Invalid payload")
		return
	}
	var u telegram.Update
	if json.Unmarshal(b, &u) != nil || u.ID == 0 {
		bad(w, 400, "Invalid update")
		return
	}
	a.saveEvent(w, r, "telegram", jsonNumber(u.ID), b)
}
func (a *App) lavaWebhook(w http.ResponseWriter, r *http.Request) {
	if !equal(a.Config.LavaSecret, r.Header.Get("X-Api-Key")) {
		bad(w, 401, "Unauthorized")
		return
	}
	b, e := io.ReadAll(r.Body)
	if e != nil || !json.Valid(b) {
		bad(w, 400, "Invalid payload")
		return
	}
	var event lava.Event
	if json.Unmarshal(b, &event) != nil {
		bad(w, 400, "Invalid event")
		return
	}
	key := digest(string(b))
	if event.EventID != "" {
		key = event.EnvelopeType + ":" + event.EventID
	}
	a.saveEvent(w, r, "lava", key, b)
}
func (a *App) saveEvent(w http.ResponseWriter, r *http.Request, provider, key string, b []byte) {
	if _, e := a.DB.Exec(r.Context(), "INSERT INTO webhook_events(provider,dedup_key,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", provider, key, b); e != nil {
		bad(w, 503, "Database unavailable")
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
