package app

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type loggerContextKey struct{}

func withLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey{}, logger)
}

func contextLogger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerContextKey{}).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}

type loggedResponse struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (response *loggedResponse) Unwrap() http.ResponseWriter {
	return response.ResponseWriter
}

func (response *loggedResponse) WriteHeader(status int) {
	if response.status != 0 {
		return
	}
	response.ResponseWriter.WriteHeader(status)
	if status >= 200 || status == http.StatusSwitchingProtocols {
		response.status = status
	}
}

func (response *loggedResponse) Write(body []byte) (int, error) {
	if response.status == 0 {
		response.WriteHeader(http.StatusOK)
	}
	count, err := response.ResponseWriter.Write(body)
	response.bytes += int64(count)
	return count, err
}

func (response *loggedResponse) FlushError() error {
	if response.status == 0 {
		response.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(response.ResponseWriter).Flush()
}

func requestLogging(next http.Handler, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		_, route := mux.Handler(request)
		if route == "" {
			route = "unmatched"
		}
		requestID := NewID()
		logger := contextLogger(request.Context()).With("request_id", requestID, "method", request.Method, "route", route)
		request = request.WithContext(withLogger(request.Context(), logger))
		writer.Header().Set("X-Request-ID", requestID)
		response := &loggedResponse{ResponseWriter: writer}
		defer logHTTPResponse(request.Context(), response, route, started)
		next.ServeHTTP(response, request)
	})
}

func logHTTPResponse(ctx context.Context, response *loggedResponse, route string, started time.Time) {
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	if status < 400 && (strings.HasPrefix(route, "GET /health/") || route == "GET /assets/") {
		return
	}
	level := slog.LevelInfo
	if status >= 500 {
		level = slog.LevelError
	} else if status >= 400 {
		level = slog.LevelWarn
	}
	contextLogger(ctx).Log(ctx, level, "HTTP request completed", "status", status,
		"duration_ms", time.Since(started).Milliseconds(), "response_bytes", response.bytes)
}
