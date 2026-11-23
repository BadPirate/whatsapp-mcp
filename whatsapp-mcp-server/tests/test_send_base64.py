import base64
import os
import tempfile
import unittest
from unittest.mock import Mock, patch

import whatsapp

PNG = b"\x89PNG\r\n\x1a\n" + b"x" * 64


def b64(data=PNG):
    return base64.b64encode(data).decode()


class SendBase64Tests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.seen = {}

        def post(url, json, headers, timeout):
            self.seen["payload"] = json
            path = json.get("media_path")
            if path:
                self.seen["bytes"] = open(path, "rb").read()
                self.seen["name"] = os.path.basename(path)
            return Mock(status_code=200, json=lambda: {"success": True, "message": "ok", "message_id": "3EB0"})

        self.post = Mock(side_effect=post)
        for p in (
            patch.object(whatsapp, "MEDIA_DIR", self.dir.name),
            patch.object(whatsapp, "BRIDGE_TOKEN", "t" * 32),
            patch.object(whatsapp.requests, "post", self.post),
        ):
            p.start()
            self.addCleanup(p.stop)
        self.addCleanup(self.dir.cleanup)

    def left_over(self):
        return os.listdir(self.dir.name)

    def test_bytes_are_sent_under_their_name_and_removed(self):
        ok, msg, mid = whatsapp.send_file_full("123", caption="hi", media_base64=b64(), filename="photo.png")
        self.assertTrue(ok, msg)
        self.assertEqual(mid, "3EB0")
        self.assertEqual(self.seen["bytes"], PNG)
        self.assertEqual(self.seen["name"], "photo.png")
        self.assertEqual(self.seen["payload"]["message"], "hi")
        self.assertEqual(self.left_over(), [])

    def test_removed_when_the_bridge_fails(self):
        self.post.side_effect = whatsapp.requests.ConnectionError("down")
        ok, _, _ = whatsapp.send_file_full("123", media_base64=b64(), filename="a.png")
        self.assertFalse(ok)
        self.assertEqual(self.left_over(), [])

    def test_exactly_one_source(self):
        for kwargs in ({}, {"media_path": "/x/a.png", "media_base64": b64(), "filename": "a.png"}):
            ok, msg, _ = whatsapp.send_file_full("123", **kwargs)
            self.assertFalse(ok)
            self.assertIn("exactly one", msg)
        self.post.assert_not_called()

    def test_refusals_send_nothing(self):
        cases = [
            ({"media_base64": "not base64!!", "filename": "a.png"}, "valid base64"),
            ({"media_base64": b64(), "filename": ""}, "filename"),
            ({"media_base64": b64(), "filename": "noext"}, "filename"),
            ({"media_base64": "  ", "filename": "a.png"}, "empty"),
        ]
        for kwargs, word in cases:
            ok, msg, _ = whatsapp.send_file_full("123", **kwargs)
            self.assertFalse(ok)
            self.assertIn(word, msg)
        self.post.assert_not_called()
        self.assertEqual(self.left_over(), [])

    def test_size_limit(self):
        with patch.object(whatsapp, "MAX_MEDIA_BYTES", 10):
            ok, msg, _ = whatsapp.send_file_full("123", media_base64=b64(b"y" * 11), filename="a.bin")
        self.assertFalse(ok)
        self.assertIn("limit", msg)
        self.post.assert_not_called()

    def test_filename_cannot_leave_the_folder(self):
        ok, msg, _ = whatsapp.send_file_full("123", media_base64=b64(), filename="../../evil.png")
        self.assertTrue(ok, msg)
        self.assertEqual(self.seen["name"], "evil.png")

    def test_mime_type_gives_the_extension(self):
        ok, msg, _ = whatsapp.send_file_full("123", media_base64=b64(), filename="photo", mime_type="image/png")
        self.assertTrue(ok, msg)
        self.assertEqual(self.seen["name"], "photo.png")

    def test_data_url_prefix_and_line_breaks_are_accepted(self):
        text = "data:image/png;base64," + b64()[:20] + "\n" + b64()[20:]
        ok, msg, _ = whatsapp.send_file_full("123", media_base64=text, filename="a.png")
        self.assertTrue(ok, msg)
        self.assertEqual(self.seen["bytes"], PNG)

    def test_no_media_dir_refuses(self):
        with patch.object(whatsapp, "MEDIA_DIR", ""):
            ok, msg, _ = whatsapp.send_file_full("123", media_base64=b64(), filename="a.png")
        self.assertFalse(ok)
        self.assertIn("WHATSAPP_MEDIA_DIR", msg)

    def test_audio_ogg_is_sent_as_is_and_removed(self):
        ok, msg, mid = whatsapp.send_audio_message_full("123", media_base64=b64(b"OggS" + b"0" * 20), filename="v.ogg")
        self.assertTrue(ok, msg)
        self.assertEqual(mid, "3EB0")
        self.assertEqual(self.seen["name"], "v.ogg")
        self.assertEqual(self.left_over(), [])

    def test_audio_is_converted_and_both_files_removed(self):
        def convert(src, directory=None):
            out = os.path.join(directory, "conv.ogg")
            open(out, "wb").write(b"OggS")
            return out

        with patch.object(whatsapp.audio, "convert_to_opus_ogg_temp", side_effect=convert):
            ok, msg, _ = whatsapp.send_audio_message_full("123", media_base64=b64(b"mp3"), filename="v.mp3")
        self.assertTrue(ok, msg)
        self.assertEqual(self.seen["name"], "conv.ogg")
        self.assertEqual(self.left_over(), [])

    def test_send_message_returns_the_id(self):
        ok, msg, mid = whatsapp.send_message_full("123", "hello")
        self.assertEqual((ok, mid), (True, "3EB0"))


if __name__ == "__main__":
    unittest.main()
