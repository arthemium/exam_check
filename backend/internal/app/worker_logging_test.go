package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func workerLogCapture() (context.Context, *bytes.Buffer) {
	output := new(bytes.Buffer)
	logger := slog.New(slog.NewJSONHandler(output, nil))
	return withLogger(context.Background(), logger), output
}

func workerLogEvents(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	events := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func findWorkerLog(t *testing.T, events []map[string]any, message string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event["msg"] == message {
			return event
		}
	}
	t.Fatalf("missing log event %q: %+v", message, events)
	return nil
}

func TestWorkerLogsCommittedCompletionAndContext(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "exam")
	ctx, output := workerLogCapture()
	checker := workerCheckerFunc(func(ctx context.Context, _ string) (json.RawMessage, error) {
		reportCheckProgress(ctx, 10, "transcribing")
		contextLogger(ctx).Info("Mock provider request")
		reportCheckProgress(ctx, 60, "assessing")
		return json.RawMessage(`{"transcript":"PRIVATE STUDENT TEXT"}`), nil
	})
	worker := NewWorker(config, store, checker)
	claim := requireWorkerClaim(t, worker)
	if err := worker.process(ctx, claim); err != nil {
		t.Fatal(err)
	}
	events := workerLogEvents(t, output)
	for _, event := range events {
		if event["job_id"] != claim.JobID || event["exam_id"] != "exam" || event["attempt"] != float64(1) {
			t.Fatalf("job correlation missing: %+v", event)
		}
	}
	findWorkerLog(t, events, "Check job started")
	findWorkerLog(t, events, "Mock provider request")
	completed := findWorkerLog(t, events, "Check job completed")
	if _, ok := completed["duration_ms"]; !ok {
		t.Fatal("completion duration missing")
	}
	if strings.Contains(output.String(), "PRIVATE STUDENT TEXT") || strings.Contains(output.String(), claim.Token) {
		t.Fatal("worker logs disclosed private content or lease token")
	}
	job, exam := workerJobStatus(t, store, "exam")
	if job != "completed" || exam != "completed" {
		t.Fatalf("logged completion without committed state: %s %s", job, exam)
	}
}

func TestWorkerLogsRetryAndFailureWithoutErrorContents(t *testing.T) {
	for _, retryable := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "retry"}[retryable], func(t *testing.T) {
			store, config := workerTestStore(t)
			seedWorkerExam(t, store, "exam")
			ctx, output := workerLogCapture()
			checker := workerCheckerFunc(func(context.Context, string) (json.RawMessage, error) {
				return nil, &CheckingError{"PRIVATE PROVIDER CONTENT", retryable}
			})
			worker := NewWorker(config, store, checker)
			if worked, err := worker.RunOnce(ctx); !worked || err != nil {
				t.Fatalf("process: %t %v", worked, err)
			}
			message, status := "Check job failed", "failed"
			if retryable {
				message, status = "Check job retry scheduled", "queued"
			}
			event := findWorkerLog(t, workerLogEvents(t, output), message)
			if event["retryable"] != retryable || event["attempt"] != float64(1) {
				t.Fatalf("incorrect failure fields: %+v", event)
			}
			if retryable && event["retry_after_ms"] != float64(30000) {
				t.Fatalf("retry delay missing: %+v", event)
			}
			job, exam := workerJobStatus(t, store, "exam")
			if job != status || exam != status || strings.Contains(output.String(), "PRIVATE") {
				t.Fatalf("unsafe or inaccurate failure log: %s", output.String())
			}
		})
	}
}

