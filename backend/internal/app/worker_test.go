package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func workerTestStore(t *testing.T) (*Store, Config) {
	t.Helper()
	config := DefaultConfig()
	config.DBPath = filepath.Join(t.TempDir(), "examcheck.db")
	config.UploadDir = t.TempDir()
	config.OpenAIKey, config.OpenAIModel = "offline-test-key", "test-model"
	store, err := OpenStore(config.DBPath, config.OpenAIModel)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, config
}

func seedWorkerExam(t *testing.T, store *Store, id string) {
	t.Helper()
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT OR IGNORE INTO users(id,google_sub,email,display_name,created_at) VALUES('local-owner','local-owner','owner@examcheck.invalid','Teacher',0)`, nil},
		{`INSERT OR IGNORE INTO folders(id,owner_id,name,name_key,created_at) VALUES('test-folder','local-owner','Class','class',0)`, nil},
		{`INSERT INTO exams(id,owner_id,folder_id,title,checks,status,created_at) VALUES(?,'local-owner','test-folder','Essay','["grammar"]','queued',0)`, []any{id}},
		{`INSERT INTO check_jobs(id,exam_id,status,available_at,created_at) VALUES(?,?,'queued',0,0)`, []any{"job-" + id, id}},
	}
	for _, statement := range queries {
		if _, err := store.DB.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func requireWorkerClaim(t *testing.T, worker *Worker) jobClaim {
	t.Helper()
	claim, err := worker.claim(context.Background())
	if err != nil || claim == nil {
		t.Fatalf("claim: value=%v err=%v", claim, err)
	}
	return *claim
}

func workerJobStatus(t *testing.T, store *Store, examID string) (string, string) {
	t.Helper()
	var jobStatus, examStatus string
	if err := store.DB.QueryRow(`SELECT check_jobs.status, exams.status FROM check_jobs JOIN exams ON exams.id=check_jobs.exam_id WHERE exams.id=?`, examID).Scan(&jobStatus, &examStatus); err != nil {
		t.Fatal(err)
	}
	return jobStatus, examStatus
}

func TestWorkerClaimCompletionAndFencing(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "exam")
	worker := NewWorker(config, store, nil)
	claim := requireWorkerClaim(t, worker)
	if next, err := worker.claim(context.Background()); next != nil || err != nil {
		t.Fatalf("active job claimed twice: %v %v", next, err)
	}
	stale := claim
	stale.Token = "wrong-token"
	if changed, err := worker.complete(context.Background(), stale, json.RawMessage(`{"bad":true}`)); changed || err != nil {
		t.Fatalf("stale completion: %v %v", changed, err)
	}
	if changed, err := worker.complete(context.Background(), claim, json.RawMessage(`{"transcript":"student text"}`)); !changed || err != nil {
		t.Fatalf("completion: %v %v", changed, err)
	}
	jobStatus, examStatus := workerJobStatus(t, store, "exam")
	if jobStatus != "completed" || examStatus != "completed" {
		t.Fatalf("statuses: %s %s", jobStatus, examStatus)
	}
	var stored string
	var cleared bool
	if err := store.DB.QueryRow(`SELECT result FROM exams WHERE id='exam'`).Scan(&stored); err != nil || !strings.Contains(stored, "student text") {
		t.Fatalf("result: %s %v", stored, err)
	}
	if err := store.DB.QueryRow(`SELECT lease_token IS NULL AND locked_at IS NULL FROM check_jobs`).Scan(&cleared); err != nil || !cleared {
		t.Fatalf("lease was not cleared: %v", err)
	}
}

func TestWorkerClaimsAreAtomicAcrossSQLiteConnections(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "one")
	seedWorkerExam(t, store, "two")
	second, err := OpenStore(config.DBPath, config.OpenAIModel)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	workers := []*Worker{NewWorker(config, store, nil), NewWorker(config, second, nil)}
	start := make(chan struct{})
	claims := make(chan *jobClaim, 2)
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for _, worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			claim, err := worker.claim(context.Background())
			claims <- claim
			errors <- err
		}()
	}
	close(start)
	group.Wait()
	for range workers {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	firstClaim, secondClaim := <-claims, <-claims
	if firstClaim == nil || secondClaim == nil || firstClaim.JobID == secondClaim.JobID {
		t.Fatalf("nonexclusive claims: %v %v", firstClaim, secondClaim)
	}
}

func TestWorkerExpiredLeaseRejectsEveryStaleWrite(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "exam")
	worker := NewWorker(config, store, nil)
	now := time.Now()
	worker.now = func() time.Time { return now }
	first := requireWorkerClaim(t, worker)
	now = now.Add(config.JobLease + time.Second)
	second := requireWorkerClaim(t, worker)
	if first.Token == second.Token {
		t.Fatal("lease token was reused")
	}
	if changed, err := worker.renew(context.Background(), first); changed || err != nil {
		t.Fatalf("stale renewal: %v %v", changed, err)
	}
	if changed, err := worker.fail(context.Background(), first, errors.New("old worker")); changed || err != nil {
		t.Fatalf("stale failure: %v %v", changed, err)
	}
	if changed, err := worker.complete(context.Background(), first, json.RawMessage(`{}`)); changed || err != nil {
		t.Fatalf("stale completion: %v %v", changed, err)
	}
	if changed, err := worker.renew(context.Background(), second); !changed || err != nil {
		t.Fatalf("current renewal: %v %v", changed, err)
	}
}

func TestWorkerRetryDeadlineAndAttemptLimit(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "exam")
	worker := NewWorker(config, store, nil)
	now := time.Now()
	worker.now = func() time.Time { return now }
	for attempt := 1; attempt <= config.JobMaxAttempts; attempt++ {
		claim := requireWorkerClaim(t, worker)
		if changed, err := worker.fail(context.Background(), claim, &CheckingError{"Try again shortly.", true}); !changed || err != nil {
			t.Fatalf("retry: %v %v", changed, err)
		}
		if claim, err := worker.claim(context.Background()); claim != nil || err != nil {
			t.Fatalf("claimed before retry deadline: %v %v", claim, err)
		}
		now = now.Add(10 * time.Minute)
	}
	jobStatus, examStatus := workerJobStatus(t, store, "exam")
	if jobStatus != "failed" || examStatus != "failed" {
		t.Fatalf("statuses: %s %s", jobStatus, examStatus)
	}
}

func TestWorkerAbandonedFinalAttemptAndSecretRedaction(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "redacted", true: "interrupted"}[interrupted], func(t *testing.T) {
			store, config := workerTestStore(t)
			seedWorkerExam(t, store, "exam")
			worker := NewWorker(config, store, nil)
			claim := requireWorkerClaim(t, worker)
			if interrupted {
				if _, err := store.DB.Exec(`UPDATE check_jobs SET attempts=?,locked_at=0`, config.JobMaxAttempts); err != nil {
					t.Fatal(err)
				}
				if claim, err := worker.claim(context.Background()); claim != nil || err != nil {
					t.Fatalf("exhausted claim: %v %v", claim, err)
				}
			} else if _, err := worker.fail(context.Background(), claim, errors.New("secret provider token")); err != nil {
				t.Fatal(err)
			}
			var message string
			if err := store.DB.QueryRow(`SELECT last_error FROM check_jobs`).Scan(&message); err != nil || strings.Contains(message, "secret") {
				t.Fatalf("unsafe error: %q %v", message, err)
			}
			job, exam := workerJobStatus(t, store, "exam")
			if job != "failed" || exam != "failed" {
				t.Fatalf("statuses: %s %s", job, exam)
			}
		})
	}
}

type workerCheckerFunc func(context.Context, string) (json.RawMessage, error)

func (function workerCheckerFunc) Check(ctx context.Context, id string) (json.RawMessage, error) {
	return function(ctx, id)
}

func TestWorkerShutdownLeavesJobRecoverable(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "exam")
	checker := workerCheckerFunc(func(ctx context.Context, _ string) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	worker := NewWorker(config, store, checker)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if worked, err := worker.RunOnce(ctx); !worked || err != nil {
		t.Fatalf("interrupted work: %v %v", worked, err)
	}
	job, exam := workerJobStatus(t, store, "exam")
	if job != "processing" || exam != "processing" {
		t.Fatalf("shutdown finalized job: %s %s", job, exam)
	}
	worker.now = func() time.Time { return time.Now().Add(config.JobLease + time.Second) }
	if claim := requireWorkerClaim(t, worker); claim.ExamID != "exam" {
		t.Fatalf("wrong recovered exam: %v", claim)
	}
}
