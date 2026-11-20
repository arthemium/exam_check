package app

import (
	"path/filepath"
	"testing"
)

func TestConfigOriginsAndProduction(t *testing.T) {
	for _, origin := range []string{"https://example.com?x=1", "https://example.com#", "https://user:pass@example.com", "https://example.com/path", "https://example.com:invalid", "https://example.com:70000", "https://example.com:", "https://bad host", "*"} {
		cfg := DefaultConfig()
		cfg.PublicBaseURL = origin
		if cfg.Validate() == nil {
			t.Errorf("accepted origin %q", origin)
		}
		cfg = DefaultConfig()
		cfg.FrontendOrigins = []string{origin}
		if cfg.Validate() == nil {
			t.Errorf("accepted frontend origin %q", origin)
		}
	}
	cfg := DefaultConfig()
	cfg.AppEnv = "production"
	if cfg.Validate() == nil {
		t.Fatal("insecure production accepted")
	}
	cfg.PublicBaseURL, cfg.CookieSecure = "https://example.com/", true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.PublicBaseURL != "https://example.com" {
		t.Fatal("origin not normalized")
	}
}

func TestConfigEnvironmentAndLimits(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:8000/")
	t.Setenv("FRONTEND_ORIGINS", `["http://localhost:5173/"]`)
	t.Setenv("LOGIN_PASSWORD", "test-only-password")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicBaseURL != "http://localhost:8000" || !cfg.allowsOrigin("http://localhost:5173") {
		t.Fatal("environment origin normalization failed")
	}
	t.Setenv("JOB_MAX_ATTEMPTS", "0")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("invalid retry limit accepted")
	}
}

func TestConfigListenAddressPrecedence(t *testing.T) {
	for _, test := range []struct {
		name, address, port, expected string
	}{
		{"default", "", "", ":8000"},
		{"platform port", "", "49152", ":49152"},
		{"minimum port", "", "1", ":1"},
		{"maximum port", "", "65535", ":65535"},
		{"explicit address", "127.0.0.1:9000", "49152", "127.0.0.1:9000"},
		{"explicit address ignores unused port", "[::]:9000", "invalid", "[::]:9000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("LISTEN_ADDR", test.address)
			t.Setenv("PORT", test.port)
			cfg, err := LoadConfig()
			if err != nil || cfg.ListenAddr != test.expected {
				t.Fatalf("listen address: %q, %v", cfg.ListenAddr, err)
			}
			if DefaultConfig().ListenAddr != ":8000" {
				t.Fatal("default configuration depends on the environment")
			}
		})
	}
}

func TestConfigRejectsInvalidPlatformPort(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	for _, port := range []string{"0", "-1", "65536", "999999999999999999999", "8000.5", "private-token", " 8000 "} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("PORT", port)
			_, err := LoadConfig()
			if err == nil || err.Error() != "PORT must be an integer between 1 and 65535" {
				t.Fatalf("unexpected port validation: %v", err)
			}
		})
	}
}

func TestStorePersistsSchemaAndForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db", "examcheck.db")
	store, err := OpenStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec("INSERT INTO folders VALUES('folder','missing','A','a',1)"); err == nil {
		t.Fatal("foreign keys disabled")
	}
	var mode string
	if err = store.DB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL not enabled: %s %v", mode, err)
	}
	store.Close()
	store, err = OpenStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err = store.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema not persistent: %d %v", version, err)
	}
}
