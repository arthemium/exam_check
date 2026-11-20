package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

const ownerID = "local-owner"

type Folder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ExamCount int    `json:"exam_count"`
	CreatedAt string `json:"created_at"`
}

type Exam struct {
	ID          string   `json:"id"`
	FolderID    string   `json:"folder_id"`
	Title       string   `json:"title"`
	StudentName string   `json:"student_name"`
	Checks      []string `json:"checks"`
	Status      string   `json:"status"`
	CreatedAt   string   `json:"created_at"`
	Criteria    Criteria `json:"criteria"`
	BatchID     string   `json:"batch_id,omitempty"`
	Progress    int      `json:"progress"`
	Stage       string   `json:"stage"`
}

type Page struct {
	ID         string `json:"id"`
	PageNumber int    `json:"page_number"`
	URL        string `json:"url"`
}

type ExamDetail struct {
	Exam
	Pages  []Page          `json:"pages"`
	Result json.RawMessage `json:"result"`
	Error  *string         `json:"error"`
}

type Job struct {
	ID     string `json:"id"`
	ExamID string `json:"exam_id"`
	Status string `json:"status"`
}

func NewID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("secure randomness unavailable")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func utcMillis() int64 { return time.Now().UTC().UnixMilli() }

func timeString(milliseconds int64) string {
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339Nano)
}
