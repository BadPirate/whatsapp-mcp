"""list_messages says its history may be incomplete, and what it covers."""

import sqlite3
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import whatsapp


class HistoryNoteTests(unittest.TestCase):
    def run_with_db(self, rows, func):
        with tempfile.TemporaryDirectory() as d:
            db = str(Path(d) / "messages.db")
            conn = sqlite3.connect(db)
            conn.execute("CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP)")
            conn.execute(
                "CREATE TABLE messages (id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP, "
                "is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT, media_key BLOB, file_sha256 BLOB, "
                "file_enc_sha256 BLOB, file_length INTEGER, quoted_message_id TEXT, PRIMARY KEY (id, chat_jid))")
            conn.execute("INSERT INTO chats VALUES ('c@lid', 'Chat', '2026-11-16 12:02:38+00:00')")
            for i, (ts, me) in enumerate(rows):
                conn.execute(
                    "INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES (?, 'c@lid', 's', 'text', ?, ?)",
                    (f"M{i}", ts, me))
            conn.commit()
            conn.close()
            with patch.object(whatsapp, "MESSAGES_DB_PATH", db), \
                 patch.object(whatsapp, "WHATSAPP_DB_PATH", str(Path(d) / "missing.db")):
                return func()

    def test_note_is_always_there(self):
        out = self.run_with_db([("2026-11-16 12:02:38+00:00", 1)], lambda: whatsapp.list_messages(chat_jid="c@lid"))
        self.assertIn("may be incomplete", out)
        self.assertIn("Stored history of c@lid: 1 messages from 2026-11-16 12:02:38+00:00", out)
        self.assertNotIn("other side", out)

    def test_first_message_from_other_side_is_flagged(self):
        out = self.run_with_db([("2026-11-16 12:02:38+00:00", 0), ("2026-11-16 12:03:00+00:00", 1)],
                               lambda: whatsapp.list_messages(chat_jid="c@lid"))
        self.assertIn("earliest stored message is from the other side", out)

    def test_note_without_chat_filter(self):
        out = self.run_with_db([("2026-11-16 12:02:38+00:00", 0)], lambda: whatsapp.list_messages(query="text"))
        self.assertIn("may be incomplete", out)
        self.assertIn("c@lid", out)


if __name__ == "__main__":
    unittest.main()
