package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"examcheck/internal/app"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	var err error
	switch command {
	case "serve":
		err = serve()
	case "healthcheck":
		err = healthcheck()
	default:
		err = fmt.Errorf("use serve or healthcheck")
	}
	if err != nil {
		slog.Error("application_failed", "error", err)
		os.Exit(1)
	}
}

func serve() error {
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	store, err := app.OpenStore(cfg.DBPath, cfg.OpenAIModel)
	if err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}
	defer store.Close()
	slog.Info("database_ready", "database", "sqlite")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := startWorker(ctx, cfg, store)
	server := &http.Server{Addr: cfg.ListenAddr, Handler: app.NewServer(cfg, store).Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 120 * time.Second, WriteTimeout: 120 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- server.ListenAndServe() }()
	slog.Info("server_starting", "listen_addr", cfg.ListenAddr, "worker_enabled", cfg.EnableWorker,
		"openai_key_configured", cfg.OpenAIKey != "")
	select {
	case <-ctx.Done():
	case err = <-errorsCh:
		stop()
	}
	slog.Info("server_stopping")
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if shutdownErr := server.Shutdown(shutdown); shutdownErr != nil {
		server.Close()
	}
	<-workerDone
	slog.Info("server_stopped")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func startWorker(ctx context.Context, cfg app.Config, store *app.Store) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if cfg.EnableWorker {
			app.NewWorker(cfg, store, app.NewChecker(cfg, store)).Run(ctx)
		} else {
			slog.Info("worker_disabled")
		}
	}()
	return done
}

func healthcheck() error {
	address, err := app.ListenAddress()
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid LISTEN_ADDR")
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://" + net.JoinHostPort(host, port) + "/health/ready")
	if err != nil {
		return fmt.Errorf("healthcheck failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("service is not ready")
	}
	return nil
}
