package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
			if len(entries) != 2 || entries[0]["msg"] != "HTTP request received" {
				t.Fatalf("expected arrival and completion entries: %s", output.String())
			}
			entry := entries[1]
			if response.Code != item.status || entry["status"] != float64(item.status) || entry["route"] != item.route || entry["level"] != "WARN" {
				t.Fatalf("incorrect rejection log: %s", output.String())
			}
			if entry["request_id"] != response.Header().Get("X-Request-ID") || entries[0]["request_id"] != entry["request_id"] || response.Header().Get("X-Request-ID") == "" {
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

func TestHTTPLogIncludesSuccessfulHealthAndAssets(t *testing.T) {
	cases := []struct {
		path, route string
		status      int
	}{
		{"/health/ready", "GET /health/ready", 200},
		{"/health/ready", "GET /health/ready", 503},
		{"/assets/app.js", "GET /assets/", 200},
		{"/assets/missing.js", "GET /assets/", 404},
		{"/", "GET /{$}", 200},
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
		if len(entries) != 2 || entries[0]["msg"] != "HTTP request received" || entries[1]["msg"] != "HTTP request completed" {
			t.Fatalf("unexpected logging for %s (%d): %s", item.path, item.status, output.String())
		}
		if item.status == 503 && entries[1]["level"] != "ERROR" {
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
	if len(entries) != 2 || entries[0]["route"] != "unmatched" || entries[1]["status"] != float64(404) {
		t.Fatalf("wrong unmatched route log: %s", output.String())
	}
	if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "secret") {
		t.Fatalf("unmatched URL leaked: %s", output.String())
	}
}

func TestHTTPLogRecordsArrivalBeforeBlockedHandlerCompletes(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /waiting", func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	})
	var output bytes.Buffer
	request := captureHTTPLog(httptest.NewRequest("GET", "/waiting", nil), &output)
	go func() {
		defer close(done)
		requestLogging(mux, mux).ServeHTTP(httptest.NewRecorder(), request)
	}()
	defer func() { close(release); <-done }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not receive the request")
	}
	entries := decodeHTTPLogs(t, &output)
	if len(entries) != 1 || entries[0]["msg"] != "HTTP request received" || entries[0]["route"] != "GET /waiting" {
		t.Fatalf("arrival was not logged while the handler was blocked: %s", output.String())
	}
}

func TestReadinessLogsSafeFailureReasons(t *testing.T) {
	cases := []struct{ reason, statement string }{
		{"schema_query_failed", ""},
		{"schema_version_mismatch", fmt.Sprintf("PRAGMA user_version=%d", schemaVersion+1)},
		{"tables_query_failed", "DROP TABLE check_jobs; CREATE VIEW check_jobs AS SELECT * FROM private_student_data"},
	}
	for _, item := range cases {
		t.Run(item.reason, func(t *testing.T) {
			server := authFixture(t)
			if item.statement == "" {
				if err := server.Store.DB.Close(); err != nil {
					t.Fatal(err)
				}
			} else if _, err := server.Store.DB.Exec(item.statement); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			request := captureHTTPLog(httptest.NewRequest("GET", "/health/ready?token=private-token", nil), &output)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			entries := decodeHTTPLogs(t, &output)
			if response.Code != 503 || len(entries) != 3 || entries[1]["msg"] != "readiness_failed" || entries[1]["reason"] != item.reason {
				t.Fatalf("missing readiness failure reason: %s", output.String())
			}
			failure := entries[1]
			if failure["level"] != "ERROR" || failure["request_id"] != response.Header().Get("X-Request-ID") {
				t.Fatalf("readiness failure lacks request correlation: %s", output.String())
			}
			if item.reason == "schema_version_mismatch" && (failure["expected_version"] != float64(schemaVersion) || failure["actual_version"] != float64(schemaVersion+1)) {
				t.Fatalf("missing schema versions: %s", output.String())
			}
			if failure["error"] != nil || strings.Contains(output.String(), "private") || strings.Contains(output.String(), "sql:") {
				t.Fatalf("readiness log exposed private details: %s", output.String())
			}
		})
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
	if response.Code != 500 || len(entries) != 3 || entries[1]["msg"] != "HTTP request panicked" || entries[2]["status"] != float64(500) {
		t.Fatalf("panic was not captured: %s", output.String())
	}
	if entries[1]["request_id"] != response.Header().Get("X-Request-ID") || strings.Contains(output.String(), "private-password") {
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
