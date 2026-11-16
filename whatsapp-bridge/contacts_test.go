package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waVnameCert"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type fakeContacts map[types.JID]types.ContactInfo

func (f fakeContacts) GetAllContacts(context.Context) (map[types.JID]types.ContactInfo, error) {
	return f, nil
}

type fakeChecker struct {
	asked []string
	resp  []types.IsOnWhatsAppResponse
	err   error
}

func (f *fakeChecker) IsOnWhatsApp(_ context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	f.asked = append(f.asked, phones...)
	return f.resp, f.err
}

func newContactTestStore(t *testing.T) *MessageStore {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.Mkdir("store", 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewMessageStore()
	if err != nil {
		t.Fatalf("NewMessageStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestNormalizePhone(t *testing.T) {
	good := map[string]string{
		"+31653275958":        "+31653275958",
		"31 6 5327-5958":      "+31653275958",
		" +1 (469) 999.1654 ": "+14699991654",
	}
	for in, want := range good {
		if got, err := normalizePhone(in); err != nil || got != want {
			t.Errorf("normalizePhone(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "12345", "0653275958", "+31abc", "1234567890123456", "31653275958@s.whatsapp.net"} {
		if _, err := normalizePhone(in); err == nil {
			t.Errorf("normalizePhone(%q): want an error", in)
		}
	}
}

func TestParseContactTarget(t *testing.T) {
	good := map[string]string{
		"+31653275958":                  "31653275958@s.whatsapp.net",
		"31653275958@s.whatsapp.net":    "31653275958@s.whatsapp.net",
		"31653275958:12@s.whatsapp.net": "31653275958@s.whatsapp.net",
		"123456789@lid":                 "123456789@lid",
	}
	for in, want := range good {
		if got, err := parseContactTarget(in); err != nil || got.String() != want {
			t.Errorf("parseContactTarget(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"", "120363@g.us", "x@broadcast", "nonsense"} {
		if _, err := parseContactTarget(in); err == nil {
			t.Errorf("parseContactTarget(%q): want an error", in)
		}
	}
}

func TestCleanContactName(t *testing.T) {
	if got, err := cleanContactName("  Frela (water heater) "); err != nil || got != "Frela (water heater)" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := cleanContactName("   "); err != nil || got != "" {
		t.Errorf("blank name should clear: %q, %v", got, err)
	}
	for _, bad := range []string{strings.Repeat("a", 101), "a\nb", "a‮b", "a​b"} {
		if _, err := cleanContactName(bad); err == nil {
			t.Errorf("cleanContactName(%q): want an error", bad)
		}
	}
}

func TestLocalNameWinsInList(t *testing.T) {
	store := newContactTestStore(t)
	frela := types.NewJID("31653275958", types.DefaultUserServer)
	anna := types.NewJID("14699991654", types.DefaultUserServer)
	contacts := fakeContacts{
		frela: {Found: true, PushName: "Frela Verwarming"},
		anna:  {Found: true, FullName: "Anna Smith", PushName: "annie"},
		types.NewJID("120363", types.GroupServer): {FullName: "not a person"},
	}
	list := func(query string, limit, offset int) []ContactView {
		t.Helper()
		got, err := listContacts(context.Background(), contacts, nil, store, query, limit, offset)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := list("", 0, 0)
	if len(got) != 2 || got[0].Name != "Anna Smith" || got[1].Name != "Frela Verwarming" {
		t.Fatalf("unexpected list: %+v", got)
	}

	if err := store.setLocalContactName(frela, "Frela (water heater)"); err != nil {
		t.Fatal(err)
	}
	got = list("water", 0, 0)
	if len(got) != 1 || got[0].JID != frela.String() || got[0].Name != "Frela (water heater)" ||
		got[0].LocalName != "Frela (water heater)" || got[0].PushName != "Frela Verwarming" || got[0].PhoneNumber != "31653275958" {
		t.Fatalf("local name not applied: %+v", got)
	}
	if got = list("+31 653", 0, 0); len(got) != 1 {
		t.Errorf("search by number: %+v", got)
	}
	if got = list("", 1, 1); len(got) != 1 || got[0].JID != frela.String() {
		t.Errorf("paging: %+v", got)
	}
	if got = list("", 5, 9); len(got) != 0 {
		t.Errorf("offset past the end: %+v", got)
	}

	// A local name for a number whatsmeow has not seen still lists.
	stranger := types.NewJID("4915112345678", types.DefaultUserServer)
	if err := store.setLocalContactName(stranger, "Neighbour"); err != nil {
		t.Fatal(err)
	}
	if got = list("neighbour", 0, 0); len(got) != 1 || got[0].JID != stranger.String() {
		t.Errorf("local-only contact: %+v", got)
	}

	// An empty name removes the override and the other names show again.
	if err := store.setLocalContactName(frela, ""); err != nil {
		t.Fatal(err)
	}
	if got = list("frela", 0, 0); len(got) != 1 || got[0].LocalName != "" || got[0].Name != "Frela Verwarming" {
		t.Errorf("after clearing: %+v", got)
	}
	if _, err := listContacts(context.Background(), contacts, nil, store, "", 10, -1); err == nil {
		t.Error("negative offset: want an error")
	}
}

func TestCheckNumbers(t *testing.T) {
	checker := &fakeChecker{resp: []types.IsOnWhatsAppResponse{
		{Query: "31653275958", IsIn: true, JID: types.NewJID("31653275958", types.DefaultUserServer),
			VerifiedName: &types.VerifiedName{Details: &waVnameCert.VerifiedNameCertificate_Details{VerifiedName: proto.String("Frela BV")}}},
		{Query: "14699991654", IsIn: false},
	}}
	got, err := checkNumbers(context.Background(), checker, []string{"+31 6 5327 5958", "bogus", "+1 469 999 1654", "+4915100000000"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(checker.asked, ",") != "+31653275958,+14699991654,+4915100000000" {
		t.Errorf("asked WhatsApp for %v", checker.asked)
	}
	if !got[0].IsOnWhatsApp || got[0].JID != "31653275958@s.whatsapp.net" || got[0].VerifiedName != "Frela BV" {
		t.Errorf("registered number: %+v", got[0])
	}
	if got[1].Error == "" || got[1].IsOnWhatsApp {
		t.Errorf("bogus number: %+v", got[1])
	}
	if got[2].IsOnWhatsApp || got[2].Error != "" {
		t.Errorf("unregistered number: %+v", got[2])
	}
	if got[3].IsOnWhatsApp {
		t.Errorf("number WhatsApp did not answer for: %+v", got[3])
	}

	if _, err := checkNumbers(context.Background(), checker, nil); err == nil {
		t.Error("no numbers: want an error")
	}
	if _, err := checkNumbers(context.Background(), checker, make([]string, maxCheckNumbers+1)); err == nil {
		t.Error("too many numbers: want an error")
	}
	// Only invalid numbers: WhatsApp is not asked.
	checker.asked = nil
	if _, err := checkNumbers(context.Background(), checker, []string{"nope"}); err != nil || len(checker.asked) != 0 {
		t.Errorf("invalid only: %v, asked %v", err, checker.asked)
	}
	failing := &fakeChecker{err: errors.New("offline")}
	if _, err := checkNumbers(context.Background(), failing, []string{"+31653275958"}); err == nil {
		t.Error("WhatsApp failure: want an error")
	}
}
