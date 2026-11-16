package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// Contacts. What WhatsApp itself says about a number comes from whatsmeow:
// IsOnWhatsApp asks the server, and the contact store holds the names the
// phone synced (saved name) and the ones people chose for themselves (push
// name). whatsmeow cannot write a contact back to the phone's address book,
// so "edit" here means a local name override kept in messages.db. It wins over
// every other name in the MCP server's chat lists and searches, and the phone
// never sees it.

const (
	maxContactNameLen   = 100
	maxCheckNumbers     = 20
	defaultContactLimit = 50
	maxContactLimit     = 200
)

// ContactView is one contact as the bridge reports it.
type ContactView struct {
	JID          string `json:"jid"`
	PhoneNumber  string `json:"phone_number,omitempty"`
	Name         string `json:"name,omitempty"` // the name shown: local, else saved, else push, else business
	LocalName    string `json:"local_name,omitempty"`
	SavedName    string `json:"saved_name,omitempty"`
	PushName     string `json:"push_name,omitempty"`
	BusinessName string `json:"business_name,omitempty"`
}

// NumberCheck is the answer for one number in a check.
type NumberCheck struct {
	Query        string `json:"query"`
	IsOnWhatsApp bool   `json:"is_on_whatsapp"`
	JID          string `json:"jid,omitempty"`
	PhoneNumber  string `json:"phone_number,omitempty"`
	VerifiedName string `json:"verified_name,omitempty"`
	Error        string `json:"error,omitempty"`
}

type ListContactsRequest struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type CheckNumbersRequest struct {
	Phones []string `json:"phones"`
}

// SetContactNameRequest names a contact. Contact is a phone number or a JID;
// an empty name removes the local name.
type SetContactNameRequest struct {
	Contact string `json:"contact"`
	Name    string `json:"name"`
}

type contactStoreReader interface {
	GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error)
}

type phoneResolver interface {
	GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error)
}

type numberChecker interface {
	IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error)
}

// ensureContactTables creates the table of local names.
func ensureContactTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS contact_names (
			jid TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	return err
}

// normalizePhone turns "+31 6 5327-5958" into "+31653275958". It accepts
// spaces, dashes, dots and brackets between digits, and needs 6 to 15 digits
// with no leading zero (an international number).
func normalizePhone(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "+")
	var digits strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case strings.ContainsRune(" -.()", r):
		default:
			return "", fmt.Errorf("%q is not a phone number: use international format such as +31653275958", s)
		}
	}
	d := digits.String()
	if len(d) < 6 || len(d) > 15 || d[0] == '0' {
		return "", fmt.Errorf("%q is not an international phone number (6 to 15 digits, country code first)", s)
	}
	return "+" + d, nil
}

// parseContactTarget takes a phone number or a user JID (phone or LID).
func parseContactTarget(s string) (types.JID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return types.EmptyJID, errors.New("contact is required")
	}
	if strings.Contains(s, "@") {
		jid, err := types.ParseJID(s)
		if err != nil {
			return types.EmptyJID, fmt.Errorf("invalid JID: %v", err)
		}
		if jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer {
			return types.EmptyJID, errors.New("only a person's JID (@s.whatsapp.net or @lid) can be named; groups keep their own subject")
		}
		return jid.ToNonAD(), nil
	}
	phone, err := normalizePhone(s)
	if err != nil {
		return types.EmptyJID, err
	}
	return types.NewJID(strings.TrimPrefix(phone, "+"), types.DefaultUserServer), nil
}

// cleanContactName trims a name and refuses one that is too long or holds
// characters that do not show on screen.
func cleanContactName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxContactNameLen {
		return "", fmt.Errorf("name is longer than %d characters", maxContactNameLen)
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", errors.New("name has a control or invisible character")
		}
	}
	return name, nil
}

func (store *MessageStore) localContactNames() (map[string]string, error) {
	rows, err := store.db.Query(`SELECT jid, name FROM contact_names`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var jid, name string
		if err := rows.Scan(&jid, &name); err != nil {
			return nil, err
		}
		out[jid] = name
	}
	return out, rows.Err()
}

// setLocalContactName stores or, with an empty name, removes a local name.
func (store *MessageStore) setLocalContactName(jid types.JID, name string) error {
	if name == "" {
		_, err := store.db.Exec(`DELETE FROM contact_names WHERE jid = ?`, jid.String())
		return err
	}
	_, err := store.db.Exec(
		`INSERT INTO contact_names (jid, name, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(jid) DO UPDATE SET name = excluded.name, updated_at = CURRENT_TIMESTAMP`,
		jid.String(), name)
	return err
}

