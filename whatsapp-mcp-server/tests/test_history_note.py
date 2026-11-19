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


class PlaceholderAndPnTests(unittest.TestCase):
    def make(self, d, placeholder_col=True):
        db = str(Path(d) / "messages.db")
        wa = str(Path(d) / "whatsapp.db")
        conn = sqlite3.connect(db)
        conn.execute("CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP)")
        conn.execute(
            "CREATE TABLE messages (id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP, "
            "is_from_me BOOLEAN, media_type TEXT, quoted_message_id TEXT"
            + (", placeholder INTEGER NOT NULL DEFAULT 0" if placeholder_col else "")
            + ", PRIMARY KEY (id, chat_jid))")
        conn.execute("INSERT INTO chats VALUES ('239@lid', 'Cris', '2026-11-12 08:00:00+00:00')")
        conn.execute("INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) "
                     "VALUES ('R', '239@lid', 'me', 'real words', '2026-11-12 07:00:00+00:00', 1)")
        if placeholder_col:
            conn.execute("INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, placeholder) "
                         "VALUES ('P', '239@lid', 'me', '[message could not be decrypted yet]', '2026-11-12 08:00:00+00:00', 1, 1)")
        conn.commit()
        conn.close()
        w = sqlite3.connect(wa)
        w.execute("CREATE TABLE whatsmeow_contacts (their_jid TEXT, first_name TEXT, full_name TEXT, push_name TEXT, business_name TEXT)")
        w.execute("CREATE TABLE whatsmeow_lid_map (lid TEXT, pn TEXT)")
        w.execute("INSERT INTO whatsmeow_lid_map VALUES ('239', '3162')")
        w.commit()
        w.close()
        return db, wa

    def run_with(self, func, **kw):
        with tempfile.TemporaryDirectory() as d:
            db, wa = self.make(d, **kw)
            with patch.object(whatsapp, "MESSAGES_DB_PATH", db), patch.object(whatsapp, "WHATSAPP_DB_PATH", wa), \
                 patch.object(whatsapp, "_resolver_cache", (0.0, None)):
                return func()

    def test_phone_number_jid_finds_the_lid_chat(self):
        out = self.run_with(lambda: whatsapp.list_messages(chat_jid="3162@s.whatsapp.net", include_context=False))
        self.assertIn("real words", out)

    def test_placeholder_is_marked(self):
        out = self.run_with(lambda: whatsapp.list_messages(chat_jid="239@lid", include_context=False))
        self.assertIn("(placeholder: the bridge could not read this message", out)
        real = [l for l in out.splitlines() if "real words" in l][0]
        self.assertNotIn("placeholder", real)

    def test_old_database_without_the_column_still_lists(self):
        out = self.run_with(lambda: whatsapp.list_messages(chat_jid="239@lid", include_context=False), placeholder_col=False)
        self.assertIn("real words", out)

    def test_filtered_query_says_stats_cover_the_whole_chat(self):
        out = self.run_with(lambda: whatsapp.list_messages(chat_jid="239@lid", after="2026-11-12T06:00:00", include_context=False))
        self.assertIn("whole stored history", out)
        out = self.run_with(lambda: whatsapp.list_messages(chat_jid="239@lid", include_context=False))
        self.assertNotIn("whole stored history", out)
