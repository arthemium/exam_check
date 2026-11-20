package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func saveCheckingCriteria(t *testing.T, store *Store, criteria Criteria) {
	t.Helper()
	raw, err := json.Marshal(criteria)
	if err != nil {
		t.Fatal(err)
	}
	checks, _ := json.Marshal(criteria.Checks)
	if _, err = store.DB.Exec("UPDATE exams SET criteria=?,checks=? WHERE id='exam'", string(raw), string(checks)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckingExtractsEmbeddedTaskAndUsesAdvancedCriteria(t *testing.T) {
	store, config := workerTestStore(t)
	seedCheckingPhoto(t, store, config)
	minimum, maximum := 5, 10
	criteria := defaultCriteria([]string{"grammar", "format", "taskCompletion"})
	criteria.DetectTask, criteria.Assessment, criteria.CustomCriteria = true, "cae", "Explain one benefit"
	criteria.MinWords, criteria.MaxWords = &minimum, &maximum
	saveCheckingCriteria(t, store, criteria)
	var input atomic.Value
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if calls.Add(1) == 1 {
			writeProviderResponse(w, taskTranscription{Paragraphs: []transcriptParagraph{testParagraph("She go home.", "clear", 1)}, TaskText: "Discuss a benefit of studying at home."})
			return
		}
		input.Store(string(body["input"]))
		score := 3
		feedback := assessmentAnalysis{Findings: []finding{testFinding()}, Assessments: []criterionAssessment{}, CustomAssessments: []customAssessment{{"Explain one benefit", "The answer needs an explained benefit."}}}
		for _, name := range caeCriteria {
			feedback.Assessments = append(feedback.Assessments, criterionAssessment{name, "Supported assessment evidence.", &score})
		}
		writeProviderResponse(w, feedback)
	}))
	defer provider.Close()
	checker := NewChecker(config, store).(*openAIChecker)
	checker.endpoint = provider.URL
	var stages []string
	ctx := withCheckProgress(context.Background(), func(_ int, stage string) { stages = append(stages, stage) })
	data, err := checker.Check(ctx, "exam")
	if err != nil {
		t.Fatal(err)
	}
	var result checkResult
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Task.Source != "essay" || !strings.Contains(result.Task.Text, "studying") || result.WordCount != 3 || result.WordLimit.Status != "below" {
		t.Fatalf("wrong extracted task/count: %s", data)
	}
	if len(result.Assessments) != 4 || len(result.CustomAssessments) != 1 || len(result.Issues) != 1 {
		t.Fatalf("assessment missing: %s", data)
	}
	if strings.Contains(result.Transcript, "Discuss") || !strings.Contains(input.Load().(string), "Discuss a benefit") || !strings.Contains(input.Load().(string), "Explain one benefit") {
		t.Fatalf("task or criteria leaked/lost: %s", data)
	}
	if !reflect.DeepEqual(stages, []string{"transcribing", "assessing", "finalizing"}) {
		t.Fatalf("stages: %v", stages)
	}
}

func TestCheckingTaskPhotoIsSeparateAndManualTaskWins(t *testing.T) {
	store, config := workerTestStore(t)
	photo := seedCheckingPhoto(t, store, config)
	_, err := store.DB.Exec("INSERT INTO exam_task_photos(id,exam_id,storage_key,mime_type,file_size) VALUES('task','exam','photo.png','image/png',?)", len(photo))
	if err != nil {
		t.Fatal(err)
	}
	criteria := defaultCriteria([]string{"grammar"})
	criteria.TaskPhoto = "/api/exams/exam/task-photos/task"
	checker := NewChecker(config, store).(*openAIChecker)
	photos, err := checker.loadPhotos(context.Background(), "exam")
	if err != nil {
		t.Fatal(err)
	}
	var input atomic.Value
	var manual atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		input.Store(string(body["input"]))
		paragraphs := []transcriptParagraph{testParagraph("She go home.", "clear", 1)}
		if manual.Load() {
			writeProviderResponse(w, transcription{Paragraphs: paragraphs})
		} else {
			writeProviderResponse(w, taskTranscription{paragraphs, "Describe your home."})
		}
	}))
	defer provider.Close()
	checker.endpoint = provider.URL
	source, task, err := checker.transcribeExam(context.Background(), "exam", photos, criteria)
	if err != nil || task.Source != "photo" || task.Text != "Describe your home." {
		t.Fatalf("task photo extraction: %+v %v", task, err)
	}
	if !strings.Contains(input.Load().(string), "Separate task instructions photo") || len(source.Paragraphs) != 1 {
		t.Fatalf("photo not labelled separately: %s", input.Load())
	}
	if _, err = canonicalize(source, photos); err != nil {
		t.Fatal(err)
	}
	manual.Store(true)
	criteria.TaskText, criteria.TaskSource = "Use my reviewed task.", "manual"
	_, task, err = checker.transcribeExam(context.Background(), "exam", photos, criteria)
	if err != nil || task.Text != criteria.TaskText || task.Source != "manual" || strings.Contains(input.Load().(string), "Separate task instructions photo") {
		t.Fatalf("manual task did not win: %+v %v", task, err)
	}
	if _, err = checker.loadTaskPhoto(context.Background(), "exam", "/api/exams/other/task-photos/task"); err == nil {
		t.Fatal("accepted another exam's photo")
	}
}

