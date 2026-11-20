package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"time"
)

func taskPhotoURL(examID, photoID string) string {
	return "/api/exams/" + examID + "/task-photos/" + photoID
}

func (s *Server) storeTaskPhoto(ctx context.Context, examID string, photo stagedPhoto) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exam, err := loadExam(ctx, tx, examID)
	if err != nil {
		return err
	}
	if exam.Status == "queued" || exam.Status == "processing" {
		return apiProblem{409, "Wait for checking to finish before changing task photos"}
	}
	var count int
	var total int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(file_size),0) FROM exam_task_photos WHERE exam_id=?`, examID).Scan(&count, &total); err != nil {
		return err
	}
	if count >= 20 || total+photo.Size > s.Config.MaxExamBytes {
		return apiProblem{413, "Task photo storage limit reached for this exam"}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO exam_task_photos(id,exam_id,storage_key,mime_type,file_size) VALUES(?,?,?,?,?)`, photo.ID, examID, photo.Key, "image/jpeg", photo.Size)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) uploadTaskPhoto(w http.ResponseWriter, r *http.Request) {
	examID := r.PathValue("exam_id")
	if _, err := loadExam(r.Context(), s.Store.DB, examID); err != nil {
		respondAPIError(w, err)
		return
	}
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	case <-r.Context().Done():
		respondAPIError(w, r.Context().Err())
		return
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var oversized *http.MaxBytesError
		if !errors.As(err, &oversized) {
			err = apiProblem{422, "Provide multipart form data with one task photo"}
		}
		respondAPIError(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	photos := r.MultipartForm.File["photo"]
	if len(photos) != 1 {
		writeError(w, 422, "Upload one task photo using the photo field")
		return
	}
	photo, err := s.stagePhoto(r.Context(), photos[0])
	if err != nil {
		respondAPIError(w, err)
		return
	}
	if err = s.storeTaskPhoto(r.Context(), examID, photo); err != nil {
		s.removeStagedPhotos([]stagedPhoto{photo})
		respondAPIError(w, err)
		return
	}
	contextLogger(r.Context()).Info("task_photo_uploaded", "exam_id", examID, "photo_id", photo.ID)
	writeJSON(w, 201, map[string]string{"url": taskPhotoURL(examID, photo.ID)})
}

func (s *Server) getTaskPhoto(w http.ResponseWriter, r *http.Request) {
	var key string
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT p.storage_key FROM exam_task_photos p JOIN exams e ON e.id=p.exam_id WHERE p.id=? AND p.exam_id=? AND e.owner_id=?`, r.PathValue("photo_id"), r.PathValue("exam_id"), ownerID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		err = apiProblem{404, "Task photo not found"}
	}
	if err != nil {
		respondAPIError(w, err)
		return
	}
	path, err := safePhotoPath(s.Config.UploadDir, key)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, 404, "Task photo not found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, 404, "Task photo not found")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, "task.jpg", time.Time{}, file)
}
