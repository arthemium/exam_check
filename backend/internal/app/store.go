package app

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct{ DB *sql.DB }

const schemaVersion = 7

func OpenStore(path, initialModel string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(absolute, 0600); err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: absolute}
	dsn := uri.String() + "?_journal_mode=WAL&_synchronous=FULL&_foreign_keys=on&_busy_timeout=5000&_txlock=immediate&_cache_size=-2048"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{DB: db}
	if err := store.initialize(initialModel); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) initialize(initialModel string) error {
	var version string
	if err := store.DB.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		return err
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return err
	}
	if major < 3 || (major == 3 && (minor < 51 || (minor == 51 && patch < 3))) {
		return fmt.Errorf("SQLite 3.51.3 or newer is required")
	}
	var currentVersion int
	if err := store.DB.QueryRow("PRAGMA user_version").Scan(&currentVersion); err != nil {
		return err
	}
	if currentVersion > schemaVersion {
		return fmt.Errorf("database schema is newer than this application")
	}
	if currentVersion == schemaVersion {
		return nil
	}
	tx, err := store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Another process may have finished the migration while we waited for the write lock.
	if err := tx.QueryRow("PRAGMA user_version").Scan(&currentVersion); err != nil {
		return err
	}
	if currentVersion == schemaVersion {
		return tx.Commit()
	}
	if currentVersion > schemaVersion {
		return fmt.Errorf("database schema is newer than this application")
	}
	if currentVersion == 0 {
		if _, err = tx.Exec(initialSchema); err != nil {
			return err
		}
	}
	if currentVersion < 2 {
		if _, err = tx.Exec(assessmentSchema); err != nil {
			return err
		}
	}
	if currentVersion < 3 {
		if err = migratePromptTemplates(tx); err != nil {
			return err
		}
	}
	if currentVersion < 4 {
		if err = splitPromptTemplates(tx, initialModel); err != nil {
			return err
		}
	}
	if currentVersion < 5 {
		if err = migrateAssessmentSteps(tx); err != nil {
			return err
		}
	}
	if currentVersion < 6 {
		if err = migrateB2RubricAndCheckerEvidence(tx); err != nil {
			return err
		}
	}
	if currentVersion < 7 {
		if err = replaceC1WithB2(tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *Store) Close() error { return store.DB.Close() }

const initialSchema = `
CREATE TABLE IF NOT EXISTS users (
 id TEXT PRIMARY KEY, google_sub TEXT UNIQUE NOT NULL, email TEXT UNIQUE NOT NULL,
 display_name TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS login_sessions (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_sessions_expires ON login_sessions(expires_at);
CREATE TABLE IF NOT EXISTS folders (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), name TEXT NOT NULL,
 name_key TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(owner_id,name_key)
);
CREATE TABLE IF NOT EXISTS exams (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id),
 folder_id TEXT NOT NULL REFERENCES folders(id), title TEXT NOT NULL, student_name TEXT NOT NULL DEFAULT '',
 checks TEXT NOT NULL DEFAULT '[]', status TEXT NOT NULL DEFAULT 'uploaded', result TEXT,
 error TEXT, created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_exams_folder ON exams(folder_id,created_at,id);
CREATE TABLE IF NOT EXISTS exam_pages (
 id TEXT PRIMARY KEY, exam_id TEXT NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
 storage_key TEXT UNIQUE NOT NULL, mime_type TEXT NOT NULL, page_number INTEGER NOT NULL,
 file_size INTEGER NOT NULL, UNIQUE(exam_id,page_number)
);
CREATE TABLE IF NOT EXISTS check_jobs (
 id TEXT PRIMARY KEY, exam_id TEXT NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
 status TEXT NOT NULL DEFAULT 'queued', attempts INTEGER NOT NULL DEFAULT 0,
 available_at INTEGER NOT NULL, locked_at INTEGER, lease_token TEXT, last_error TEXT,
 created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_jobs_claim ON check_jobs(status,available_at,created_at);
CREATE INDEX IF NOT EXISTS ix_jobs_created ON check_jobs(created_at);
CREATE UNIQUE INDEX IF NOT EXISTS ix_jobs_one_active ON check_jobs(exam_id) WHERE status IN ('queued','processing');
PRAGMA user_version=1;
`

const assessmentSchema = `
ALTER TABLE exams ADD COLUMN criteria TEXT NOT NULL DEFAULT '{}';
ALTER TABLE exams ADD COLUMN batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE exams ADD COLUMN progress INTEGER NOT NULL DEFAULT 0;
ALTER TABLE exams ADD COLUMN stage TEXT NOT NULL DEFAULT 'uploaded';
UPDATE exams SET stage=status, progress=CASE WHEN status='completed' THEN 100 WHEN status='processing' THEN 10 ELSE 0 END;
CREATE TABLE exam_task_photos (
 id TEXT PRIMARY KEY, exam_id TEXT NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
 storage_key TEXT UNIQUE NOT NULL, mime_type TEXT NOT NULL, file_size INTEGER NOT NULL
);
CREATE INDEX ix_task_photos_exam ON exam_task_photos(exam_id);
CREATE TABLE check_batches (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id),
 folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
 request_key TEXT NOT NULL UNIQUE, exam_ids TEXT NOT NULL, created_at INTEGER NOT NULL
);
PRAGMA user_version=2;
`
