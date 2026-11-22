package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode"
)

type checkedTask struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}

type checkedWordLimit struct {
	Minimum *int   `json:"minimum"`
	Maximum *int   `json:"maximum"`
	Status  string `json:"status"`
}

type criterionAssessment struct {
	Criterion string `json:"criterion"`
	Feedback  string `json:"feedback"`
	Score     *int   `json:"score"`
}

type customAssessment struct {
	Criterion string `json:"criterion"`
	Feedback  string `json:"feedback"`
}

type assessmentAnalysis struct {
	Findings          []finding             `json:"findings"`
	Assessments       []criterionAssessment `json:"assessments"`
	CustomAssessments []customAssessment    `json:"custom_assessments"`
	Model             string                `json:"-"`
}

type taskTranscription struct {
	Paragraphs []transcriptParagraph `json:"paragraphs"`
	TaskText   string                `json:"task_text"`
}

type checkingProgressKey struct{}

func withCheckProgress(ctx context.Context, callback func(int, string)) context.Context {
	return context.WithValue(ctx, checkingProgressKey{}, callback)
}

func reportCheckProgress(ctx context.Context, progress int, stage string) {
	if callback, ok := ctx.Value(checkingProgressKey{}).(func(int, string)); ok {
		callback(progress, stage)
	}
}

func (checker *openAIChecker) loadCriteria(ctx context.Context, examID string, checks []string) (Criteria, error) {
	criteria := defaultCriteria(checks)
	var raw string
	if err := checker.store.DB.QueryRowContext(ctx, "SELECT criteria FROM exams WHERE id=?", examID).Scan(&raw); err != nil {
		return criteria, &CheckingError{"The saved checking criteria could not be loaded.", true}
	}
	if raw == "{}" || raw == "" {
		criteria.DetectTask = false // Existing exams retain their original transcription behavior.
	} else if err := json.Unmarshal([]byte(raw), &criteria); err != nil {
		return criteria, &CheckingError{"The saved checking criteria could not be read.", false}
	}
	criteria.Checks = checks
	criteria, err := normalizeCriteria(criteria)
	if err != nil {
		return criteria, &CheckingError{"The saved checking criteria are invalid.", false}
	}
	if len(checks) == 0 && criteria.Assessment != "b2_first_schools" && strings.TrimSpace(criteria.CustomCriteria) == "" && criteria.MinWords == nil && criteria.MaxWords == nil {
		return criteria, &CheckingError{"Select at least one checking criterion.", false}
	}
	return criteria, nil
}

func taskTranscriptionSchema() map[string]any {
	schema := transcriptionSchema()
	schema["properties"].(map[string]any)["task_text"] = map[string]any{"type": "string"}
	schema["required"] = []string{"paragraphs", "task_text"}
	return schema
}

func (checker *openAIChecker) loadTaskPhoto(ctx context.Context, examID, url string) (examPhoto, error) {
	photo := examPhoto{Number: 0}
	prefix := "/api/exams/" + examID + "/task-photos/"
	id := strings.TrimPrefix(url, prefix)
	if !strings.HasPrefix(url, prefix) || id == "" || strings.ContainsAny(id, "/?#%\\") {
		return photo, &CheckingError{"The task photo reference is invalid.", false}
	}
	err := checker.store.DB.QueryRowContext(ctx, "SELECT storage_key,mime_type,file_size FROM exam_task_photos WHERE id=? AND exam_id=?", id, examID).
		Scan(&photo.Key, &photo.MIME, &photo.Size)
	if err != nil {
		return photo, &CheckingError{"The task photo is missing or inaccessible.", false}
	}
	return photo, checker.preparePhoto(&photo)
}

func (checker *openAIChecker) transcribeExam(ctx context.Context, examID string, photos []examPhoto, criteria Criteria) (transcription, checkedTask, error) {
	task := checkedTask{Text: strings.TrimSpace(criteria.TaskText), Source: criteria.TaskSource}
	if task.Text != "" {
		if task.Source == "" {
			task.Source = "manual"
		}
	} else {
		task.Source = ""
		if criteria.DetectTask || criteria.TaskPhoto != "" {
			return checker.transcribeWithTask(ctx, examID, photos, criteria)
		}
	}
	prompt, err := checker.transcriptionPrompt(ctx, "")
	if err != nil {
		return transcription{}, checkedTask{}, err
	}
	var source transcription
	err = checker.request(providerContext(ctx, prompt, "transcription"), checker.photoRequestWithSchema(photos, prompt, transcriptionSchema()), transcriptionSchema(), &source)
	source.Model = prompt.Model
	return source, task, err
}

