package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPromptMigrationPreservesVersionTwoExam(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(initialSchema + assessmentSchema); err != nil {
		t.Fatal(err)
	}
	seedWorkerExam(t, &Store{DB: db}, "exam")
	const criteria = `{"checks":["grammar"],"assessment":"cae","min_words":200}`
	const result = `{"transcript":"Saved student text."}`
	if _, err = db.Exec(`UPDATE exams SET criteria=?,result=?,status='completed',stage='completed',progress=100 WHERE id='exam'`, criteria, result); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path, "migration-model")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var savedCriteria, savedResult, stage string
	var progress, version int
	if err = store.DB.QueryRow("SELECT criteria,result,stage,progress FROM exams WHERE id='exam'").Scan(&savedCriteria, &savedResult, &stage, &progress); err != nil {
		t.Fatal(err)
	}
	if savedCriteria != criteria || savedResult != result || stage != "completed" || progress != 100 {
		t.Fatalf("migration changed the exam: %s %s %s %d", savedCriteria, savedResult, stage, progress)
	}
	if err = store.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("migration version: %d %v", version, err)
	}
	templates, err := store.loadPromptTemplates(t.Context())
	if err != nil || !reflect.DeepEqual(templates, defaultPromptTemplates) {
		t.Fatalf("prompt templates were not seeded: %v", err)
	}
	assertPromptTables(t, store, 15)
	for _, key := range []string{"transcriber", "checker"} {
		prompt, err := store.loadPrompt(t.Context(), key)
		if err != nil || prompt.Model != "migration-model" {
			t.Fatalf("model not migrated for %s: %+v %v", key, prompt, err)
		}
	}
}

