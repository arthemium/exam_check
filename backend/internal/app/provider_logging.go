package app

import (
	"context"
	"errors"
	"time"
)

type providerCallLog struct {
	started   time.Time
	status    int
	requestID string
	timedOut  bool
}

func providerContext(ctx context.Context, prompt modelPrompt, phase string) context.Context {
	return withLogger(ctx, contextLogger(ctx).With("phase", phase, "model", prompt.Model))
}

func (call providerCallLog) finish(ctx context.Context, failure error) {
	attributes := []any{"duration_ms", time.Since(call.started).Milliseconds(), "http_status", call.status}
	if id := safeProviderRequestID(call.requestID); id != "" {
		attributes = append(attributes, "provider_request_id", id)
	}
	if failure == nil {
		contextLogger(ctx).Info("openai_request_completed", attributes...)
		return
	}
	_, retryable := safeCheckingError(failure)
	attributes = append(attributes, "retryable", retryable, "error_kind", call.failureKind(ctx))
	contextLogger(ctx).Warn("openai_request_failed", attributes...)
}

func (call providerCallLog) failureKind(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	if call.timedOut || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if call.status == 0 {
		return "transport_error"
	}
	if call.status < 200 || call.status >= 300 {
		return "http_error"
	}
	return "invalid_response"
}

func safeProviderRequestID(value string) string {
	if len(value) > 128 {
		return ""
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
			return ""
		}
	}
	return value
}
