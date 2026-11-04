#!/usr/bin/env bash
# Manual test for dev-main features:
#   1. non-text messages (reactions, polls, edits, ...) are stored  (#345)
#   2. send_message can reply to a message in a direct chat          (#277)
#   3. send_message can reply to a message in a group                (#277)
#
# Run from the repo (or anywhere, with WHATSAPP_REPO set) while the bridge is
# running. Uses WHATSAPP_BRIDGE_TOKEN from the environment, or from .env in the
# repo root. Steps 2 and 3 send real WhatsApp messages, and ask before each send.

set -euo pipefail

ROOT="${WHATSAPP_REPO:-$(cd "$(dirname "$0")/.." && pwd)}"
DB="$ROOT/whatsapp-bridge/store/messages.db"
API="http://127.0.0.1:8080/api"
SEP=$'\x1f' # field separator; unlike tab, `read` keeps empty fields with it

# --- setup -------------------------------------------------------------------

if [[ -z "${WHATSAPP_BRIDGE_TOKEN:-}" && -f "$ROOT/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ROOT/.env"
  set +a
fi
if [[ -z "${WHATSAPP_BRIDGE_TOKEN:-}" ]]; then
  echo "WHATSAPP_BRIDGE_TOKEN is not set (and not found in $ROOT/.env)." >&2
  exit 1
fi
for cmd in sqlite3 curl python3; do
  command -v "$cmd" >/dev/null || {
    echo "Missing command: $cmd" >&2
    exit 1
  }
done
[[ -f "$DB" ]] || {
  echo "Message database not found: $DB" >&2
  exit 1
}

http_code=$(curl -s -o /dev/null -w '%{http_code}' \
  -H "X-Bridge-Token: $WHATSAPP_BRIDGE_TOKEN" "$API/send" || true)
if [[ "$http_code" == "000" ]]; then
  echo "The bridge is not reachable at $API. Start it first: cd whatsapp-bridge && go run ." >&2
  exit 1
elif [[ "$http_code" == "401" ]]; then
  echo "The bridge rejected the token. Use the same WHATSAPP_BRIDGE_TOKEN as the bridge." >&2
  exit 1
fi

section() { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }

# Run a query; fields are separated by $SEP.
query() { sqlite3 -separator "$SEP" "$DB" "$1"; }

# Prompt into REPLY; an empty answer or end of input gives "".
ask() {
  REPLY=""
  read -r -p "$1" REPLY || true
}

# Build the JSON body safely (message text may contain quotes etc.).
json_payload() {
  python3 -I -c 'import json, sys
keys = ["recipient", "message", "reply_to_message_id", "reply_to_sender_jid"]
print(json.dumps({k: v for k, v in zip(keys, sys.argv[1:]) if v}))' "$@"
}

send() {
  local payload
  payload=$(json_payload "$@")
  echo "Request: $payload"
  ask "Send this message? [y/N] "
  if [[ "$REPLY" != [yY] ]]; then
    echo "Skipped."
    return
  fi
  curl -s -X POST "$API/send" \
    -H 'Content-Type: application/json' \
    -H "X-Bridge-Token: $WHATSAPP_BRIDGE_TOKEN" \
    -d "$payload"
  echo
  echo "Check WhatsApp: the message should appear as a reply, with the original quoted above it."
}

# Show numbered rows; set PICKED to the chosen row (empty when skipped).
pick() {
  local rows=() line i=1 _id chat sender text
  PICKED=""
  # Process substitution, not a pipe: a pipe would fill `rows` in a subshell.
  while IFS= read -r line; do rows+=("$line"); done < <(query "$1")
  if [[ ${#rows[@]} -eq 0 ]]; then
    echo "No messages found."
    return
  fi
  for line in "${rows[@]}"; do
    IFS="$SEP" read -r _id chat sender text <<<"$line"
    printf '%2d) %-28s %-16s %s\n' "$i" "$chat" "${sender:--}" "$text"
    i=$((i + 1))
  done
  ask "Pick a message to reply to (number, Enter to skip): "
  if [[ "$REPLY" =~ ^[0-9]+$ ]] && ((REPLY >= 1 && REPLY <= ${#rows[@]})); then
    PICKED="${rows[$((REPLY - 1))]}"
  fi
}

# id, chat, sender, and the first 50 characters of the text on one line
COLS="id, chat_jid, sender, substr(replace(replace(content, char(10), ' '), char(9), ' '), 1, 50)"

# --- 1. non-text messages ------------------------------------------------------

section "1. Stored non-text messages (#345)"
echo "React to a message, edit one or send a poll first; they show up here as [..] text."
echo
rows=$(sqlite3 -separator $'\t' "$DB" \
  "SELECT timestamp, chat_jid, sender, substr(replace(content, char(10), ' '), 1, 70)
   FROM messages WHERE content LIKE '[%' ORDER BY timestamp DESC LIMIT 20;")
if [[ -n "$rows" ]]; then
  column -t -s $'\t' <<<"$rows"
else
  echo "None stored yet."
fi

# --- 2. reply in a direct chat ---------------------------------------------------

section "2. Reply in a direct chat (#277)"
echo "Tip: use your own \"Message yourself\" chat so nobody else gets the test."
pick "SELECT $COLS FROM messages
      WHERE chat_jid NOT LIKE '%@g.us' AND content != ''
      ORDER BY timestamp DESC LIMIT 10;"
if [[ -n "$PICKED" ]]; then
  IFS="$SEP" read -r msg_id chat _sender _text <<<"$PICKED"
  ask "Reply text [Test reply]: "
  send "$chat" "${REPLY:-Test reply}" "$msg_id" ""
fi

# --- 3. reply in a group -----------------------------------------------------------

section "3. Reply in a group (#277)"
pick "SELECT $COLS FROM messages
      WHERE chat_jid LIKE '%@g.us' AND is_from_me = 0 AND content != ''
      ORDER BY timestamp DESC LIMIT 10;"
if [[ -n "$PICKED" ]]; then
  IFS="$SEP" read -r msg_id chat sender _text <<<"$PICKED"
  # Older rows store only the user part of the sender, newer ones the full JID.
  # Phone numbers get @s.whatsapp.net; LIDs (longer, not a phone number) get @lid.
  if [[ "$sender" == *@* ]]; then
    default_jid="$sender"
  elif [[ ${#sender} -gt 13 ]]; then
    default_jid="$sender@lid"
  else
    default_jid="$sender@s.whatsapp.net"
  fi
  ask "Original sender JID [$default_jid]: "
  sender_jid="${REPLY:-$default_jid}"
  ask "Reply text [Test group reply]: "
  send "$chat" "${REPLY:-Test group reply}" "$msg_id" "$sender_jid"
fi

echo
echo "Done."
