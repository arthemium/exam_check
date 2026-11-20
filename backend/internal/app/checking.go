package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxProviderResponse = 2 * 1024 * 1024

type CheckingError struct {
	Message   string
	Retryable bool
}

func (err *CheckingError) Error() string { return err.Message }

type Checker interface {
	Check(context.Context, string) (json.RawMessage, error)
}

type openAIChecker struct {
	config   Config
	store    *Store
	client   *http.Client
	endpoint string
}

type examPhoto struct {
	Number int
	Key    string
	MIME   string
	Size   int64
	Path   string
}

func NewChecker(config Config, store *Store) Checker {
	return &openAIChecker{config: config, store: store, endpoint: "https://api.openai.com/v1/responses",
		client: &http.Client{Timeout: config.OpenAITimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}}
}

func (checker *openAIChecker) Check(ctx context.Context, examID string) (json.RawMessage, error) {
	if checker.config.OpenAIKey == "" {
		return nil, &CheckingError{"Exam checking is not configured.", false}
	}
	checks, photos, err := checker.loadInput(ctx, examID)
	if err != nil {
		return nil, err
	}
	criteria, err := checker.loadCriteria(ctx, examID, checks)
	if err != nil {
		return nil, err
	}
	reportCheckProgress(ctx, 10, "transcribing")
	source, task, err := checker.transcribeExam(ctx, examID, photos, criteria)
	if err != nil {
		return nil, err
	}
	paragraphs, err := canonicalize(source, photos)
	if err != nil {
		return nil, err
	}
	reportCheckProgress(ctx, 60, "assessing")
	feedback, err := checker.assessExam(ctx, criteria, task, paragraphs)
	if err != nil {
		return nil, err
	}
	reportCheckProgress(ctx, 90, "finalizing")
	return buildAssessmentResult(source.Model, paragraphs, feedback, criteria, task)
}

func (checker *openAIChecker) loadInput(ctx context.Context, examID string) ([]string, []examPhoto, error) {
	var rawChecks string
	err := checker.store.DB.QueryRowContext(ctx, "SELECT checks FROM exams WHERE id = ?", examID).Scan(&rawChecks)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, &CheckingError{"The exam no longer exists.", false}
	}
	if err != nil {
		return nil, nil, &CheckingError{"The exam could not be loaded. Try again shortly.", true}
	}
	var checks []string
	if err = json.Unmarshal([]byte(rawChecks), &checks); err != nil {
		return nil, nil, &CheckingError{"The saved checking criteria could not be read.", false}
	}
	photos, err := checker.loadPhotos(ctx, examID)
	return checks, photos, err
}

func (checker *openAIChecker) loadPhotos(ctx context.Context, examID string) ([]examPhoto, error) {
	rows, err := checker.store.DB.QueryContext(ctx,
		"SELECT page_number, storage_key, mime_type, file_size FROM exam_pages WHERE exam_id = ? ORDER BY page_number", examID)
	if err != nil {
		return nil, &CheckingError{"Exam photos could not be loaded. Try again shortly.", true}
	}
	defer rows.Close()
	photos, total := make([]examPhoto, 0), int64(0)
	for rows.Next() {
		var photo examPhoto
		if err = rows.Scan(&photo.Number, &photo.Key, &photo.MIME, &photo.Size); err != nil {
			return nil, err
		}
		if err = checker.preparePhoto(&photo); err != nil {
			return nil, err
		}
		total += photo.Size
		photos = append(photos, photo)
		if total > checker.config.MaxExamBytes || len(photos) > checker.config.MaxPagesPerExam {
			return nil, &CheckingError{"Exam photos exceed the processing limit.", false}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(photos) == 0 {
		return nil, &CheckingError{"The exam has no uploaded pages.", false}
	}
	return photos, nil
}

func (checker *openAIChecker) preparePhoto(photo *examPhoto) error {
	if photo.Key == "" || filepath.Base(photo.Key) != photo.Key || photo.Key == "." || photo.Key == ".." {
		return &CheckingError{"An exam photo is missing or inaccessible.", false}
	}
	if photo.MIME != "image/jpeg" && photo.MIME != "image/png" && photo.MIME != "image/webp" {
		return &CheckingError{"An exam photo has an unsupported format.", false}
	}
	root, err := filepath.EvalSymlinks(checker.config.UploadDir)
	if err != nil {
		return &CheckingError{"An exam photo is missing or inaccessible.", false}
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, photo.Key))
	if err != nil || filepath.Dir(path) != root {
		return &CheckingError{"An exam photo is missing or inaccessible.", false}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > checker.config.MaxPageBytes {
		return &CheckingError{"An exam photo is missing or exceeds the processing limit.", false}
	}
	photo.Path, photo.Size = path, info.Size()
	return nil
}

func (checker *openAIChecker) requestHeader(prompt modelPrompt, name string, schema map[string]any) map[string]any {
	return map[string]any{
		"model": prompt.Model, "instructions": prompt.Instructions, "max_output_tokens": 12000, "store": false,
		"text": map[string]any{"format": map[string]any{"type": "json_schema", "name": name, "strict": true, "schema": schema}},
	}
}

func (checker *openAIChecker) photoRequestWithSchema(photos []examPhoto, prompt modelPrompt, schema map[string]any) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		header, _ := json.Marshal(checker.requestHeader(prompt, "transcription", schema))
		_, err := writer.Write(header[:len(header)-1])
		if err == nil {
			_, err = io.WriteString(writer, `,"input":[{"role":"user","content":[`)
		}
		for index, photo := range photos {
			if err != nil {
				break
			}
			if index > 0 {
				_, err = io.WriteString(writer, ",")
			}
			if err == nil {
				err = writePhotoInput(writer, photo)
			}
		}
		if err == nil {
			_, err = io.WriteString(writer, `]}]}`)
		}
		_ = writer.CloseWithError(err)
	}()
	return reader
}

