FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/runbook-agent ./cmd/runbook-agent
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home agent && mkdir /data && chown agent:agent /data
COPY --from=build /out/runbook-agent /usr/local/bin/runbook-agent
USER agent
WORKDIR /data
ENV ADDR=0.0.0.0:8084 CONTAINER_MODE=1 DB_PATH=/data/agent.db
EXPOSE 8084
HEALTHCHECK --interval=5s --timeout=3s CMD curl -fsS http://127.0.0.1:8084/health || exit 1
ENTRYPOINT ["runbook-agent"]
