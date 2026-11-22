package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var b2StepKeys = []string{"content", "communicative_achievement", "organisation", "language"}

// Load each subscale independently; saved descriptors are the grading source of truth.
func loadB2RubricPrompt(ctx context.Context, db queryRower) (string, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM assessment_rubric_steps WHERE assessment=?", "b2_first_schools").Scan(&count); err != nil {
		return "", assessmentStepReadError()
	}
	if count != len(b2StepKeys) {
		return "", assessmentStepConfigError()
	}
	parts := make([]string, 0, len(b2StepKeys))
	for index, key := range b2StepKeys {
		part, err := loadB2SubscalePrompt(ctx, db, key, index)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "\n\n"), nil
}

func loadB2SubscalePrompt(ctx context.Context, db queryRower, key string, index int) (string, error) {
	var order int
	var title, summary, guidance string
	err := db.QueryRowContext(ctx, `SELECT step_order,title,summary,score_guidance
 FROM assessment_rubric_steps WHERE assessment=? AND step_key=?`, "b2_first_schools", key).
		Scan(&order, &title, &summary, &guidance)
	if errors.Is(err, sql.ErrNoRows) {
		return "", assessmentStepConfigError()
	}
	if err != nil {
		return "", assessmentStepReadError()
	}
	if order != index+1 || title != b2Criteria[index] || strings.TrimSpace(summary) == "" || !completeScoreGuidance(guidance) {
		return "", assessmentStepConfigError()
	}
	return "B2 First for Schools — " + title + "\n" + summary + "\n" + guidance, nil
}

func completeScoreGuidance(guidance string) bool {
	for band := 0; band <= 5; band++ {
		present := false
		prefix := fmt.Sprintf("%d:", band)
		for _, line := range strings.Split(guidance, "\n") {
			if text, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok && strings.TrimSpace(text) != "" {
				present = true
			}
		}
		if !present {
			return false
		}
	}
	return true
}
