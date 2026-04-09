package whatsapp

import (
	"context"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
)

// defaultWhatsAppSendMessageRunes caps outbound text when MCP config does not set whatsapp_send_max_runes.
const defaultWhatsAppSendMessageRunes = core.DefaultWhatsAppSendMaxRunes

// RegisterTools wires WhatsApp's SQLite-backed read tools and REST-backed write tools into the MCP server under the source prefix.
func (w *Source) RegisterTools(s core.ToolAdder) {
	svc := w.svc
	p := w.Name() + "_"

	// ---- Contact & Chat Discovery ----

	s.AddTool(core.NewReadOnlyTool(p+"search_contacts",
		core.ToolDescription("Search WhatsApp contacts by name or phone number.", `{"query":"alice"}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search term to match against contact names or phone numbers")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, errResult := core.RequireStringArgument(req, "query", `{"query":"alice"}`)
		if errResult != nil {
			return errResult, nil
		}
		contacts, err := svc.SearchContacts(q)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(contacts)
	})

	s.AddTool(core.NewReadOnlyTool(p+"list_chats",
		core.ToolDescription("Get WhatsApp chats matching specified criteria. Supports fuzzy search by chat name or participant name.", `{"query":"family","limit":10}`),
		mcp.WithString("query", mcp.Description("Optional search term to filter chats by name, JID, or participant name")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of chats to return (default 20)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
		mcp.WithBoolean("include_last_message", mcp.Description("Whether to include the last message in each chat (default true)")),
		mcp.WithString("sort_by", mcp.Description("Sort by 'last_active' or 'name' (default 'last_active')")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, _ := args["query"].(string)
		limit := core.IntArg(args, "limit", 20)
		page := core.IntArg(args, "page", 0)
		includeLast := core.BoolArg(args, "include_last_message", true)
		sortBy, _ := args["sort_by"].(string)
		if sortBy == "" {
			sortBy = "last_active"
		}
		chats, err := svc.ListChats(query, limit, page, includeLast, sortBy)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(chats)
	})

	s.AddTool(core.NewReadOnlyTool(p+"get_chat",
		core.ToolDescription("Get WhatsApp chat metadata by JID.", `{"chat_jid":"120363025246810101@g.us"}`),
		mcp.WithString("chat_jid", mcp.Required(), mcp.Description("The JID of the chat to retrieve; obtain from whatsapp_list_chats or search result metadata")),
		mcp.WithBoolean("include_last_message", mcp.Description("Whether to include the last message (default true)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, errResult := core.RequireStringArgument(req, "chat_jid", `{"chat_jid":"120363025246810101@g.us"}`)
		if errResult != nil {
			return errResult, nil
		}
		includeLast := core.BoolArg(req.GetArguments(), "include_last_message", true)
		chat, err := svc.GetChat(jid, includeLast)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(chat)
	})

	s.AddTool(core.NewReadOnlyTool(p+"get_direct_chat_by_contact",
		core.ToolDescription("Get WhatsApp chat metadata by sender phone number.", `{"sender_phone_number":"15551234567"}`),
		mcp.WithString("sender_phone_number", mcp.Required(), mcp.Description("The phone number to search for")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		phone, errResult := core.RequireStringArgument(req, "sender_phone_number", `{"sender_phone_number":"15551234567"}`)
		if errResult != nil {
			return errResult, nil
		}
		chat, err := svc.GetDirectChatByContact(phone)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(chat)
	})

	s.AddTool(core.NewReadOnlyTool(p+"get_contact_chats",
		core.ToolDescription("Get all WhatsApp chats involving the contact.", `{"jid":"15551234567@s.whatsapp.net","limit":10}`),
		mcp.WithString("jid", mcp.Required(), mcp.Description("The contact's JID; obtain from whatsapp_search_contacts or search result metadata")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of chats to return (default 20)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, errResult := core.RequireStringArgument(req, "jid", `{"jid":"15551234567@s.whatsapp.net","limit":10}`)
		if errResult != nil {
			return errResult, nil
		}
		args := req.GetArguments()
		limit := core.IntArg(args, "limit", 20)
		page := core.IntArg(args, "page", 0)
		chats, err := svc.GetContactChats(jid, limit, page)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(chats)
	})

	// ---- Message Reading ----

	s.AddTool(core.NewReadOnlyTool(p+"list_messages",
		core.ToolDescription("Get WhatsApp messages matching specified criteria with optional context. When a query is provided, uses BM25 keyword search for relevance-ranked results.", `{"chat_jid":"15551234567@s.whatsapp.net","query":"dinner plans","limit":20}`),
		mcp.WithString("after", mcp.Description("Optional RFC3339 timestamp to only return messages after (e.g. '2024-01-01T00:00:00Z')")),
		mcp.WithString("before", mcp.Description("Optional RFC3339 timestamp to only return messages before (e.g. '2025-01-01T00:00:00Z')")),
		mcp.WithString("sender_phone_number", mcp.Description("Optional phone number to filter by sender")),
		mcp.WithString("chat_jid", mcp.Description("Optional chat JID to filter by chat; obtain from whatsapp_list_chats or search result metadata")),
		mcp.WithString("query", mcp.Description("Optional BM25 keyword search term; use 2–4 core keywords and include synonyms for better recall (e.g. 'dinner plans tonight')")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of messages to return (default 200)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
		mcp.WithBoolean("include_context", mcp.Description("Include messages before and after matches (default true)")),
		mcp.WithNumber("context_before", mcp.Description("Number of messages to include before each match (default 1)")),
		mcp.WithNumber("context_after", mcp.Description("Number of messages to include after each match (default 1)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		after, _ := args["after"].(string)
		before, _ := args["before"].(string)
		sender, _ := args["sender_phone_number"].(string)
		chatJID, _ := args["chat_jid"].(string)
		query, _ := args["query"].(string)
		limit := core.IntArg(args, "limit", 200)
		page := core.IntArg(args, "page", 0)
		includeCtx := core.BoolArg(args, "include_context", true)
		ctxBefore := core.IntArg(args, "context_before", 1)
		ctxAfter := core.IntArg(args, "context_after", 1)
		result, err := svc.ListMessages(after, before, sender, chatJID, query, limit, page, includeCtx, ctxBefore, ctxAfter)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.UntrustedTextResult(result, "whatsapp"), nil
	})

	s.AddTool(core.NewReadOnlyTool(p+"get_message_context",
		core.ToolDescription("Get context around a specific WhatsApp message.", `{"message_id":"ABCD1234","before":3,"after":3}`),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The ID of the message to get context for")),
		mcp.WithNumber("before", mcp.Description("Number of messages before (default 5)")),
		mcp.WithNumber("after", mcp.Description("Number of messages after (default 5)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msgID, errResult := core.RequireStringArgument(req, "message_id", `{"message_id":"ABCD1234","before":3,"after":3}`)
		if errResult != nil {
			return errResult, nil
		}
		args := req.GetArguments()
		before := core.IntArg(args, "before", 5)
		after := core.IntArg(args, "after", 5)
		result, err := svc.GetMessageContext(msgID, before, after)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.UntrustedJSONResult(result, "whatsapp")
	})

	s.AddTool(core.NewReadOnlyTool(p+"get_last_interaction",
		core.ToolDescription("Get most recent WhatsApp message involving the contact.", `{"jid":"15551234567@s.whatsapp.net"}`),
		mcp.WithString("jid", mcp.Required(), mcp.Description("The JID of the contact; obtain from whatsapp_search_contacts or search result metadata")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, errResult := core.RequireStringArgument(req, "jid", `{"jid":"15551234567@s.whatsapp.net"}`)
		if errResult != nil {
			return errResult, nil
		}
		result, err := svc.GetLastInteraction(jid)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.UntrustedTextResult(result, "whatsapp"), nil
	})

	// ---- Sending ----

	s.AddTool(core.NewMutatingTool(p+"send_message",
		core.ToolDescription("Send a WhatsApp message to a person or group. For group chats use the JID.", `{"recipient":"15551234567","message":"On my way"}`),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("message", mcp.Required(), mcp.Description("The message text to send")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, errResult := core.RequireStringArgument(req, "recipient", `{"recipient":"15551234567","message":"On my way"}`)
		if errResult != nil {
			return errResult, nil
		}
		message, errResult := core.RequireStringArgument(req, "message", `{"recipient":"15551234567","message":"On my way"}`)
		if errResult != nil {
			return errResult, nil
		}
		limit := defaultWhatsAppSendMessageRunes
		if w != nil && w.sendMessageMaxRunes > 0 {
			limit = w.sendMessageMaxRunes
		}
		if tooLong := core.CheckStringMaxLen(message, limit, "message"); tooLong != nil {
			return tooLong, nil
		}
		success, msg, err := svc.SendMessage(recipient, message)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	s.AddTool(core.NewMutatingTool(p+"send_file",
		core.ToolDescription("Send a file via WhatsApp. For group messages use the JID.", `{"recipient":"15551234567","media_path":"/tmp/photo.jpg"}`),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("media_path", mcp.Required(), mcp.Description("Absolute path to the file to send")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, errResult := core.RequireStringArgument(req, "recipient", `{"recipient":"15551234567","media_path":"/tmp/photo.jpg"}`)
		if errResult != nil {
			return errResult, nil
		}
		path, errResult := core.RequireStringArgument(req, "media_path", `{"recipient":"15551234567","media_path":"/tmp/photo.jpg"}`)
		if errResult != nil {
			return errResult, nil
		}
		success, msg, err := svc.SendFile(recipient, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	s.AddTool(core.NewMutatingTool(p+"send_audio_message",
		core.ToolDescription("Send an audio file as a WhatsApp voice message. Non-ogg files require ffmpeg for conversion.", `{"recipient":"15551234567","media_path":"/tmp/voice.ogg"}`),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("media_path", mcp.Required(), mcp.Description("Absolute path to the audio file")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, errResult := core.RequireStringArgument(req, "recipient", `{"recipient":"15551234567","media_path":"/tmp/voice.ogg"}`)
		if errResult != nil {
			return errResult, nil
		}
		path, errResult := core.RequireStringArgument(req, "media_path", `{"recipient":"15551234567","media_path":"/tmp/voice.ogg"}`)
		if errResult != nil {
			return errResult, nil
		}
		success, msg, err := svc.SendAudioMessage(recipient, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	// ---- Media ----

	s.AddTool(core.NewMutatingTool(p+"download_media",
		core.ToolDescription("Download media from a WhatsApp message and get the local file path.", `{"message_id":"ABCD1234","chat_jid":"15551234567@s.whatsapp.net"}`),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The ID of the message containing the media")),
		mcp.WithString("chat_jid", mcp.Required(), mcp.Description("The JID of the chat containing the message")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msgID, errResult := core.RequireStringArgument(req, "message_id", `{"message_id":"ABCD1234","chat_jid":"15551234567@s.whatsapp.net"}`)
		if errResult != nil {
			return errResult, nil
		}
		chatJID, errResult := core.RequireStringArgument(req, "chat_jid", `{"message_id":"ABCD1234","chat_jid":"15551234567@s.whatsapp.net"}`)
		if errResult != nil {
			return errResult, nil
		}
		path, err := svc.DownloadMedia(msgID, chatJID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(map[string]interface{}{"success": true, "message": "Media downloaded successfully", "file_path": path})
	})
}
