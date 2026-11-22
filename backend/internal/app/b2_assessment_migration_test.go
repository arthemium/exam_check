package app

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
)

func versionSixAssessmentStore(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path, db := versionFiveRubricStore(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := migrateB2RubricAndCheckerEvidence(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestReplaceC1PreservesHistoricalResultsAndCustomLimits(t *testing.T) {
	for _, limits := range []struct{ min, max, wantMin, wantMax int }{{220, 260, 140, 190}, {200, 300, 200, 300}} {
		path, db := versionSixAssessmentStore(t)
		seedWorkerExam(t, &Store{DB: db}, "exam")
		criteria, _ := json.Marshal(map[string]any{"assessment": "cae", "min_words": limits.min, "max_words": limits.max, "custom_criteria": "Use examples"})
		const result = `{"transcript":"Original  text.\nNext line.","issues":[],"assessments":[{"criterion":"Language","score":4,"feedback":"Saved feedback"}],"word_limit":{"minimum":220,"maximum":260,"status":"within"}}`
		_, err := db.Exec(`UPDATE exams SET status='completed',criteria=?,result=? WHERE id='exam';
 INSERT INTO prompt_additions(key,body) VALUES('assessment.cae','Legacy C1 rules');
 UPDATE prompt_additions SET body='Saved B2 instructions' WHERE key='assessment.b2_first_schools';`, string(criteria), result)
		if err != nil {
			t.Fatal(err)
		}
		// Schema 6 may predate the B2 instruction row.
		if _, err := db.Exec("INSERT INTO prompt_additions(key,body) VALUES('assessment.b2_first_schools','Saved B2 instructions') ON CONFLICT(key) DO NOTHING"); err != nil {
			t.Fatal(err)
		}
		db.Close()
		store, err := OpenStore(path, "replacement-model")
		if err != nil {
			t.Fatal(err)
		}
		var rawCriteria, rawResult, status string
		if err := store.DB.QueryRow("SELECT criteria,result,status FROM exams WHERE id='exam'").Scan(&rawCriteria, &rawResult, &status); err != nil {
			t.Fatal(err)
		}
		var savedCriteria Criteria
		if err := json.Unmarshal([]byte(rawCriteria), &savedCriteria); err != nil {
			t.Fatal(err)
		}
		if savedCriteria.Assessment != "b2_first_schools" || *savedCriteria.MinWords != limits.wantMin || *savedCriteria.MaxWords != limits.wantMax || savedCriteria.CustomCriteria != "Use examples" || status != "completed" {
			t.Fatalf("incorrect migrated settings: %s %s", rawCriteria, status)
		}
		var before, after map[string]any
		json.Unmarshal([]byte(result), &before)
		json.Unmarshal([]byte(rawResult), &after)
		if after["assessment"] != "cae" {
			t.Fatal("historical C1 score was relabelled")
		}
		delete(after, "assessment")
		if !reflect.DeepEqual(before, after) {
			t.Fatal("historical result content changed")
		}
		var oldPrompts, oldSteps int
		if err := store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM prompt_additions WHERE key='assessment.cae'),
 (SELECT COUNT(*) FROM assessment_rubric_steps WHERE assessment='cae')`).Scan(&oldPrompts, &oldSteps); err != nil || oldPrompts != 0 || oldSteps != 0 {
			t.Fatalf("C1 still active: %d %d %v", oldPrompts, oldSteps, err)
		}
		var body string
		if err := store.DB.QueryRow("SELECT body FROM prompt_additions WHERE key='assessment.b2_first_schools'").Scan(&body); err != nil || body != "Saved B2 instructions" {
			t.Fatalf("saved B2 prompt overwritten: %v", err)
		}
		store.Close()
	}
}

func TestReplaceC1MigrationIsAtomic(t *testing.T) {
	path, db := versionSixAssessmentStore(t)
	seedWorkerExam(t, &Store{DB: db}, "exam")
	_, err := db.Exec(`UPDATE exams SET criteria='{"assessment":"cae"}',result='{"saved":true}' WHERE id='exam';
 CREATE TRIGGER reject_rubric_delete BEFORE DELETE ON assessment_rubric_steps
 BEGIN SELECT RAISE(ABORT,'test failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(path, ""); err == nil {
		store.Close()
		t.Fatal("failed migration was accepted")
	}
	var criteria, result string
	var version int
	if err := db.QueryRow("SELECT criteria,result FROM exams WHERE id='exam'").Scan(&criteria, &result); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 || criteria != `{"assessment":"cae"}` || result != `{"saved":true}` {
		t.Fatalf("partial migration persisted: %d %s %s %v", version, criteria, result, err)
	}
}
