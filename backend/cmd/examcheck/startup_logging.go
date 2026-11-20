package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"examcheck/internal/app"
)

func logStartupConfig(cfg app.Config) {
	source := "default"
	if os.Getenv("LISTEN_ADDR") != "" {
		source = "LISTEN_ADDR"
	} else if os.Getenv("PORT") != "" {
		source = "PORT"
	}
	slog.Info("configuration_ready", "app_env", cfg.AppEnv, "listen_addr", cfg.ListenAddr,
		"listen_source", source, "cookie_secure", cfg.CookieSecure,
		"public_url_https", strings.HasPrefix(cfg.PublicBaseURL, "https://"),
		"worker_enabled", cfg.EnableWorker, "openai_key_configured", cfg.OpenAIKey != "",
		"login_configured", cfg.LoginUsername != "" && cfg.LoginPassword != "")
	logBindingWarnings(cfg.ListenAddr)
}

func logBindingWarnings(address string) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return // The listener reports invalid addresses during startup.
	}
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		slog.Warn("loopback_listener", "hint", "Use a wildcard listener so the platform proxy can connect")
	}
	value := os.Getenv("PORT")
	if value == "" {
		return
	}
	platformPort, err := strconv.Atoi(value)
	if err != nil || platformPort < 1 || platformPort > 65535 {
		slog.Warn("invalid_platform_port", "hint", "PORT must be an integer between 1 and 65535")
		return
	}
	slog.Info("platform_port", "port", platformPort)
	listenPort, _ := strconv.Atoi(port)
	if listenPort != platformPort {
		slog.Warn("platform_port_mismatch", "platform_port", platformPort,
			"hint", "Unset LISTEN_ADDR and match the public domain target port to the listener")
	}
}

func openHTTPListener(address string) (net.Listener, error) {
	slog.Info("server_starting", "listen_addr", address)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("HTTP listener failed: %w", err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	slog.Info("server_listening", "listen_addr", listener.Addr().String(), "port", port)
	return listener, nil
}