func (checker *openAIChecker) transcriptionPrompt(ctx context.Context, taskSource string) (modelPrompt, error) {
	saved, err := checker.store.loadPrompt(ctx, "transcriber")
	if err != nil {
		return modelPrompt{}, err
	}
	instructions, err := promptBuilder(saved.Templates).transcription(taskSource)
	return modelPrompt{Instructions: instructions, Model: saved.Model}, err
}

func (checker *openAIChecker) transcribeWithTask(ctx context.Context, examID string, photos []examPhoto, criteria Criteria) (transcription, checkedTask, error) {
	inputs := append([]examPhoto{}, photos...)
	task := checkedTask{Source: "essay"}
	if criteria.TaskPhoto != "" {
		photo, err := checker.loadTaskPhoto(ctx, examID, criteria.TaskPhoto)
		if err != nil {
			return transcription{}, checkedTask{}, err
		}
		inputs = append(inputs, photo)
		task.Source = "photo"
	}
	prompt, err := checker.transcriptionPrompt(ctx, task.Source)
	if err != nil {
		return transcription{}, checkedTask{}, err
	}
	var source taskTranscription
	schema := taskTranscriptionSchema()
	err = checker.request(providerContext(ctx, prompt, "transcription"), checker.photoRequestWithSchema(inputs, prompt, schema), schema, &source)
	if err != nil {
		return transcription{}, checkedTask{}, err
	}
	task.Text = strings.TrimSpace(source.TaskText)
	if len([]rune(task.Text)) > 4000 {
		return transcription{}, checkedTask{}, invalidModelOutput()
	}
	if task.Text == "" {
		task.Source = ""
	}
	return transcription{Paragraphs: source.Paragraphs, Model: prompt.Model}, task, nil
}

// The four subscales and 0–5 range follow the supplied B2 First for Schools rubric.
var b2Criteria = []string{"Content", "Communicative Achievement", "Organisation", "Language"}

func assessmentOutputSchema() map[string]any {
	schema := analysisSchema()
	properties := schema["properties"].(map[string]any)
	properties["assessments"] = map[string]any{"type": "array", "maxItems": 4, "items": objectSchema(map[string]any{
		"criterion": enumSchema(b2Criteria...), "feedback": map[string]any{"type": "string"},
		"score": map[string]any{"type": []string{"integer", "null"}, "minimum": 0, "maximum": 5},
	})}
	properties["custom_assessments"] = map[string]any{"type": "array", "maxItems": 100, "items": objectSchema(map[string]any{
		"criterion": map[string]any{"type": "string"}, "feedback": map[string]any{"type": "string"},
	})}
	schema["required"] = []string{"findings", "assessments", "custom_assessments"}
	return schema
}

func customCriteriaLines(value string) []string {
	lines, seen := []string{}, map[string]bool{}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			lines = append(lines, line)
			seen[line] = true
		}
	}
	return lines
}

func needsAssessment(criteria Criteria) bool {
	return criteria.Assessment == "b2_first_schools" || len(customCriteriaLines(criteria.CustomCriteria)) > 0 || len(criteria.Checks) > 0
}

func (checker *openAIChecker) assessExam(ctx context.Context, criteria Criteria, task checkedTask, paragraphs []checkedParagraph) (assessmentAnalysis, error) {
	if !needsAssessment(criteria) {
		return assessmentAnalysis{Findings: []finding{}, Assessments: []criterionAssessment{}, CustomAssessments: []customAssessment{}}, nil
	}
	saved, err := checker.store.loadPrompt(ctx, "checker")
	if err != nil {
		return assessmentAnalysis{}, err
	}
	if criteria.Assessment == "b2_first_schools" {
		rubric, err := loadB2RubricPrompt(ctx, checker.store.DB)
		if err != nil {
			return assessmentAnalysis{}, err
		}
		saved.Templates["rubric.b2_first_schools"] = rubric
	}
	prompt, err := promptBuilder(saved.Templates).checking(criteria, task, paragraphs)
	if err != nil {
		return assessmentAnalysis{}, err
	}
	prompt.Model = saved.Model
	if criteria.Assessment != "b2_first_schools" && len(customCriteriaLines(criteria.CustomCriteria)) == 0 {
		var feedback analysis
		err := checker.requestAnalysis(ctx, prompt.modelPrompt, analysisSchema(), prompt.Content, &feedback)
		return assessmentAnalysis{Findings: feedback.Findings, Assessments: []criterionAssessment{}, CustomAssessments: []customAssessment{}, Model: prompt.Model}, err
	}
	var feedback assessmentAnalysis
	err = checker.requestAnalysis(ctx, prompt.modelPrompt, assessmentOutputSchema(), prompt.Content, &feedback)
	feedback.Model = prompt.Model
	if err == nil {
		err = validateAssessmentFeedback(feedback, criteria)
	}
	return feedback, err
}