// listContacts returns the contacts whatsmeow knows plus those only named
// locally, sorted by shown name, filtered by a case-insensitive match on name
// or number, and paged.
func listContacts(ctx context.Context, contacts contactStoreReader, lids phoneResolver, store *MessageStore, query string, limit, offset int) ([]ContactView, error) {
	if limit <= 0 {
		limit = defaultContactLimit
	}
	if limit > maxContactLimit {
		limit = maxContactLimit
	}
	if offset < 0 {
		return nil, errors.New("offset must be 0 or greater")
	}
	all, err := contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading contacts: %w", err)
	}
	local, err := store.localContactNames()
	if err != nil {
		return nil, fmt.Errorf("reading local names: %w", err)
	}

	views := make([]ContactView, 0, len(all))
	seen := map[string]bool{}
	build := func(jid types.JID, info types.ContactInfo) ContactView {
		v := ContactView{
			JID:          jid.String(),
			LocalName:    local[jid.String()],
			SavedName:    firstNonEmpty(info.FullName, info.FirstName),
			PushName:     info.PushName,
			BusinessName: info.BusinessName,
		}
		v.Name = firstNonEmpty(v.LocalName, v.SavedName, v.PushName, v.BusinessName)
		switch jid.Server {
		case types.DefaultUserServer:
			v.PhoneNumber = jid.User
		case types.HiddenUserServer:
			if lids != nil {
				if pn, err := lids.GetPNForLID(ctx, jid); err == nil && !pn.IsEmpty() {
					v.PhoneNumber = pn.User
				}
			}
		}
		return v
	}
	for jid, info := range all {
		if jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer {
			continue
		}
		seen[jid.String()] = true
		views = append(views, build(jid, info))
	}
	for jidStr := range local {
		if seen[jidStr] {
			continue
		}
		if jid, err := types.ParseJID(jidStr); err == nil {
			views = append(views, build(jid, types.ContactInfo{}))
		}
	}

	q := strings.ToLower(strings.TrimSpace(query))
	qDigits := strings.NewReplacer("+", "", " ", "", "-", "").Replace(q)
	if q != "" {
		kept := views[:0]
		for _, v := range views {
			haystack := strings.ToLower(strings.Join([]string{v.LocalName, v.SavedName, v.PushName, v.BusinessName}, "\n"))
			if strings.Contains(haystack, q) ||
				(qDigits != "" && (strings.Contains(v.PhoneNumber, qDigits) || strings.Contains(v.JID, qDigits))) {
				kept = append(kept, v)
			}
		}
		views = kept
	}
	sort.Slice(views, func(i, j int) bool {
		a, b := strings.ToLower(views[i].Name), strings.ToLower(views[j].Name)
		if (a == "") != (b == "") {
			return b == "" // named contacts first
		}
		if a != b {
			return a < b
		}
		return views[i].JID < views[j].JID
	})
	if offset >= len(views) {
		return []ContactView{}, nil
	}
	views = views[offset:]
	if len(views) > limit {
		views = views[:limit]
	}
	return views, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// checkNumbers asks WhatsApp which of the numbers are registered. A number
// that cannot be a phone number is answered with an error entry, and never
// sent to WhatsApp.
func checkNumbers(ctx context.Context, checker numberChecker, phones []string) ([]NumberCheck, error) {
	if len(phones) == 0 {
		return nil, errors.New("phones is required")
	}
	if len(phones) > maxCheckNumbers {
		return nil, fmt.Errorf("at most %d numbers per check", maxCheckNumbers)
	}
	results := make([]NumberCheck, len(phones))
	var valid []string
	var validAt []int
	for i, p := range phones {
		norm, err := normalizePhone(p)
		if err != nil {
			results[i] = NumberCheck{Query: p, Error: err.Error()}
			continue
		}
		results[i] = NumberCheck{Query: norm}
		valid = append(valid, norm)
		validAt = append(validAt, i)
	}
	if len(valid) == 0 {
		return results, nil
	}
	resp, err := checker.IsOnWhatsApp(ctx, valid)
	if err != nil {
		return nil, fmt.Errorf("asking WhatsApp: %w", err)
	}
	byQuery := map[string]types.IsOnWhatsAppResponse{}
	for _, r := range resp {
		byQuery[strings.TrimPrefix(r.Query, "+")] = r
	}
	for k, i := range validAt {
		r, ok := byQuery[strings.TrimPrefix(valid[k], "+")]
		if !ok {
			continue // WhatsApp did not answer for it: not registered
		}
		results[i].IsOnWhatsApp = r.IsIn
		if r.IsIn {
			results[i].JID = r.JID.String()
			if !r.PhoneNumber.IsEmpty() {
				results[i].PhoneNumber = r.PhoneNumber.User
			}
			if r.VerifiedName != nil && r.VerifiedName.Details != nil {
				results[i].VerifiedName = r.VerifiedName.Details.GetVerifiedName()
			}
		}
	}
	return results, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"success": false, "message": msg})
}

// registerContactHandlers adds POST /api/contacts/list, /api/contacts/check
// and /api/contacts/set. They sit behind the same token check as the other
// endpoints.
func registerContactHandlers(mux *http.ServeMux, client *whatsmeow.Client, messageStore *MessageStore) {
	post := func(path string, handle func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handle(w, r)
		})
	}

	post("/api/contacts/list", func(w http.ResponseWriter, r *http.Request) {
		var req ListContactsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "Invalid request format")
			return
		}
		contacts, err := listContacts(r.Context(), client.Store.Contacts, client.Store.LIDs, messageStore, req.Query, req.Limit, req.Offset)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "contacts": contacts})
	})

	post("/api/contacts/check", func(w http.ResponseWriter, r *http.Request) {
		var req CheckNumbersRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "Invalid request format")
			return
		}
		if len(req.Phones) == 0 || len(req.Phones) > maxCheckNumbers {
			writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("phones needs 1 to %d numbers", maxCheckNumbers))
			return
		}
		if !client.IsConnected() {
			writeAPIError(w, http.StatusServiceUnavailable, "Not connected to WhatsApp")
			return
		}
		results, err := checkNumbers(r.Context(), client, req.Phones)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "results": results})
	})

	post("/api/contacts/set", func(w http.ResponseWriter, r *http.Request) {
		var req SetContactNameRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "Invalid request format")
			return
		}
		jid, err := parseContactTarget(req.Contact)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		name, err := cleanContactName(req.Name)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := messageStore.setLocalContactName(jid, name); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "Error saving the name: "+err.Error())
			return
		}
		fmt.Println("Contact name changed for", jid.String())
		msg := fmt.Sprintf("Named %s %q (kept locally; WhatsApp and the phone are unchanged)", jid.String(), name)
		if name == "" {
			msg = fmt.Sprintf("Removed the local name of %s", jid.String())
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": msg, "jid": jid.String()})
	})
}
