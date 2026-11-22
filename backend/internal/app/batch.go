package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

type batchRequest struct {
	ExamIDs        []string                   `json:"exam_ids"`
	Criteria       json.RawMessage            `json:"criteria"`
	CriteriaByExam map[string]json.RawMessage `json:"criteria_by_exam"`
	RequestID      string                     `json:"request_id"`
}

type batchResponse struct {
	BatchID string   `json:"batch_id"`
	ExamIDs []string `json:"exam_ids"`
}

func parseCriteriaJSON(raw json.RawMessage) (Criteria, error) {
	criteria := defaultCriteria(nil)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || decoder.Decode(&criteria) != nil {
		return criteria, apiProblem{422, "Provide valid assessment criteria"}
	}
	if decoder.Decode(new(any)) != io.EOF {
		return criteria, apiProblem{422, "Provide valid assessment criteria"}
	}
	return normalizeCriteria(criteria)
}

func hasCheckingCriteria(criteria Criteria) bool {
	return len(criteria.Checks) > 0 || criteria.Assessment == "b2_first_schools" || strings.TrimSpace(criteria.CustomCriteria) != "" || criteria.MinWords != nil || criteria.MaxWords != nil
}

func saveExamCriteria(ctx context.Context, tx *sql.Tx, id string, criteria Criteria, reset bool) error {
	checks, _ := json.Marshal(criteria.Checks)
	encoded, err := json.Marshal(criteria)
	if err != nil {
		return err
	}
	query := "UPDATE exams SET checks=?,criteria=?"
	if reset {
		query += ",status='uploaded',stage='uploaded',progress=0,batch_id='',result=NULL,error=NULL"
	}
	_, err = tx.ExecContext(ctx, query+" WHERE id=? AND owner_id=?", string(checks), string(encoded), id, ownerID)
	return err
}

func selectedExamIDs(ids []string) ([]string, error) {
	if len(ids) < 1 || len(ids) > 50 {
		return nil, apiProblem{422, "Select between 1 and 50 exams"}
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 120 || seen[id] {
			return nil, apiProblem{422, "Select distinct existing exams"}
		}
		unique = append(unique, id)
		seen[id] = true
	}
	return unique, nil
}

func existingBatch(ctx context.Context, tx *sql.Tx, folderID, key string, ids []string) (*batchResponse, error) {
	var batch batchResponse
	var storedFolder, encoded string
	err := tx.QueryRowContext(ctx, "SELECT id,folder_id,exam_ids FROM check_batches WHERE request_key=? AND owner_id=?", key, ownerID).Scan(&batch.BatchID, &storedFolder, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(encoded), &batch.ExamIDs); err != nil {
		return nil, err
	}
	if folderID != storedFolder || !slices.Equal(ids, batch.ExamIDs) {
		return nil, apiProblem{409, "This request ID was already used for different exams"}
	}
	return &batch, nil
}

func requestedCriteria(request batchRequest, exam Exam, preserveTask bool) (Criteria, error) {
	raw := request.Criteria
	if request.CriteriaByExam != nil {
		raw = request.CriteriaByExam[exam.ID]
	}
	criteria := exam.Criteria
	var err error
	if len(raw) > 0 {
		criteria, err = parseCriteriaJSON(raw)
		if err != nil {
			return criteria, err
		}
	}
	if preserveTask && criteria.TaskText == "" && criteria.TaskPhoto == "" {
		criteria.TaskText, criteria.TaskSource, criteria.TaskPhoto = exam.Criteria.TaskText, exam.Criteria.TaskSource, exam.Criteria.TaskPhoto
	}
	if !hasCheckingCriteria(criteria) {
		return criteria, apiProblem{422, "Select at least one checking criterion"}
	}
	return criteria, nil
}

