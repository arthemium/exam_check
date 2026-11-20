package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type apiHarness struct {
	t      *testing.T
	server *Server
	http   *httptest.Server
	client *http.Client
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	cfg := DefaultConfig()
	cfg.LoginUsername, cfg.LoginPassword = "teacher", "test-only-password"
	cfg.OpenAIKey, cfg.OpenAIModel = "test-key-never-sent", "test-model"
	cfg.DBPath, cfg.UploadDir = filepath.Join(t.TempDir(), "db.sqlite"), filepath.Join(t.TempDir(), "uploads")
	store, err := OpenStore(cfg.DBPath, cfg.OpenAIModel)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, store)
	httpServer := httptest.NewServer(server.Handler())
	jar, _ := cookiejar.New(nil)
	h := &apiHarness{t, server, httpServer, &http.Client{Jar: jar}}
	t.Cleanup(func() { httpServer.Close(); _ = store.Close() })
	h.expect(h.json(http.MethodPost, "/auth/login", map[string]string{"username": cfg.LoginUsername, "password": cfg.LoginPassword}), 200)
	return h
}

func (h *apiHarness) json(method, path string, value any) *http.Response {
	h.t.Helper()
	var body []byte
	if value != nil {
		var err error
		body, err = json.Marshal(value)
		if err != nil {
			h.t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, h.http.URL+path, bytes.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("Origin", h.server.Config.PublicBaseURL)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	return response
}

func (h *apiHarness) expect(response *http.Response, status int) []byte {
	h.t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	if response.StatusCode != status {
		h.t.Fatalf("expected status %d, got %d: %s", status, response.StatusCode, body)
	}
	return body
}

func decodeAPI[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func (h *apiHarness) folder(name string) Folder {
	return decodeAPI[Folder](h.t, h.expect(h.json(http.MethodPost, "/api/folders", map[string]string{"name": name}), 201))
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	photo := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := range photo.Pix {
		photo.Pix[i] = 255
	}
	photo.Set(0, 0, color.NRGBA{R: 255, A: 255})
	if err := png.Encode(&output, photo); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func (h *apiHarness) upload(folderID string, photos [][]byte, checks []string) *http.Response {
	h.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("title", " Essay 1 ")
	_ = writer.WriteField("student_name", " Jane Smith ")
	for _, check := range checks {
		_ = writer.WriteField("checks", check)
	}
	for _, photo := range photos {
		part, err := writer.CreateFormFile("photos", "../../exam.png")
		if err != nil {
			h.t.Fatal(err)
		}
		if _, err := part.Write(photo); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		h.t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, h.http.URL+"/api/folders/"+folderID+"/exams", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", h.server.Config.PublicBaseURL)
	response, err := h.client.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	return response
}

func (h *apiHarness) exam(folderID string) ExamDetail {
	return decodeAPI[ExamDetail](h.t, h.expect(h.upload(folderID, [][]byte{testPNG(h.t)}, []string{"grammar", "spelling"}), 201))
}

func TestAPIFoldersNamesCountsAndPagination(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("Straße")
	h.expect(h.json(http.MethodPost, "/api/folders", map[string]string{"name": " STRASSE "}), 409)
	h.expect(h.json(http.MethodPost, "/api/folders", map[string]string{"name": "   "}), 422)
	h.exam(folder.ID)
	folders := decodeAPI[[]Folder](t, h.expect(h.json(http.MethodGet, "/api/folders", nil), 200))
	if len(folders) != 1 || folders[0].ExamCount != 1 {
		t.Fatalf("unexpected folders: %+v", folders)
	}
	renamed := decodeAPI[Folder](t, h.expect(h.json(http.MethodPatch, "/api/folders/"+folder.ID, map[string]string{"name": "Class B"}), 200))
	if renamed.Name != "Class B" || renamed.ExamCount != 1 {
		t.Fatalf("unexpected renamed folder: %+v", renamed)
	}
	h.expect(h.json(http.MethodGet, "/api/folders?limit=0", nil), 422)
	h.expect(h.json(http.MethodGet, "/api/folders?offset=-1", nil), 422)
	if string(h.expect(h.json(http.MethodGet, "/api/folders?offset=1", nil), 200)) != "[]\n" {
		t.Fatal("empty list must be []")
	}
}

func TestAPIUploadNamesOrderedPagesAndPrivateAccess(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("Class A")
	exam := decodeAPI[ExamDetail](t, h.expect(h.upload(folder.ID, [][]byte{testPNG(t), testPNG(t)}, []string{"grammar", "grammar"}), 201))
	if exam.Title != "Essay 1" || exam.StudentName != "Jane Smith" || len(exam.Checks) != 1 {
		t.Fatalf("unexpected exam: %+v", exam)
	}
	if len(exam.Pages) != 2 || exam.Pages[0].PageNumber != 1 || exam.Pages[1].PageNumber != 2 || string(exam.Result) != "null" || exam.Error != nil {
		t.Fatalf("unexpected detail: %+v", exam)
	}
	page := h.json(http.MethodGet, exam.Pages[0].URL, nil)
	if page.Header.Get("Cache-Control") != "private, no-store" || page.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatal("private image headers missing")
	}
	_, format, err := image.Decode(bytes.NewReader(h.expect(page, 200)))
	if err != nil || format != "jpeg" {
		t.Fatalf("normalized page: %s %v", format, err)
	}
	h.expect(h.json(http.MethodPost, "/auth/logout", nil), 204)
	h.expect(h.json(http.MethodGet, exam.Pages[0].URL, nil), 401)
}

func TestAPIInvalidSecondPhotoRollsBackFilesAndExam(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("Class A")
	h.expect(h.upload(folder.ID, [][]byte{testPNG(t), []byte("not an image")}, []string{"grammar"}), 415)
	photos, err := os.ReadDir(h.server.Config.UploadDir)
	if err != nil || len(photos) != 0 {
		t.Fatalf("failed upload left files: %v %v", photos, err)
	}
	exams := decodeAPI[[]Exam](t, h.expect(h.json(http.MethodGet, "/api/folders/"+folder.ID+"/exams", nil), 200))
	if len(exams) != 0 {
		t.Fatal("failed upload left an exam")
	}
}

func TestAPIUploadLimitsAndCriteriaValidation(t *testing.T) {
	h := newAPIHarness(t)
	folder := h.folder("Class A")
	h.expect(h.upload(folder.ID, [][]byte{testPNG(t)}, []string{"grading"}), 422)
	h.server.Config.MaxExamBytes = 1
	h.expect(h.upload(folder.ID, [][]byte{testPNG(t)}, []string{"grammar"}), 413)
	photos, err := os.ReadDir(h.server.Config.UploadDir)
	if err != nil || len(photos) != 0 {
		t.Fatalf("size rejection left photos: %v %v", photos, err)
	}
	h.server.Config.MaxPagesPerExam = 1
	h.expect(h.upload(folder.ID, [][]byte{testPNG(t), testPNG(t)}, nil), 422)
}

func TestAPIRenamePreservesResultAndCriteriaResetIt(t *testing.T) {
	h := newAPIHarness(t)
	exam := h.exam(h.folder("Class A").ID)
	_, err := h.server.Store.DB.Exec("UPDATE exams SET status='completed',result=? WHERE id=?", `{"transcript":"original"}`, exam.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/exams/" + exam.ID
	h.expect(h.json(http.MethodPatch, path, map[string]string{"title": "Renamed", "student_name": "Jane"}), 200)
	detail := decodeAPI[ExamDetail](t, h.expect(h.json(http.MethodGet, path, nil), 200))
	if string(detail.Result) != `{"transcript":"original"}` || detail.Status != "completed" {
		t.Fatal("rename changed completed result")
	}
	h.expect(h.json(http.MethodPatch, path, map[string]any{"checks": []string{"structure"}}), 200)
	detail = decodeAPI[ExamDetail](t, h.expect(h.json(http.MethodGet, path, nil), 200))
	if string(detail.Result) != "null" || detail.Status != "uploaded" {
		t.Fatal("criteria change did not clear old results")
	}
	h.expect(h.json(http.MethodPatch, path, map[string]any{"title": nil}), 422)
	h.expect(h.json(http.MethodPatch, path, map[string]any{"unknown": "value"}), 422)
}

func TestAPIQueueIsIdempotentUnderConcurrentRequests(t *testing.T) {
	h := newAPIHarness(t)
	exam := h.exam(h.folder("Class A").ID)
	path := "/api/exams/" + exam.ID
	var wait sync.WaitGroup
	results := make(chan Job, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- decodeAPI[Job](t, h.expect(h.json(http.MethodPost, path+"/checks", nil), 202))
		}()
	}
	wait.Wait()
	close(results)
	jobID := ""
	for job := range results {
		if jobID != "" && job.ID != jobID {
			t.Fatal("concurrent requests created duplicate jobs")
		}
		jobID = job.ID
	}
	var count int
	if err := h.server.Store.DB.QueryRow("SELECT count(*) FROM check_jobs").Scan(&count); err != nil || count != 1 {
		t.Fatalf("job count %d: %v", count, err)
	}
	h.expect(h.json(http.MethodPatch, path, map[string]any{"checks": []string{"structure"}}), 409)
	h.expect(h.json(http.MethodPatch, path, map[string]string{"student_name": "New name"}), 200)
}

func TestAPIQueueRequiresConfigurationAndSelectedChecks(t *testing.T) {
	h := newAPIHarness(t)
	exam := decodeAPI[ExamDetail](t, h.expect(h.upload(h.folder("Class A").ID, [][]byte{testPNG(t)}, nil), 201))
	path := "/api/exams/" + exam.ID + "/checks"
	h.expect(h.json(http.MethodPost, path, nil), 422)
	h.server.Config.OpenAIKey = ""
	h.expect(h.json(http.MethodPost, path, nil), 503)
}

func TestAPIQueueEnforcesActiveAndDailyLimits(t *testing.T) {
	for _, daily := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "daily"}[daily], func(t *testing.T) {
			h := newAPIHarness(t)
			folder := h.folder("Class A")
			first, second := h.exam(folder.ID), h.exam(folder.ID)
			if daily {
				h.server.Config.MaxJobsPerUserPerDay = 1
			} else {
				h.server.Config.MaxActiveJobsPerUser = 1
			}
			h.expect(h.json(http.MethodPost, "/api/exams/"+first.ID+"/checks", nil), 202)
			h.expect(h.json(http.MethodPost, "/api/exams/"+second.ID+"/checks", nil), 429)
		})
	}
}

func TestAPIOtherOwnerDataCannotBeAccessed(t *testing.T) {
	h := newAPIHarness(t)
	statements := []string{
		`INSERT INTO users VALUES('other','other','other@example.invalid','Other',0)`,
		`INSERT INTO folders VALUES('private-folder','other','Private','private',0)`,
		`INSERT INTO exams(id,owner_id,folder_id,title,created_at) VALUES('private-exam','other','private-folder','Private',0)`,
		`INSERT INTO exam_pages VALUES('private-page','private-exam','private.jpg','image/jpeg',1,10)`,
	}
	for _, statement := range statements {
		if _, err := h.server.Store.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if folders := decodeAPI[[]Folder](t, h.expect(h.json(http.MethodGet, "/api/folders", nil), 200)); len(folders) != 0 {
		t.Fatal("another owner's folder was listed")
	}
	h.expect(h.json(http.MethodGet, "/api/folders/private-folder/exams", nil), 404)
	h.expect(h.json(http.MethodPatch, "/api/folders/private-folder", map[string]string{"name": "Changed"}), 404)
	h.expect(h.upload("private-folder", [][]byte{testPNG(t)}, nil), 404)
	h.expect(h.json(http.MethodGet, "/api/exams/private-exam", nil), 404)
	h.expect(h.json(http.MethodPatch, "/api/exams/private-exam", map[string]string{"title": "Changed"}), 404)
	h.expect(h.json(http.MethodGet, "/api/exams/private-exam/pages/private-page", nil), 404)
	h.expect(h.json(http.MethodPost, "/api/exams/private-exam/checks", nil), 404)
}
