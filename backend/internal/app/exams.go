package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type apiProblem struct {
	status int
	detail string
}

func (p apiProblem) Error() string { return p.detail }

func respondAPIError(w http.ResponseWriter, err error) {
	var problem apiProblem
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &problem):
		writeError(w, problem.status, problem.detail)
	case errors.As(err, &tooLarge):
		writeError(w, 413, "Request exceeds the size limit")
	case errors.Is(err, context.Canceled):
		writeError(w, 408, "Request cancelled")
	default:
		writeError(w, 500, "The request could not be completed")
	}
}

const examColumns = "id,folder_id,title,student_name,checks,status,created_at,criteria,batch_id,progress,stage"

type rowScanner interface{ Scan(...any) error }
type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanExam(row rowScanner) (Exam, error) {
	return scanExamValues(row)
}

func scanExamValues(row rowScanner, extra ...any) (Exam, error) {
	var exam Exam
	var checks, criteria string
	var created int64
	values := []any{&exam.ID, &exam.FolderID, &exam.Title, &exam.StudentName, &checks, &exam.Status, &created, &criteria, &exam.BatchID, &exam.Progress, &exam.Stage}
	if err := row.Scan(append(values, extra...)...); err != nil {
		return exam, err
	}
	if err := json.Unmarshal([]byte(checks), &exam.Checks); err != nil {
		return exam, err
	}
	if exam.Checks == nil {
		exam.Checks = []string{}
	}
	exam.Criteria = defaultCriteria(exam.Checks)
	if err := json.Unmarshal([]byte(criteria), &exam.Criteria); err != nil {
		return exam, err
	}
	exam.Criteria.Checks = exam.Checks
	exam.CreatedAt = timeString(created)
	return exam, nil
}

func loadExam(ctx context.Context, db queryRower, id string) (Exam, error) {
	exam, err := scanExam(db.QueryRowContext(ctx, "SELECT "+examColumns+" FROM exams WHERE id=? AND owner_id=?", id, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		err = apiProblem{404, "Exam not found"}
	}
	return exam, err
}

func (s *Server) listExams(w http.ResponseWriter, r *http.Request) {
	if err := s.folderExists(r, r.PathValue("folder_id")); err != nil {
		respondAPIError(w, err)
		return
	}
	limit, offset, err := pagination(r)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), "SELECT "+examColumns+" FROM exams WHERE folder_id=? AND owner_id=? ORDER BY created_at,id LIMIT ? OFFSET ?",
		r.PathValue("folder_id"), ownerID, limit, offset)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	defer rows.Close()
	exams := []Exam{}
	for rows.Next() {
		exam, err := scanExam(rows)
		if err != nil {
			respondAPIError(w, err)
			return
		}
		exams = append(exams, exam)
	}
	if err := rows.Err(); err != nil {
		respondAPIError(w, err)
		return
	}
	writeJSON(w, 200, exams)
}

func (s *Server) examDetail(ctx context.Context, id string) (ExamDetail, error) {
	var result, failure sql.NullString
	exam, err := scanExamValues(s.Store.DB.QueryRowContext(ctx,
		"SELECT "+examColumns+",result,error FROM exams WHERE id=? AND owner_id=?", id, ownerID), &result, &failure)
	if errors.Is(err, sql.ErrNoRows) {
		err = apiProblem{404, "Exam not found"}
	}
	if err != nil {
		return ExamDetail{}, err
	}
	detail := ExamDetail{Exam: exam, Pages: []Page{}}
	if result.Valid {
		if !json.Valid([]byte(result.String)) {
			return detail, errors.New("invalid stored result")
		}
		detail.Result = json.RawMessage(result.String)
	}
	if failure.Valid {
		detail.Error = &failure.String
	}
	rows, err := s.Store.DB.QueryContext(ctx, "SELECT id,page_number FROM exam_pages WHERE exam_id=? ORDER BY page_number", id)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	for rows.Next() {
		var page Page
		if err := rows.Scan(&page.ID, &page.PageNumber); err != nil {
			return detail, err
		}
		page.URL = fmt.Sprintf("/api/exams/%s/pages/%s", id, page.ID)
		detail.Pages = append(detail.Pages, page)
	}
	return detail, rows.Err()
}

func (s *Server) getExam(w http.ResponseWriter, r *http.Request) {
	detail, err := s.examDetail(r.Context(), r.PathValue("exam_id"))
	if err != nil {
		respondAPIError(w, err)
		return
	}
	writeJSON(w, 200, detail)
}

func validChecks(checks []string) ([]string, error) {
	if len(checks) > 8 {
		return nil, apiProblem{422, "Select up to eight checking criteria"}
	}
	unique := []string{}
	seen := map[string]bool{}
	for _, check := range checks {
		switch check {
		case "grammar", "spelling", "punctuation", "structure", "format", "register", "style", "taskCompletion":
		default:
			return nil, apiProblem{422, "Unknown checking criterion"}
		}
		if !seen[check] {
			unique = append(unique, check)
			seen[check] = true
		}
	}
	return unique, nil
}

