package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func updateTestPromptModel(t *testing.T, store *Store, key, model string) {
	t.Helper()
	result, err := store.DB.Exec("UPDATE main_prompts SET model=? WHERE key=?", model, key)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("model row %q was not updated: %d %v", key, count, err)
	}
}

type promptModelHarness struct {
	store    *Store
	checker  *openAIChecker
	requests chan capturedPromptRequest
}

func newPromptModelHarness(t *testing.T, criteria Criteria) promptModelHarness {
	t.Helper()
	store, config := workerTestStore(t)
	photo := seedCheckingPhoto(t, store, config)
	if criteria.TaskPhoto != "" {
		_, err := store.DB.Exec("INSERT INTO exam_task_photos(id,exam_id,storage_key,mime_type,file_size) VALUES('task','exam','photo.png','image/png',?)", len(photo))
		if err != nil {
			t.Fatal(err)
		}
	}
	saveCheckingCriteria(t, store, criteria)
	config.OpenAIModel = ""
	requests := make(chan capturedPromptRequest, 8)
	provider := promptModelProvider(t, criteria, requests)
	t.Cleanup(provider.Close)
	checker := NewChecker(config, store).(*openAIChecker)
	checker.endpoint = provider.URL
	updateTestPromptModel(t, store, "transcriber", "ocr-model-one")
	updateTestPromptModel(t, store, "checker", "checker-model-one")
	return promptModelHarness{store, checker, requests}
}

func promptModelProvider(t *testing.T, criteria Criteria, requests chan capturedPromptRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			capturedPromptRequest
			Text struct{ Format struct{ Name string } }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- body.capturedPromptRequest
		if body.Text.Format.Name != "transcription" {
			writeProviderResponse(w, promptIntegrationFeedback(criteria))
			return
		}
		paragraphs := []transcriptParagraph{testParagraph(promptIntegrationStudentText, "clear", 1)}
		if criteria.TaskText == "" && (criteria.DetectTask || criteria.TaskPhoto != "") {
			writeProviderResponse(w, taskTranscription{paragraphs, "Describe your home."})
			return
		}
		writeProviderResponse(w, transcription{Paragraphs: paragraphs})
	}))
}

func TestCheckingReloadsMainPromptModelsWithoutRestart(t *testing.T) {
	criteria := defaultCriteria([]string{"grammar"})
	criteria.TaskText = "Describe your home."
	h := newPromptModelHarness(t, criteria)
	for _, suffix := range []string{"one", "two"} {
		updateTestPromptModel(t, h.store, "transcriber", "ocr-model-"+suffix)
		updateTestPromptModel(t, h.store, "checker", "checker-model-"+suffix)
		data, err := h.checker.Check(t.Context(), "exam")
		if err != nil {
			t.Fatal(err)
		}
		if len(h.requests) != 2 {
			t.Fatalf("expected two provider calls, got %d", len(h.requests))
		}
		ocr, assessment := <-h.requests, <-h.requests
		if ocr.Model != "ocr-model-"+suffix || assessment.Model != "checker-model-"+suffix {
			t.Fatalf("stale model selection: %q, %q", ocr.Model, assessment.Model)
		}
		var result checkResult
		if err := json.Unmarshal(data, &result); err != nil || result.Model != assessment.Model || result.TranscriptionModel != ocr.Model {
			t.Fatalf("result model does not identify the assessment model: %s %v", data, err)
		}
	}
}

func TestTaskExtractionUsesSavedTranscriberModel(t *testing.T) {
	for _, source := range []string{"essay", "photo"} {
		t.Run(source, func(t *testing.T) {
			criteria := defaultCriteria([]string{"grammar"})
			criteria.DetectTask = true
			if source == "photo" {
				criteria.TaskPhoto = "/api/exams/exam/task-photos/task"
			}
			h := newPromptModelHarness(t, criteria)
			data, err := h.checker.Check(t.Context(), "exam")
			if err != nil {
				t.Fatal(err)
			}
			if len(h.requests) != 2 {
				t.Fatalf("expected transcription and assessment, got %d", len(h.requests))
			}
			ocr, assessment := <-h.requests, <-h.requests
			if ocr.Model != "ocr-model-one" || assessment.Model != "checker-model-one" {
				t.Fatalf("wrong models for task extraction: %q, %q", ocr.Model, assessment.Model)
			}
			var result checkResult
			if err := json.Unmarshal(data, &result); err != nil || result.Task.Source != source {
				t.Fatalf("task extraction failed: %s %v", data, err)
			}
		})
	}
}

