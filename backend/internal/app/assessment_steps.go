package app

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"strings"
)

//go:embed assessment_steps.sql
var assessmentStepsSQL string

type AssessmentStepInfo struct {
	StepKey   string `json:"step_key"`
	StepOrder int    `json:"step_order"`
	Title     string `json:"title"`
}

type AssessmentStep struct {
	AssessmentStepInfo
	Assessment       string `json:"assessment"`
	Summary          string `json:"summary"`
	ScoreGuidance    string `json:"score_guidance"`
	SourceURL        string `json:"source_url"`
	SourcePages      string `json:"source_pages"`
	SourceSHA256     string `json:"source_sha256"`
	ReferenceVersion string `json:"reference_version"`
}

func migrateAssessmentSteps(tx *sql.Tx) error {
	if _, err := tx.Exec(assessmentStepsSQL); err != nil {
		return err
	}
	_, err := tx.Exec("PRAGMA user_version=5")
	return err
}

// ListAssessmentSteps returns the ordered manifest without loading rubric bodies.
func (store *Store) ListAssessmentSteps(ctx context.Context, assessment string) ([]AssessmentStepInfo, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT step_key,step_order,title
 FROM assessment_rubric_steps WHERE assessment=? ORDER BY step_order`, assessment)
	if err != nil {
		return nil, assessmentStepReadError()
	}
	defer rows.Close()
	var steps []AssessmentStepInfo
	for rows.Next() {
		var step AssessmentStepInfo
		if err := rows.Scan(&step.StepKey, &step.StepOrder, &step.Title); err != nil {
			return nil, assessmentStepReadError()
		}
		if step.StepOrder < 1 || strings.TrimSpace(step.StepKey) == "" || strings.TrimSpace(step.Title) == "" {
			return nil, assessmentStepConfigError()
		}
		steps = append(steps, step)
	}
	if rows.Err() != nil {
		return nil, assessmentStepReadError()
	}
	if len(steps) == 0 {
		return nil, assessmentStepConfigError()
	}
	return steps, nil
}

// LoadAssessmentStep retrieves exactly one criterion and its source metadata.
func (store *Store) LoadAssessmentStep(ctx context.Context, assessment, key string) (AssessmentStep, error) {
	var step AssessmentStep
	err := store.DB.QueryRowContext(ctx, `SELECT assessment,step_key,step_order,title,summary,
 score_guidance,source_url,source_pages,source_sha256,reference_version
 FROM assessment_rubric_steps WHERE assessment=? AND step_key=?`, assessment, key).
		Scan(&step.Assessment, &step.StepKey, &step.StepOrder, &step.Title, &step.Summary,
			&step.ScoreGuidance, &step.SourceURL, &step.SourcePages, &step.SourceSHA256, &step.ReferenceVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return AssessmentStep{}, assessmentStepConfigError()
	}
	if err != nil {
		return AssessmentStep{}, assessmentStepReadError()
	}
	if step.StepOrder < 1 {
		return AssessmentStep{}, assessmentStepConfigError()
	}
	for _, value := range []string{step.Title, step.Summary, step.ScoreGuidance, step.SourceURL,
		step.SourcePages, step.SourceSHA256, step.ReferenceVersion} {
		if strings.TrimSpace(value) == "" {
			return AssessmentStep{}, assessmentStepConfigError()
		}
	}
	return step, nil
}

func assessmentStepReadError() error {
	return &CheckingError{"The saved assessment steps could not be loaded. Try again shortly.", true}
}

func assessmentStepConfigError() error {
	return &CheckingError{"The requested assessment step is missing or incomplete.", false}
}
