package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func captureHTTPLog(request *http.Request, output *bytes.Buffer) *http.Request {
	logger := slog.New(slog.NewJSONHandler(output, nil))
	return request.WithContext(withLogger(request.Context(), logger))
}

func decodeHTTPLogs(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	entries := []map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	for decoder.More() {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestHTTPLogIncludesGuardFailuresWithoutPrivateRequestData(t *testing.T) {
	server := authFixture(t)
	cases := []struct {
		name, method, path, route string
		status                    int
	}{
		{"auth", "GET", "/api/exams/private-student?token=private-token", "GET /api/exams/{exam_id}", 401},
		{"origin", "POST", "/auth/login?token=private-token", "POST /auth/login", 403},
		{"size", "POST", "/auth/login?token=private-token", "POST /auth/login", 413},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(item.method, item.path, strings.NewReader("private-password"))
			request.Header.Set("X-Request-ID", "private-client-id")
			request.Header.Set("Authorization", "Bearer private-api-key")
			request.Header.Set("Cookie", "exam_session=private-cookie")
			if item.name == "size" {
				request.Header.Set("Origin", server.Config.PublicBaseURL)
				request.ContentLength = 65537
			}
			var output bytes.Buffer
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, captureHTTPLog(request, &output))
			entries := decodeHTTPLogs(t, &output)
			if len(entries) != 1 {
				t.Fatalf("expected one access entry: %s", output.String())
			}
			entry := entries[0]
			if response.Code != item.status || entry["status"] != float64(item.status) || entry["route"] != item.route || entry["level"] != "WARN" {
				t.Fatalf("incorrect rejection log: %s", output.String())
			}
			if entry["request_id"] != response.Header().Get("X-Request-ID") || response.Header().Get("X-Request-ID") == "" {
				t.Fatalf("missing request correlation: %s", output.String())
			}
			if entry["response_bytes"] != float64(response.Body.Len()) || entry["duration_ms"] == nil {
				t.Fatalf("missing response metrics: %s", output.String())
			}
			if strings.Contains(output.String(), "private-") {
				t.Fatalf("private request data reached logs: %s", output.String())
			}
		})
	}
}

func TestHTTPLogQuietSuccessfulHealthAndAssets(t *testing.T) {
	cases := []struct {
		path, route string
		status      int
		logged      bool
	}{
		{"/health/ready", "GET /health/ready", 200, false},
		{"/health/ready", "GET /health/ready", 503, true},
		{"/assets/app.js", "GET /assets/", 200, false},
		{"/assets/missing.js", "GET /assets/", 404, true},
		{"/", "GET /{$}", 200, true},
	}
	for _, item := range cases {
		mux := http.NewServeMux()
		mux.HandleFunc(item.route, func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(item.status)
			_, _ = writer.Write([]byte("response"))
		})
		var output bytes.Buffer
		request := captureHTTPLog(httptest.NewRequest("GET", item.path, nil), &output)
		requestLogging(mux, mux).ServeHTTP(httptest.NewRecorder(), request)
		entries := decodeHTTPLogs(t, &output)
		if (len(entries) == 1) != item.logged {
			t.Fatalf("unexpected logging for %s (%d): %s", item.path, item.status, output.String())
		}
		if item.status == 503 && entries[0]["level"] != "ERROR" {
			t.Fatalf("server failure is not an error log: %s", output.String())
		}
	}
}

func TestHTTPLogDoesNotExposeUnmatchedPaths(t *testing.T) {
	mux := http.NewServeMux()
	var output bytes.Buffer
	request := httptest.NewRequest("GET", "/private-student/private-token?password=secret", nil)
	response := httptest.NewRecorder()
	requestLogging(mux, mux).ServeHTTP(response, captureHTTPLog(request, &output))
	entries := decodeHTTPLogs(t, &output)
	if len(entries) != 1 || entries[0]["route"] != "unmatched" || entries[0]["status"] != float64(404) {
		t.Fatalf("wrong unmatched route log: %s", output.String())
	}
	if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "secret") {
		t.Fatalf("unmatched URL leaked: %s", output.String())
	}
}

func TestHTTPPanicLogsRequestIDWithoutPanicValue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) { panic("private-password") })
	server := NewServer(DefaultConfig(), nil)
	var output bytes.Buffer
	request := captureHTTPLog(httptest.NewRequest("GET", "/panic", nil), &output)
	response := httptest.NewRecorder()
	requestLogging(server.guard(mux), mux).ServeHTTP(response, request)
	entries := decodeHTTPLogs(t, &output)
	if response.Code != 500 || len(entries) != 2 || entries[0]["msg"] != "HTTP request panicked" || entries[1]["status"] != float64(500) {
		t.Fatalf("panic was not captured: %s", output.String())
	}
	if entries[0]["request_id"] != response.Header().Get("X-Request-ID") || strings.Contains(output.String(), "private-password") {
		t.Fatalf("unsafe or uncorrelated panic: %s", output.String())
	}
}

func TestHTTPResponseLoggingPreservesFlushAndFirstStatus(t *testing.T) {
	underlying := httptest.NewRecorder()
	response := &loggedResponse{ResponseWriter: underlying}
	if err := http.NewResponseController(response).Flush(); err != nil {
		t.Fatal(err)
	}
	response.WriteHeader(http.StatusInternalServerError)
	_, _ = response.Write([]byte("hello"))
	if !underlying.Flushed || underlying.Code != 200 || response.status != 200 || response.bytes != 5 {
		t.Fatalf("response semantics changed: %+v", response)
	}
	if response.Unwrap() != underlying {
		t.Fatal("response controller cannot reach the underlying writer")
	}
}
