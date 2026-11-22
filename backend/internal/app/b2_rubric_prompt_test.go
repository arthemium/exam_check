package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestB2CheckingUsesSavedRubricAndReloadsEdits(t *testing.T) {
	criteria := defaultCriteria(nil)
	criteria.Assessment, criteria.TaskText = "b2_first_schools", "Explain a benefit of language learning."
	h := newPromptModelHarness(t, criteria)
	if _, err := h.store.DB.Exec("INSERT INTO prompt_additions(key,body) VALUES('assessment.cae','OLD C1 RUBRIC MUST NOT BE USED')"); err != nil {
		t.Fatal(err)
	}
	var original string
	if err := h.store.DB.QueryRow("SELECT score_guidance FROM assessment_rubric_steps WHERE assessment='b2_first_schools' AND step_key='language'").Scan(&original); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"Saved B2 wording one", "Saved B2 wording two"} {
		if _, err := h.store.DB.Exec("UPDATE assessment_rubric_steps SET score_guidance=? WHERE assessment='b2_first_schools' AND step_key='language'", original+"\n"+marker); err != nil {
			t.Fatal(err)
		}
		data, err := h.checker.Check(t.Context(), "exam")
		if err != nil {
			t.Fatal(err)
		}
		if len(h.requests) != 2 {
			t.Fatalf("unexpected provider request count: %d", len(h.requests))
		}
		<-h.requests
		request := <-h.requests
		if strings.Count(request.Instructions, marker) != 1 || strings.Contains(request.Instructions, "OLD C1 RUBRIC") {
			t.Fatal("checker bypassed saved B2 descriptors or included C1")
		}
		previous := -1
		for _, name := range b2Criteria {
			position := strings.Index(request.Instructions, "B2 First for Schools — "+name)
			if position <= previous {
				t.Fatal("rubric subscales were missing or out of order")
			}
			previous = position
		}
		var result checkResult
		if err := json.Unmarshal(data, &result); err != nil || result.Assessment != "b2_first_schools" || len(result.Assessments) != 4 {
			t.Fatalf("saved result lacks B2 provenance or subscales: %s %v", data, err)
		}
	}
}

func TestGeneralCheckingDoesNotDependOnB2Rubric(t *testing.T) {
	h := newPromptModelHarness(t, defaultCriteria([]string{"grammar"}))
	if _, err := h.store.DB.Exec("DROP TABLE assessment_rubric_steps"); err != nil {
		t.Fatal(err)
	}
	data, err := h.checker.Check(t.Context(), "exam")
	if err != nil {
		t.Fatal(err)
	}
	<-h.requests
	request := <-h.requests
	if strings.Contains(request.Instructions, "B2 First for Schools") {
		t.Fatal("general checking included an unselected rubric")
	}
	var result checkResult
	if err := json.Unmarshal(data, &result); err != nil || result.Assessment != "general" || len(result.Assessments) != 0 {
		t.Fatalf("general result acquired rubric scores: %s %v", data, err)
	}
}

func TestB2RubricRejectsMissingAndIncompleteSteps(t *testing.T) {
	for _, statement := range []string{
		"DELETE FROM assessment_rubric_steps WHERE assessment='b2_first_schools' AND step_key='language'",
		"UPDATE assessment_rubric_steps SET title='Unknown criterion' WHERE assessment='b2_first_schools' AND step_key='language'",
		"UPDATE assessment_rubric_steps SET score_guidance='5: Excellent' WHERE assessment='b2_first_schools' AND step_key='language'",
	} {
		t.Run(statement, func(t *testing.T) {
			criteria := defaultCriteria(nil)
			criteria.Assessment = "b2_first_schools"
			h := newPromptModelHarness(t, criteria)
			if _, err := h.store.DB.Exec(statement); err != nil {
				t.Fatal(err)
			}
			_, err := h.checker.Check(t.Context(), "exam")
			var failure *CheckingError
			if !errors.As(err, &failure) || failure.Retryable {
				t.Fatalf("invalid rubric was accepted: %v", err)
			}
			if len(h.requests) != 1 {
				t.Fatal("incomplete rubric reached the assessment provider")
			}
		})
	}
}

func TestB2QueueRejectsMissingRubricWithoutLosingSavedFeedback(t *testing.T) {
	h := newAPIHarness(t)
	exam := h.exam(h.folder("Class").ID)
	markChecked(t, h.server.Store, exam.ID)
	if _, err := h.server.Store.DB.Exec("DELETE FROM assessment_rubric_steps WHERE assessment='b2_first_schools' AND step_key='content'"); err != nil {
		t.Fatal(err)
	}
	criteria := defaultCriteria(nil)
	criteria.Assessment = "b2_first_schools"
	h.expect(h.json(http.MethodPost, "/api/exams/"+exam.ID+"/checks", map[string]any{"criteria": criteria, "request_id": NewID()}), 503)
	requirePreservedResult(t, h, exam.ID)
	var jobs int
	if err := h.server.Store.DB.QueryRow("SELECT COUNT(*) FROM check_jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("missing rubric queued a job: %d %v", jobs, err)
	}
}

func TestCriteriaRejectsRetiredC1Assessment(t *testing.T) {
	criteria := defaultCriteria(nil)
	criteria.Assessment = "cae"
	if _, err := normalizeCriteria(criteria); err == nil {
		t.Fatal("retired C1 assessment is still selectable")
	}
}
