package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type storedPrompt struct {
	Model     string
	Templates map[string]string
}

func migratePromptTemplates(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE prompt_templates (
 key TEXT PRIMARY KEY, body TEXT NOT NULL
);`); err != nil {
		return err
	}
	for key, body := range defaultPromptTemplates {
		if _, err := tx.Exec("INSERT INTO prompt_templates(key,body) VALUES(?,?) ON CONFLICT(key) DO NOTHING", key, body); err != nil {
			return err
		}
	}
	_, err := tx.Exec("PRAGMA user_version=3")
	return err
}

func splitPromptTemplates(tx *sql.Tx, initialModel string) error {
	if _, err := tx.Exec(splitPromptSchema); err != nil {
		return err
	}
	transcriberModel, checkerModel := defaultTranscriberModel, defaultCheckerModel
	if model := strings.TrimSpace(initialModel); model != "" {
		transcriberModel, checkerModel = model, model
	}
	result, err := tx.Exec(`INSERT INTO main_prompts(key,body,model)
 SELECT key,body,CASE key WHEN 'transcriber' THEN ? ELSE ? END
 FROM prompt_templates WHERE key IN ('transcriber','checker')`, transcriberModel, checkerModel)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 2 {
		return fmt.Errorf("prompt migration requires the transcriber and checker templates")
	}
	_, err = tx.Exec(`INSERT INTO prompt_additions(key,body)
 SELECT key,body FROM prompt_templates WHERE key NOT IN ('transcriber','checker');
 DROP TABLE prompt_templates;
 PRAGMA user_version=4;`)
	return err
}

const splitPromptSchema = `
CREATE TABLE main_prompts (
 key TEXT PRIMARY KEY CHECK(key IN ('transcriber','checker')),
 body TEXT NOT NULL, model TEXT NOT NULL
);
CREATE TABLE prompt_additions (
 key TEXT PRIMARY KEY CHECK(key NOT IN ('transcriber','checker')),
 body TEXT NOT NULL
);
`

func (store *Store) loadPrompt(ctx context.Context, key string) (storedPrompt, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT key,body,model FROM main_prompts WHERE key=?
 UNION ALL SELECT key,body,NULL FROM prompt_additions`, key)
	if err != nil {
		return storedPrompt{}, promptReadError()
	}
	defer rows.Close()
	result := storedPrompt{Templates: make(map[string]string)}
	for rows.Next() {
		var name, body string
		var model sql.NullString
		if err := rows.Scan(&name, &body, &model); err != nil {
			return storedPrompt{}, promptReadError()
		}
		result.Templates[name] = body
		if model.Valid {
			result.Model = strings.TrimSpace(model.String)
		}
	}
	if rows.Err() != nil {
		return storedPrompt{}, promptReadError()
	}
	if strings.TrimSpace(result.Templates[key]) == "" || result.Model == "" {
		return storedPrompt{}, &CheckingError{fmt.Sprintf("The %s prompt or its OpenAI model is not configured.", key), false}
	}
	return result, nil
}

func promptReadError() error {
	return &CheckingError{"The saved prompts could not be loaded. Try again shortly.", true}
}

func (store *Store) loadPromptTemplates(ctx context.Context) (map[string]string, error) {
	rows, err := store.DB.QueryContext(ctx, "SELECT key,body FROM main_prompts UNION ALL SELECT key,body FROM prompt_additions")
	if err != nil {
		return nil, promptReadError()
	}
	defer rows.Close()
	templates := make(map[string]string)
	for rows.Next() {
		var key, body string
		if err = rows.Scan(&key, &body); err != nil {
			return nil, promptReadError()
		}
		templates[key] = body
	}
	if err = rows.Err(); err != nil {
		return nil, promptReadError()
	}
	return templates, nil
}
