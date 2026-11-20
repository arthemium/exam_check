package main

import (
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
