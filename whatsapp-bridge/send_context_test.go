package main

import (
	"reflect"
	"testing"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeMentionJIDs(t *testing.T) {
	got := normalizeMentionJIDs([]string{"31612345678", " @31687654321 ", "", "123@lid", "456@s.whatsapp.net"})
	want := []string{"31612345678@s.whatsapp.net", "31687654321@s.whatsapp.net", "123@lid", "456@s.whatsapp.net"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildContextInfo(t *testing.T) {
	if got := buildContextInfo("", "", nil); got != nil {
		t.Errorf("no reply or mentions: got %v, want nil", got)
	}
	if got := buildContextInfo("", "", []string{" ", "@"}); got != nil {
		t.Errorf("only empty mentions: got %v, want nil", got)
	}

	c := buildContextInfo("MSG1", "111@lid", []string{"222"})
	if c.GetStanzaID() != "MSG1" || c.GetParticipant() != "111@lid" {
		t.Errorf("reply fields: got %q %q", c.GetStanzaID(), c.GetParticipant())
	}
	if !reflect.DeepEqual(c.GetMentionedJID(), []string{"222@s.whatsapp.net"}) {
		t.Errorf("mentions: got %v", c.GetMentionedJID())
	}

	c = buildContextInfo("", "", []string{"222"})
	if c.StanzaID != nil || c.Participant != nil {
		t.Errorf("mention only must not look like a reply: %v", c)
	}
}

func TestAttachContextInfo(t *testing.T) {
	ctx := buildContextInfo("MSG1", "", []string{"222"})

	text := &waProto.Message{Conversation: proto.String("hi @222")}
	attachContextInfo(text, "hi @222", ctx)
	if text.Conversation != nil || text.GetExtendedTextMessage().GetText() != "hi @222" ||
		text.GetExtendedTextMessage().GetContextInfo() != ctx {
		t.Errorf("plain text not turned into ExtendedTextMessage with context: %v", text)
	}

	image := &waProto.Message{ImageMessage: &waProto.ImageMessage{Caption: proto.String("look @222")}}
	attachContextInfo(image, "look @222", ctx)
	if image.GetImageMessage().GetContextInfo() != ctx || image.ExtendedTextMessage != nil {
		t.Errorf("caption context not set on the image: %v", image)
	}

	plain := &waProto.Message{Conversation: proto.String("hi")}
	attachContextInfo(plain, "hi", nil)
	if plain.GetConversation() != "hi" || plain.ExtendedTextMessage != nil {
		t.Errorf("nil context changed the message: %v", plain)
	}
}
