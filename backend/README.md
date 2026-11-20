# Examcheck backend

One Go process serves the API and runs a durable background job worker. SQLite stores folders, exams, sessions, and jobs; private photos stay on disk. One fixed username and password controls access.

The previous Python source and tests are retained in `legacy/python/` for reference and excluded from the Docker build.

## Local startup

Run from the repository root:

```sh
cp .env.example .env
# Set LOGIN_PASSWORD and, when needed, OpenAI credentials.
docker compose --env-file .env up --build -d
docker compose --env-file .env logs -f --tail=100 app
```

Configure `LOGIN_USERNAME` and a strong `LOGIN_PASSWORD` in the ignored, server-side `.env` file. A blank username or password disables login. Changing either credential and recreating the app container invalidates sessions while retaining the same account's folders and exams. Keep credentials out of frontend code.

Set `PUBLIC_BASE_URL=http://localhost:8000`, `APP_ENV=development`, and `COOKIE_SECURE=false` locally. `FRONTEND_ORIGINS` is a JSON array; keep it `[]` when frontend and API share an origin. For a separate local frontend, list its exact origin.

Supply `OPENAI_API_KEY` before requesting checks. Model selection comes from SQLite's `main_prompts.model`: `transcriber` defaults to `gpt-6.1-terra`, and `checker` defaults to `gpt-6.2-sol`. Both use the Responses API with structured outputs; transcription also requires image input. The legacy `OPENAI_MODEL`, when nonempty during database creation or migration to schema 4, seeds both model fields. It does not override saved models afterward. `ENABLE_WORKER=true` runs one background processor inside the API process. Jobs remain in SQLite across restarts; setting it to `false` leaves queued jobs pending.

OpenAI prompts use two tables: `main_prompts(key, body, model)` holds `transcriber` and `checker`; `prompt_additions(key, body)` holds the 15 check, assessment, task, custom, and word-limit sections. Migration moves existing text unchanged and removes the old `prompt_templates` table. Defaults live in `internal/app/prompt_defaults.go` and are seeded once. Saved prompt and model edits survive restarts and are loaded together for each model request; missing or blank selected prompts/models fail explicitly. Task text, custom criteria, the unchanged transcript, and server-calculated word limits stay in separate JSON input. Changing Go defaults does not overwrite saved rows. Results record the OCR model in `transcription_model` and the assessment model in `model`; word-limit-only results use the OCR model for both because no assessment call is made.

The app and API share `PUBLIC_BASE_URL`; Docker publishes port `APP_PORT` (8000 by default) on the host loopback interface. `/health/live` checks the process and `/health/ready` checks database readiness. The root Dockerfile compiles the React frontend in API mode and includes it in the Go image. No separate frontend build is required for Docker. See the [frontend README](../frontend/README.md) for local development and currently supported controls. The React frontend connects to this API by default.

The Go server preserves the application endpoints below; it does not expose the former FastAPI Swagger `/docs` page. Local Go commands read environment variables directly; Docker Compose loads the root `.env` file.

The server and its healthcheck use `LISTEN_ADDR` when set, otherwise `PORT` when provided by a hosting platform, otherwise `:8000`. See [Railway setup](../deploy/railway.md) for the root Docker build, persistent volume, and production variables.

## Logs

The app writes structured JSON logs to stdout. Follow them with `docker compose --env-file .env logs -f --tail=100 app`. Logs cover startup, requests, image input/stored sizes, queued checks, worker stages, retries, failures, and OpenAI model/status/timing. Requests get an `X-Request-ID`; worker and provider events share job and exam IDs. Successful health checks and static assets are quiet. Bodies, prompts, student text, credentials, cookies, and API keys are excluded. Docker rotates logs at 10 MiB per file, retaining up to three files.

Unexpected checker panics are logged and mark the affected job as failed, allowing the worker to continue with later jobs. Shutdown and lost leases remain recoverable interruptions.

## API workflow

Sign in with `POST /auth/login` and JSON containing `username` and `password`. The response sets an opaque HttpOnly session cookie for subsequent requests. `GET /auth/me` returns `id`, `username`, and `display_name`; `POST /auth/logout` revokes the current session.

Mutating requests, including login, require an `Origin` header matching `PUBLIC_BASE_URL` or an entry in `FRONTEND_ORIGINS`. Browsers send it automatically; include it explicitly in API clients. Login allows five attempts per 60 seconds per process; a successful login or process restart resets the limiter.

- `GET /api/folders` lists folders; `POST /api/folders` creates one; `PATCH /api/folders/{id}` renames it; `DELETE` removes it and its exams/photos.
- `GET /api/folders/{id}/exams` lists exams. `POST` to the same path uploads multipart form data: `title`, `student_name`, repeated `checks` fields, and repeated `photos` files.
- `GET /api/exams/{id}` returns exam details and processing results; `PATCH` updates names or `criteria`; `DELETE` removes the exam and its photos. Deletion returns `409` while affected exams are queued or processing.
- `POST /api/exams/{id}/task-photos` accepts one multipart `photo` and returns its authenticated `url`. Put that URL in `criteria.task_photo`. Task photos belong to their exam; removing a reference does not delete an immutable uploaded asset. Each exam allows up to 20 task-photo assets, totaling at most 20 MiB by default; deleting the exam removes them.
- `POST /api/exams/{id}/checks` accepts `{ "criteria": { ... }, "request_id": "unique-id" }`. Criteria and job creation commit together; rejected checks preserve prior feedback. Bodyless legacy calls remain supported.
- `POST /api/folders/{id}/checks` accepts `{ "exam_ids": ["..."], "criteria": { ... }, "request_id": "unique-id" }` and queues all selected exams atomically. The response contains `batch_id` and `exam_ids`. Repeating the request ID returns the existing batch. Optional `criteria_by_exam` provides a full criteria object for every selected exam, allowing separate task-photo URLs. Blank shared task fields preserve individual tasks.
- Poll exam details for `status`, `batch_id`, `stage`, `progress`, and `result`. All accepted bulk jobs survive browser closure and server restarts.

