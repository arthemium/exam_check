package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type providerLogTransport func(*http.Request) (*http.Response, error)

func (transport providerLogTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type providerLogCase struct {
	name, requestID, kind string
	status                int
	retryable             bool
}

func mockLoggedProvider(t *testing.T, test providerLogCase) providerLogTransport {
	t.Helper()
	return func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil || !strings.Contains(string(body), "private-student") || request.Header.Get("Authorization") != "Bearer private-api-key" {
			t.Errorf("mock provider did not receive expected input: %v", err)
		}
		if test.status == 0 {
			if test.kind == "timeout" {
				return nil, context.DeadlineExceeded
			}
			return nil, errors.New("transport failed for https://private-host/?token=private-token")
		}
		response := httptest.NewRecorder()
		response.Header().Set("X-Request-ID", test.requestID)
		response.WriteHeader(test.status)
		if test.kind != "" {
			_, _ = response.WriteString("private-provider-response")
		} else {
			writeProviderResponse(response, analysis{Findings: []finding{}})
		}
		return response.Result(), nil
	}
}

func runLoggedProvider(t *testing.T, test providerLogCase) (*bytes.Buffer, error) {
	t.Helper()
	config := DefaultConfig()
	config.OpenAIKey = "private-api-key"
	checker := NewChecker(config, nil).(*openAIChecker)
	checker.endpoint = "https://provider.invalid/private-path?token=private-token"
	checker.client.Transport = mockLoggedProvider(t, test)
	output := new(bytes.Buffer)
	logger := slog.New(slog.NewJSONHandler(output, nil)).With("request_id", "request-test", "exam_id", "exam-test")
	ctx := withLogger(t.Context(), logger)
	prompt := modelPrompt{Instructions: "private-prompt", Model: "saved-checker-model"}
	content := map[string]string{"text": "private-student", "image": "private-image-data"}
	var feedback analysis
	err := checker.requestAnalysis(ctx, prompt, analysisSchema(), content, &feedback)
	return output, err
}

func assertProviderLogContext(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	entries := decodeHTTPLogs(t, output)
	if len(entries) != 2 || entries[0]["msg"] != "openai_request_started" {
		t.Fatalf("missing provider lifecycle logs: %s", output.String())
	}
	for _, entry := range entries {
		if entry["phase"] != "assessment" || entry["model"] != "saved-checker-model" || entry["request_id"] != "request-test" || entry["exam_id"] != "exam-test" {
			t.Fatalf("missing provider context: %s", output.String())
		}
	}
	if strings.Contains(output.String(), "private-") {
		t.Fatalf("private provider/request data reached logs: %s", output.String())
	}
	return entries
}

func TestProviderLoggingRecordsResultsWithoutPrivateData(t *testing.T) {
	cases := []providerLogCase{
		{name: "success", status: 200, requestID: "req_test-1"},
		{name: "unavailable", status: 503, requestID: "req_test-2", kind: "http_error", retryable: true},
		{name: "rate_limit", status: 429, requestID: "req_test-3", kind: "http_error", retryable: true},
		{name: "unauthorized", status: 401, requestID: "req_test-4", kind: "http_error"},
		{name: "invalid_json", status: 200, requestID: "req_test-5", kind: "invalid_response", retryable: true},
		{name: "transport", kind: "transport_error", retryable: true},
		{name: "timeout", kind: "timeout", retryable: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			output, err := runLoggedProvider(t, test)
			entries := assertProviderLogContext(t, output)
			entry := entries[1]
			if (err != nil) != (test.kind != "") || entry["http_status"] != float64(test.status) || entry["duration_ms"] == nil {
				t.Fatalf("incorrect provider result: %s, %v", output.String(), err)
			}
			if test.requestID != "" && entry["provider_request_id"] != test.requestID {
				t.Fatalf("missing provider request ID: %s", output.String())
			}
			if test.kind == "" {
				if entry["msg"] != "openai_request_completed" || entry["level"] != "INFO" {
					t.Fatalf("success logged as failure: %s", output.String())
				}
			} else if entry["msg"] != "openai_request_failed" || entry["error_kind"] != test.kind || entry["retryable"] != test.retryable || entry["level"] != "WARN" {
				t.Fatalf("incorrect provider failure metadata: %s", output.String())
			}
		})
	}
}

func TestProviderLoggingOmitsUnsafeRequestIDs(t *testing.T) {
	for _, requestID := range []string{"private-header with spaces", "private-header\nforged", strings.Repeat("private-header", 20)} {
		output, err := runLoggedProvider(t, providerLogCase{status: 200, requestID: requestID})
		if err != nil {
			t.Fatal(err)
		}
		entries := assertProviderLogContext(t, output)
		if _, exists := entries[1]["provider_request_id"]; exists {
			t.Fatalf("unsafe request ID was logged: %s", output.String())
		}
	}
}
