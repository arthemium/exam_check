package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

type Criteria struct {
	Checks         []string `json:"checks"`
	Assessment     string   `json:"assessment"`
	CustomCriteria string   `json:"custom_criteria"`
	MinWords       *int     `json:"min_words"`
	MaxWords       *int     `json:"max_words"`
	DetectTask     bool     `json:"detect_task"`
	TaskText       string   `json:"task_text"`
	TaskSource     string   `json:"task_source"`
	TaskPhoto      string   `json:"task_photo"`
}

func defaultCriteria(checks []string) Criteria {
	return Criteria{Checks: append([]string{}, checks...), Assessment: "general"}
}

func normalizeCriteria(criteria Criteria) (Criteria, error) {
	checks, err := validChecks(criteria.Checks)
	if err != nil {
		return criteria, err
	}
	criteria.Checks = checks
	if criteria.Assessment == "" {
		criteria.Assessment = "general"
	}
	if criteria.Assessment != "general" && criteria.Assessment != "b2_first_schools" {
		return criteria, apiProblem{422, "Choose general or B2 First for Schools assessment"}
	}
	criteria.CustomCriteria = strings.TrimSpace(criteria.CustomCriteria)
	criteria.TaskText = strings.TrimSpace(criteria.TaskText)
	if len(customCriteriaLines(criteria.CustomCriteria)) > 100 {
		return criteria, apiProblem{422, "Use at most 100 custom criteria"}
	}
	if !utf8.ValidString(criteria.CustomCriteria) || utf8.RuneCountInString(criteria.CustomCriteria) > 1200 || !utf8.ValidString(criteria.TaskText) || utf8.RuneCountInString(criteria.TaskText) > 4000 {
		return criteria, apiProblem{422, "Custom criteria must be at most 1200 characters and task instructions at most 4000"}
	}
	for _, limit := range []*int{criteria.MinWords, criteria.MaxWords} {
		if limit != nil && (*limit < 1 || *limit > 10000) {
			return criteria, apiProblem{422, "Word limits must be between 1 and 10000"}
		}
	}
	if criteria.MinWords != nil && criteria.MaxWords != nil && *criteria.MinWords > *criteria.MaxWords {
		return criteria, apiProblem{422, "Minimum words cannot exceed maximum words"}
	}
	switch criteria.TaskSource {
	case "", "essay", "photo", "manual":
	default:
		return criteria, apiProblem{422, "Unknown task source"}
	}
	if criteria.TaskText == "" {
		criteria.TaskSource = ""
	}
	if criteria.TaskText != "" && criteria.TaskSource == "" {
		criteria.TaskSource = "manual"
	}
	return criteria, nil
}

func criteriaEqual(first, second Criteria) bool {
	first.Checks, second.Checks = slices.Clone(first.Checks), slices.Clone(second.Checks)
	slices.Sort(first.Checks)
	slices.Sort(second.Checks)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	return string(a) == string(b)
}

func validateTaskPhoto(ctx context.Context, db queryRower, examID string, criteria Criteria) error {
	if criteria.TaskPhoto == "" {
		return nil
	}
	prefix := "/api/exams/" + examID + "/task-photos/"
	photoID, valid := strings.CutPrefix(criteria.TaskPhoto, prefix)
	if !valid || photoID == "" || strings.ContainsAny(photoID, "/?#%\\") {
		return apiProblem{422, "Choose a task photo uploaded for this exam"}
	}
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM exam_task_photos p JOIN exams e ON e.id=p.exam_id WHERE p.id=? AND p.exam_id=? AND e.owner_id=?`, photoID, examID, ownerID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return apiProblem{422, "Task photo not found for this exam"}
	}
	return err
}
