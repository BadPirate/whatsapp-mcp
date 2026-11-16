import os
from typing import Annotated, List, Dict, Any, Optional
from mcp.server.mcpserver import MCPServer
from mcp.types import ToolAnnotations
from pydantic import Field
from whatsapp import Chat, Contact, MessageContext
from whatsapp import (
    search_contacts as whatsapp_search_contacts,
    list_messages as whatsapp_list_messages,
    list_chats as whatsapp_list_chats,
    get_chat as whatsapp_get_chat,
    get_direct_chat_by_contact as whatsapp_get_direct_chat_by_contact,
    get_contact_chats as whatsapp_get_contact_chats,
    get_last_interaction as whatsapp_get_last_interaction,
    get_message_context as whatsapp_get_message_context,
    send_message as whatsapp_send_message,
    send_file as whatsapp_send_file,
    send_audio_message as whatsapp_audio_voice_message,
    download_media as whatsapp_download_media,
    react_to_message as whatsapp_react_to_message,
    list_contacts as whatsapp_list_contacts,
    check_numbers_on_whatsapp as whatsapp_check_numbers,
    set_contact_name as whatsapp_set_contact_name,
)

# Initialize the MCP server
mcp = MCPServer("whatsapp")

# With WHATSAPP_MCP_READ_ONLY=1 the tools that send messages are not exposed to the model
READ_ONLY = os.environ.get("WHATSAPP_MCP_READ_ONLY", "").strip().lower() in ("1", "true", "yes", "on")


def send_tool(**tool_kwargs):
    """Register a tool that sends messages or changes data, unless read-only mode is enabled."""
    if READ_ONLY:
        return lambda func: func
    return mcp.tool(**tool_kwargs)

@mcp.tool()
def search_contacts(query: str) -> List[Contact]:
    """Search WhatsApp contacts by name or phone number.
    
    Args:
        query: Search term to match against contact names or phone numbers
    """
    contacts = whatsapp_search_contacts(query)
    return contacts

@mcp.tool(annotations=ToolAnnotations(readOnlyHint=True, destructiveHint=False, idempotentHint=True, openWorldHint=False))
def list_contacts(
    query: str = "",
    limit: Annotated[int, Field(ge=1, le=200, description="Contacts per page, 1 to 200")] = 50,
    page: Annotated[int, Field(ge=0, description="Page number, starting at 0")] = 0,
) -> Dict[str, Any]:
    """List WhatsApp contacts with every name the bridge holds for each.

    Each contact has: jid, phone_number (when known), name (the one shown: local, else saved,
    else push, else business), local_name (set with set_contact_name), saved_name (from the
    phone's address book, synced by WhatsApp), push_name (the name the person chose for
    themselves) and business_name. Sorted by name. Use check_whatsapp_numbers to ask whether
    a number is registered on WhatsApp.

    Args:
        query: Optional text matched against any of the names and the phone number
        limit: Contacts per page, 1 to 200 (default 50)
        page: Page number, starting at 0
    """
    success, body = whatsapp_list_contacts(query, limit, page)
    if not success:
        return {"success": False, "message": body.get("message", "Unknown error")}
    return {"success": True, "contacts": body.get("contacts", [])}

@mcp.tool(annotations=ToolAnnotations(readOnlyHint=True, destructiveHint=False, idempotentHint=True, openWorldHint=True))
def check_whatsapp_numbers(
    phone_numbers: Annotated[List[str], Field(min_length=1, max_length=20, description="1 to 20 phone numbers in international format")],
) -> Dict[str, Any]:
    """Check whether phone numbers are registered on WhatsApp, before messaging them.

    Asks WhatsApp's servers; nothing is sent to the numbers. Each result has query,
    is_on_whatsapp, and for a registered number its jid, phone_number and, for a business,
    verified_name. A number that is not valid international format gets an error entry.

    Args:
        phone_numbers: 1 to 20 numbers in international format with the country code,
            such as "+31653275958" (spaces, dashes and brackets are ignored)
    """
    success, body = whatsapp_check_numbers(phone_numbers)
    if not success:
        return {"success": False, "message": body.get("message", "Unknown error")}
    return {"success": True, "results": body.get("results", [])}

