# Examcheck

- [`frontend/`](frontend/README.md): React + TypeScript application, connected to the API by default.
- [`backend/`](backend/README.md): Go API, SQLite, private photo storage, and background checking worker.
- `compose.yaml`: one application container with persistent database and upload volumes.

With the root `.env` configured:

```sh
cd frontend
npm ci
npm run build:server
cd ..
docker compose up --build -d
```

Open `PUBLIC_BASE_URL` from `.env`. Use `LOGIN_USERNAME` and `LOGIN_PASSWORD` to sign in. Add `OPENAI_API_KEY` to enable exam checking; each main prompt's OpenAI model is stored in SQLite's `main_prompts` table.
