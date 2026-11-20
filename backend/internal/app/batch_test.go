package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func markChecked(t *testing.T, store *Store, id string) {
	t.Helper()
	_, err := store.DB.Exec("UPDATE exams SET status='completed',stage='completed',progress=100,result=? WHERE id=?", `{"transcript":"preserve this feedback"}`, id)
	if err != nil {
		t.Fatal(err)
	}
}

func requirePreservedResult(t *testing.T, h *apiHarness, id string) ExamDetail {
	t.Helper()
	detail := decodeAPI[ExamDetail](t, h.expect(h.json(http.MethodGet, "/api/exams/"+id, nil), 200))
	if detail.Status != "completed" || string(detail.Result) != `{"transcript":"preserve this feedback"}` {
		t.Fatalf("completed result was lost: %+v %s", detail, detail.Result)
	}
	return detail
}

func TestAPIUnchangedCriteriaSavePreservesFeedback(t *testing.T) {
	h := newAPIHarness(t)
	exam := h.exam(h.folder("Class").ID)
	markChecked(t, h.server.Store, exam.ID)
	path := "/api/exams/" + exam.ID
	h.expect(h.json(http.MethodPatch, path, map[string]any{"title": "New name", "criteria": exam.Criteria}), 200)
	detail := requirePreservedResult(t, h, exam.ID)
	if detail.Title != "New name" {
		t.Fatalf("title not saved: %s", detail.Title)
	}
	h.expect(h.json(http.MethodPatch, path, map[string]any{"checks": exam.Checks}), 200)
	requirePreservedResult(t, h, exam.ID)
}

func TestAPIRejectedAtomicRecheckPreservesFeedbackAndCriteria(t *testing.T) {
	for _, reason := range []string{"configuration", "daily-limit", "invalid-criteria"} {
		t.Run(reason, func(t *testing.T) {
			h := newAPIHarness(t)
			exam := h.exam(h.folder("Class").ID)
			markChecked(t, h.server.Store, exam.ID)
			criteria := defaultCriteria([]string{"structure"})
			expected := 503
			switch reason {
			case "configuration":
				h.server.Config.OpenAIKey = ""
			case "daily-limit":
				h.server.Config.MaxJobsPerUserPerDay = 0
				expected = 429
			case "invalid-criteria":
				criteria.Assessment = "unknown"
				expected = 422
			}
			h.expect(h.json(http.MethodPost, "/api/exams/"+exam.ID+"/checks", map[string]any{"criteria": criteria, "request_id": NewID()}), expected)
			detail := requirePreservedResult(t, h, exam.ID)
			if !criteriaEqual(detail.Criteria, exam.Criteria) {
				t.Fatal("rejected check changed criteria")
			}
		})
	}
}

func TestAPIBulkQueuePersistsAllJobsAndRetriesIdempotently(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("Class")
	ids := []string{h.exam(folder.ID).ID, h.exam(folder.ID).ID, h.exam(folder.ID).ID}
	body := map[string]any{"exam_ids": ids, "criteria": defaultCriteria([]string{"grammar"}), "request_id": NewID()}
	path := "/api/folders/" + folder.ID + "/checks"
	first := decodeAPI[batchResponse](t, h.expect(h.json(http.MethodPost, path, body), 202))
	second := decodeAPI[batchResponse](t, h.expect(h.json(http.MethodPost, path, body), 202))
	if first.BatchID == "" || first.BatchID != second.BatchID {
		t.Fatal("retry duplicated the batch")
	}
	reopened, err := OpenStore(h.server.Config.DBPath, h.server.Config.OpenAIModel)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.DB.QueryRow("SELECT count(*) FROM check_jobs WHERE status='queued'").Scan(&count); err != nil || count != 3 {
		t.Fatalf("all selected jobs must survive reopening: %d %v", count, err)
	}
	worker := NewWorker(h.server.Config, reopened, workerCheckerFunc(func(context.Context, string) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }))
	for range ids {
		if worked, err := worker.RunOnce(context.Background()); !worked || err != nil {
			t.Fatalf("server could not finish batch: %v %v", worked, err)
		}
	}
	h.expect(h.json(http.MethodPost, path, body), 202)
	if err := reopened.DB.QueryRow("SELECT count(*) FROM check_jobs").Scan(&count); err != nil || count != 3 {
		t.Fatalf("completed retry duplicated work: %d %v", count, err)
	}
	for _, id := range ids {
		detail := decodeAPI[ExamDetail](t, h.expect(h.json(http.MethodGet, "/api/exams/"+id, nil), 200))
		if detail.BatchID != first.BatchID || detail.Progress != 100 || detail.Stage != "completed" {
			t.Fatalf("persistent batch/progress missing: %+v", detail.Exam)
		}
	}
}

