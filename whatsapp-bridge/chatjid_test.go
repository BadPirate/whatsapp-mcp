package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type fakePNtoLID map[string]string

func (f fakePNtoLID) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	if l, ok := f[pn.User]; ok {
		return types.NewJID(l, types.HiddenUserServer), nil
	}
	return types.EmptyJID, errors.New("none")
}

func TestCanonicalChatJID(t *testing.T) {
	ctx := context.Background()
	pn := types.NewJID("31629806991", types.DefaultUserServer)
	lid := types.NewJID("239062275358755", types.HiddenUserServer)
	r := fakePNtoLID{"31629806991": "239062275358755"}

	if got := canonicalChatJID(ctx, r, pn, types.EmptyJID); got != lid {
		t.Errorf("mapped PN: got %v", got)
	}
	if got := canonicalChatJID(ctx, nil, pn, lid); got != lid {
		t.Errorf("alt LID wins without a store: got %v", got)
	}
	if got := canonicalChatJID(ctx, r, lid, types.EmptyJID); got != lid {
		t.Errorf("LID stays: got %v", got)
	}
	if got := canonicalChatJID(ctx, fakePNtoLID{}, pn, types.EmptyJID); got != pn {
		t.Errorf("unknown PN stays: got %v", got)
	}
	g := types.NewJID("123-456", types.GroupServer)
	if got := canonicalChatJID(ctx, r, g, lid); got != g {
		t.Errorf("group stays: got %v", got)
	}
}

