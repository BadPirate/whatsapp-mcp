#!/bin/bash
# Runs the bridge (REST on 127.0.0.1:8080 inside the container, token-protected)
# and the MCP server (streamable HTTP on 0.0.0.0:${MCP_PORT:-8091}/mcp).
set -euo pipefail
: "${WHATSAPP_BRIDGE_TOKEN:?WHATSAPP_BRIDGE_TOKEN must be set}"

cd /app/whatsapp-bridge
./whatsapp-bridge &
BRIDGE=$!

cd /app/whatsapp-mcp-server
.venv/bin/python http_main.py &
MCP=$!

trap 'kill "$BRIDGE" "$MCP" 2>/dev/null || true' TERM INT
# Exit (and let the container restart) if either process dies.
wait -n "$BRIDGE" "$MCP"
STATUS=$?
kill "$BRIDGE" "$MCP" 2>/dev/null || true
exit "$STATUS"
