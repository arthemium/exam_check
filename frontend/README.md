# Examcheck React frontend

The application frontend uses React, TypeScript, React Router, and Vite. It connects to the Go API by default. Entry point: `src/main.tsx`; screens and routing: `src/App.tsx`.

## Development

Use Node 22.12 or newer:

```sh
cd frontend
npm ci
npm run dev
```

Open the URL Vite prints and sign in using the credentials in the server's root `.env`. Start the Go backend first using [its README](../backend/README.md).

Vite proxies `/api` and `/auth` to `http://localhost:8000` by default. For a different backend port, set `API_PROXY_TARGET` in `frontend/.env.local` to the server's `PUBLIC_BASE_URL`. This proxy setting is used only by the development server; deployed requests use the same origin as the frontend. Keep login credentials and OpenAI keys in the root server `.env`, never in frontend variables.

## Build and run with Go in Docker

```sh
cd frontend
npm run build:server
cd ..
docker compose up --build -d
```

`build:server` compiles the React app and copies its assets into `backend/static/`. Go then serves both the frontend and API from one container. Node is not needed at runtime. The host port comes from `APP_PORT` in the root `.env`; `PUBLIC_BASE_URL` must use the same port.

The app supports folders, student names, uploads/camera capture, deletion, individual/bulk checks, task transcription, eight writing checks, CAE/custom assessments, and word limits. Bulk jobs and results are stored by the backend in SQLite. CAE scores are estimates; missing task evidence and uncertain handwriting are flagged.

API requests time out after 20 seconds, with 120 seconds allowed for image uploads, including reading the response body. Failures release the form controls and show an error. Check retries reuse the request ID when the server's response was lost. Failed detail loads have a retry button; a failed workspace refresh keeps the current view and clears its notice after recovery. Use **Refresh** to confirm the current state before repeating an upload or other change whose response was interrupted.

## Verification

```sh
npm test
npm run typecheck
npm run build
```

Tests cover the React workflows and API adapter with mocked responses. Phone camera behavior and visual layout still require device/browser checks.

## Optional standalone demo

`npm run dev:demo` or `npm run build:demo` explicitly selects browser-local sample data. Normal development and production builds always use the API. Demo mode is separate from the application and is not required to run it. The old `src/mockup.html` remains a design reference and is not bundled.