func TestCheckingWithholdsUnsupportedScoresAndTaskFindings(t *testing.T) {
	criteria := defaultCriteria([]string{"taskCompletion"})
	criteria.Assessment = "cae"
	score := 5
	feedback := assessmentAnalysis{Findings: []finding{{Category: "taskCompletion", Scope: "document", Suggestion: "Add the missing task content.", Explanation: "Unsupported without a task."}}}
	for _, name := range caeCriteria {
		feedback.Assessments = append(feedback.Assessments, criterionAssessment{name, "Provisional feedback.", &score})
	}
	paragraphs := []checkedParagraph{canonicalParagraph(testParagraph("Read this.", "clear", 1), "p1")}
	data, err := buildAssessmentResult("test", paragraphs, feedback, criteria, checkedTask{})
	if err != nil {
		t.Fatal(err)
	}
	var result checkResult
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Issues) != 0 || result.Assessments[0].Score != nil || result.Assessments[1].Score != nil || result.Assessments[2].Score == nil {
		t.Fatalf("unsupported score/finding accepted: %s", data)
	}
	paragraphs = []checkedParagraph{canonicalParagraph(transcriptParagraph{PageNumber: 1, Segments: []transcriptSegment{{"Read ", "clear"}, {"", "unreadable"}, {" now.", "clear"}}}, "p1")}
	data, err = buildAssessmentResult("test", paragraphs, feedback, criteria, checkedTask{Text: "Explain reading.", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.WordCount != 2 || !result.NeedsTranscriptionReview {
		t.Fatalf("unreadable marker counted: %s", data)
	}
	for _, item := range result.Assessments {
		if item.Score != nil {
			t.Fatal("score survived uncertain handwriting")
		}
	}
}

func TestCheckingAssessmentRejectsMissingInventedAndOutOfRangeCriteria(t *testing.T) {
	criteria := defaultCriteria(nil)
	criteria.Assessment, criteria.CustomCriteria = "cae", "Explain evidence"
	score := 3
	feedback := assessmentAnalysis{CustomAssessments: []customAssessment{{"Explain evidence", "Evidence is limited."}}}
	for _, name := range caeCriteria {
		feedback.Assessments = append(feedback.Assessments, criterionAssessment{name, "Evidence-based assessment.", &score})
	}
	if err := validateAssessmentFeedback(feedback, criteria); err != nil {
		t.Fatal(err)
	}
	feedback.CustomAssessments[0].Criterion = "Invented criterion"
	if err := validateAssessmentFeedback(feedback, criteria); err == nil {
		t.Fatal("accepted invented custom criterion")
	}
	feedback.CustomAssessments[0].Criterion = "Explain evidence"
	score = 6
	if err := validateAssessmentFeedback(feedback, criteria); err == nil {
		t.Fatal("accepted score above 5")
	}
	data, _ := json.Marshal(feedback)
	if err := decodeModelJSON(data, assessmentOutputSchema(), &assessmentAnalysis{}); err == nil {
		t.Fatal("schema accepted score above 5")
	}
}

func TestCheckingWordLimitAloneUsesOnlyTranscription(t *testing.T) {
	store, config := workerTestStore(t)
	seedCheckingPhoto(t, store, config)
	maximum := 2
	criteria := defaultCriteria(nil)
	criteria.MaxWords = &maximum
	saveCheckingCriteria(t, store, criteria)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeProviderResponse(w, transcription{Paragraphs: []transcriptParagraph{testParagraph("Three written words.", "clear", 1)}})
	}))
	defer provider.Close()
	checker := NewChecker(config, store).(*openAIChecker)
	checker.endpoint = provider.URL
	data, err := checker.Check(context.Background(), "exam")
	if err != nil {
		t.Fatal(err)
	}
	var result checkResult
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || result.WordCount != 3 || result.WordLimit.Status != "above" || len(result.Issues) != 0 {
		t.Fatalf("word-only check: calls=%d result=%s", calls.Load(), data)
	}
}
