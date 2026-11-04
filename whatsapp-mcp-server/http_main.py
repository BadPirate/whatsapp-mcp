"""Run the WhatsApp MCP server over streamable HTTP (for containers).

main.py runs stdio when executed directly; importing it only builds the server.
"""
import os

from main import mcp

if __name__ == "__main__":
    mcp.run(
        transport="streamable-http",
        host=os.environ.get("MCP_HOST", "0.0.0.0"),
        port=int(os.environ.get("MCP_PORT", "8091")),
        streamable_http_path=os.environ.get("MCP_PATH", "/mcp"),
    )
