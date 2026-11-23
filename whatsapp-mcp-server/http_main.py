"""Run the WhatsApp MCP server over streamable HTTP (for containers).

main.py runs stdio when executed directly; importing it only builds the server.
"""
import os

from main import mcp
from whatsapp import MAX_MEDIA_BYTES


def max_request_body_size() -> int:
    """Largest request body the HTTP transport takes.

    The MCP SDK refuses bodies over 4 MiB with HTTP 413 by default, which is less than one
    phone photo sent as media_base64. Base64 is 4/3 of the bytes, plus 1 MiB for the rest
    of the JSON-RPC request. MCP_MAX_REQUEST_BYTES overrides it.
    """
    configured = os.environ.get("MCP_MAX_REQUEST_BYTES")
    if configured:
        return int(configured)
    return (MAX_MEDIA_BYTES + 2) // 3 * 4 + 1024 * 1024


if __name__ == "__main__":
    mcp.run(
        transport="streamable-http",
        host=os.environ.get("MCP_HOST", "0.0.0.0"),
        port=int(os.environ.get("MCP_PORT", "8091")),
        streamable_http_path=os.environ.get("MCP_PATH", "/mcp"),
        max_request_body_size=max_request_body_size(),
    )
