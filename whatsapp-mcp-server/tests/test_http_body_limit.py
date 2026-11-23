import base64
import json
import os
import unittest
from unittest.mock import patch

from starlette.testclient import TestClient

import http_main
from main import mcp

# A phone photo of the size that was refused with HTTP 413 (kaperkunde/pcp#160).
PHOTO_BYTES = 9_200_000


def send_file_request(size):
    media = base64.b64encode(b"\xff" * size).decode()
    return json.dumps(
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {
                "name": "send_file",
                "arguments": {"recipient": "123", "media_base64": media, "filename": "photo.jpg"},
            },
        }
    )


def post(limit, body):
    app = mcp.streamable_http_app(max_request_body_size=limit)
    with TestClient(app) as client:
        return client.post(
            "/mcp",
            content=body,
            headers={"content-type": "application/json", "accept": "application/json, text/event-stream"},
        )


class HttpBodyLimitTests(unittest.TestCase):
    def test_limit_covers_the_largest_media_as_base64(self):
        body = send_file_request(0)
        largest = len(body) + (http_main.MAX_MEDIA_BYTES + 2) // 3 * 4
        self.assertGreaterEqual(http_main.max_request_body_size(), largest)

    def test_environment_overrides_the_limit(self):
        with patch.dict(os.environ, {"MCP_MAX_REQUEST_BYTES": "12345"}):
            self.assertEqual(http_main.max_request_body_size(), 12345)

    def test_sdk_default_refuses_a_phone_photo(self):
        self.assertEqual(post(4 * 1024 * 1024, send_file_request(PHOTO_BYTES)).status_code, 413)

    def test_a_phone_photo_gets_past_the_body_limit(self):
        response = post(http_main.max_request_body_size(), send_file_request(PHOTO_BYTES))
        self.assertNotEqual(response.status_code, 413)


if __name__ == "__main__":
    unittest.main()