func (s *Server) validateBatchLimits(ctx context.Context, tx *sql.Tx, count int, single bool) error {
	if single {
		return s.checkJobLimits(ctx, tx)
	}
	var daily int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM check_jobs j JOIN exams e ON e.id=j.exam_id
		WHERE e.owner_id=? AND j.created_at>=?`, ownerID, utcMillis()-int64(24*time.Hour/time.Millisecond)).Scan(&daily)
	if err != nil {
		return err
	}
	if daily+count > s.Config.MaxJobsPerUserPerDay {
		return apiProblem{429, "The selected batch exceeds the daily checking limit"}
	}
	return nil
}

func batchCriteriaKeys(request batchRequest) error {
	if request.CriteriaByExam == nil {
		return nil
	}
	if len(request.CriteriaByExam) != len(request.ExamIDs) {
		return apiProblem{422, "Provide individual criteria for every selected exam"}
	}
	for _, id := range request.ExamIDs {
		if len(request.CriteriaByExam[id]) == 0 {
			return apiProblem{422, "Individual criteria must match the selected exams"}
		}
	}
	return nil
}

func (s *Server) prepareBatch(ctx context.Context, tx *sql.Tx, folderID string, request batchRequest) ([]Exam, error) {
	if err := batchCriteriaKeys(request); err != nil {
		return nil, err
	}
	exams := make([]Exam, 0, len(request.ExamIDs))
	for _, id := range request.ExamIDs {
		exam, err := loadExam(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if exam.FolderID != folderID {
			return nil, apiProblem{422, "Select exams from this folder only"}
		}
		if exam.Status == "queued" || exam.Status == "processing" {
			return nil, apiProblem{409, "Some selected exams are already being checked"}
		}
		criteria, err := requestedCriteria(request, exam, len(request.ExamIDs) > 1)
		if err != nil {
			return nil, err
		}
		if err := validateTaskPhoto(ctx, tx, id, criteria); err != nil {
			return nil, err
		}
		if err := s.validateCheckingSetup(ctx, tx, criteria); err != nil {
			return nil, err
		}
		exam.Criteria = criteria
		exams = append(exams, exam)
	}
	return exams, nil
}

func insertBatchJob(ctx context.Context, tx *sql.Tx, exam Exam, batchID string, now int64) error {
	if err := saveExamCriteria(ctx, tx, exam.ID, exam.Criteria, false); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO check_jobs(id,exam_id,status,attempts,available_at,created_at) VALUES(?,?,'queued',0,?,?)`, NewID(), exam.ID, now, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE exams SET status='queued',stage='queued',progress=0,batch_id=?,error=NULL,result=NULL WHERE id=? AND owner_id=?`, batchID, exam.ID, ownerID)
	return err
}

func (s *Server) enqueueBatch(ctx context.Context, folderID string, request batchRequest, single bool) (batchResponse, error) {
	ids, err := selectedExamIDs(request.ExamIDs)
	if err != nil {
		return batchResponse{}, err
	}
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 120 {
		return batchResponse{}, apiProblem{422, "Provide a request ID of 1–120 characters"}
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return batchResponse{}, err
	}
	defer tx.Rollback()
	if previous, err := existingBatch(ctx, tx, folderID, request.RequestID, ids); err != nil || previous != nil {
		if previous != nil {
			return *previous, nil
		}
		return batchResponse{}, err
	}
	if err := s.validateBatchLimits(ctx, tx, len(ids), single); err != nil {
		return batchResponse{}, err
	}
	exams, err := s.prepareBatch(ctx, tx, folderID, request)
	if err != nil {
		return batchResponse{}, err
	}
	batch := batchResponse{BatchID: NewID(), ExamIDs: ids}
	encoded, _ := json.Marshal(ids)
	now := utcMillis()
	_, err = tx.ExecContext(ctx, `INSERT INTO check_batches(id,owner_id,folder_id,request_key,exam_ids,created_at) VALUES(?,?,?,?,?,?)`, batch.BatchID, ownerID, folderID, request.RequestID, string(encoded), now)
	if err != nil {
		return batchResponse{}, err
	}
	for _, exam := range exams {
		if err := insertBatchJob(ctx, tx, exam, batch.BatchID, now); err != nil {
			return batchResponse{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return batchResponse{}, err
	}
	contextLogger(ctx).Info("check_batch_queued", "batch_id", batch.BatchID, "exam_count", len(ids), "exam_ids", ids)
	return batch, nil
}

func (s *Server) queueBatch(w http.ResponseWriter, r *http.Request) {
	var request batchRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	batch, err := s.enqueueBatch(r.Context(), r.PathValue("folder_id"), request, false)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, batch)
}

func (s *Server) queueExamWithCriteria(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Criteria  json.RawMessage `json:"criteria"`
		RequestID string          `json:"request_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	exam, err := loadExam(r.Context(), s.Store.DB, r.PathValue("exam_id"))
	if err != nil {
		respondAPIError(w, err)
		return
	}
	if body.RequestID == "" {
		body.RequestID = NewID()
	}
	request := batchRequest{ExamIDs: []string{exam.ID}, Criteria: body.Criteria, RequestID: body.RequestID}
	batch, err := s.enqueueBatch(r.Context(), exam.FolderID, request, true)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, batch)
}