func newTestStore(t *testing.T) *MessageStore {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.Mkdir("store", 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := NewMessageStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A from-me text stored under the phone-number chat while the replies sit under
// the LID chat is one conversation: merging puts both sides in one chat.
func TestMergePNChats(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Date(2026, 11, 12, 7, 47, 56, 0, time.UTC)
	pn, lid := "31629806991@s.whatsapp.net", "239062275358755@lid"
	if err := s.StoreChat(pn, "Cris", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreChat(lid, "Cris", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.StoreMessage("OUT1", pn, "me", "hello from my phone", t0, true, "", "", "", nil, nil, nil, 0, "")
	s.StoreMessage("IN1", lid, "239062275358755", "reply", t0.Add(time.Hour), false, "", "", "", nil, nil, nil, 0, "")
	s.StoreMessage("IN1", pn, "x", "dup", t0, false, "", "", "", nil, nil, nil, 0, "")

	n, err := s.mergePNChats(context.Background(), fakePNtoLID{"31629806991": "239062275358755"})
	if err != nil || n != 1 {
		t.Fatalf("merge: n=%d err=%v", n, err)
	}
	var cnt int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_jid = ?`, lid).Scan(&cnt)
	if cnt != 2 {
		t.Errorf("messages under LID chat = %d, want 2", cnt)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_jid = ?`, pn).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("messages left under PN chat = %d", cnt)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE jid = ?`, pn).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("PN chat row left")
	}
}

// An older history-sync timestamp must not pull a chat's last activity back.
func TestStoreChatKeepsNewestTime(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Date(2026, 11, 12, 7, 0, 0, 0, time.UTC)
	s.StoreChat("a@lid", "A", t0.Add(time.Hour))
	s.StoreChat("a@lid", "A", t0)
	var got time.Time
	if err := s.db.QueryRow(`SELECT last_message_time FROM chats WHERE jid = 'a@lid'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if want := t0.Add(time.Hour); !got.Equal(want) {
		t.Errorf("last_message_time = %v, want %v", got, want)
	}
}

// What the owner types on their phone arrives in a DeviceSentMessage in
// history sync payloads; the text must come out, as it does for its media.
func TestExtractTextContentFromEveryShape(t *testing.T) {
	text := &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{
		Text:        proto.String("Hey Cris, Kevin here"),
		MatchedText: proto.String("https://example.com"),
		ContextInfo: &waProto.ContextInfo{StanzaID: proto.String("Q1")},
		Title:       proto.String("preview"),
		Description: proto.String("preview body"),
		PreviewType: waProto.ExtendedTextMessage_NONE.Enum(),
	}}
	dsm := &waProto.Message{DeviceSentMessage: &waProto.DeviceSentMessage{
		DestinationJID: proto.String("239062275358755@lid"),
		Message:        text,
	}}
	cases := map[string]struct {
		msg  *waProto.Message
		want string
	}{
		"device sent":              {dsm, "Hey Cris, Kevin here"},
		"device sent conversation": {&waProto.Message{DeviceSentMessage: &waProto.DeviceSentMessage{Message: &waProto.Message{Conversation: proto.String("hi")}}}, "hi"},
		"ephemeral device sent":    {&waProto.Message{EphemeralMessage: &waProto.FutureProofMessage{Message: dsm}}, "Hey Cris, Kevin here"},
		"link preview":             {text, "Hey Cris, Kevin here"},
		"edit of device sent":      {&waProto.Message{ProtocolMessage: &waProto.ProtocolMessage{Type: waProto.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: dsm}}, "[edited] Hey Cris, Kevin here"},
		"buttons response":         {&waProto.Message{ButtonsResponseMessage: &waProto.ButtonsResponseMessage{Response: &waProto.ButtonsResponseMessage_SelectedDisplayText{SelectedDisplayText: "Yes"}}}, "Yes"},
		"list response":            {&waProto.Message{ListResponseMessage: &waProto.ListResponseMessage{Title: proto.String("Option A")}}, "Option A"},
	}
	for name, c := range cases {
		if got := extractTextContent(c.msg); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestHasUserVisiblePayload(t *testing.T) {
	if hasUserVisiblePayload(&waProto.Message{SenderKeyDistributionMessage: &waProto.SenderKeyDistributionMessage{}}) {
		t.Error("key distribution is plumbing")
	}
	if hasUserVisiblePayload(nil) {
		t.Error("nil")
	}
	if !hasUserVisiblePayload(&waProto.Message{OrderMessage: &waProto.OrderMessage{}}) {
		t.Error("an order is visible")
	}
}

func TestMergeKeepsRealMessageOverPlaceholder(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Date(2026, 11, 12, 7, 47, 56, 0, time.UTC)
	pn, lid := "31629806991@s.whatsapp.net", "239062275358755@lid"
	s.StoreChat(pn, "Cris", t0)
	s.StoreChat(lid, "239062275358755", t0)
	s.StoreMessage("M1", pn, "me", "the real text", t0, true, "", "", "", nil, nil, nil, 0, "Q1")
	if err := s.StorePlaceholder("M1", lid, "me", "[message could not be decrypted yet]", t0, true); err != nil {
		t.Fatal(err)
	}
	// A placeholder in the phone-number chat never beats a real LID row.
	s.StoreMessage("M2", lid, "x", "real lid", t0, false, "", "", "", nil, nil, nil, 0, "")
	s.StorePlaceholder("M2", pn, "x", "[unsupported message]", t0, false)

	if _, err := s.mergePNChats(context.Background(), fakePNtoLID{"31629806991": "239062275358755"}); err != nil {
		t.Fatal(err)
	}
	var content string
	var ph int
	var quoted *string
	s.db.QueryRow(`SELECT content, placeholder, quoted_message_id FROM messages WHERE id='M1' AND chat_jid=?`, lid).Scan(&content, &ph, &quoted)
	if content != "the real text" || ph != 0 || quoted == nil || *quoted != "Q1" {
		t.Errorf("M1 = %q placeholder=%d quoted=%v", content, ph, quoted)
	}
	s.db.QueryRow(`SELECT content, placeholder FROM messages WHERE id='M2' AND chat_jid=?`, lid).Scan(&content, &ph)
	if content != "real lid" || ph != 0 {
		t.Errorf("M2 = %q placeholder=%d", content, ph)
	}
	var name string
	s.db.QueryRow(`SELECT name FROM chats WHERE jid=?`, lid).Scan(&name)
	if name != "Cris" {
		t.Errorf("digit-only LID name should give way to the PN chat's: %q", name)
	}
}

func TestRealMessageReplacesPlaceholder(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Now().UTC()
	s.StoreChat("a@lid", "A", t0)
	s.StorePlaceholder("X", "a@lid", "me", "[message could not be decrypted yet]", t0, true)
	s.StoreMessage("X", "a@lid", "me", "hello", t0, true, "", "", "", nil, nil, nil, 0, "")
	var content string
	var ph int
	s.db.QueryRow(`SELECT content, placeholder FROM messages WHERE id='X'`).Scan(&content, &ph)
	if content != "hello" || ph != 0 {
		t.Errorf("got %q placeholder=%d", content, ph)
	}
}

func TestMergeKeepsRealLIDName(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Now().UTC()
	s.StoreChat("1@s.whatsapp.net", "Old PN name", t0)
	s.StoreChat("2@lid", "Real Name", t0)
	s.mergePNChats(context.Background(), fakePNtoLID{"1": "2"})
	var name string
	s.db.QueryRow(`SELECT name FROM chats WHERE jid='2@lid'`).Scan(&name)
	if name != "Real Name" {
		t.Errorf("name = %q", name)
	}
}

func TestMergeThrottle(t *testing.T) {
	m := &mergeThrottle{every: time.Minute}
	t0 := time.Now()
	if !m.allow(t0) || m.allow(t0.Add(30*time.Second)) || !m.allow(t0.Add(61*time.Second)) {
		t.Error("throttle")
	}
	pn := types.NewJID("1", types.DefaultUserServer)
	lid := types.NewJID("2", types.HiddenUserServer)
	if !seesBothAddresses(pn, lid) || !seesBothAddresses(lid, pn) || seesBothAddresses(pn, types.EmptyJID) || seesBothAddresses(lid, lid) {
		t.Error("seesBothAddresses")
	}
}

func TestPlumbingIsNotAUserVisibleMessage(t *testing.T) {
	for name, m := range map[string]*waProto.Message{
		"poll vote":    {PollUpdateMessage: &waProto.PollUpdateMessage{}},
		"enc reaction": {EncReactionMessage: &waProto.EncReactionMessage{}},
		"keep in chat": {KeepInChatMessage: &waProto.KeepInChatMessage{}},
		"pin":          {PinInChatMessage: &waProto.PinInChatMessage{}},
		"call":         {Call: &waProto.Call{}},
	} {
		if hasUserVisiblePayload(m) {
			t.Errorf("%s should not be stored as unsupported", name)
		}
	}
}
