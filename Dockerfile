# syntax=docker/dockerfile:1
FROM node:22-alpine AS frontend

WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --include=dev
COPY frontend/ ./
RUN VITE_DATA_MODE=api npm run build

FROM golang:1.27.4-bookworm AS build

WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download all
COPY backend/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/examcheck ./cmd/examcheck

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 app \
    && useradd --uid 10001 --gid app --no-create-home --shell /usr/sbin/nologin app \
    && mkdir -p /app/static /data/db /data/uploads \
    && chmod 0700 /data/db /data/uploads \
    && chown -R app:app /data

WORKDIR /app
ENV DB_PATH=/data/db/examcheck.db \
    UPLOAD_DIR=/data/uploads
COPY --from=build /out/examcheck /app/examcheck
COPY --from=frontend /frontend/dist /app/static

USER 10001:10001
EXPOSE 8000
ENTRYPOINT ["/app/examcheck"]
CMD ["serve"]
