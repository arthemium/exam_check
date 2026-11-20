package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckUsesConfiguredAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/ready" {
			t.Error("wrong health endpoint")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	t.Setenv("LISTEN_ADDR", strings.TrimPrefix(server.URL, "http://"))
	if err := healthcheck(); err != nil {
		t.Fatal(err)
	}
}

func TestHealthcheckRejectsUnreadyServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	t.Setenv("LISTEN_ADDR", strings.TrimPrefix(server.URL, "http://"))
	if err := healthcheck(); err == nil {
		t.Fatal("unready server accepted")
	}
}

func TestHealthcheckUsesPlatformPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/ready" {
			t.Error("wrong health endpoint")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("PORT", port)
	if err := healthcheck(); err != nil {
		t.Fatal(err)
	}
}

func TestHealthcheckRejectsInvalidPlatformPort(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("PORT", "private-token")
	if err := healthcheck(); err == nil || err.Error() != "PORT must be an integer between 1 and 65535" {
		t.Fatalf("unexpected port validation: %v", err)
	}
}