Supported checks are `grammar`, `spelling`, `punctuation`, `structure`, `format`, `register`, `style`, and `taskCompletion`. Criteria also contain `assessment` (`general` or `cae`), `custom_criteria` (one per line, at most 1200 characters/100 distinct criteria), nullable integer `min_words`/`max_words` (1–10000), `detect_task`, `task_text`, `task_source`, and `task_photo`. Task text is limited to 4000 characters. Unchanged criteria saves preserve results; changing criteria clears stale feedback.

Status progresses from `uploaded` to `queued`, `processing`, then `completed` or `failed`. Progress records milestones: queued 0, transcribing 10, assessing 60, finalizing 90, completed 100; it does not predict elapsed time. Manual task instructions take precedence over a separate task photo, then optional extraction from the essay. Extracted instructions are saved separately from the student's answer. Word counts exclude task instructions, titles, and unreadable markers; uncertain handwriting is flagged.

CAE feedback uses Content, Communicative Achievement, Organisation, and Language, with estimated 0–5 scores based on the [Cambridge C1 Advanced handbook](https://www.cambridgeenglish.org/Images/167804-c1-advanced-handbook.pdf). These are model estimates, not official exam marks. Missing tasks prevent task-dependent scores; uncertain handwriting withholds all numerical scores. Results include `task`, `word_count`, `word_limit`, `assessments`, and `custom_assessments` alongside the transcript and issues.

Page URLs require authentication. Result issue offsets are paragraph-local UTF-16 code units, matching JavaScript string indexing. Render transcript and feedback as text, not HTML.

Defaults allow 10 JPEG/PNG/WebP pages, original uploads up to 100 MiB per file (`MAX_UPLOAD_BYTES`), 20 megapixels per image, and a 120 MiB multipart request (`MAX_REQUEST_BYTES`). A file over 10 MiB is accepted for transcoding within those input limits.

Answer and task photos are rotated, composited onto white, resized to at most 3200 pixels on the longest edge without enlargement or cropping, and encoded as JPEG quality 90 without EXIF metadata. Only the processed image is saved, with a 3 MiB ceiling per photo and 20 MiB total for an exam's answer photos. Images still over the output ceiling are rejected without lowering quality further. `MAX_PAGE_BYTES` remains a 10 MiB processing ceiling for existing saved images; setting it lower also tightens the new-output limit.

Browser task-photo uploads preserve the original for server processing. When a task photo is shared across a batch, later exams upload a cached copy of the first server-processed JPEG, avoiding repeated transfers of the large original. Each copy is normalized by its upload endpoint; copies always start from the first processed image. Demo previews use their own smaller images. These settings need validation on representative handwriting before relying on OCR accuracy.

Bulk requests allow 1–50 exams within the 50-check rolling daily limit, processed by the single worker. Individual submissions retain the two-active-job limit. Checking normally makes two model calls; word-limit-only checking uses transcription plus a server word count. Transient failures allow up to three attempts.

## Development checks

Use the Go version in `go.mod` and a C compiler for the bundled SQLite driver. From `backend/`, run:

```sh
go test ./...
go test -race ./...
go build ./cmd/examcheck
```

Tests use temporary SQLite databases and mocked external services; they require no real OpenAI credentials. Live provider behavior still needs verification with configured credentials.

## One-server HTTPS setup

Point a hostname at the server and allow inbound TCP ports 80 and 443. Set `DOMAIN` in `.env` to the hostname only, then run:

```sh
docker compose -f compose.yaml -f compose.production.yaml --profile production up --build -d
```

This adds Caddy for HTTPS and forces production mode, secure cookies, and the HTTPS public URL. Keep `FRONTEND_ORIGINS=[]` for the shared origin. Use both Compose files and the profile for subsequent production commands. Do not expose port 8000 publicly.

## Data and backups

Named volumes retain SQLite (`sqlitedata`), private photos (`uploads`), and Caddy certificate state. The app runs as UID/GID `10001`; new data volumes inherit that directory ownership from the image. Existing upload volumes must grant UID `10001` access.

SQLite uses WAL mode and must live on local storage on the same server as the app. Use one application instance with its built-in worker. Startup migrates schema versions 1–3 to version 4 transactionally, preserving existing folders, exams, feedback, and saved prompt text. Version 4 separates the two main prompts and their models from prompt additions. Older application versions cannot open a version 4 database; retain a pre-upgrade backup for rollback.

For a consistent simple backup, stop the app, copy the complete SQLite data directory and uploads together, then restart it. Keep backups off-server and verify restoration. Do not copy only a live `.db` file while omitting its WAL. Named volumes are not backups; `docker compose down -v` deletes the current stack's data volumes.

This Go version starts a new SQLite database. Existing PostgreSQL data is **not imported automatically**. If upgrading a running Python deployment, stop its services using the previous Compose configuration before starting Go; the new configuration does not stop old orphan services automatically. Preserve the previous PostgreSQL volume until any required migration has been reviewed. The uploads volume name is preserved, but files only appear in the app when matching exam records exist in SQLite.
