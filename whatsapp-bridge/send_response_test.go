package main

import (
	"encoding/json"
	"testing"
)

func TestSendMessageResponseCarriesTheMessageID(t *testing.T) {
	b, err := json.Marshal(SendMessageResponse{Success: true, Message: "ok", MessageID: "3EB0ABC"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"success":true,"message":"ok","message_id":"3EB0ABC"}` {
		t.Fatalf("unexpected body: %s", b)
	}
	b, _ = json.Marshal(SendMessageResponse{Success: false, Message: "no"})
	if string(b) != `{"success":false,"message":"no"}` {
		t.Fatalf("an empty id must be left out: %s", b)
	}
}