@send_tool(annotations=ToolAnnotations(readOnlyHint=False, destructiveHint=False, idempotentHint=True, openWorldHint=False))
def set_contact_name(contact: str, name: str) -> Dict[str, Any]:
    """Save or rename a contact in this server's own contact names.

    The name is kept by the bridge and shown by list_chats, search_contacts, list_messages
    and list_contacts instead of the saved or push name. It does NOT change the phone's
    address book or WhatsApp: the person's phone never sees it. Use an empty name to remove it.

    Args:
        contact: A phone number in international format ("+31653275958") or a person's JID
            ("31653275958@s.whatsapp.net" or "...@lid"); groups cannot be named here
        name: The name to show, at most 100 characters (for example "Frela (water heater)");
            exactly empty to remove the local name; a name of only white space is refused
    """
    success, status_message = whatsapp_set_contact_name(contact, name)
    return {"success": success, "message": status_message}

@mcp.tool()
def list_messages(
    after: Optional[str] = None,
    before: Optional[str] = None,
    sender_phone_number: Optional[str] = None,
    chat_jid: Optional[str] = None,
    query: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_context: bool = True,
    context_before: int = 1,
    context_after: int = 1
) -> str:
    """Get WhatsApp messages matching specified criteria with optional context.

    Returns formatted text, one message per line, with each message's ID.
    
    Args:
        after: Optional ISO-8601 formatted string to only return messages after this date
        before: Optional ISO-8601 formatted string to only return messages before this date
        sender_phone_number: Optional phone number to filter messages by sender
        chat_jid: Optional chat JID to filter messages by chat
        query: Optional search term to filter messages by content
        limit: Maximum number of messages to return (default 20)
        page: Page number for pagination (default 0)
        include_context: Whether to include messages before and after matches (default True)
        context_before: Number of messages to include before each match (default 1)
        context_after: Number of messages to include after each match (default 1)
    """
    messages = whatsapp_list_messages(
        after=after,
        before=before,
        sender_phone_number=sender_phone_number,
        chat_jid=chat_jid,
        query=query,
        limit=limit,
        page=page,
        include_context=include_context,
        context_before=context_before,
        context_after=context_after
    )
    return messages

@mcp.tool()
def list_chats(
    query: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_last_message: bool = True,
    sort_by: str = "last_active"
) -> List[Chat]:
    """Get WhatsApp chats matching specified criteria.
    
    Args:
        query: Optional search term to filter chats by name or JID
        limit: Maximum number of chats to return (default 20)
        page: Page number for pagination (default 0)
        include_last_message: Whether to include the last message in each chat (default True)
        sort_by: Field to sort results by, either "last_active" or "name" (default "last_active")
    """
    chats = whatsapp_list_chats(
        query=query,
        limit=limit,
        page=page,
        include_last_message=include_last_message,
        sort_by=sort_by
    )
    return chats

@mcp.tool()
def get_chat(chat_jid: str, include_last_message: bool = True) -> Optional[Chat]:
    """Get WhatsApp chat metadata by JID.
    
    Args:
        chat_jid: The JID of the chat to retrieve
        include_last_message: Whether to include the last message (default True)
    """
    chat = whatsapp_get_chat(chat_jid, include_last_message)
    return chat

@mcp.tool()
def get_direct_chat_by_contact(sender_phone_number: str) -> Optional[Chat]:
    """Get WhatsApp chat metadata by sender phone number.
    
    Args:
        sender_phone_number: The phone number to search for
    """
    chat = whatsapp_get_direct_chat_by_contact(sender_phone_number)
    return chat

@mcp.tool()
def get_contact_chats(jid: str, limit: int = 20, page: int = 0) -> List[Chat]:
    """Get all WhatsApp chats involving the contact.
    
    Args:
        jid: The contact's JID to search for
        limit: Maximum number of chats to return (default 20)
        page: Page number for pagination (default 0)
    """
    chats = whatsapp_get_contact_chats(jid, limit, page)
    return chats

@mcp.tool()
def get_last_interaction(jid: str) -> Optional[str]:
    """Get most recent WhatsApp message involving the contact.
    
    Args:
        jid: The JID of the contact to search for
    """
    message = whatsapp_get_last_interaction(jid)
    return message