func (checker *openAIChecker) requestAnalysis(ctx context.Context, prompt modelPrompt, schema map[string]any, content any, destination any) error {
	encoded, err := json.Marshal(content)
	if err != nil {
		return err
	}
	header := checker.requestHeader(prompt, "analysis", schema)
	header["input"] = []any{map[string]any{"role": "user", "content": string(encoded)}}
	body, err := json.Marshal(header)
	if err != nil {
		return err
	}
	return checker.request(providerContext(ctx, prompt, "assessment"), io.NopCloser(bytes.NewReader(body)), schema, destination)
}

func validateAssessmentFeedback(feedback assessmentAnalysis, criteria Criteria) error {
	expected := []string{}
	if criteria.Assessment == "b2_first_schools" {
		expected = b2Criteria
	}
	if len(feedback.Assessments) != len(expected) {
		return invalidModelOutput()
	}
	for index, item := range feedback.Assessments {
		if item.Criterion != expected[index] || strings.TrimSpace(item.Feedback) == "" || (item.Score != nil && (*item.Score < 0 || *item.Score > 5)) {
			return invalidModelOutput()
		}
	}
	custom := customCriteriaLines(criteria.CustomCriteria)
	if len(feedback.CustomAssessments) != len(custom) {
		return invalidModelOutput()
	}
	for index, item := range feedback.CustomAssessments {
		if item.Criterion != custom[index] || strings.TrimSpace(item.Feedback) == "" {
			return invalidModelOutput()
		}
	}
	return nil
}

func answerWordCount(paragraphs []checkedParagraph) int {
	var answer strings.Builder
	for _, paragraph := range paragraphs {
		if paragraph.IsTitle {
			continue
		}
		for _, segment := range paragraph.Segments {
			if segment.Certainty == "unreadable" {
				answer.WriteString(" ")
			} else {
				answer.WriteString(segment.Text)
			}
		}
		answer.WriteString(" ")
	}
	count := 0
	for _, word := range strings.Fields(answer.String()) {
		if strings.ContainsFunc(word, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			count++
		}
	}
	return count
}

func wordLimitResult(count int, criteria Criteria) checkedWordLimit {
	limit := checkedWordLimit{Minimum: criteria.MinWords, Maximum: criteria.MaxWords, Status: "within"}
	switch {
	case criteria.MinWords == nil && criteria.MaxWords == nil:
		limit.Status = "not_set"
	case criteria.MinWords != nil && count < *criteria.MinWords:
		limit.Status = "below"
	case criteria.MaxWords != nil && count > *criteria.MaxWords:
		limit.Status = "above"
	}
	return limit
}

func buildAssessmentResult(transcriptionModel string, paragraphs []checkedParagraph, feedback assessmentAnalysis, criteria Criteria, task checkedTask) (json.RawMessage, error) {
	model := feedback.Model
	if model == "" {
		model = transcriptionModel
	}
	checks := append([]string{}, criteria.Checks...)
	if task.Text == "" {
		checks = filterTaskCompletion(checks)
	}
	data, err := buildCheckResult(model, paragraphs, analysis{Findings: feedback.Findings}, checks)
	if err != nil {
		return nil, err
	}
	var result checkResult
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	result.SchemaVersion, result.Task, result.Checks = 2, task, criteria.Checks
	result.Assessment = criteria.Assessment
	result.TranscriptionModel = transcriptionModel
	result.WordCount = answerWordCount(paragraphs)
	result.WordLimit = wordLimitResult(result.WordCount, criteria)
	result.Assessments, result.CustomAssessments = feedback.Assessments, feedback.CustomAssessments
	if result.Assessments == nil {
		result.Assessments = []criterionAssessment{}
	}
	if result.CustomAssessments == nil {
		result.CustomAssessments = []customAssessment{}
	}
	for index, item := range result.Assessments {
		if result.NeedsTranscriptionReview || (task.Text == "" && (item.Criterion == "Content" || item.Criterion == "Communicative Achievement")) {
			result.Assessments[index].Score = nil
		}
	}
	if len(checks) != len(criteria.Checks) || (criteria.Assessment == "b2_first_schools" && task.Text == "") {
		result.ValidationWarnings = append(result.ValidationWarnings, "Task instructions are missing; task completion and task-dependent scores cannot be assessed.")
	}
	if result.NeedsTranscriptionReview {
		result.ValidationWarnings = append(result.ValidationWarnings, "Review uncertain handwriting before relying on word count or assessment feedback. Numerical assessment scores are withheld.")
	}
	return json.Marshal(result)
}

func filterTaskCompletion(checks []string) []string {
	result := []string{}
	for _, check := range checks {
		if check != "taskCompletion" {
			result = append(result, check)
		}
	}
	return result
}
