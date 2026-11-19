package main

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/types"
)

// WhatsApp addresses one person two ways: a phone-number JID
// (31...@s.whatsapp.net) and a LID (123...@lid). Which one a message carries
// depends on who sent it and from where, so a from-me message typed on the
// owner's phone, one from history sync and an incoming reply can name the same
// contact differently. Stored as they came, they land in different chats and
// list_messages for the chat shows only one side. Every direct chat is stored
// under the LID once one is known.

// lidResolver is the part of whatsmeow's LID store the bridge needs.
type lidResolver interface {
	GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error)
}

// canonicalChatJID returns the JID a chat is stored under. alt is the other
// address whatsmeow sent with the message (Info.RecipientAlt for a from-me
// message, Info.SenderAlt for an incoming one), empty when it sent none.
// Groups, broadcasts and chats with no known LID keep their own JID.
func canonicalChatJID(ctx context.Context, r lidResolver, jid, alt types.JID) types.JID {
	jid = jid.ToNonAD()
	if jid.Server != types.DefaultUserServer {
		return jid
	}
	if alt.Server == types.HiddenUserServer && alt.User != "" {
		return alt.ToNonAD()
	}
	if r != nil {
		if lid, err := r.GetLIDForPN(ctx, jid); err == nil && lid.User != "" && lid.Server == types.HiddenUserServer {
			return lid.ToNonAD()
		}
	}
	return jid
}

// mergePNChats moves the messages stored under phone-number chats into the
// LID chat of the same contact, for chats stored before canonicalChatJID
// existed. Rows already present under the LID chat win. It returns how many
// chats were merged.
func (store *MessageStore) mergePNChats(ctx context.Context, r lidResolver) (int, error) {
	rows, err := store.db.Query(`SELECT jid, COALESCE(name, ''), last_message_time FROM chats WHERE jid LIKE '%@s.whatsapp.net'`)
	if err != nil {
		return 0, err
	}
	type pnChat struct {
		jid, name string
		last      any
	}
	var chats []pnChat
	for rows.Next() {
		var c pnChat
		if err := rows.Scan(&c.jid, &c.name, &c.last); err != nil {
			rows.Close()
			return 0, err
		}
		chats = append(chats, c)
	}
	rows.Close()

	merged := 0
	for _, c := range chats {
		pn, err := types.ParseJID(c.jid)
		if err != nil {
			continue
		}
		lid := canonicalChatJID(ctx, r, pn, types.EmptyJID)
		if lid.Server != types.HiddenUserServer {
			continue
		}
		if err := store.mergeChatInto(c.jid, lid.String(), c.name, c.last); err != nil {
			return merged, fmt.Errorf("merge %s: %w", c.jid, err)
		}
		merged++
	}
	return merged, nil
}

func (store *MessageStore) mergeChatInto(fromJID, toJID, name string, last any) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)
		 ON CONFLICT(jid) DO UPDATE SET
		   name = CASE WHEN COALESCE(chats.name, '') = '' THEN excluded.name ELSE chats.name END,
		   last_message_time = CASE WHEN chats.last_message_time IS NULL
		     OR datetime(excluded.last_message_time) > datetime(chats.last_message_time)
		     THEN excluded.last_message_time ELSE chats.last_message_time END`,
		toJID, name, last,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE OR IGNORE messages SET chat_jid = ? WHERE chat_jid = ?`, toJID, fromJID); err != nil {
		return err
	}
	// Whatever is left was already stored under the LID chat.
	if _, err := tx.Exec(`DELETE FROM messages WHERE chat_jid = ?`, fromJID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM chats WHERE jid = ?`, fromJID); err != nil {
		return err
	}
	return tx.Commit()
}
