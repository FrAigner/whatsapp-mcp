# WhatsApp bridge (Go, CGO for sqlite3). Runs as non-root; state lives in /app/store.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY whatsapp-bridge/go.mod whatsapp-bridge/go.sum ./
RUN go mod download
COPY whatsapp-bridge/ ./
RUN CGO_ENABLED=1 go build -o /out/whatsapp-bridge .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home --home-dir /app app \
    && mkdir -p /app/store && chown -R app:app /app
COPY --from=build /out/whatsapp-bridge /usr/local/bin/whatsapp-bridge
USER app
WORKDIR /app
CMD ["whatsapp-bridge"]