@mcp.tool()
def get_message_context(
    message_id: str,
    before: int = 5,
    after: int = 5
) -> MessageContext:
    """Get context around a specific WhatsApp message.
    
    Args:
        message_id: The ID of the message to get context for
        before: Number of messages to include before the target message (default 5)
        after: Number of messages to include after the target message (default 5)
    """
    context = whatsapp_get_message_context(message_id, before, after)
    return context

@send_tool()
def send_message(
    recipient: str,
    message: str,
    reply_to_message_id: Optional[str] = None,
    reply_to_sender_jid: Optional[str] = None,
    mentions: Optional[List[str]] = None,
) -> Dict[str, Any]:
    """Send a WhatsApp message to a person or group. For group chats use the JID.

    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        message: The message text to send
        reply_to_message_id: Optional message id to quote-reply to. The id can be obtained
                 from list_messages or get_message_context (the `id` field). Passing this
                 makes the outbound message appear as a reply in the recipient's chat.
        reply_to_sender_jid: JID of the original message's sender. Required for group
                 replies (WhatsApp needs to know whose message you're quoting); optional
                 for direct chats.
        mentions: Optional list of people to @mention, as phone numbers (country code, no +)
                 or JIDs. Also put "@<number>" for each of them in the message text,
                 otherwise WhatsApp does not highlight the mention.

    Returns:
        A dictionary containing success status and a status message
    """
    # Validate input
    if not recipient:
        return {
            "success": False,
            "message": "Recipient must be provided"
        }

    success, status_message = whatsapp_send_message(
        recipient,
        message,
        reply_to_message_id=reply_to_message_id,
        reply_to_sender_jid=reply_to_sender_jid,
        mentions=mentions,
    )
    return {
        "success": success,
        "message": status_message
    }

@send_tool()
def send_file(recipient: str, media_path: str, caption: str = "") -> Dict[str, Any]:
    """Send a file such as a picture, raw audio, video or document via WhatsApp to the specified recipient. For group messages use the JID. Optionally include a caption that appears attached to the media in WhatsApp.
    
    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        media_path: The absolute path to the media file to send (image, video, document)
        caption: Optional text caption that appears attached to the media (default empty). Captions on documents may not be shown on all WhatsApp clients.
    
    Returns:
        A dictionary containing success status and a status message
    """
    
    # Call the whatsapp_send_file function
    success, status_message = whatsapp_send_file(recipient, media_path, caption)
    return {
        "success": success,
        "message": status_message
    }

@send_tool()
def send_audio_message(recipient: str, media_path: str) -> Dict[str, Any]:
    """Send any audio file as a WhatsApp audio message to the specified recipient. For group messages use the JID. If it errors due to ffmpeg not being installed, use send_file instead.
    
    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        media_path: The absolute path to the audio file to send (will be converted to Opus .ogg if it's not a .ogg file)
    
    Returns:
        A dictionary containing success status and a status message
    """
    success, status_message = whatsapp_audio_voice_message(recipient, media_path)
    return {
        "success": success,
        "message": status_message
    }

@mcp.tool()
def download_media(message_id: str, chat_jid: str) -> Dict[str, Any]:
    """Download media from a WhatsApp message and get the local file path.
    
    Args:
        message_id: The ID of the message containing the media
        chat_jid: The JID of the chat containing the message
    
    Returns:
        A dictionary containing success status, a status message, and the file path if successful
    """
    file_path = whatsapp_download_media(message_id, chat_jid)
    
    if file_path:
        return {
            "success": True,
            "message": "Media downloaded successfully",
            "file_path": file_path
        }
    else:
        return {
            "success": False,
            "message": "Failed to download media"
        }

@send_tool()
def react_to_message(message_id: str, chat_jid: str, emoji: str) -> Dict[str, Any]:
    """React to a WhatsApp message with an emoji.

    Args:
        message_id: The ID of the message to react to
        chat_jid: The JID of the chat containing the message
        emoji: The emoji to react with (e.g., "❤️", "👍", "😂"). Use empty string to remove reaction.

    Returns:
        A dictionary containing success status and a status message
    """
    success, status_message = whatsapp_react_to_message(message_id, chat_jid, emoji)
    return {
        "success": success,
        "message": status_message
    }


if __name__ == "__main__":
    # Initialize and run the server
    mcp.run(transport='stdio')
