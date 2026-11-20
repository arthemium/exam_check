package app

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-sqlite3"
	"golang.org/x/text/cases"
)

func pagination(r *http.Request) (int, int, error) {
	limit, offset := 50, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, apiProblem{422, "Limit must be between 1 and 100"}
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 {
			return 0, 0, apiProblem{422, "Offset must be non-negative"}
		}
	}
	return limit, offset, nil
}

func validName(value string, allowEmpty bool) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && length <= 120 && (allowEmpty || length > 0)
}

func uniqueConflict(err error) bool {
	var sqliteError sqlite3.Error
	return errors.As(err, &sqliteError) && sqliteError.ExtendedCode == sqlite3.ErrConstraintUnique
}

func (s *Server) folderExists(r *http.Request, id string) error {
	var found int
	err := s.Store.DB.QueryRowContext(r.Context(), "SELECT 1 FROM folders WHERE id=? AND owner_id=?", id, ownerID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return apiProblem{404, "Folder not found"}
	}
	return err
}

func (s *Server) listFolders(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT f.id,f.name,f.created_at,count(e.id)
		FROM folders f LEFT JOIN exams e ON e.folder_id=f.id AND e.owner_id=f.owner_id
		WHERE f.owner_id=? GROUP BY f.id ORDER BY f.created_at,f.id LIMIT ? OFFSET ?`, ownerID, limit, offset)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	defer rows.Close()
	folders := []Folder{}
	for rows.Next() {
		var folder Folder
		var created int64
		if err := rows.Scan(&folder.ID, &folder.Name, &created, &folder.ExamCount); err != nil {
			respondAPIError(w, err)
			return
		}
		folder.CreatedAt = timeString(created)
		folders = append(folders, folder)
	}
	if err := rows.Err(); err != nil {
		respondAPIError(w, err)
		return
	}
	writeJSON(w, 200, folders)
}

func folderName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return "", false
	}
	name := strings.TrimSpace(body.Name)
	if !validName(name, false) {
		writeError(w, 422, "Provide a folder name of 1–120 characters")
		return "", false
	}
	return name, true
}

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) {
	name, valid := folderName(w, r)
	if !valid {
		return
	}
	created := utcMillis()
	folder := Folder{ID: NewID(), Name: name, CreatedAt: timeString(created)}
	_, err := s.Store.DB.ExecContext(r.Context(), "INSERT INTO folders(id,owner_id,name,name_key,created_at) VALUES(?,?,?,?,?)",
		folder.ID, ownerID, name, cases.Fold().String(name), created)
	if uniqueConflict(err) {
		err = apiProblem{409, "A folder with this name already exists"}
	}
	if err != nil {
		respondAPIError(w, err)
		return
	}
	writeJSON(w, 201, folder)
}

func (s *Server) renameFolder(w http.ResponseWriter, r *http.Request) {
	name, valid := folderName(w, r)
	if !valid {
		return
	}
	id := r.PathValue("folder_id")
	result, err := s.Store.DB.ExecContext(r.Context(), "UPDATE folders SET name=?,name_key=? WHERE id=? AND owner_id=?",
		name, cases.Fold().String(name), id, ownerID)
	if uniqueConflict(err) {
		err = apiProblem{409, "A folder with this name already exists"}
	}
	if err != nil {
		respondAPIError(w, err)
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		writeError(w, 404, "Folder not found")
		return
	}
	folder := Folder{ID: id, Name: name}
	var created int64
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT f.created_at,count(e.id) FROM folders f
		LEFT JOIN exams e ON e.folder_id=f.id AND e.owner_id=f.owner_id WHERE f.id=? AND f.owner_id=? GROUP BY f.id`,
		id, ownerID).Scan(&created, &folder.ExamCount)
	if err != nil {
		respondAPIError(w, err)
		return
	}
	folder.CreatedAt = timeString(created)
	writeJSON(w, 200, folder)
}
