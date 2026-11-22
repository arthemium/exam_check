package app

import (
	"database/sql"
	_ "embed"
)

//go:embed assessment_b2_steps.sql
var assessmentB2StepsSQL string

func migrateB2RubricAndCheckerEvidence(tx *sql.Tx) error {
	if _, err := tx.Exec(assessmentB2StepsSQL); err != nil {
		return err
	}
	// Append the requested policy without replacing saved prompt text or models.
	if _, err := tx.Exec(`UPDATE main_prompts SET body=body || char(10) || char(10) || ?
 WHERE key='checker' AND length(trim(body)) > 0 AND instr(body,?)=0`, checkerEvidencePolicy, checkerEvidencePolicy); err != nil {
		return err
	}
	_, err := tx.Exec("PRAGMA user_version=6")
	return err
}
