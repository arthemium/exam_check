package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
)

type transcriptSegment struct {
	Text      string `json:"text"`
	Certainty string `json:"certainty"`
}

type transcriptParagraph struct {
	PageNumber int                 `json:"page_number"`
	IsTitle    bool                `json:"is_title"`
	Segments   []transcriptSegment `json:"segments"`
}

type transcription struct {
	Paragraphs []transcriptParagraph `json:"paragraphs"`
	Model      string                `json:"-"`
}

type finding struct {
	Category    string  `json:"category"`
	Scope       string  `json:"scope"`
	ParagraphID *string `json:"paragraph_id"`
	Quote       *string `json:"quote"`
	Occurrence  *int    `json:"occurrence"`
	Suggestion  string  `json:"suggestion"`
	Explanation string  `json:"explanation"`
}

type analysis struct {
	Findings []finding `json:"findings"`
}

type uncertainRange struct {
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Certainty string `json:"certainty"`
}

type checkedParagraph struct {
	ID              string              `json:"id"`
	PageNumber      int                 `json:"page_number"`
	IsTitle         bool                `json:"is_title"`
	Text            string              `json:"text"`
	Segments        []transcriptSegment `json:"segments"`
	UncertainRanges []uncertainRange    `json:"uncertain_ranges"`
}

type checkedIssue struct {
	finding
	ID    string `json:"id"`
	Start *int   `json:"start"`
	End   *int   `json:"end"`
	Kind  string `json:"kind"`
}

type checkResult struct {
	SchemaVersion            int                   `json:"schema_version"`
	Model                    string                `json:"model"`
	TranscriptionModel       string                `json:"transcription_model,omitempty"`
	TranscriptVersion        string                `json:"transcript_version"`
	OffsetUnit               string                `json:"offset_unit"`
	Transcript               string                `json:"transcript"`
	Paragraphs               []checkedParagraph    `json:"paragraphs"`
	NeedsTranscriptionReview bool                  `json:"needs_transcription_review"`
	Checks                   []string              `json:"checks"`
	Issues                   []checkedIssue        `json:"issues"`
	ValidationWarnings       []string              `json:"validation_warnings"`
	Task                     checkedTask           `json:"task"`
	WordCount                int                   `json:"word_count"`
	WordLimit                checkedWordLimit      `json:"word_limit"`
	Assessments              []criterionAssessment `json:"assessments"`
	CustomAssessments        []customAssessment    `json:"custom_assessments"`
}

func objectSchema(properties map[string]any) map[string]any {
	required := make([]string, 0, len(properties))
	for key := range properties {
		required = append(required, key)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func enumSchema(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func transcriptionSchema() map[string]any {
	segment := objectSchema(map[string]any{
		"text": map[string]any{"type": "string"}, "certainty": enumSchema("clear", "uncertain", "unreadable"),
	})
	paragraph := objectSchema(map[string]any{
		"page_number": map[string]any{"type": "integer", "minimum": 1},
		"is_title":    map[string]any{"type": "boolean"},
		"segments":    map[string]any{"type": "array", "minItems": 1, "maxItems": 2000, "items": segment},
	})
	return objectSchema(map[string]any{
		"paragraphs": map[string]any{"type": "array", "minItems": 1, "maxItems": 2000, "items": paragraph},
	})
}

func analysisSchema() map[string]any {
	item := objectSchema(map[string]any{
		"category":     enumSchema("grammar", "spelling", "punctuation", "structure", "format", "register", "style", "taskCompletion"),
		"scope":        enumSchema("span", "paragraph", "document"),
		"paragraph_id": map[string]any{"type": []string{"string", "null"}},
		"quote":        map[string]any{"type": []string{"string", "null"}},
		"occurrence":   map[string]any{"type": []string{"integer", "null"}, "minimum": 1},
		"suggestion":   map[string]any{"type": "string"},
		"explanation":  map[string]any{"type": "string"},
	})
	return objectSchema(map[string]any{
		"findings": map[string]any{"type": "array", "maxItems": 2000, "items": item},
	})
}

func decodeModelJSON(data []byte, schema map[string]any, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return invalidModelOutput()
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return invalidModelOutput()
	}
	if err := validateModelValue(value, schema); err != nil {
		return invalidModelOutput()
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return invalidModelOutput()
	}
	return nil
}

func invalidModelOutput() error {
	return &CheckingError{Message: "The model returned an invalid structured result.", Retryable: true}
}

func schemaAllowsType(value any, declaration any) bool {
	if name, ok := declaration.(string); ok {
		return modelValueHasType(value, name)
	}
	for _, name := range declaration.([]string) {
		if modelValueHasType(value, name) {
			return true
		}
	}
	return false
}

func modelValueHasType(value any, name string) bool {
	switch name {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := strconv.ParseInt(string(number), 10, 64)
		return err == nil
	}
	return false
}

func validateModelValue(value any, schema map[string]any) error {
	if !schemaAllowsType(value, schema["type"]) {
		return errors.New("invalid type")
	}
	if value == nil {
		return nil
	}
	if options, ok := schema["enum"].([]string); ok {
		for _, option := range options {
			if value == option {
				return nil
			}
		}
		return errors.New("invalid enum")
	}
	switch item := value.(type) {
	case map[string]any:
		return validateModelObject(item, schema)
	case []any:
		return validateModelArray(item, schema)
	case json.Number:
		number, _ := item.Int64()
		if minimum, ok := schema["minimum"].(int); ok && number < int64(minimum) {
			return errors.New("number below minimum")
		}
		if maximum, ok := schema["maximum"].(int); ok && number > int64(maximum) {
			return errors.New("number above maximum")
		}
	}
	return nil
}

func validateModelObject(value map[string]any, schema map[string]any) error {
	properties := schema["properties"].(map[string]any)
	for _, key := range schema["required"].([]string) {
		if _, exists := value[key]; !exists {
			return errors.New("missing property")
		}
	}
	for key, item := range value {
		property, exists := properties[key]
		if !exists {
			return errors.New("unknown property")
		}
		if err := validateModelValue(item, property.(map[string]any)); err != nil {
			return err
		}
	}
	return nil
}

func validateModelArray(value []any, schema map[string]any) error {
	if minimum, ok := schema["minItems"].(int); ok && len(value) < minimum {
		return errors.New("array too short")
	}
	if maximum, ok := schema["maxItems"].(int); ok && len(value) > maximum {
		return errors.New("array too long")
	}
	for _, item := range value {
		if err := validateModelValue(item, schema["items"].(map[string]any)); err != nil {
			return err
		}
	}
	return nil
}
