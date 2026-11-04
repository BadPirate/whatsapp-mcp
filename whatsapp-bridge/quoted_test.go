package main

import (
	"database/sql"
	"os"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

func TestExtractQuotedMessageID(t *testing.T) {
	ctx := &waProto.ContextInfo{StanzaID: proto.String("ORIG1")}
	cases := map[string]struct {
		msg  *waProto.Message
		want string
	}{
		"nil":        {nil, ""},
		"plain text": {&waProto.Message{Conversation: proto.String("hi")}, ""},
		"text reply": {&waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String("yes"), ContextInfo: ctx}}, "ORIG1"},
		"mention only, no reply": {&waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String("@1"), ContextInfo: &waProto.ContextInfo{MentionedJID: []string{"1@s.whatsapp.net"}}}}, ""},
		"image reply":    {&waProto.Message{ImageMessage: &waProto.ImageMessage{ContextInfo: ctx}}, "ORIG1"},
		"sticker reply":  {&waProto.Message{StickerMessage: &waProto.StickerMessage{ContextInfo: ctx}}, "ORIG1"},
		"location reply": {&waProto.Message{LocationMessage: &waProto.LocationMessage{ContextInfo: ctx}}, "ORIG1"},
		"inside ephemeral envelope": {&waProto.Message{EphemeralMessage: &waProto.FutureProofMessage{
			Message: &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String("x"), ContextInfo: ctx}}}}, "ORIG1"},
		"document with caption": {&waProto.Message{DocumentWithCaptionMessage: &waProto.FutureProofMessage{
			Message: &waProto.Message{DocumentMessage: &waProto.DocumentMessage{ContextInfo: ctx}}}}, "ORIG1"},
	}
	for name, tc := range cases {
		if got := extractQuotedMessageID(tc.msg); got != tc.want {
			t.Errorf("%s: got %q want %q", name, got, tc.want)
		}
	}
}

// An existing messages.db without quoted_message_id gets the column, and
// replies are stored with the quoted ID while other messages store NULL.
func TestQuotedMessageIDMigrationAndStorage(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("store", 0o700); err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite3", "file:store/messages.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE messages (
		id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
		is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT,
		media_key BLOB, file_sha256 BLOB, file_enc_sha256 BLOB, file_length INTEGER,
		PRIMARY KEY (id, chat_jid))`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	for i := 0; i < 2; i++ { // the second start must not fail on the existing column
		store, err := NewMessageStore()
		if err != nil {
			t.Fatalf("start %d: %v", i+1, err)
		}
		store.Close()
	}

	store, err := NewMessageStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	store.StoreChat("c@s.whatsapp.net", "Chat", now)
	if err := store.StoreMessage("r1", "c@s.whatsapp.net", "1", "a reply", now, false, "", "", "", nil, nil, nil, 0, "ORIG1"); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreMessage("p1", "c@s.whatsapp.net", "1", "not a reply", now, false, "", "", "", nil, nil, nil, 0, ""); err != nil {
		t.Fatal(err)
	}
	var reply, plain sql.NullString
	store.db.QueryRow(`SELECT quoted_message_id FROM messages WHERE id = 'r1'`).Scan(&reply)
	store.db.QueryRow(`SELECT quoted_message_id FROM messages WHERE id = 'p1'`).Scan(&plain)
	if reply.String != "ORIG1" || plain.Valid {
		t.Errorf("got reply=%v plain=%v; want ORIG1 and NULL", reply, plain)
	}
}
