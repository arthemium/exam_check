package app

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLargerUploadLimitDoesNotApplyToLogin(t *testing.T) {
	server := authFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(strings.Repeat(" ", 65537)))
	request.Header.Set("Origin", server.Config.PublicBaseURL)
	request.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("login inherited the image-upload limit: %d", response.Code)
	}
}

func largeOriginalPhoto(t *testing.T) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 2200, 1700))
	draw.Draw(source, source.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(source, image.Rect(200, 200, 210, 500), image.Black, image.Point{}, draw.Src)
	raw := encodePhotoPNG(t, source, png.NoCompression)
	if len(raw) <= 10<<20 {
		t.Fatalf("fixture must exceed the old upload limit: %d bytes", len(raw))
	}
	return raw
}

func uploadOriginalTaskPhoto(t *testing.T, h *apiHarness, examID string, raw []byte) string {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("photo", "large-original.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(raw); err != nil {
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

func assertTranscodedUpload(t *testing.T, h *apiHarness, url string) {
	t.Helper()
	data := h.expect(h.json(http.MethodGet, url, nil), 200)
	configuration, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || len(data) > 3<<20 {
		t.Fatalf("stored photo was not compressed: format=%s bytes=%d error=%v", format, len(data), err)
	}
	if configuration.Width != 2200 || configuration.Height != 1700 {
		t.Fatalf("small original was resized unnecessarily: %+v", configuration)
	}
}

func TestAPILargeOriginalsAreTranscodedForExamAndTaskPhotos(t *testing.T) {
	h := newAPIHarness(t)
	raw := largeOriginalPhoto(t)
	folder := h.folder("Large originals")
	exam := decodeAPI[ExamDetail](t, h.expect(h.upload(folder.ID, [][]byte{raw}, []string{"grammar"}), 201))
	if len(exam.Pages) != 1 {
		t.Fatalf("expected one page: %+v", exam.Pages)
	}
	assertTranscodedUpload(t, h, exam.Pages[0].URL)
	assertTranscodedUpload(t, h, uploadOriginalTaskPhoto(t, h, exam.ID, raw))
	files, err := os.ReadDir(h.server.Config.UploadDir)
	if err != nil || len(files) != 2 {
		t.Fatalf("unexpected saved originals or files: %v %v", files, err)
	}
	for _, file := range files {
		info, err := file.Info()
		if err != nil || filepath.Ext(file.Name()) != ".jpg" || info.Size() > 3<<20 {
			t.Fatalf("uncompressed original retained: %s %v", file.Name(), err)
		}
	}
}
