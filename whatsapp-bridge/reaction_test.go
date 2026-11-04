package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

// fakeLIDs knows a phone number for the LIDs in its map.
type fakeLIDs struct{ pnForLID map[string]string }

func (f fakeLIDs) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	if pn, ok := f.pnForLID[lid.User]; ok {
		return types.NewJID(pn, types.DefaultUserServer), nil
	}
	return types.EmptyJID, nil
}
func (fakeLIDs) PutManyLIDMappings(context.Context, []store.LIDMapping) error { return nil }
func (fakeLIDs) PutLIDMapping(context.Context, types.JID, types.JID) error    { return nil }
func (fakeLIDs) GetLIDForPN(context.Context, types.JID) (types.JID, error) {
	return types.EmptyJID, nil
}
func (fakeLIDs) GetManyLIDsForPNs(context.Context, []types.JID) (map[types.JID]types.JID, error) {
	return nil, nil
}

func TestResolveStoredSender(t *testing.T) {
	lids := fakeLIDs{pnForLID: map[string]string{"123456789012345": "31612345678"}}
	cases := map[string]string{
		"31612345678":                "31612345678@s.whatsapp.net", // bare phone number
		"123456789012345":            "123456789012345@lid",        // bare LID known to the store
		"999@lid":                    "999@lid",                    // full JID from history sync
		"31687654321@s.whatsapp.net": "31687654321@s.whatsapp.net",
		" 31600000000 ":              "31600000000@s.whatsapp.net",
	}
	for in, want := range cases {
		got, err := resolveStoredSender(context.Background(), lids, in)
		if err != nil || got.String() != want {
			t.Errorf("resolveStoredSender(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	if _, err := resolveStoredSender(context.Background(), lids, ""); err == nil {
		t.Error("empty sender: want an error")
	}
	if got, _ := resolveStoredSender(context.Background(), nil, "31612345678"); got.String() != "31612345678@s.whatsapp.net" {
		t.Errorf("without a LID store: got %v", got)
	}
}

// /api/react must sit behind the same token check as the other endpoints.
func TestReactEndpointRequiresToken(t *testing.T) {
	old := bridgeToken
	bridgeToken = strings.Repeat("t", 32)
	defer func() { bridgeToken = old }()

	mux := http.NewServeMux()
	registerReactHandler(mux, nil, nil)
	srv := requireToken(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/react", strings.NewReader(`{"message_id":"M","chat_jid":"1@s.whatsapp.net","emoji":"👍"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without token: got HTTP %d, want 401", rec.Code)
	}

	// With the token, validation runs (missing fields give 400 before any
	// WhatsApp call is made).
	req = httptest.NewRequest(http.MethodPost, "/api/react", strings.NewReader(`{"emoji":"👍"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bridge-Token", bridgeToken)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("with token, missing fields: got HTTP %d, want 400", rec.Code)
	}
}
