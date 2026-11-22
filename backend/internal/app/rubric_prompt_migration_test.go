package app

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func versionFiveRubricStore(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path, db := versionFourAssessmentStore(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := migrateAssessmentSteps(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestB2RubricStoresAllBandsAndRemovesC1(t *testing.T) {
	store, _ := workerTestStore(t)
	steps, err := store.ListAssessmentSteps(t.Context(), "b2_first_schools")
	if err != nil || len(steps) != 4 {
		t.Fatalf("B2 manifest: %+v %v", steps, err)
	}
	for index, info := range steps {
		step, err := store.LoadAssessmentStep(t.Context(), "b2_first_schools", info.StepKey)
		if err != nil || step.StepOrder != index+1 || step.Assessment != "b2_first_schools" {
			t.Fatalf("B2 step: %+v %v", step, err)
		}
		for band := 0; band <= 5; band++ {
			if !strings.Contains(step.ScoreGuidance, fmt.Sprintf("%d:", band)) {
				t.Errorf("missing band %d for %s", band, info.StepKey)
			}
		}
		if step.SourceSHA256 != "d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139" || step.SourceURL != "attachment:sha256:"+step.SourceSHA256 {
			t.Fatal("B2 source is not the supplied image")
		}
		if _, err := store.LoadAssessmentStep(t.Context(), "cae", info.StepKey); err == nil {
			t.Fatal("C1 rubric is still active")
		}
	}
}

func TestB2MigrationPreservesDataAndAppendsPolicyOnce(t *testing.T) {
	path, db := versionFiveRubricStore(t)
	seedWorkerExam(t, &Store{DB: db}, "exam")
	_, err := db.Exec(`UPDATE main_prompts SET body='Saved checker',model='saved-model' WHERE key='checker';
 UPDATE exams SET status='completed',result='{"saved":true}' WHERE id='exam';
 UPDATE assessment_rubric_steps SET summary='Saved C1' WHERE assessment='cae';`)
	if err != nil {
		t.Fatal(err)
	}
	// A running older app may already have received these additive updates.
	if _, err := db.Exec(assessmentB2StepsSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE assessment_rubric_steps SET summary='Saved B2' WHERE assessment='b2_first_schools'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		store, err := OpenStore(path, "replacement-model")
		if err != nil {
			t.Fatal(err)
		}
		prompt, err := store.loadPrompt(t.Context(), "checker")
		if err != nil || prompt.Model != "saved-model" || prompt.Templates["checker"] != "Saved checker\n\n"+checkerEvidencePolicy {
			t.Fatalf("saved prompt/model lost or policy duplicated: %+v %v", prompt, err)
		}
		var result, status string
		err = store.DB.QueryRow("SELECT result,status FROM exams WHERE id='exam'").Scan(&result, &status)
		if err != nil || result != `{"saved":true}` || status != "completed" {
			t.Fatalf("saved result changed: %s %s %v", result, status, err)
		}
		for assessment, want := range map[string]string{"b2_first_schools": "Saved B2"} {
			step, err := store.LoadAssessmentStep(t.Context(), assessment, "content")
			if err != nil || step.Summary != want {
				t.Fatalf("saved rubric changed: %+v %v", step, err)
			}
		}
		store.Close()
	}
}

func TestB2MigrationRollsBackIfCheckerUpdateFails(t *testing.T) {
	path, db := versionFiveRubricStore(t)
	_, err := db.Exec(`UPDATE main_prompts SET body='Saved checker' WHERE key='checker';
 CREATE TRIGGER reject_checker_update BEFORE UPDATE ON main_prompts
 BEGIN SELECT RAISE(ABORT,'test failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(path, ""); err == nil {
		store.Close()
		t.Fatal("accepted a failed checker update")
	}
	var version, count int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow("SELECT COUNT(*) FROM assessment_rubric_steps WHERE assessment='b2_first_schools'").Scan(&count)
	if err != nil || version != 5 || count != 0 {
		t.Fatalf("partial migration persisted: version=%d B2=%d %v", version, count, err)
	}
}

func TestB2MigrationDoesNotReplaceBlankCheckerWithPolicy(t *testing.T) {
	path, db := versionFiveRubricStore(t)
	if _, err := db.Exec("UPDATE main_prompts SET body='  ' WHERE key='checker'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := OpenStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.loadPrompt(t.Context(), "checker"); err == nil {
		t.Fatal("blank checker was made usable by a policy-only prompt")
	}
}