func TestWorkerDoesNotLogUncommittedOrStaleCompletion(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "stale_lease"}[stale], func(t *testing.T) {
			store, config := workerTestStore(t)
			seedWorkerExam(t, store, "exam")
			ctx, output := workerLogCapture()
			checker := workerCheckerFunc(func(ctx context.Context, _ string) (json.RawMessage, error) {
				query := `CREATE TRIGGER reject_completion BEFORE UPDATE ON exams WHEN NEW.status='completed'
					BEGIN SELECT RAISE(ABORT,'PRIVATE DATABASE FAILURE'); END;`
				if stale {
					query = "UPDATE check_jobs SET lease_token='replacement'"
				}
				if _, err := store.DB.Exec(query); err != nil {
					t.Fatal(err)
				}
				if stale {
					reportCheckProgress(ctx, 60, "assessing")
				}
				return json.RawMessage(`{}`), nil
			})
			worker := NewWorker(config, store, checker)
			_, err := worker.RunOnce(ctx)
			if (err == nil) != stale {
				t.Fatalf("unexpected persistence result: %v", err)
			}
			message := "Check job outcome could not be saved"
			if stale {
				message = "Check job outcome ignored after lease loss"
			}
			findWorkerLog(t, workerLogEvents(t, output), message)
			for _, forbidden := range []string{"Check job completed", "Check job progress", "PRIVATE DATABASE FAILURE"} {
				if strings.Contains(output.String(), forbidden) {
					t.Fatalf("inaccurate or unsafe log: %s", output.String())
				}
			}
			job, _ := workerJobStatus(t, store, "exam")
			if job != "processing" {
				t.Fatalf("uncommitted outcome changed job: %s", job)
			}
		})
	}
}

func TestWorkerRecoversPanicAndProcessesNextJob(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "first")
	seedWorkerExam(t, store, "second")
	ctx, output := workerLogCapture()
	checker := workerCheckerFunc(func(_ context.Context, id string) (json.RawMessage, error) {
		if id == "first" {
			panic("PRIVATE PANIC CONTENT")
		}
		return json.RawMessage(`{}`), nil
	})
	worker := NewWorker(config, store, checker)
	for range 2 {
		if worked, err := worker.RunOnce(ctx); !worked || err != nil {
			t.Fatalf("worker stopped after panic: %t %v", worked, err)
		}
	}
	failed, _ := workerJobStatus(t, store, "first")
	completed, _ := workerJobStatus(t, store, "second")
	if failed != "failed" || completed != "completed" {
		t.Fatalf("panic left jobs stuck: %s %s", failed, completed)
	}
	findWorkerLog(t, workerLogEvents(t, output), "Check job panicked")
	if strings.Contains(output.String(), "PRIVATE PANIC CONTENT") {
		t.Fatal("raw panic appeared in logs")
	}
	var saved string
	if err := store.DB.QueryRow("SELECT last_error FROM check_jobs WHERE exam_id='first'").Scan(&saved); err != nil || strings.Contains(saved, "PRIVATE") {
		t.Fatalf("unsafe stored panic: %q %v", saved, err)
	}
}

func TestWorkerLogsInterruptionAndLifecycle(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "exam")
	ctx, output := workerLogCapture()
	ctx, cancel := context.WithCancel(ctx)
	checker := workerCheckerFunc(func(context.Context, string) (json.RawMessage, error) {
		cancel()
		return nil, errors.New("PRIVATE CANCELLATION")
	})
	worker := NewWorker(config, store, checker)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	worker.Run(ctx)
	events := workerLogEvents(t, output)
	for _, message := range []string{"Check job interrupted", "Worker started", "Worker stopped"} {
		findWorkerLog(t, events, message)
	}
	if strings.Contains(output.String(), "Check job failed") || strings.Contains(output.String(), "PRIVATE") {
		t.Fatalf("unsafe interruption handling: %s", output.String())
	}
}

func TestWorkerFailsInvalidJSONAndProcessesNextJob(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "first")
	seedWorkerExam(t, store, "second")
	ctx, output := workerLogCapture()
	checker := workerCheckerFunc(func(_ context.Context, id string) (json.RawMessage, error) {
		if id == "first" {
			return json.RawMessage("PRIVATE INVALID RESULT"), nil
		}
		return json.RawMessage(`{}`), nil
	})
	worker := NewWorker(config, store, checker)
	for range 2 {
		if worked, err := worker.RunOnce(ctx); !worked || err != nil {
			t.Fatalf("worker stopped after invalid result: %t %v", worked, err)
		}
	}
	failed, _ := workerJobStatus(t, store, "first")
	completed, _ := workerJobStatus(t, store, "second")
	if failed != "failed" || completed != "completed" {
		t.Fatalf("invalid result left jobs stuck: %s %s", failed, completed)
	}
	findWorkerLog(t, workerLogEvents(t, output), "Check job returned an invalid result")
	if strings.Contains(output.String(), "PRIVATE INVALID RESULT") {
		t.Fatal("invalid result content appeared in logs")
	}
}
