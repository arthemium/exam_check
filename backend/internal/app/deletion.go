package app

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
)

func examPhotoKeys(ctx context.Context, tx *sql.Tx, column, id string) ([]string, error) {
	query := `SELECT p.storage_key FROM exam_pages p JOIN exams e ON e.id=p.exam_id WHERE e.owner_id=? AND ` + column + `=?
UNION ALL SELECT p.storage_key FROM exam_task_photos p JOIN exams e ON e.id=p.exam_id WHERE e.owner_id=? AND ` + column + `=?`
	rows, err := tx.QueryContext(ctx, query, ownerID, id, ownerID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Server) deleteExamRecords(ctx context.Context, id string, folder bool) ([]string, error) {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	table, column, label := "exams", "e.id", "Exam"
	if folder {
		table, column, label = "folders", "e.folder_id", "Folder"
	}
	var exists int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id=? AND owner_id=?", id, ownerID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		err = apiProblem{404, label + " not found"}
	}
	if err != nil {
		return nil, err
	}
	var active int
	err = tx.QueryRowContext(ctx, "SELECT count(*) FROM exams e WHERE e.owner_id=? AND "+column+"=? AND e.status IN ('queued','processing')", ownerID, id).Scan(&active)
	if err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, apiProblem{409, "Wait for the selected exams to finish checking before deleting"}
	}
	keys, err := examPhotoKeys(ctx, tx, column, id)
	if err != nil {
		return nil, err
	}
	deleteColumn := "id"
	if folder {
		deleteColumn = "folder_id"
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM exams WHERE owner_id=? AND "+deleteColumn+"=?", ownerID, id); err != nil {
		return nil, err
	}
	if folder {
		if _, err = tx.ExecContext(ctx, "DELETE FROM folders WHERE owner_id=? AND id=?", ownerID, id); err != nil {
			return nil, err
		}
	}
	return keys, tx.Commit()
}

func (s *Server) removeDeletedPhotos(keys []string) {
	for _, key := range keys {
		path, err := safePhotoPath(s.Config.UploadDir, key)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Print("Deleted exam photo could not be removed from disk")
		}
	}
}

func (s *Server) deleteExam(w http.ResponseWriter, r *http.Request) {
	keys, err := s.deleteExamRecords(r.Context(), r.PathValue("exam_id"), false)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	s.removeDeletedPhotos(keys)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) {
	keys, err := s.deleteExamRecords(r.Context(), r.PathValue("folder_id"), true)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	s.removeDeletedPhotos(keys)
	w.WriteHeader(http.StatusNoContent)
}
