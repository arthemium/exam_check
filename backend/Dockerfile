# syntax=docker/dockerfile:1
FROM golang:1.27.4-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download all
COPY . .
RUN mkdir -p static \
    && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/examcheck ./cmd/examcheck

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
COPY --from=build /out/examcheck /app/examcheck
# The build stage creates this directory even when the frontend is not connected yet.
COPY --from=build /src/static /app/static

USER 10001:10001
EXPOSE 8000
ENTRYPOINT ["/app/examcheck"]
CMD ["serve"]
