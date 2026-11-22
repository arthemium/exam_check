package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func positiveAssessmentFeedback() assessmentAnalysis {
	score := 5
	feedback := assessmentAnalysis{Findings: []finding{}, Assessments: []criterionAssessment{},
		CustomAssessments: []customAssessment{{"Explain a benefit", "The response clearly explains a practical benefit."}}}
	for _, criterion := range b2Criteria {
		feedback.Assessments = append(feedback.Assessments, criterionAssessment{criterion, "The response meets this criterion effectively.", &score})
	}
	return feedback
}

func TestCheckingPreservesEmptyFindingsAndPositiveAssessments(t *testing.T) {
	for _, assessment := range []string{"general", "b2_first_schools"} {
		t.Run(assessment, func(t *testing.T) {
			store, config := workerTestStore(t)
			seedCheckingPhoto(t, store, config)
			criteria := defaultCriteria([]string{"grammar", "spelling", "punctuation", "structure", "style"})
			criteria.Assessment = assessment
			criteria.TaskText = "Explain a benefit of learning a language."
			if assessment == "b2_first_schools" {
				criteria.CustomCriteria = "Explain a benefit"
			}
			saveCheckingCriteria(t, store, criteria)
			var savedBody string
			if err := store.DB.QueryRow("SELECT body FROM main_prompts WHERE key='checker'").Scan(&savedBody); err != nil {
				t.Fatal(err)
			}
			if strings.Count(savedBody, checkerEvidencePolicy) != 1 {
				t.Fatal("saved checker must contain the evidence policy once")
			}
			savedBody += "\n\nSaved reviewer note: preserve the student's voice."
			if _, err := store.DB.Exec("UPDATE main_prompts SET body=? WHERE key='checker'", savedBody); err != nil {
				t.Fatal(err)
			}
			const text = "Learning a language helps people communicate.\nRegular practice builds confidence."
			requests := make(chan capturedPromptRequest, 2)
			var calls atomic.Int32
			feedback := positiveAssessmentFeedback()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request capturedPromptRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests <- request
				if calls.Add(1) == 1 {
					writeProviderResponse(w, transcription{Paragraphs: []transcriptParagraph{testParagraph(text, "clear", 1)}})
				} else if assessment == "general" {
					writeProviderResponse(w, analysis{Findings: []finding{}})
				} else {
					writeProviderResponse(w, feedback)
				}
			}))
			defer provider.Close()
			checker := NewChecker(config, store).(*openAIChecker)
			checker.endpoint = provider.URL
			data, err := checker.Check(t.Context(), "exam")
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 {
				t.Fatalf("empty findings triggered extra model calls: %d", calls.Load())
			}
			<-requests // The first request transcribes the image.
			request := <-requests
			if !strings.HasPrefix(request.Instructions, savedBody+"\n\n") || strings.Count(request.Instructions, checkerEvidencePolicy) != 1 {
				t.Fatal("checker instructions did not use the saved policy exactly once")
			}
			var result checkResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.Issues == nil || len(result.Issues) != 0 || len(result.ValidationWarnings) != 0 {
				t.Fatalf("clean output gained findings or warnings: %s", data)
			}
			if result.Transcript != text || len(result.Paragraphs) != 1 || result.Paragraphs[0].Text != text {
				t.Fatal("clean student text changed")
			}
			if assessment == "b2_first_schools" && (!reflect.DeepEqual(result.Assessments, feedback.Assessments) || !reflect.DeepEqual(result.CustomAssessments, feedback.CustomAssessments)) {
				t.Fatal("positive feedback or justified full scores were changed")
			}
		})
	}
}
