package app

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAssessmentStepsSeedAndRetrieveIndividually(t *testing.T) {
	store, _ := workerTestStore(t)
	steps, err := store.ListAssessmentSteps(t.Context(), "b2_first_schools")
	want := []AssessmentStepInfo{
		{"content", 1, "Content"}, {"communicative_achievement", 2, "Communicative Achievement"},
		{"organisation", 3, "Organisation"}, {"language", 4, "Language"},
	}
	if err != nil || !reflect.DeepEqual(steps, want) {
		t.Fatalf("incorrect step manifest: %+v %v", steps, err)
	}
	manifest, err := json.Marshal(steps)
	if err != nil || strings.Contains(string(manifest), "summary") || strings.Contains(string(manifest), "score_guidance") {
		t.Fatalf("manifest included full rubric bodies: %s %v", manifest, err)
	}
	for _, info := range steps {
		step, err := store.LoadAssessmentStep(t.Context(), "b2_first_schools", info.StepKey)
		if err != nil || step.AssessmentStepInfo != info || step.Assessment != "b2_first_schools" {
			t.Fatalf("incorrect individual step: %+v %v", step, err)
		}
		for band := 0; band <= 5; band++ {
			if !strings.Contains(step.ScoreGuidance, fmt.Sprintf("%d:", band)) {
				t.Errorf("%s missing guidance for band %d", info.StepKey, band)
			}
		}
		hash, err := hex.DecodeString(step.SourceSHA256)
		if err != nil || len(hash) != 32 || !strings.HasPrefix(step.SourceURL, "attachment:sha256:") {
			t.Fatalf("missing source provenance: %+v", step)
		}
	}
}

func TestAssessmentStepsUseExactParameterizedLookups(t *testing.T) {
	store, _ := workerTestStore(t)
	_, err := store.DB.Exec(`INSERT INTO assessment_rubric_steps
 SELECT 'other',step_key,step_order,title,'Other exam rules',score_guidance,
 source_url,source_pages,source_sha256,reference_version FROM assessment_rubric_steps WHERE assessment='b2_first_schools'`)
	if err != nil {
		t.Fatal(err)
	}
	for _, assessment := range []string{"b2_first_schools", "other"} {
		step, err := store.LoadAssessmentStep(t.Context(), assessment, "content")
		if err != nil || step.Assessment != assessment || (step.Summary == "Other exam rules") != (assessment == "other") {
			t.Fatalf("lookup crossed assessment boundaries: %+v %v", step, err)
		}
	}
	_, err = store.ListAssessmentSteps(t.Context(), "private-value' OR 1=1 --")
	assertAssessmentStepError(t, err, false)
	_, err = store.LoadAssessmentStep(t.Context(), "b2_first_schools", "private-value' OR 1=1 --")
	assertAssessmentStepError(t, err, false)
}

