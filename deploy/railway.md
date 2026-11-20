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

## Logs and 502 errors

Open the latest deployment's deploy logs (stdout/stderr), and clear any error-only filters. Startup logs now show `application_starting`, `configuration_ready`, `database_initializing`, `database_ready`, `server_starting`, `server_listening`, and `application_ready`. `server_listening` confirms a successful bind and includes the actual port. `configuration_ready` identifies whether `LISTEN_ADDR`, `PORT`, or the default selected the address; credentials are represented only as configured/not configured.

Requests, including health checks and assets, log both arrival and completion with the same `request_id`. Readiness failures include a fixed reason. No request bodies, query strings, cookies, credentials, or student text are logged.

For a 502, compare the domain's target port under the service's networking settings with `server_listening.port`. Leave `LISTEN_ADDR` unset to use Railway's `PORT`. A `platform_port_mismatch` or `loopback_listener` warning points to a binding configuration problem. If startup stops before `application_ready`, inspect `application_failed` and the last startup stage. If the app is ready but refreshing the public URL produces no request-arrival event, inspect Railway's HTTP logs and domain routing: the proxy may be failing before the request reaches Go. See [Railway's 502 troubleshooting](https://docs.railway.com/networking/troubleshooting/application-failed-to-respond).