func TestMissingMainPromptModelsNeverReachProvider(t *testing.T) {
	for _, key := range []string{"transcriber", "checker"} {
		for _, mode := range []string{"empty", "whitespace", "missing"} {
			t.Run(key+"/"+mode, func(t *testing.T) {
				h := newPromptModelHarness(t, defaultCriteria([]string{"grammar"}))
				h.checker.config.OpenAIModel = "must-not-be-used-as-fallback"
				if mode == "missing" {
					if _, err := h.store.DB.Exec("DELETE FROM main_prompts WHERE key=?", key); err != nil {
						t.Fatal(err)
					}
				} else {
					model := ""
					if mode == "whitespace" {
						model = " \n\t"
					}
					updateTestPromptModel(t, h.store, key, model)
				}
				_, err := h.checker.Check(t.Context(), "exam")
				var failure *CheckingError
				if !errors.As(err, &failure) || failure.Retryable {
					t.Fatalf("expected nonretryable configuration error, got %v", err)
				}
				for len(h.requests) > 0 {
					request := <-h.requests
					if key == "transcriber" || request.Model != "ocr-model-one" {
						t.Fatalf("unconfigured prompt reached provider: %+v", request)
					}
				}
			})
		}
	}
}

func TestWordLimitOnlyResultUsesTranscriberModel(t *testing.T) {
	criteria := defaultCriteria(nil)
	maximum := 2
	criteria.MaxWords = &maximum
	h := newPromptModelHarness(t, criteria)
	updateTestPromptModel(t, h.store, "checker", "")
	data, err := h.checker.Check(t.Context(), "exam")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.requests) != 1 || (<-h.requests).Model != "ocr-model-one" {
		t.Fatal("word-limit-only check should call the saved transcription model once")
	}
	var result checkResult
	if err := json.Unmarshal(data, &result); err != nil || result.Model != "ocr-model-one" || result.TranscriptionModel != "ocr-model-one" {
		t.Fatalf("word-limit-only result names an unused model: %s %v", data, err)
	}
}

func TestWordLimitOnlyQueueDoesNotRequireCheckerModel(t *testing.T) {
	h := newAPIHarness(t)
	h.server.Config.OpenAIModel = ""
	updateTestPromptModel(t, h.server.Store, "checker", "")
	exam := h.exam(h.folder("Class").ID)
	criteria := defaultCriteria(nil)
	maximum := 2
	criteria.MaxWords = &maximum
	body := map[string]any{"criteria": criteria, "request_id": NewID()}
	h.expect(h.json(http.MethodPost, "/api/exams/"+exam.ID+"/checks", body), http.StatusAccepted)
}

func TestQueueChecksUsesSavedModels(t *testing.T) {
	for _, route := range []string{"legacy", "single", "batch"} {
		for _, unavailable := range []string{"", "transcriber", "checker"} {
			t.Run(route+"/"+unavailable, func(t *testing.T) {
				h := newAPIHarness(t)
				h.server.Config.OpenAIModel = ""
				folder := h.folder("Class")
				exam := h.exam(folder.ID)
				status := http.StatusAccepted
				if unavailable != "" {
					updateTestPromptModel(t, h.server.Store, unavailable, " \n\t")
					status = http.StatusServiceUnavailable
				}
				path := "/api/exams/" + exam.ID + "/checks"
				var body any
				if route != "legacy" {
					payload := map[string]any{"criteria": exam.Criteria, "request_id": NewID()}
					if route == "batch" {
						path = "/api/folders/" + folder.ID + "/checks"
						payload["exam_ids"] = []string{exam.ID}
					}
					body = payload
				}
				response := h.expect(h.json(http.MethodPost, path, body), status)
				if strings.Contains(string(response), "main_prompts") {
					t.Fatalf("internal table leaked through API: %s", response)
				}
			})
		}
	}
}