func writePhotoInput(writer io.Writer, photo examPhoto) error {
	label := map[string]string{"type": "input_text", "text": fmt.Sprintf("Exam page %d", photo.Number)}
	if photo.Number == 0 {
		label["text"] = "Separate task instructions photo: extract instructions only, never include this page in the student's answer."
	}
	if err := json.NewEncoder(writer).Encode(label); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, `,{"type":"input_image","detail":"high","image_url":"data:%s;base64,`, photo.MIME); err != nil {
		return err
	}
	file, err := os.Open(photo.Path)
	if err != nil {
		return &CheckingError{"An exam photo is missing or inaccessible.", false}
	}
	defer file.Close()
	encoded := base64.NewEncoder(base64.StdEncoding, writer)
	_, err = io.CopyN(encoded, file, photo.Size)
	if err != nil {
		return err
	}
	if err = encoded.Close(); err != nil {
		return err
	}
	_, err = io.WriteString(writer, `"}`)
	return err
}

func (checker *openAIChecker) request(ctx context.Context, body io.ReadCloser, schema map[string]any, destination any) (err error) {
	defer body.Close()
	call := providerCallLog{started: time.Now()}
	contextLogger(ctx).Info("openai_request_started")
	defer func() { call.finish(ctx, err) }()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, checker.endpoint, body)
	if err != nil {
		return &CheckingError{"Exam checking is not configured correctly.", false}
	}
	request.Header.Set("Authorization", "Bearer "+checker.config.OpenAIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := checker.client.Do(request)
	if err != nil {
		call.timedOut = errors.Is(err, context.DeadlineExceeded)
		return &CheckingError{"The checking provider could not be reached in time.", true}
	}
	defer response.Body.Close()
	call.status, call.requestID = response.StatusCode, response.Header.Get("x-request-id")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerStatusError(response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponse+1))
	if err != nil || len(data) > maxProviderResponse {
		call.timedOut = errors.Is(err, context.DeadlineExceeded)
		return invalidModelOutput()
	}
	output, err := responseText(data)
	if err != nil {
		return err
	}
	return decodeModelJSON([]byte(output), schema, destination)
}

func providerStatusError(status int) error {
	if status == http.StatusTooManyRequests {
		return &CheckingError{"The checking provider is busy or its usage allowance was reached.", true}
	}
	return &CheckingError{"The checking provider rejected the request. Check the server configuration.", status >= 500 || status == 408}
}

func responseText(data []byte) (string, error) {
	var response struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &response); err != nil || response.Status != "completed" {
		return "", &CheckingError{"The model did not return a complete result.", true}
	}
	var output strings.Builder
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "refusal" {
				return "", &CheckingError{"The model could not review this exam.", false}
			}
			if content.Type == "output_text" {
				output.WriteString(content.Text)
			}
		}
	}
	if output.Len() == 0 {
		return "", invalidModelOutput()
	}
	return output.String(), nil
}