func TestPromptTemplatesPersistEditsAcrossReopen(t *testing.T) {
	store, config := workerTestStore(t)
	templates, err := store.loadPromptTemplates(t.Context())
	if err != nil || templates["transcriber"] == "" || templates["checker"] == "" {
		t.Fatalf("base prompts missing: %v", err)
	}
	const edited = "Teacher's edited prompt.\nKeep the new wording."
	if _, err = store.DB.Exec("UPDATE main_prompts SET body=?,model='checker-model' WHERE key='checker'", edited); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec("UPDATE prompt_additions SET body='Edited grammar rules' WHERE key='check.grammar'"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(config.DBPath, "replacement-model")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	templates, err = reopened.loadPromptTemplates(t.Context())
	if err != nil || templates["checker"] != edited {
		t.Fatalf("reopening replaced the edited prompt: %q %v", templates["checker"], err)
	}
	if templates["check.grammar"] != "Edited grammar rules" {
		t.Fatal("reopening replaced the edited addition")
	}
	prompt, err := reopened.loadPrompt(t.Context(), "checker")
	if err != nil || prompt.Model != "checker-model" {
		t.Fatalf("reopening replaced the saved model: %+v %v", prompt, err)
	}
}

func TestPromptTemplateReadFailureIsRetryableAndRedacted(t *testing.T) {
	store, _ := workerTestStore(t)
	if _, err := store.DB.Exec("DROP TABLE prompt_additions"); err != nil {
		t.Fatal(err)
	}
	_, err := store.loadPrompt(context.Background(), "checker")
	var failure *CheckingError
	if !errors.As(err, &failure) || !failure.Retryable || strings.Contains(err.Error(), "prompt_additions") {
		t.Fatalf("unexpected prompt read failure: %v", err)
	}
}

func assertPromptTables(t *testing.T, store *Store, additions int) {
	t.Helper()
	var mainCount, additionCount, legacyCount int
	err := store.DB.QueryRow(`SELECT
 (SELECT COUNT(*) FROM main_prompts), (SELECT COUNT(*) FROM prompt_additions),
 (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='prompt_templates')`).
		Scan(&mainCount, &additionCount, &legacyCount)
	if err != nil || mainCount != 2 || additionCount != additions || legacyCount != 0 {
		t.Fatalf("prompt table split: main=%d additions=%d legacy=%d err=%v", mainCount, additionCount, legacyCount, err)
	}
}

func legacyPromptDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v3.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(initialSchema + assessmentSchema); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = migratePromptTemplates(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestSplitPromptMigrationPreservesSavedVersionThreeTemplates(t *testing.T) {
	path, db := legacyPromptDatabase(t)
	edits := map[string]string{
		"transcriber":        "  Preserve typos.\nDo not trim these spaces.  ",
		"checker":            "Teacher's reviewed checker — keep exact text.",
		"check.grammar":      "Saved grammar addition.\n",
		"check.teacher_note": "Preserve this additional saved template.",
	}
	for key, body := range edits {
		if _, err := db.Exec("INSERT INTO prompt_templates(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", key, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path, "  original-model  ")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertPromptTables(t, store, 16)
	templates, err := store.loadPromptTemplates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]string, len(defaultPromptTemplates)+1)
	for key, body := range defaultPromptTemplates {
		want[key] = body
	}
	for key, body := range edits {
		want[key] = body
	}
	if !reflect.DeepEqual(templates, want) {
		t.Fatal("migration changed saved template text or keys")
	}
	for _, key := range []string{"transcriber", "checker"} {
		prompt, err := store.loadPrompt(t.Context(), key)
		if err != nil || prompt.Model != "original-model" {
			t.Errorf("initial model for %s: %q %v", key, prompt.Model, err)
		}
	}
}

func TestSplitPromptMigrationRollsBackWhenMainPromptIsMissing(t *testing.T) {
	path, db := legacyPromptDatabase(t)
	if _, err := db.Exec("DELETE FROM prompt_templates WHERE key='checker'"); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(path, "test-model"); err == nil {
		store.Close()
		t.Fatal("incomplete main prompts were migrated")
	}
	var version, legacyCount, newTables int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM prompt_templates),
 (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('main_prompts','prompt_additions'))`).Scan(&legacyCount, &newTables)
	if err != nil || version != 3 || legacyCount != 16 || newTables != 0 {
		t.Fatalf("migration did not roll back: version=%d rows=%d newTables=%d err=%v", version, legacyCount, newTables, err)
	}
}

func TestPromptLoadsOnlySelectedMainAndItsModel(t *testing.T) {
	store, _ := workerTestStore(t)
	if _, err := store.DB.Exec("UPDATE main_prompts SET model='assessment-model' WHERE key='checker'"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"transcriber", "checker"} {
		prompt, err := store.loadPrompt(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		wantModel := "test-model"
		if key == "checker" {
			wantModel = "assessment-model"
		}
		if prompt.Model != wantModel || len(prompt.Templates) != 16 || prompt.Templates[key] != defaultPromptTemplates[key] {
			t.Fatalf("wrong saved prompt: key=%s model=%q templates=%d", key, prompt.Model, len(prompt.Templates))
		}
		for addition, body := range defaultPromptTemplates {
			if addition != "checker" && addition != "transcriber" && prompt.Templates[addition] != body {
				t.Errorf("missing addition %s for %s", addition, key)
			}
		}
	}
}

func TestNewStoreUsesSeparateDefaultModels(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "defaults.sqlite"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertPromptTables(t, store, 15)
	for key, model := range map[string]string{"transcriber": "gpt-6.1-terra", "checker": "gpt-6.2-sol"} {
		prompt, err := store.loadPrompt(t.Context(), key)
		if err != nil || prompt.Model != model {
			t.Errorf("default model for %s: %q %v", key, prompt.Model, err)
		}
	}
}

func TestPromptRejectsMissingOrBlankMainConfiguration(t *testing.T) {
	for _, statement := range []string{
		"DELETE FROM main_prompts WHERE key='checker'",
		"UPDATE main_prompts SET body='  ' WHERE key='checker'",
		"UPDATE main_prompts SET model='  ' WHERE key='checker'",
	} {
		t.Run(statement, func(t *testing.T) {
			store, _ := workerTestStore(t)
			if _, err := store.DB.Exec(statement); err != nil {
				t.Fatal(err)
			}
			_, err := store.loadPrompt(t.Context(), "checker")
			var failure *CheckingError
			if !errors.As(err, &failure) || failure.Retryable {
				t.Fatalf("invalid main configuration accepted: %v", err)
			}
			if _, err := store.loadPrompt(t.Context(), "transcriber"); err != nil {
				t.Fatalf("invalid checker blocked transcription: %v", err)
			}
		})
	}
}