func versionFourAssessmentStore(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path, db := legacyPromptDatabase(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := splitPromptTemplates(tx, "saved-model"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestAssessmentMigrationPreservesExistingData(t *testing.T) {
	for _, provisioned := range []bool{false, true} {
		t.Run(fmt.Sprintf("provisioned=%t", provisioned), func(t *testing.T) {
			path, db := versionFourAssessmentStore(t)
			seedWorkerExam(t, &Store{DB: db}, "exam")
			_, err := db.Exec(`UPDATE main_prompts SET body='Saved checker',model='saved-checker-model' WHERE key='checker';
 UPDATE prompt_additions SET body='Saved addition' WHERE key='assessment.b2_first_schools';
 UPDATE exams SET status='completed',criteria='{"assessment":"b2_first_schools"}',result='{"saved":true}' WHERE id='exam';`)
			if err != nil {
				t.Fatal(err)
			}
			if provisioned {
				if _, err := db.Exec(assessmentStepsSQL + assessmentB2StepsSQL); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("UPDATE assessment_rubric_steps SET summary='Saved summary' WHERE assessment='b2_first_schools' AND step_key='content'"); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			store, err := OpenStore(path, "replacement-model")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			prompt, err := store.loadPrompt(t.Context(), "checker")
			if err != nil || prompt.Model != "saved-checker-model" || prompt.Templates["checker"] != "Saved checker\n\n"+checkerEvidencePolicy || prompt.Templates["assessment.b2_first_schools"] != "Saved addition" {
				t.Fatalf("migration altered saved prompts: %+v %v", prompt, err)
			}
			var status, criteria, result string
			err = store.DB.QueryRow("SELECT status,criteria,result FROM exams WHERE id='exam'").Scan(&status, &criteria, &result)
			if err != nil || status != "completed" || criteria != `{"assessment":"b2_first_schools"}` || result != `{"saved":true}` {
				t.Fatalf("migration altered saved exam: %s %s %s %v", status, criteria, result, err)
			}
			step, err := store.LoadAssessmentStep(t.Context(), "b2_first_schools", "content")
			if err != nil || (provisioned && step.Summary != "Saved summary") {
				t.Fatalf("migration altered saved reference: %+v %v", step, err)
			}
			var version int
			if err := store.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
				t.Fatalf("wrong schema version: %d %v", version, err)
			}
		})
	}
}

func TestAssessmentStepsPreserveEditsAcrossReopen(t *testing.T) {
	store, config := workerTestStore(t)
	if _, err := store.DB.Exec("UPDATE assessment_rubric_steps SET summary='Reviewed summary' WHERE assessment='b2_first_schools' AND step_key='language'"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	reopened, err := OpenStore(config.DBPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	step, err := reopened.LoadAssessmentStep(t.Context(), "b2_first_schools", "language")
	if err != nil || step.Summary != "Reviewed summary" {
		t.Fatalf("reopen overwrote saved reference: %+v %v", step, err)
	}
}

func TestAssessmentMigrationRollsBackSeedConflict(t *testing.T) {
	path, db := versionFourAssessmentStore(t)
	if _, err := db.Exec(assessmentStepsSQL); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`DELETE FROM assessment_rubric_steps WHERE step_key<>'language';
 UPDATE assessment_rubric_steps SET step_key='custom-step' WHERE step_key='language';`)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(path, ""); err == nil {
		store.Close()
		t.Fatal("conflicting rubric sequence was accepted")
	}
	var version, count int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM assessment_rubric_steps").Scan(&count); err != nil || version != 4 || count != 1 {
		t.Fatalf("partial migration persisted: version=%d count=%d err=%v", version, count, err)
	}
}

func TestAssessmentStepErrorsAreSafeAndClassified(t *testing.T) {
	store, _ := workerTestStore(t)
	_, err := store.LoadAssessmentStep(t.Context(), "b2_first_schools", "private-missing-step")
	assertAssessmentStepError(t, err, false)
	if _, err := store.DB.Exec(`PRAGMA ignore_check_constraints=ON;
 UPDATE assessment_rubric_steps SET summary='   ' WHERE assessment='b2_first_schools' AND step_key='content';
 PRAGMA ignore_check_constraints=OFF;`); err != nil {
		t.Fatal(err)
	}
	_, err = store.LoadAssessmentStep(t.Context(), "b2_first_schools", "content")
	assertAssessmentStepError(t, err, false)
	store.Close()
	_, err = store.LoadAssessmentStep(t.Context(), "b2_first_schools", "language")
	assertAssessmentStepError(t, err, true)
	_, err = store.ListAssessmentSteps(t.Context(), "b2_first_schools")
	assertAssessmentStepError(t, err, true)
}

func assertAssessmentStepError(t *testing.T, err error, retryable bool) {
	t.Helper()
	var failure *CheckingError
	if !errors.As(err, &failure) || failure.Retryable != retryable {
		t.Fatalf("incorrect error classification: %v", err)
	}
	if strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "sql:") || strings.Contains(err.Error(), "assessment_rubric_steps") {
		t.Fatalf("error exposed internal data: %v", err)
	}
}
