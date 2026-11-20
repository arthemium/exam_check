package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

type jobClaim struct {
	JobID   string
	ExamID  string
	Token   string
	Attempt int
}

type Worker struct {
	config  Config
	store   *Store
	checker Checker
	now     func() time.Time
}

func NewWorker(config Config, store *Store, checker Checker) *Worker {
	return &Worker{config: config, store: store, checker: checker, now: time.Now}
}

func (worker *Worker) claim(ctx context.Context) (*jobClaim, error) {
	transaction, err := worker.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback()
	now := worker.now().UnixMilli()
	var claim jobClaim
	err = transaction.QueryRowContext(ctx, `SELECT id, exam_id, attempts FROM check_jobs
		WHERE (status = 'queued' AND available_at <= ?) OR (status = 'processing' AND locked_at < ?)
		ORDER BY available_at, created_at, id LIMIT 1`, now, now-worker.config.JobLease.Milliseconds()).Scan(&claim.JobID, &claim.ExamID, &claim.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if claim.Attempt >= worker.config.JobMaxAttempts {
		return nil, worker.exhaustClaim(ctx, transaction, claim)
	}
	claim.Attempt++
	claim.Token = NewID()
	if _, err = transaction.ExecContext(ctx, `UPDATE check_jobs SET status = 'processing', attempts = attempts + 1,
		locked_at = ?, lease_token = ? WHERE id = ?`, now, claim.Token, claim.JobID); err != nil {
		return nil, err
	}
	if _, err = transaction.ExecContext(ctx, "UPDATE exams SET status = 'processing', stage = 'transcribing', progress = 10, error = NULL WHERE id = ?", claim.ExamID); err != nil {
		return nil, err
	}
	if err = transaction.Commit(); err != nil {
		return nil, err
	}
	return &claim, nil
}

func (worker *Worker) exhaustClaim(ctx context.Context, transaction *sql.Tx, claim jobClaim) error {
	message := "Processing stopped after repeated interrupted attempts."
	_, err := transaction.ExecContext(ctx, `UPDATE check_jobs SET status = 'failed', last_error = ?,
		locked_at = NULL, lease_token = NULL WHERE id = ?`, message, claim.JobID)
	if err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, "UPDATE exams SET status = 'failed', stage = 'failed', error = ? WHERE id = ?", message, claim.ExamID); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	contextLogger(ctx).Error("Check job attempts exhausted", "job_id", claim.JobID,
		"exam_id", claim.ExamID, "attempt", claim.Attempt)
	return nil
}

