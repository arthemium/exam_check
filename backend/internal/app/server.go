package app

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type Server struct {
	Config   Config
	Store    *Store
	uploads  chan struct{}
	throttle loginThrottle
}

func NewServer(cfg Config, store *Store) *Server {
	return &Server{Config: cfg, Store: store, uploads: make(chan struct{}, 1)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/login", s.login)
	mux.HandleFunc("POST /auth/logout", s.logout)
	mux.HandleFunc("GET /auth/me", s.profile)
	mux.HandleFunc("GET /api/folders", s.listFolders)
	mux.HandleFunc("POST /api/folders", s.createFolder)
	mux.HandleFunc("PATCH /api/folders/{folder_id}", s.renameFolder)
	mux.HandleFunc("DELETE /api/folders/{folder_id}", s.deleteFolder)
	mux.HandleFunc("GET /api/folders/{folder_id}/exams", s.listExams)
	mux.HandleFunc("POST /api/folders/{folder_id}/exams", s.uploadExam)
	mux.HandleFunc("GET /api/exams/{exam_id}", s.getExam)
	mux.HandleFunc("PATCH /api/exams/{exam_id}", s.updateExam)
	mux.HandleFunc("DELETE /api/exams/{exam_id}", s.deleteExam)
	mux.HandleFunc("POST /api/exams/{exam_id}/task-photos", s.uploadTaskPhoto)
	mux.HandleFunc("GET /api/exams/{exam_id}/task-photos/{photo_id}", s.getTaskPhoto)
	mux.HandleFunc("GET /api/exams/{exam_id}/pages/{page_id}", s.getPage)
	mux.HandleFunc("POST /api/exams/{exam_id}/checks", s.queueCheck)
	mux.HandleFunc("POST /api/folders/{folder_id}/checks", s.queueBatch)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /{$}", s.index)
	if info, err := os.Stat(filepath.Join(s.Config.StaticDir, "assets")); err == nil && info.IsDir() {
		assets := http.FileServer(http.Dir(filepath.Join(s.Config.StaticDir, "assets")))
		mux.Handle("GET /assets/", http.StripPrefix("/assets/", assets))
	}
	return requestLogging(s.guard(mux), mux)
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				contextLogger(r.Context()).Error("HTTP request panicked")
				writeError(w, 500, "Request failed")
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		if !s.allowRequestOrigin(w, r) {
			return
		}
		limit := int64(65536)
		if strings.HasPrefix(r.URL.Path, "/api/folders/") && strings.HasSuffix(r.URL.Path, "/checks") {
			limit = 2 * 1024 * 1024
		}
		if isPhotoUploadRequest(r) {
			limit = s.Config.MaxRequestBytes
		}
		if r.ContentLength > limit {
			writeError(w, 413, "Request exceeds the size limit")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/auth/me" {
			if !s.authorize(w, r) {
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isPhotoUploadRequest(r *http.Request) bool {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" {
		return false
	}
	return (parts[1] == "folders" && parts[3] == "exams") || (parts[1] == "exams" && parts[3] == "task-photos")
}

func (s *Server) allowRequestOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	allowed := s.Config.allowsOrigin(origin)
	w.Header().Add("Vary", "Origin")
	if allowed {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		if !allowed {
			writeError(w, 403, "Origin not allowed")
			return false
		}
		method := r.Header.Get("Access-Control-Request-Method")
		if method != "GET" && method != "POST" && method != "PATCH" && method != "DELETE" {
			writeError(w, 403, "Method not allowed")
			return false
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !allowed {
		writeError(w, 403, "Origin not allowed")
		return false
	}
	return true
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	var version int
	if err := s.Store.DB.QueryRowContext(r.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		writeError(w, 503, "Database is not ready")
		return
	}
	var users, jobs int
	if err := s.Store.DB.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM users), EXISTS(SELECT 1 FROM check_jobs)").Scan(&users, &jobs); err != nil {
		writeError(w, 503, "Database is not ready")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.Config.StaticDir, "index.html")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	writeJSON(w, 200, map[string]string{"service": "Examcheck API", "login": "/auth/login", "api": "/api/folders"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Print("response could not be written")
	}
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); trailing != io.EOF {
			err = trailing
			if err == nil {
				err = errors.New("multiple JSON values")
			}
		}
	}
	if err == nil {
		return true
	}
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		writeError(w, 413, "Request exceeds the size limit")
	} else {
		writeError(w, 422, "Provide valid request fields")
	}
	return false
}
