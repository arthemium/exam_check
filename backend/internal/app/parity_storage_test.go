package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func taskPhotoForTest(t *testing.T, h *apiHarness, examID string) string {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	photo, err := form.CreateFormFile("photo", "task.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = photo.Write(testPNG(t)); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, h.http.URL+"/api/exams/"+examID+"/task-photos", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Origin", h.server.Config.PublicBaseURL)
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return decodeAPI[map[string]string](t, h.expect(response, 201))["url"]
}

func TestTaskPhotosOwnedAndDeletedWithExam(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("Tasks")
	exam := h.exam(folder.ID)
	other := h.exam(folder.ID)
	if !exam.Criteria.DetectTask {
		t.Fatal("new uploads should find embedded task instructions by default")
	}
	url := taskPhotoForTest(t, h, exam.ID)
	h.expect(h.json(http.MethodGet, url, nil), 200)
	criteria := defaultCriteria(exam.Checks)
	criteria.TaskPhoto = url
	h.expect(h.json(http.MethodPatch, "/api/exams/"+other.ID, map[string]any{"criteria": criteria}), 422)
	h.expect(h.json(http.MethodPatch, "/api/exams/"+exam.ID, map[string]any{"criteria": criteria}), 200)
	var keys []string
	rows, err := h.server.Store.DB.Query(`SELECT storage_key FROM exam_pages WHERE exam_id=? UNION ALL SELECT storage_key FROM exam_task_photos WHERE exam_id=?`, exam.ID, exam.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	h.expect(h.json(http.MethodDelete, "/api/exams/"+exam.ID, nil), 204)
	h.expect(h.json(http.MethodGet, url, nil), 404)
	for _, key := range keys {
		if _, err := os.Stat(filepath.Join(h.server.Config.UploadDir, key)); !os.IsNotExist(err) {
			t.Fatalf("deleted photo remains: %v", err)
		}
	}
	h.expect(h.json(http.MethodGet, "/api/exams/"+other.ID, nil), 200)
}

func TestFolderDeletionBlocksActiveChecksAndRemovesFiles(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("Class")
	exam := h.exam(folder.ID)
	_ = taskPhotoForTest(t, h, exam.ID)
	h.expect(h.json(http.MethodPost, "/api/exams/"+exam.ID+"/checks", nil), 202)
	h.expect(h.json(http.MethodDelete, "/api/folders/"+folder.ID, nil), 409)
	if _, err := h.server.Store.DB.Exec(`UPDATE check_jobs SET status='failed'; UPDATE exams SET status='failed'`); err != nil {
		t.Fatal(err)
	}
	h.expect(h.json(http.MethodDelete, "/api/folders/"+folder.ID, nil), 204)
	h.expect(h.json(http.MethodGet, "/api/exams/"+exam.ID, nil), 404)
	photos, err := os.ReadDir(h.server.Config.UploadDir)
	if err != nil || len(photos) != 0 {
		t.Fatalf("photo cleanup: %v %v", photos, err)
	}
	var jobs int
	if err := h.server.Store.DB.QueryRow("SELECT count(*) FROM check_jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("jobs remain: %d %v", jobs, err)
	}
}

func TestSchemaUpgradePreservesExistingFeedback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(initialSchema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users VALUES('local-owner','local-owner','owner@example.test','Owner',1);
INSERT INTO folders VALUES('folder','local-owner','Class','class',1);
INSERT INTO exams(id,owner_id,folder_id,title,checks,status,result,created_at) VALUES('exam','local-owner','folder','Essay','["grammar"]','completed','{"transcript":"saved"}',1)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(DefaultConfig(), store)
	exam, err := server.examDetail(t.Context(), "exam")
	if err != nil {
		t.Fatal(err)
	}
	if exam.Status != "completed" || exam.Progress != 100 || string(exam.Result) != `{"transcript":"saved"}` || exam.Criteria.Assessment != "general" || len(exam.Criteria.Checks) != 1 || exam.Criteria.DetectTask {
		t.Fatalf("migration lost existing record: %+v", exam)
	}
	var version int
	if err = store.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("migration version: %d %v", version, err)
	}
}

func TestCriteriaValidationRejectsInvalidWordLimitsAndSources(t *testing.T) {
	minimum, maximum := 300, 200
	for _, criteria := range []Criteria{
		{Assessment: "other"}, {MinWords: &minimum, MaxWords: &maximum}, {TaskSource: "external"}, {Checks: []string{"unsupported"}},
	} {
		if _, err := normalizeCriteria(criteria); err == nil {
			raw, _ := json.Marshal(criteria)
			t.Fatalf("accepted invalid criteria %s", raw)
		}
	}
}

func TestConcurrentSchemaUpgradeUsesOneMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.sqlite")
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(initialSchema); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			store, err := OpenStore(path, "")
			if err == nil {
				err = store.Close()
			}
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func TestTaskPhotoRejectsMalformedMultipart(t *testing.T) {
	h := newAPIHarness(t)
	exam := h.exam(h.folder("Class").ID)
	h.expect(h.json(http.MethodPost, "/api/exams/"+exam.ID+"/task-photos", map[string]string{"photo": "invalid"}), 422)
}