func (worker *Worker) renew(ctx context.Context, claim jobClaim) (bool, error) {
	result, err := worker.store.DB.ExecContext(ctx, `UPDATE check_jobs SET locked_at = ?
		WHERE id = ? AND exam_id = ? AND status = 'processing' AND lease_token = ?`,
		worker.now().UnixMilli(), claim.JobID, claim.ExamID, claim.Token)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (worker *Worker) complete(ctx context.Context, claim jobClaim, result json.RawMessage) (bool, error) {
	if !json.Valid(result) {
		return false, errors.New("checker returned invalid JSON")
	}
	transaction, err := worker.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	updated, err := transaction.ExecContext(ctx, `UPDATE check_jobs SET status = 'completed', last_error = NULL,
		locked_at = NULL, lease_token = NULL WHERE id = ? AND exam_id = ? AND status = 'processing' AND lease_token = ?`,
		claim.JobID, claim.ExamID, claim.Token)
	if err != nil {
		return false, err
	}
	count, err := updated.RowsAffected()
	if err != nil || count == 0 {
		return false, err
	}
	if err := persistExtractedTask(ctx, transaction, claim.ExamID, result); err != nil {
		return false, err
	}
	if _, err = transaction.ExecContext(ctx, "UPDATE exams SET status = 'completed', stage = 'completed', progress = 100, error = NULL, result = ? WHERE id = ?", string(result), claim.ExamID); err != nil {
		return false, err
	}
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func persistExtractedTask(ctx context.Context, tx *sql.Tx, examID string, result json.RawMessage) error {
	var envelope struct {
		Task checkedTask `json:"task"`
	}
	if json.Unmarshal(result, &envelope) != nil {
		return nil
	}
	task := envelope.Task
	task.Text = strings.TrimSpace(task.Text)
	if task.Text == "" || !utf8.ValidString(task.Text) || utf8.RuneCountInString(task.Text) > 4000 || (task.Source != "essay" && task.Source != "photo") {
		return nil
	}
	exam, err := loadExam(ctx, tx, examID)
	if err != nil {
		return err
	}
	if exam.Criteria.TaskText != "" {
		return nil
	}
	exam.Criteria.TaskText, exam.Criteria.TaskSource = task.Text, task.Source
	return saveExamCriteria(ctx, tx, examID, exam.Criteria, false)
}

func safeCheckingError(err error) (string, bool) {
	var checking *CheckingError
	if errors.As(err, &checking) {
		return checking.Message, checking.Retryable
	}
	return "Exam processing failed. Check the server configuration and try again.", false
}

func retryDelay(attempts int) time.Duration {
	delay := 30 * time.Second
	for index := 1; index < attempts && delay < 300*time.Second; index++ {
		delay *= 2
	}
	return min(delay, 300*time.Second)
}

func (worker *Worker) fail(ctx context.Context, claim jobClaim, failure error) (bool, error) {
	transaction, err := worker.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	var attempts int
	err = transaction.QueryRowContext(ctx, `SELECT attempts FROM check_jobs
		WHERE id = ? AND exam_id = ? AND status = 'processing' AND lease_token = ?`, claim.JobID, claim.ExamID, claim.Token).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	message, retryable := safeCheckingError(failure)
	status := "failed"
	if retryable && attempts < worker.config.JobMaxAttempts {
		status = "queued"
	}
	next := worker.now().Add(retryDelay(attempts)).UnixMilli()
	if _, err = transaction.ExecContext(ctx, `UPDATE check_jobs SET status = ?, last_error = ?, available_at = ?,
		locked_at = NULL, lease_token = NULL WHERE id = ?`, status, message, next, claim.JobID); err != nil {
		return false, err
	}
	if _, err = transaction.ExecContext(ctx, "UPDATE exams SET status = ?, stage = ?, error = ? WHERE id = ?", status, map[bool]string{true: "retrying", false: "failed"}[status == "queued"], message, claim.ExamID); err != nil {
		return false, err
	}
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	claim.Attempt = attempts
	logJobFailure(ctx, claim, failure, status == "queued")
	return true, nil
}

func logJobFailure(ctx context.Context, claim jobClaim, failure error, retryScheduled bool) {
	_, retryable := safeCheckingError(failure)
	kind := "unexpected_error"
	var checking *CheckingError
	if errors.As(failure, &checking) {
		kind = "checking_error"
	}
	logger := contextLogger(ctx).With("job_id", claim.JobID, "exam_id", claim.ExamID,
		"attempt", claim.Attempt, "retryable", retryable, "error_kind", kind)
	if retryScheduled {
		logger.Warn("Check job retry scheduled", "retry_after_ms", retryDelay(claim.Attempt).Milliseconds())
	} else {
		logger.Error("Check job failed")
	}
}

func (worker *Worker) heartbeat(ctx context.Context, claim jobClaim, cancel context.CancelFunc, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(max(worker.config.JobLease/3, time.Second))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			owned, err := worker.renew(ctx, claim)
			if err != nil && ctx.Err() == nil {
				contextLogger(ctx).Warn("Worker lease renewal failed")
			} else if err == nil && !owned {
				contextLogger(ctx).Warn("Worker lost job lease")
				cancel()
				return
			}
		}
	}
}

