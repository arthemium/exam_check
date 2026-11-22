package app

import "database/sql"

func replaceC1WithB2(tx *sql.Tx) error {
	if _, err := tx.Exec(`INSERT INTO prompt_additions(key,body) VALUES('assessment.b2_first_schools',?)
 ON CONFLICT(key) DO NOTHING`, defaultPromptTemplates["assessment.b2_first_schools"]); err != nil {
		return err
	}
	_, err := tx.Exec(replaceC1AssessmentSQL)
	return err
}

const replaceC1AssessmentSQL = `
-- Preserve the identity of historical scores before changing future checking criteria.
UPDATE exams SET result=json_set(result,'$.assessment','cae')
 WHERE json_valid(criteria) AND json_extract(criteria,'$.assessment')='cae'
 AND json_valid(result) AND json_type(result)='object'
 AND json_extract(result,'$.assessment') IS NULL;
UPDATE exams SET criteria=json_set(criteria,'$.min_words',140,'$.max_words',190)
 WHERE json_valid(criteria) AND json_extract(criteria,'$.assessment')='cae'
 AND json_extract(criteria,'$.min_words')=220 AND json_extract(criteria,'$.max_words')=260;
UPDATE exams SET criteria=json_set(criteria,'$.assessment','b2_first_schools')
 WHERE json_valid(criteria) AND json_extract(criteria,'$.assessment')='cae';
DELETE FROM prompt_additions WHERE key='assessment.cae';
DELETE FROM assessment_rubric_steps WHERE assessment='cae';
UPDATE prompt_additions SET body=replace(body,'or a CAE rubric','or a Cambridge rubric')
 WHERE key='assessment.general';
PRAGMA user_version=7;
`
