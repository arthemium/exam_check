package app

import (
	"context"
	"database/sql"
	"strings"
)

func (s *Server) validateCheckingSetup(ctx context.Context, tx *sql.Tx, criteria Criteria) error {
	unconfigured := apiProblem{503, "Exam checking has not been configured"}
	if s.Config.OpenAIKey == "" {
		return unconfigured
	}
	rows, err := tx.QueryContext(ctx, `SELECT body,model FROM main_prompts
		WHERE key='transcriber' OR (key='checker' AND ?)`, needsAssessment(criteria))
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var body, model string
		if err := rows.Scan(&body, &model); err != nil {
			return err
		}
		if strings.TrimSpace(body) == "" || strings.TrimSpace(model) == "" {
			return unconfigured
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count == 0 || (needsAssessment(criteria) && count != 2) {
		return unconfigured
	}
	if criteria.Assessment == "b2_first_schools" {
		if _, err := loadB2RubricPrompt(ctx, tx); err != nil {
			return apiProblem{503, "The B2 First for Schools rubric is missing or incomplete"}
		}
	}
	return nil
}
