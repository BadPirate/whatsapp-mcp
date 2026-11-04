package main

import (
	"database/sql"
	"os"
	"testing"
	"time"
)

// A bridge built once with -tags sqlite_fts5 leaves FTS triggers in
// messages.db. Started later without the tag, those triggers made every
// message insert fail with "no such module: fts5". NewMessageStore must leave
// the database in a state where storing messages works, with or without FTS5.
// Run this both as `go test ./...` and `go test -tags sqlite_fts5 ./...`.
func TestStoreMessageWorksWithLeftoverFTSTriggers(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("store", 0o700); err != nil {
		t.Fatal(err)
	}

	// Plant the schema and a trigger as a tagged build would have left it.
	db, err := sql.Open("sqlite3", "file:store/messages.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE messages (
			id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
			is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT,
			media_key BLOB, file_sha256 BLOB, file_enc_sha256 BLOB, file_length INTEGER,
			PRIMARY KEY (id, chat_jid)
		);
		CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
		END;`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	store, err := NewMessageStore()
	if err != nil {
		t.Fatalf("NewMessageStore: %v", err)
	}
	defer store.Close()

	now := time.Now()
	if err := store.StoreChat("123@s.whatsapp.net", "Test", now); err != nil {
		t.Fatalf("StoreChat: %v", err)
	}
	if err := store.StoreMessage("m1", "123@s.whatsapp.net", "123", "hello meeting", now, false,
		"", "", "", nil, nil, nil, 0); err != nil {
		t.Fatalf("StoreMessage failed; leftover FTS triggers break inserts: %v", err)
	}

	// With FTS5 available the new message must be searchable by prefix.
	var hasTrigger int
	store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='messages_ai'`).Scan(&hasTrigger)
	if hasTrigger == 1 {
		var n int
		if err := store.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '"meet"*'`).Scan(&n); err != nil {
			t.Fatalf("FTS query: %v", err)
		}
		if n != 1 {
			t.Errorf("FTS prefix search found %d messages, want 1", n)
		}
	}
}
