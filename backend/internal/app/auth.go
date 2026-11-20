package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

const sessionCookie = "exam_session"

type loginThrottle struct {
	mu       sync.Mutex
	attempts []time.Time
}

func (t *loginThrottle) reserve(now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.attempts) > 0 && !t.attempts[0].After(now.Add(-time.Minute)) {
		t.attempts = t.attempts[1:]
	}
	if len(t.attempts) >= 5 {
		return int(t.attempts[0].Add(time.Minute).Sub(now).Seconds()) + 1
	}
	t.attempts = append(t.attempts, now)
	return 0
}

func (t *loginThrottle) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attempts = nil
}

func (s *Server) loginConfigured(w http.ResponseWriter) bool {
	if s.Config.LoginUsername != "" && s.Config.LoginPassword != "" {
		return true
	}
	writeError(w, 503, "Login has not been configured.")
	return false
}

func (s *Server) sessionDigest(token string) string {
	mac := hmac.New(sha256.New, []byte(s.Config.LoginPassword))
	mac.Write([]byte(s.Config.LoginUsername + "\x00" + token))
	return hex.EncodeToString(mac.Sum(nil))
}

func credentialsMatch(actual, expected string) int {
	a, b := sha256.Sum256([]byte(actual)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(a[:], b[:])
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginConfigured(w) {
		return
	}
	var credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &credentials) {
		return
	}
	if credentials.Username == "" || credentials.Password == "" || utf8.RuneCountInString(credentials.Username) > 120 || utf8.RuneCountInString(credentials.Password) > 1024 {
		writeError(w, 422, "Provide a valid username and password.")
		return
	}
	if retry := s.throttle.reserve(time.Now()); retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeError(w, 429, "Too many login attempts. Try again shortly.")
		return
	}
	usernameOK := credentialsMatch(credentials.Username, s.Config.LoginUsername)
	passwordOK := credentialsMatch(credentials.Password, s.Config.LoginPassword)
	if usernameOK&passwordOK != 1 {
		writeError(w, 401, "Invalid username or password.")
		return
	}
	token, err := s.createSession(r)
	if err != nil {
		writeError(w, 500, "Sign-in could not be completed.")
		return
	}
	s.throttle.clear()
	s.setCookie(w, token, s.Config.SessionDays*86400)
	s.profile(w, r)
}

func (s *Server) createSession(r *http.Request) (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO users(id,google_sub,email,display_name,created_at)
	 VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name`,
		ownerID, ownerID, "owner@examcheck.invalid", s.Config.LoginUsername, utcMillis())
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM login_sessions WHERE expires_at<=?", utcMillis()); err != nil {
		return "", err
	}
	if previous, err := r.Cookie(sessionCookie); err == nil {
		if _, err = tx.ExecContext(r.Context(), "DELETE FROM login_sessions WHERE id=?", s.sessionDigest(previous.Value)); err != nil {
			return "", err
		}
	}
	expires := time.Now().Add(time.Duration(s.Config.SessionDays) * 24 * time.Hour).UnixMilli()
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO login_sessions(id,user_id,expires_at) VALUES(?,?,?)", s.sessionDigest(token), ownerID, expires); err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if !s.loginConfigured(w) {
		return false
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" || len(cookie.Value) > 256 {
		writeError(w, 401, "Sign in to continue.")
		return false
	}
	var expires int64
	var userID string
	digest := s.sessionDigest(cookie.Value)
	err = s.Store.DB.QueryRowContext(r.Context(), "SELECT user_id,expires_at FROM login_sessions WHERE id=?", digest).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 401, "Sign in to continue.")
		return false
	}
	if err != nil {
		writeError(w, 503, "Authentication is temporarily unavailable.")
		return false
	}
	if expires <= utcMillis() {
		s.Store.DB.ExecContext(r.Context(), "DELETE FROM login_sessions WHERE id=?", digest)
		writeError(w, 401, "Your session has expired. Sign in again.")
		return false
	}
	if userID != ownerID {
		writeError(w, 403, "This account does not have access.")
		return false
	}
	return true
}

func (s *Server) setCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: s.Config.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"id": ownerID, "username": s.Config.LoginUsername, "display_name": s.Config.LoginUsername})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.Store.DB.ExecContext(r.Context(), "DELETE FROM login_sessions WHERE id=?", s.sessionDigest(cookie.Value)); err != nil {
			writeError(w, 503, "Sign-out could not be completed.")
			return
		}
	}
	s.setCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}
