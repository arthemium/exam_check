package app

import (
	"fmt"
	"strings"
)

// promptBuilder composes trusted templates without interpolating exam content.
type promptBuilder map[string]string

type checkingPromptContent struct {
	Task           string             `json:"task"`
	SelectedChecks []string           `json:"selected_checks"`
	Assessment     string             `json:"assessment"`
	CustomCriteria []string           `json:"custom_criteria,omitempty"`
	WordCount      int                `json:"word_count"`
	WordLimit      *checkedWordLimit  `json:"word_limit,omitempty"`
	Paragraphs     []checkedParagraph `json:"paragraphs"`
}

type modelPrompt struct {
	Instructions string
	Model        string
}

type builtCheckingPrompt struct {
	modelPrompt
	Content checkingPromptContent
}

func (builder promptBuilder) compose(keys []string) (string, error) {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		body := builder[key]
		if strings.TrimSpace(body) == "" {
			return "", &CheckingError{Message: fmt.Sprintf("Prompt template %q is missing or empty.", key)}
		}
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n"), nil
}

func (builder promptBuilder) transcription(taskSource string) (string, error) {
	keys := []string{"transcriber"}
	switch taskSource {
	case "":
	case "essay", "photo":
		keys = append(keys, "transcription.task."+taskSource)
	default:
		return "", &CheckingError{Message: "Unknown task transcription source."}
	}
	return builder.compose(keys)
}

func checkingTemplateKeys(criteria Criteria) []string {
	keys := []string{"checker", "checker.task"}
	for _, check := range criteria.Checks {
		keys = append(keys, "check."+check)
	}
	keys = append(keys, "assessment."+criteria.Assessment)
	if len(customCriteriaLines(criteria.CustomCriteria)) > 0 {
		keys = append(keys, "checker.custom")
	}
	if criteria.MinWords != nil || criteria.MaxWords != nil {
		keys = append(keys, "checker.word_limits")
	}
	return keys
}

func (builder promptBuilder) checking(criteria Criteria, task checkedTask, paragraphs []checkedParagraph) (builtCheckingPrompt, error) {
	criteria, err := normalizeCriteria(criteria)
	if err != nil {
		return builtCheckingPrompt{}, err
	}
	instructions, err := builder.compose(checkingTemplateKeys(criteria))
	if err != nil {
		return builtCheckingPrompt{}, err
	}
	content := checkingPromptContent{
		Task: task.Text, SelectedChecks: criteria.Checks, Assessment: criteria.Assessment,
		CustomCriteria: customCriteriaLines(criteria.CustomCriteria),
		WordCount:      answerWordCount(paragraphs), Paragraphs: paragraphs,
	}
	if criteria.MinWords != nil || criteria.MaxWords != nil {
		limit := wordLimitResult(content.WordCount, criteria)
		content.WordLimit = &limit
	}
	return builtCheckingPrompt{modelPrompt: modelPrompt{Instructions: instructions}, Content: content}, nil
}
