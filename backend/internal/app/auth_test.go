package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func authFixture(t *testing.T) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.LoginUsername, cfg.LoginPassword = "teacher", "test-only-password-123"
	cfg.AppEnv, cfg.UploadDir = "test", filepath.Join(t.TempDir(), "photos")
	store, err := OpenStore(filepath.Join(t.TempDir(), "test.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return NewServer(cfg, store)
}

func authRequest(s *Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Origin", s.Config.PublicBaseURL)
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	return response
}

func authSignIn(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": s.Config.LoginUsername, "password": s.Config.LoginPassword})
	response := authRequest(s, "POST", "/auth/login", string(body), nil)
	if response.Code != 200 {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie: %v", cookies)
	}
	return cookies[0]
}

func TestAuthCookieLifecycle(t *testing.T) {
	s := authFixture(t)
	cookie := authSignIn(t, s)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 7*86400 {
		t.Fatalf("unsafe cookie: %+v", cookie)
	}
	var digest string
	if err := s.Store.DB.QueryRow("SELECT id FROM login_sessions").Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if digest == cookie.Value || digest != s.sessionDigest(cookie.Value) {
		t.Fatal("session token stored incorrectly")
	}
	profile := authRequest(s, "GET", "/auth/me", "", cookie)
	if profile.Code != 200 || !strings.Contains(profile.Body.String(), `"username":"teacher"`) {
		t.Fatal(profile.Body.String())
	}
	if response := authRequest(s, "POST", "/auth/logout", "", cookie); response.Code != 204 {
		t.Fatal(response.Code)
	}
	if response := authRequest(s, "GET", "/api/folders", "", cookie); response.Code != 401 {
		t.Fatal(response.Code)
	}
}

func TestAuthRejectsWrongCredentialsAndMissingConfiguration(t *testing.T) {
	s := authFixture(t)
	wrongUser := authRequest(s, "POST", "/auth/login", `{"username":"wrong","password":"test-only-password-123"}`, nil)
	wrongPass := authRequest(s, "POST", "/auth/login", `{"username":"teacher","password":"private-wrong-pass"}`, nil)
	if wrongUser.Code != 401 || wrongPass.Code != 401 || !bytes.Equal(wrongUser.Body.Bytes(), wrongPass.Body.Bytes()) {
		t.Fatal("credential failures differ")
	}
	var count int
	s.Store.DB.QueryRow("SELECT count(*) FROM users").Scan(&count)
	if count != 0 {
		t.Fatal("invalid login created user")
	}
	for _, clearUsername := range []bool{true, false} {
		cfg := s.Config
		if clearUsername {
			cfg.LoginUsername = ""
		} else {
			cfg.LoginPassword = ""
		}
		response := authRequest(NewServer(cfg, s.Store), "POST", "/auth/login", `{}`, nil)
		if response.Code != 503 {
			t.Fatal(response.Code)
		}
	}
}

func TestAuthSessionRotationExpiryAndCredentialChange(t *testing.T) {
	s := authFixture(t)
	old := authSignIn(t, s)
	body := `{"username":"teacher","password":"test-only-password-123"}`
	rotation := authRequest(s, "POST", "/auth/login", body, old)
	if rotation.Code != 200 {
		t.Fatal(rotation.Code)
	}
	cookie := rotation.Result().Cookies()[0]
	if response := authRequest(s, "GET", "/auth/me", "", old); response.Code != 401 {
		t.Fatal("old token still active")
	}
	if _, err := s.Store.DB.Exec("UPDATE login_sessions SET expires_at=?", utcMillis()-1); err != nil {
		t.Fatal(err)
	}
	if response := authRequest(s, "GET", "/auth/me", "", cookie); response.Code != 401 {
		t.Fatal("expired session accepted")
	}
	for _, field := range []string{"username", "password"} {
		cookie = authSignIn(t, s)
		cfg := s.Config
		if field == "username" {
			cfg.LoginUsername = "renamed"
		} else {
			cfg.LoginPassword = "changed-password"
		}
		changed := NewServer(cfg, s.Store)
		if response := authRequest(changed, "GET", "/auth/me", "", cookie); response.Code != 401 {
			t.Fatal("credential rotation did not revoke session")
		}
		authSignIn(t, changed)
		var count int
		s.Store.DB.QueryRow("SELECT count(*) FROM users").Scan(&count)
		if count != 1 {
			t.Fatal("renaming login created a new owner")
		}
	}
}

func TestAuthPersistenceAndOwnership(t *testing.T) {
	s := authFixture(t)
	cookie := authSignIn(t, s)
	restarted := NewServer(s.Config, s.Store)
	if response := authRequest(restarted, "GET", "/auth/me", "", cookie); response.Code != 200 {
		t.Fatal("session lost on restart")
	}
	_, err := s.Store.DB.Exec("INSERT INTO users VALUES(?,?,?,?,?)", "other", "other", "other@example.test", "Other", utcMillis())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Store.DB.Exec("UPDATE login_sessions SET user_id='other'")
	if err != nil {
		t.Fatal(err)
	}
	if response := authRequest(s, "GET", "/api/folders", "", cookie); response.Code != 403 {
		t.Fatal("foreign user accepted")
	}
}

func TestAuthUnicodeAndSecureCookie(t *testing.T) {
	s := authFixture(t)
	s.Config.LoginUsername, s.Config.LoginPassword, s.Config.CookieSecure = "õpetaja", "väga-pikk-parool-123!", true
	if cookie := authSignIn(t, s); !cookie.Secure {
		t.Fatal("secure flag missing")
	}
}

func TestAuthThrottleAndValidation(t *testing.T) {
	s := authFixture(t)
	for range 5 {
		if response := authRequest(s, "POST", "/auth/login", `{"username":"teacher","password":"wrong"}`, nil); response.Code != 401 {
			t.Fatal(response.Code)
		}
	}
	blocked := authRequest(s, "POST", "/auth/login", `{"username":"teacher","password":"test-only-password-123"}`, nil)
	if blocked.Code != 429 || blocked.Header().Get("Retry-After") == "" {
		t.Fatal("throttle missing")
	}
	s.throttle.attempts = []time.Time{time.Now().Add(-61 * time.Second)}
	authSignIn(t, s)
	if len(s.throttle.attempts) != 0 {
		t.Fatal("successful login did not reset throttle")
	}
	invalid := authRequest(s, "POST", "/auth/login", `{"password":"private-password"}`, nil)
	if invalid.Code != 422 || strings.Contains(invalid.Body.String(), "private-password") {
		t.Fatal("invalid credentials leaked")
	}
}

func TestAuthOriginBodyLimitsAndRemovedGoogleRoutes(t *testing.T) {
	s := authFixture(t)
	for _, origin := range []string{"", "https://evil.example"} {
		request := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{}`))
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != 403 {
			t.Fatal("untrusted mutation accepted")
		}
	}
	for _, chunked := range []bool{false, true} {
		request := httptest.NewRequest("POST", "/auth/login", strings.NewReader(strings.Repeat(" ", 65537)))
		request.Header.Set("Origin", s.Config.PublicBaseURL)
		if chunked {
			request.ContentLength = -1
		}
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != 413 {
			t.Fatalf("size limit failed: %d", response.Code)
		}
	}
	for _, path := range []string{"/auth/google", "/auth/callback", "/uploads/private.jpg"} {
		if response := authRequest(s, "GET", path, "", nil); response.Code != 404 {
			t.Fatal(response.Code)
		}
	}
}