func uploadMetadata(r *http.Request) (Exam, error) {
	exam := Exam{ID: NewID(), FolderID: r.PathValue("folder_id"), Status: "uploaded"}
	values := r.MultipartForm.Value
	if len(values["title"]) != 1 || len(values["student_name"]) > 1 {
		return exam, apiProblem{422, "Provide one exam title and student name"}
	}
	exam.Title = strings.TrimSpace(values["title"][0])
	if len(values["student_name"]) == 1 {
		exam.StudentName = strings.TrimSpace(values["student_name"][0])
	}
	if !validName(exam.Title, false) || !validName(exam.StudentName, true) {
		return exam, apiProblem{422, "Provide an exam title and student name of up to 120 characters"}
	}
	checks, err := validChecks(values["checks"])
	exam.Checks = checks
	return exam, err
}

func (s *Server) stageExamPhotos(r *http.Request) ([]stagedPhoto, error) {
	headers := r.MultipartForm.File["photos"]
	if len(headers) < 1 || len(headers) > s.Config.MaxPagesPerExam {
		return nil, apiProblem{422, fmt.Sprintf("Upload 1–%d pages per exam", s.Config.MaxPagesPerExam)}
	}
	photos := []stagedPhoto{}
	var total int64
	for _, header := range headers {
		if err := r.Context().Err(); err != nil {
			return photos, err
		}
		photo, err := s.stagePhoto(r.Context(), header)
		if err != nil {
			return photos, err
		}
		photos = append(photos, photo)
		total += photo.Size
		if total > s.Config.MaxExamBytes {
			return photos, apiProblem{413, "Exam photos exceed the total size limit"}
		}
	}
	return photos, nil
}

func (s *Server) insertExam(ctx context.Context, exam Exam, photos []stagedPhoto) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM folders WHERE id=? AND owner_id=?", exam.FolderID, ownerID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apiProblem{404, "Folder not found"}
		}
		return err
	}
	checks, _ := json.Marshal(exam.Checks)
	criteria := defaultCriteria(exam.Checks)
	criteria.DetectTask = true
	encodedCriteria, _ := json.Marshal(criteria)
	_, err = tx.ExecContext(ctx, `INSERT INTO exams(id,owner_id,folder_id,title,student_name,checks,criteria,status,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, exam.ID, ownerID, exam.FolderID, exam.Title, exam.StudentName, string(checks), string(encodedCriteria), "uploaded", utcMillis())
	if err != nil {
		return err
	}
	for index, photo := range photos {
		_, err := tx.ExecContext(ctx, `INSERT INTO exam_pages(id,exam_id,storage_key,mime_type,page_number,file_size) VALUES(?,?,?,?,?,?)`,
			photo.ID, exam.ID, photo.Key, "image/jpeg", index+1, photo.Size)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) uploadExam(w http.ResponseWriter, r *http.Request) {
	if err := s.folderExists(r, r.PathValue("folder_id")); err != nil {
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
			err = apiProblem{422, "Provide multipart form data with exam photos"}
		}
		respondAPIError(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	exam, err := uploadMetadata(r)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	photos, err := s.stageExamPhotos(r)
	if err == nil {
		err = s.insertExam(r.Context(), exam, photos)
	}
	if err != nil {
		s.removeStagedPhotos(photos)
		respondAPIError(w, err)
		return
	}
	detail, err := s.examDetail(r.Context(), exam.ID)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	contextLogger(r.Context()).Info("exam_uploaded", "exam_id", exam.ID, "page_count", len(photos))
	writeJSON(w, 201, detail)
}

type examChanges struct {
	title, studentName *string
	checks             []string
	checksSet          bool
	criteria           *Criteria
}

func parseExamChanges(body map[string]json.RawMessage) (examChanges, error) {
	var changes examChanges
	for name, raw := range body {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return changes, apiProblem{422, "Use an empty student name or checks list instead of null"}
		}
		switch name {
		case "title", "student_name":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return changes, apiProblem{422, "Names must be strings"}
			}
			value = strings.TrimSpace(value)
			if !validName(value, name == "student_name") {
				return changes, apiProblem{422, "Provide names of up to 120 characters and a non-empty title"}
			}
			if name == "title" {
				changes.title = &value
			} else {
				changes.studentName = &value
			}
		case "criteria":
			criteria, err := parseCriteriaJSON(raw)
			if err != nil {
				return changes, err
			}
			changes.criteria = &criteria
		case "checks":
			var values []string
			if err := json.Unmarshal(raw, &values); err != nil {
				return changes, apiProblem{422, "Checks must be a list"}
			}
			checks, err := validChecks(values)
			if err != nil {
				return changes, err
			}
			changes.checks, changes.checksSet = checks, true
		default:
			return changes, apiProblem{422, "Unknown exam field"}
		}
	}
	if changes.checksSet && changes.criteria != nil {
		return changes, apiProblem{422, "Provide criteria or checks, not both"}
	}
	return changes, nil
}

func (s *Server) applyExamChanges(ctx context.Context, id string, changes examChanges) (Exam, error) {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Exam{}, err
	}
	defer tx.Rollback()
	exam, err := loadExam(ctx, tx, id)
	if err != nil {
		return exam, err
	}
	if (changes.checksSet || changes.criteria != nil) && (exam.Status == "queued" || exam.Status == "processing") {
		return exam, apiProblem{409, "Wait for the current check to finish before changing criteria"}
	}
	if changes.title != nil {
		exam.Title = *changes.title
	}
	if changes.studentName != nil {
		exam.StudentName = *changes.studentName
	}
	_, err = tx.ExecContext(ctx, "UPDATE exams SET title=?,student_name=? WHERE id=? AND owner_id=?", exam.Title, exam.StudentName, id, ownerID)
	if err != nil {
		return exam, err
	}
	criteria := exam.Criteria
	if changes.criteria != nil {
		criteria = *changes.criteria
	}
	if changes.checksSet {
		criteria.Checks = changes.checks
	}
	if !criteriaEqual(criteria, exam.Criteria) {
		if err := validateTaskPhoto(ctx, tx, id, criteria); err != nil {
			return exam, err
		}
		if err := saveExamCriteria(ctx, tx, id, criteria, true); err != nil {
			return exam, err
		}
		exam.Criteria, exam.Checks, exam.Status = criteria, criteria.Checks, "uploaded"
		exam.Progress, exam.Stage, exam.BatchID = 0, "uploaded", ""
	}
	return exam, tx.Commit()
}

func (s *Server) updateExam(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if !decodeJSON(w, r, &body) {
		return
	}
	if body == nil {
		writeError(w, 422, "Provide an exam object")
		return
	}
	changes, err := parseExamChanges(body)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	exam, err := s.applyExamChanges(r.Context(), r.PathValue("exam_id"), changes)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	writeJSON(w, 200, exam)
}

func (s *Server) getPage(w http.ResponseWriter, r *http.Request) {
	var key string
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT p.storage_key FROM exam_pages p JOIN exams e ON e.id=p.exam_id
		WHERE p.id=? AND p.exam_id=? AND e.owner_id=?`, r.PathValue("page_id"), r.PathValue("exam_id"), ownerID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		err = apiProblem{404, "Photo not found"}
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
		writeError(w, 404, "Photo not found")
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		writeError(w, 404, "Photo not found")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "exam.jpg", time.Time{}, file)
}

