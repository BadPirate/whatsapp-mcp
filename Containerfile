# Single image: Go bridge + Python MCP server (streamable HTTP on 8091).
# Built from erwinvaneijk/whatsapp-mcp dev-main @ 6332d5e plus this packaging.
FROM docker.io/library/golang:1.26-bookworm AS bridge
WORKDIR /src
COPY whatsapp-bridge/go.mod whatsapp-bridge/go.sum ./
RUN go mod download
COPY whatsapp-bridge/ ./
RUN CGO_ENABLED=1 go build -tags sqlite_fts5 -trimpath -o /out/whatsapp-bridge .

FROM docker.io/library/python:3.11-slim-bookworm
RUN apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && pip install --no-cache-dir uv
WORKDIR /app
COPY whatsapp-mcp-server/ /app/whatsapp-mcp-server/
RUN cd /app/whatsapp-mcp-server && uv sync --locked --no-dev
COPY --from=bridge /out/whatsapp-bridge /app/whatsapp-bridge/whatsapp-bridge
COPY docker/entrypoint.sh /app/entrypoint.sh
RUN chmod 755 /app/entrypoint.sh && mkdir -p /app/whatsapp-bridge/store && chmod 700 /app/whatsapp-bridge/store
VOLUME /app/whatsapp-bridge/store
EXPOSE 8091
ENTRYPOINT ["/app/entrypoint.sh"]
