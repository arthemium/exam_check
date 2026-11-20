# Railway deployment

Deploy the GitHub repository with the service root directory left at `/`. Railway detects the root `Dockerfile`, which builds React and Go and serves both from one process. Leave the custom build and start commands unset. The build does not require `backend/static` or `frontend/dist` in Git. See [Railway Dockerfile detection](https://docs.railway.com/builds/dockerfiles).

Set the healthcheck path to `/health/ready`. The app listens on Railway's supplied `PORT`; leave `LISTEN_ADDR` unset. Use one replica for the SQLite database and its worker.

Attach a persistent Railway volume at `/data`. The image uses `/data/db/examcheck.db` for SQLite and `/data/uploads` for photos. Railway mounts volumes as root, so set `RAILWAY_RUN_UID=0` for this service as described in [Railway volume permissions](https://docs.railway.com/volumes#permissions). Local Compose continues to run as UID 10001.

Set these service variables in Railway:

```dotenv
APP_ENV=production
PUBLIC_BASE_URL=https://YOUR-SERVICE.up.railway.app
COOKIE_SECURE=true
FRONTEND_ORIGINS=[]
LOGIN_USERNAME=teacher
LOGIN_PASSWORD=YOUR_PRIVATE_PASSWORD
OPENAI_API_KEY=YOUR_PRIVATE_OPENAI_KEY
ENABLE_WORKER=true
RAILWAY_RUN_UID=0
```

Replace the public URL with the generated Railway domain or your custom HTTPS domain, and supply real credentials through Railway's variables. The local `.env` is excluded from the image. `OPENAI_MODEL` can remain unset: a new database seeds `gpt-6.1-terra` for transcription and `gpt-6.2-sol` for checking.

The Railway volume starts with a new database. Local Docker data and edited SQLite prompts are not transferred by a Git push; migrate them separately if needed. Keep `/data` backed up across deployments.