func (s *Server) checkJobLimits(ctx context.Context, tx *sql.Tx) error {
	var active, daily int
	err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(CASE WHEN j.status IN ('queued','processing') THEN 1 ELSE 0 END),0),
		coalesce(sum(CASE WHEN j.created_at>=? THEN 1 ELSE 0 END),0)
		FROM check_jobs j JOIN exams e ON e.id=j.exam_id WHERE e.owner_id=?`, utcMillis()-int64(24*time.Hour/time.Millisecond), ownerID).Scan(&active, &daily)
	if err != nil {
		return err
	}
	if active >= s.Config.MaxActiveJobsPerUser || daily >= s.Config.MaxJobsPerUserPerDay {
		return apiProblem{429, "Exam checking limit reached; try again later"}
	}
	return nil
}

func (s *Server) enqueueCheck(ctx context.Context, id string) (Job, error) {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	exam, err := loadExam(ctx, tx, id)
	if err != nil {
		return Job{}, err
	}
	var job Job
	err = tx.QueryRowContext(ctx, "SELECT id,exam_id,status FROM check_jobs WHERE exam_id=? AND status IN ('queued','processing')", id).Scan(&job.ID, &job.ExamID, &job.Status)
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return job, err
	}
	if err := s.validateCheckingSetup(ctx, tx, exam.Criteria); err != nil {
		return job, err
	}
	if !hasCheckingCriteria(exam.Criteria) {
		return job, apiProblem{422, "Select at least one checking criterion"}
	}
	if err := s.checkJobLimits(ctx, tx); err != nil {
		return job, err
	}
	job = Job{ID: NewID(), ExamID: id, Status: "queued"}
	now := utcMillis()
	_, err = tx.ExecContext(ctx, "INSERT INTO check_jobs(id,exam_id,status,attempts,available_at,created_at) VALUES(?,?,?,0,?,?)", job.ID, id, job.Status, now, now)
	if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE exams SET status='queued',stage='queued',progress=0,batch_id='',error=NULL,result=NULL WHERE id=? AND owner_id=?", id, ownerID)
	}
	if err != nil {
		return job, err
	}
	if err := tx.Commit(); err != nil {
		return job, err
	}
	contextLogger(ctx).Info("check_queued", "job_id", job.ID, "exam_id", job.ExamID)
	return job, nil
}

func (s *Server) queueCheck(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		s.queueExamWithCriteria(w, r)
		return
	}
	job, err := s.enqueueCheck(r.Context(), r.PathValue("exam_id"))
	if err != nil {
		respondAPIError(w, err)
		return
	}
	writeJSON(w, 202, job)
}