func TestAPIBulkQueueRejectsPartialInvalidSelection(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("First")
	first := h.exam(folder.ID)
	second := h.exam(h.folder("Second").ID)
	markChecked(t, h.server.Store, first.ID)
	body := map[string]any{"exam_ids": []string{first.ID, second.ID}, "criteria": defaultCriteria([]string{"structure"}), "request_id": NewID()}
	h.expect(h.json(http.MethodPost, "/api/folders/"+folder.ID+"/checks", body), 422)
	requirePreservedResult(t, h, first.ID)
	var jobs, batches int
	if err := h.server.Store.DB.QueryRow("SELECT (SELECT count(*) FROM check_jobs),(SELECT count(*) FROM check_batches)").Scan(&jobs, &batches); err != nil || jobs != 0 || batches != 0 {
		t.Fatalf("partial batch committed: %d %d %v", jobs, batches, err)
	}
}

func TestAPIBulkCriteriaPreserveIndividualTasksAndEnforceDailyLimit(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("Class")
	ids := []string{h.exam(folder.ID).ID, h.exam(folder.ID).ID}
	for index, id := range ids {
		criteria := defaultCriteria([]string{"grammar"})
		criteria.TaskText, criteria.TaskSource = []string{"First prompt", "Second prompt"}[index], "manual"
		h.expect(h.json(http.MethodPatch, "/api/exams/"+id, map[string]any{"criteria": criteria}), 200)
	}
	body := map[string]any{"exam_ids": ids, "criteria": defaultCriteria([]string{"taskCompletion"}), "request_id": NewID()}
	h.server.Config.MaxJobsPerUserPerDay = 1
	h.expect(h.json(http.MethodPost, "/api/folders/"+folder.ID+"/checks", body), 429)
	h.server.Config.MaxJobsPerUserPerDay = 50
	h.expect(h.json(http.MethodPost, "/api/folders/"+folder.ID+"/checks", body), 202)
	for index, id := range ids {
		detail := decodeAPI[ExamDetail](t, h.expect(h.json(http.MethodGet, "/api/exams/"+id, nil), 200))
		if detail.Criteria.TaskText != []string{"First prompt", "Second prompt"}[index] || detail.Checks[0] != "taskCompletion" {
			t.Fatalf("individual instructions lost: %+v", detail.Criteria)
		}
	}
}

func TestWorkerProgressUsesCurrentLeaseOnly(t *testing.T) {
	store, config := workerTestStore(t)
	seedWorkerExam(t, store, "exam")
	worker := NewWorker(config, store, nil)
	first := requireWorkerClaim(t, worker)
	worker.now = func() time.Time { return time.Now().Add(config.JobLease + time.Second) }
	second := requireWorkerClaim(t, worker)
	if err := worker.progress(context.Background(), second, 60, "assessing"); err != nil {
		t.Fatal(err)
	}
	if err := worker.progress(context.Background(), first, 90, "finalizing"); err != nil {
		t.Fatal(err)
	}
	var progress int
	var stage string
	if err := store.DB.QueryRow("SELECT progress,stage FROM exams WHERE id='exam'").Scan(&progress, &stage); err != nil || progress != 60 || stage != "assessing" {
		t.Fatalf("stale progress accepted: %d %s %v", progress, stage, err)
	}
}

func TestWorkerExtractedTaskPersistsWithoutReplacingManualTask(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "extracted", true: "manual"}[manual], func(t *testing.T) {
			store, config := workerTestStore(t)
			seedWorkerExam(t, store, "exam")
			criteria := defaultCriteria([]string{"grammar"})
			if manual {
				criteria.TaskText, criteria.TaskSource = "Teacher's task", "manual"
			}
			encoded, _ := json.Marshal(criteria)
			if _, err := store.DB.Exec("UPDATE exams SET criteria=? WHERE id='exam'", string(encoded)); err != nil {
				t.Fatal(err)
			}
			worker := NewWorker(config, store, nil)
			claim := requireWorkerClaim(t, worker)
			result := json.RawMessage(`{"task":{"text":"Extracted instructions","source":"essay"}}`)
			if changed, err := worker.complete(context.Background(), claim, result); !changed || err != nil {
				t.Fatalf("completion: %v %v", changed, err)
			}
			exam, err := loadExam(context.Background(), store.DB, "exam")
			if err != nil {
				t.Fatal(err)
			}
			want := "Extracted instructions"
			if manual {
				want = "Teacher's task"
			}
			if exam.Criteria.TaskText != want {
				t.Fatalf("wrong task persisted: %+v", exam.Criteria)
			}
			server := NewServer(config, store)
			if _, err := server.applyExamChanges(context.Background(), "exam", examChanges{criteria: &exam.Criteria}); err != nil {
				t.Fatal(err)
			}
			var saved string
			if err := store.DB.QueryRow("SELECT result FROM exams WHERE id='exam' AND status='completed'").Scan(&saved); err != nil || saved != string(result) {
				t.Fatalf("saving extracted task lost feedback: %s %v", saved, err)
			}
		})
	}
}