func (worker *Worker) progress(ctx context.Context, claim jobClaim, progress int, stage string) error {
	if progress < 0 || progress >= 100 {
		return errors.New("invalid checking progress")
	}
	result, err := worker.store.DB.ExecContext(ctx, `UPDATE exams SET progress=?,stage=? WHERE id=? AND EXISTS (
		SELECT 1 FROM check_jobs WHERE id=? AND exam_id=? AND status='processing' AND lease_token=?)`,
		progress, stage, claim.ExamID, claim.JobID, claim.ExamID, claim.Token)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count > 0 {
		if stage != "transcribing" && stage != "assessing" && stage != "finalizing" {
			stage = "unknown"
		}
		contextLogger(ctx).Info("Check job progress", "stage", stage, "progress", progress)
	}
	return err
}

func (worker *Worker) process(ctx context.Context, claim jobClaim) error {
	started := time.Now()
	logger := contextLogger(ctx).With("job_id", claim.JobID, "exam_id", claim.ExamID, "attempt", claim.Attempt)
	logger.Info("Check job started")
	processing, cancel := context.WithCancel(withLogger(ctx, logger))
	done := make(chan struct{})
	go worker.heartbeat(processing, claim, cancel, done)
	checking := withCheckProgress(processing, func(progress int, stage string) {
		if err := worker.progress(processing, claim, progress, stage); err != nil && processing.Err() == nil {
			logger.Warn("Worker progress update failed")
		}
	})
	result, failure := worker.checkSafely(checking, claim.ExamID)
	interrupted := processing.Err() != nil
	cancel()
	<-done
	// Shutdown or lost ownership leaves the lease for recovery, never marks a job failed.
	if interrupted {
		logger.Warn("Check job interrupted", "duration_ms", time.Since(started).Milliseconds())
		return nil
	}
	outcomeContext := withLogger(ctx, contextLogger(ctx).With("duration_ms", time.Since(started).Milliseconds()))
	if failure != nil {
		updated, err := worker.fail(outcomeContext, claim, failure)
		logJobPersistence(logger, updated, err)
		return err
	}
	updated, err := worker.complete(ctx, claim, result)
	logJobPersistence(logger, updated, err)
	if err == nil && updated {
		logger.Info("Check job completed", "duration_ms", time.Since(started).Milliseconds())
	}
	return err
}

func (worker *Worker) checkSafely(ctx context.Context, examID string) (result json.RawMessage, err error) {
	defer func() {
		if recover() != nil {
			contextLogger(ctx).Error("Check job panicked")
			result = nil
			err = &CheckingError{"Exam processing failed unexpectedly. Please try again.", false}
		}
	}()
	result, err = worker.checker.Check(ctx, examID)
	if err == nil && !json.Valid(result) {
		contextLogger(ctx).Error("Check job returned an invalid result")
		return nil, &CheckingError{"Exam processing returned an invalid result. Please try again.", false}
	}
	return result, err
}

func logJobPersistence(logger *slog.Logger, updated bool, err error) {
	if err != nil {
		logger.Error("Check job outcome could not be saved")
	} else if !updated {
		logger.Warn("Check job outcome ignored after lease loss")
	}
}

func (worker *Worker) RunOnce(ctx context.Context) (bool, error) {
	claim, err := worker.claim(ctx)
	if err != nil || claim == nil {
		return false, err
	}
	return true, worker.process(ctx, *claim)
}

func (worker *Worker) Run(ctx context.Context) {
	logger := contextLogger(ctx)
	logger.Info("Worker started", "poll_interval_ms", worker.config.WorkerPoll.Milliseconds(), "max_attempts", worker.config.JobMaxAttempts)
	defer logger.Info("Worker stopped")
	for ctx.Err() == nil {
		worked, err := worker.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			logger.Warn("Worker queue operation failed")
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(worker.config.WorkerPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
