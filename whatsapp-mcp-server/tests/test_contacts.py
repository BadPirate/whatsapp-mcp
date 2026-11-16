"""Tests for the contact tools: listing, checking numbers and local names."""
import asyncio
import os
import sqlite3
import sys
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))


@pytest.fixture(autouse=True)
def bridge_token(monkeypatch):
    import whatsapp
    monkeypatch.setattr(whatsapp, "BRIDGE_TOKEN", "t" * 32)


def reply(status=200, body=None):
    response = MagicMock()
    response.status_code = status
    response.json.return_value = body
    return response


class TestBridgeCalls:
    @patch("whatsapp.requests.post")
    def test_list_contacts_pages_into_offset(self, mock_post):
        import whatsapp
        mock_post.return_value = reply(body={"success": True, "contacts": [{"jid": "1@s.whatsapp.net"}]})
        ok, body = whatsapp.list_contacts("anna", limit=10, page=2)
        assert ok and body["contacts"][0]["jid"] == "1@s.whatsapp.net"
        assert mock_post.call_args[0][0].endswith("/contacts/list")
        assert mock_post.call_args[1]["json"] == {"query": "anna", "limit": 10, "offset": 20}
        assert mock_post.call_args[1]["headers"]["X-Bridge-Token"] == "t" * 32

    @pytest.mark.parametrize("limit,page", [(0, 0), (201, 0), (10, -1)])
    @patch("whatsapp.requests.post")
    def test_list_contacts_rejects_bad_paging(self, mock_post, limit, page):
        import whatsapp
        ok, body = whatsapp.list_contacts("", limit=limit, page=page)
        assert not ok and body["message"]
        mock_post.assert_not_called()

    @patch("whatsapp.requests.post")
    def test_check_numbers(self, mock_post):
        import whatsapp
        mock_post.return_value = reply(body={"success": True, "results": [{"query": "+31653275958", "is_on_whatsapp": True}]})
        ok, body = whatsapp.check_numbers_on_whatsapp(["+31653275958"])
        assert ok and body["results"][0]["is_on_whatsapp"] is True
        assert mock_post.call_args[0][0].endswith("/contacts/check")
        assert mock_post.call_args[1]["json"] == {"phones": ["+31653275958"]}

    @patch("whatsapp.requests.post")
    def test_check_numbers_bounds(self, mock_post):
        import whatsapp
        assert not whatsapp.check_numbers_on_whatsapp([])[0]
        assert not whatsapp.check_numbers_on_whatsapp(["+31653275958"] * 21)[0]
        mock_post.assert_not_called()

    @patch("whatsapp.requests.post")
    def test_set_contact_name(self, mock_post):
        import whatsapp
        mock_post.return_value = reply(body={"success": True, "message": "Named"})
        assert whatsapp.set_contact_name("+31653275958", "Frela (water heater)") == (True, "Named")
        assert mock_post.call_args[0][0].endswith("/contacts/set")
        assert mock_post.call_args[1]["json"] == {"contact": "+31653275958", "name": "Frela (water heater)"}

    @patch("whatsapp.requests.post")
    def test_bridge_refusal_is_reported(self, mock_post):
        import whatsapp
        mock_post.return_value = reply(400, {"success": False, "message": "name is longer than 100 characters"})
        ok, message = whatsapp.set_contact_name("+31653275958", "x" * 101)
        assert not ok and "100 characters" in message

    def test_set_contact_name_needs_a_contact(self):
        import whatsapp
        assert whatsapp.set_contact_name("  ", "x")[0] is False


class TestLocalNames:
    def test_local_name_wins_over_saved_and_push(self, tmp_path, monkeypatch):
        import whatsapp
        messages = tmp_path / "messages.db"
        contacts = tmp_path / "whatsapp.db"
        conn = sqlite3.connect(messages)
        conn.execute("CREATE TABLE contact_names (jid TEXT PRIMARY KEY, name TEXT NOT NULL, updated_at TIMESTAMP)")
        conn.execute("INSERT INTO contact_names (jid, name) VALUES ('31653275958@s.whatsapp.net', 'Frela (water heater)')")
        conn.commit(); conn.close()
        conn = sqlite3.connect(contacts)
        conn.execute("CREATE TABLE whatsmeow_contacts (their_jid TEXT, first_name TEXT, full_name TEXT, push_name TEXT, business_name TEXT)")
        conn.execute("CREATE TABLE whatsmeow_lid_map (lid TEXT, pn TEXT)")
        conn.execute("INSERT INTO whatsmeow_contacts VALUES ('31653275958@s.whatsapp.net', '', 'Old Saved', 'Frela Verwarming', '')")
        conn.commit(); conn.close()
        monkeypatch.setattr(whatsapp, "MESSAGES_DB_PATH", str(messages))
        monkeypatch.setattr(whatsapp, "WHATSAPP_DB_PATH", str(contacts))
        monkeypatch.setattr(whatsapp, "_resolver_cache", (0.0, None))
        display_name, _, _ = whatsapp._make_resolvers()
        assert display_name("31653275958@s.whatsapp.net", "31653275958") == "Frela (water heater)"
        assert display_name("999@s.whatsapp.net", "stored") == "stored"

    def test_old_bridge_without_the_table(self, tmp_path, monkeypatch):
        import whatsapp
        sqlite3.connect(tmp_path / "messages.db").close()
        monkeypatch.setattr(whatsapp, "MESSAGES_DB_PATH", str(tmp_path / "messages.db"))
        monkeypatch.setattr(whatsapp, "WHATSAPP_DB_PATH", str(tmp_path / "missing.db"))
        overrides, saved, fallback, lid_to_pn = whatsapp._load_contacts()
        assert overrides == {} and saved == {} and fallback == {} and lid_to_pn == {}


def test_tool_annotations(monkeypatch):
    monkeypatch.delenv("WHATSAPP_MCP_READ_ONLY", raising=False)
    sys.modules.pop("main", None)
    import main
    tools = {t.name: t for t in asyncio.run(main.mcp.list_tools())}
    for name in ("list_contacts", "check_whatsapp_numbers"):
        assert tools[name].annotations.read_only_hint is True, name
        assert tools[name].annotations.destructive_hint is False, name
    edit = tools["set_contact_name"].annotations
    assert edit.read_only_hint is False and edit.destructive_hint is False and edit.idempotent_hint is True
    assert tools["check_whatsapp_numbers"].annotations.open_world_hint is True
    for name in ("list_contacts", "check_whatsapp_numbers", "set_contact_name"):
        schema = tools[name].input_schema
        assert all(p.get("title") is not None or "type" in p or "anyOf" in p for p in schema["properties"].values())
    assert tools["set_contact_name"].input_schema["required"] == ["contact", "name"]
    assert tools["check_whatsapp_numbers"].input_schema["required"] == ["phone_numbers"]
