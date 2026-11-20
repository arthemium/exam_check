package main

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"testing"

	"examcheck/internal/app"
)

func captureStartupLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

func TestStartupConfigWarnsAboutUnreachableBindingWithoutSecrets(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("LISTEN_ADDR", "127.0.0.1:8000")
	cfg := app.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:8000"
	cfg.PublicBaseURL = "https://private-url.example"
	cfg.LoginUsername, cfg.LoginPassword = "private-user", "private-password"
	cfg.OpenAIKey, cfg.OpenAIModel = "private-key", "private-model"
	cfg.DBPath, cfg.UploadDir = "private-db", "private-uploads"
	output := captureStartupLogs(t)
	logStartupConfig(cfg)
	for _, expected := range []string{
		`"msg":"configuration_ready"`, `"listen_source":"LISTEN_ADDR"`,
		`"msg":"loopback_listener"`, `"msg":"platform_port_mismatch"`,
		`"platform_port":8080`, `"login_configured":true`, `"openai_key_configured":true`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %s: %s", expected, output)
		}
	}
	if strings.Contains(output.String(), "private-") {
		t.Fatalf("startup log exposed private configuration: %s", output)
	}
}

func TestStartupConfigUsesPlatformPortWithoutWarning(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("LISTEN_ADDR", "")
	cfg := app.DefaultConfig()
	cfg.ListenAddr = ":8080"
	output := captureStartupLogs(t)
	logStartupConfig(cfg)
	if !strings.Contains(output.String(), `"listen_source":"PORT"`) || strings.Contains(output.String(), `"level":"WARN"`) {
		t.Fatalf("incorrect platform binding log: %s", output)
	}
}

func TestStartupConfigDoesNotLogInvalidPlatformPortValue(t *testing.T) {
	t.Setenv("PORT", "private-token")
	t.Setenv("LISTEN_ADDR", ":8000")
	output := captureStartupLogs(t)
	logStartupConfig(app.DefaultConfig())
	if !strings.Contains(output.String(), `"msg":"invalid_platform_port"`) || strings.Contains(output.String(), "private-token") {
		t.Fatalf("unsafe port diagnostic: %s", output)
	}
}

func TestListenerLogsBoundPortOnlyAfterSuccessfulBind(t *testing.T) {
	output := captureStartupLogs(t)
	listener, err := openHTTPListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	if !strings.Contains(output.String(), `"msg":"server_listening"`) || !strings.Contains(output.String(), `"port":"`+port+`"`) {
		t.Fatalf("missing actual bound port: %s", output)
	}
	output.Reset()
	duplicate, err := openHTTPListener(listener.Addr().String())
	if err == nil {
		duplicate.Close()
		t.Fatal("binding an occupied port succeeded")
	}
	if !strings.Contains(err.Error(), "HTTP listener failed:") || strings.Contains(output.String(), `"msg":"server_listening"`) {
		t.Fatalf("failed binding reported success: %s; %v", output, err)
	}
}

func TestServeLogsStartupBeforeInvalidConfiguration(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("PORT", "private-token")
	output := captureStartupLogs(t)
	err := serve()
	if err == nil || !strings.HasPrefix(err.Error(), "configuration failed:") {
		t.Fatalf("expected configuration failure, got %v", err)
	}
	if !strings.Contains(output.String(), `"msg":"application_starting"`) || strings.Contains(output.String(), "private-token") {
		t.Fatalf("missing or unsafe early startup log: %s", output)
	}
}
