package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// WhatsAppSource implements DataSource for WhatsApp message data.
type WhatsAppSource struct {
	store *MessageStore
	svc   *MCPService
}

func NewWhatsAppSource() (*WhatsAppSource, error) {
	store, err := NewMessageStore()
	if err != nil {
		return nil, err
	}
	svc := NewMCPService(store, "http://localhost:8080/api")
	return &WhatsAppSource{store: store, svc: svc}, nil
}

// NewWhatsAppSourceFromStore creates a WhatsAppSource from an existing store
// and API URL. Used by tests to inject in-memory databases and mock servers.
func NewWhatsAppSourceFromStore(store *MessageStore, apiURL string) *WhatsAppSource {
	return &WhatsAppSource{store: store, svc: NewMCPService(store, apiURL)}
}

func (w *WhatsAppSource) Name() string        { return "whatsapp" }
func (w *WhatsAppSource) Description() string  { return "WhatsApp" }
func (w *WhatsAppSource) Close() error         { w.store.Close(); return nil }

// SetSearchStore enables vector-enhanced hybrid message search.
func (w *WhatsAppSource) SetSearchStore(ss *SearchStore) {
	w.svc.SetSearchStore(ss)
}

func (w *WhatsAppSource) SearchEntries() ([]SearchEntry, error) {
	var entries []SearchEntry
	src := w.Name()

	// Chat names
	chatRows, err := w.store.db.Query("SELECT jid, name, last_message_time FROM chats")
	if err == nil {
		defer chatRows.Close()
		for chatRows.Next() {
			var jid string
			var name sql.NullString
			var lastTime sql.NullString
			if chatRows.Scan(&jid, &name, &lastTime) != nil || !name.Valid || name.String == "" {
				continue
			}
			meta, _ := json.Marshal(map[string]interface{}{
				"jid":      jid,
				"is_group": strings.HasSuffix(jid, "@g.us"),
			})
			var ts *time.Time
			if lastTime.Valid {
				t := parseTime(lastTime.String)
				ts = &t
			}
			entries = append(entries, SearchEntry{
				Source:      src,
				SourceID:    jid,
				ContentType: "chat_name",
				Title:       name.String,
				Content:     name.String,
				Metadata:    meta,
				Timestamp:   ts,
			})
		}
	}

	// Participants (from whatsmeow_contacts if available)
	if w.store.contactsDB != nil {
		contactRows, err := w.store.contactsDB.Query("SELECT their_jid, full_name, push_name FROM whatsmeow_contacts")
		if err == nil {
			defer contactRows.Close()
			for contactRows.Next() {
				var jid string
				var fullName, pushName sql.NullString
				if contactRows.Scan(&jid, &fullName, &pushName) != nil {
					continue
				}
				if strings.HasSuffix(jid, "@g.us") {
					continue
				}
				displayName := nullStr(fullName)
				if displayName == "" {
					displayName = nullStr(pushName)
				}
				if displayName == "" {
					continue
				}
				phone := jidPhone(jid)
				content := displayName
				if phone != displayName {
					content = displayName + " " + phone
				}

				// Find groups this contact belongs to
				var groups []string
				gpRows, gpErr := w.store.db.Query(
					"SELECT group_jid FROM group_participants WHERE participant_jid = ?", jid)
				if gpErr == nil {
					for gpRows.Next() {
						var gj string
						if gpRows.Scan(&gj) == nil {
							groups = append(groups, gj)
						}
					}
					gpRows.Close()
				}

				meta, _ := json.Marshal(map[string]interface{}{
					"jid":    jid,
					"groups": groups,
				})
				entries = append(entries, SearchEntry{
					Source:      src,
					SourceID:    jid,
					ContentType: "participant",
					Title:       displayName,
					Content:     content,
					Metadata:    meta,
				})
			}
		}
	}

	// Messages (only those with meaningful content)
	msgRows, err := w.store.db.Query(`
		SELECT m.id, m.chat_jid, m.sender, m.content, m.timestamp, m.is_from_me, c.name
		FROM messages m
		JOIN chats c ON m.chat_jid = c.jid
		WHERE LENGTH(m.content) > 20`)
	if err == nil {
		defer msgRows.Close()
		for msgRows.Next() {
			var id, chatJID, sender, content, tsStr string
			var isFromMe bool
			var chatName sql.NullString
			if msgRows.Scan(&id, &chatJID, &sender, &content, &tsStr, &isFromMe, &chatName) != nil {
				continue
			}
			ts := parseTime(tsStr)
			meta, _ := json.Marshal(map[string]interface{}{
				"message_id": id,
				"chat_jid":   chatJID,
				"sender":     sender,
				"timestamp":  ts.Format(time.RFC3339),
				"is_from_me": isFromMe,
			})
			entries = append(entries, SearchEntry{
				Source:      src,
				SourceID:    id + ":" + chatJID,
				ContentType: "message",
				Title:       nullStr(chatName),
				Content:     content,
				Metadata:    meta,
				Timestamp:   &ts,
			})
		}
	}

	return entries, nil
}

func (w *WhatsAppSource) RegisterTools(s *server.MCPServer) {
	svc := w.svc
	p := w.Name() + "_"

	// ---- Contact & Chat Discovery ----

	s.AddTool(mcp.NewTool(p+"search_contacts",
		mcp.WithDescription("Search WhatsApp contacts by name or phone number."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search term to match against contact names or phone numbers")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, _ := req.RequireString("query")
		contacts, err := svc.SearchContacts(q)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(contacts)
	})

	s.AddTool(mcp.NewTool(p+"list_chats",
		mcp.WithDescription("Get WhatsApp chats matching specified criteria. Supports fuzzy search by chat name or participant name."),
		mcp.WithString("query", mcp.Description("Optional search term to filter chats by name, JID, or participant name")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of chats to return (default 20)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
		mcp.WithBoolean("include_last_message", mcp.Description("Whether to include the last message in each chat (default true)")),
		mcp.WithString("sort_by", mcp.Description("Sort by 'last_active' or 'name' (default 'last_active')")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, _ := args["query"].(string)
		limit := intArg(args, "limit", 20)
		page := intArg(args, "page", 0)
		includeLast := boolArg(args, "include_last_message", true)
		sortBy, _ := args["sort_by"].(string)
		if sortBy == "" {
			sortBy = "last_active"
		}
		chats, err := svc.ListChats(query, limit, page, includeLast, sortBy)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(chats)
	})

	s.AddTool(mcp.NewTool(p+"get_chat",
		mcp.WithDescription("Get WhatsApp chat metadata by JID."),
		mcp.WithString("chat_jid", mcp.Required(), mcp.Description("The JID of the chat to retrieve")),
		mcp.WithBoolean("include_last_message", mcp.Description("Whether to include the last message (default true)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, _ := req.RequireString("chat_jid")
		includeLast := boolArg(req.GetArguments(), "include_last_message", true)
		chat, err := svc.GetChat(jid, includeLast)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(chat)
	})

	s.AddTool(mcp.NewTool(p+"get_direct_chat_by_contact",
		mcp.WithDescription("Get WhatsApp chat metadata by sender phone number."),
		mcp.WithString("sender_phone_number", mcp.Required(), mcp.Description("The phone number to search for")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		phone, _ := req.RequireString("sender_phone_number")
		chat, err := svc.GetDirectChatByContact(phone)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(chat)
	})

	s.AddTool(mcp.NewTool(p+"get_contact_chats",
		mcp.WithDescription("Get all WhatsApp chats involving the contact."),
		mcp.WithString("jid", mcp.Required(), mcp.Description("The contact's JID to search for")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of chats to return (default 20)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, _ := req.RequireString("jid")
		args := req.GetArguments()
		limit := intArg(args, "limit", 20)
		page := intArg(args, "page", 0)
		chats, err := svc.GetContactChats(jid, limit, page)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(chats)
	})

	// ---- Message Reading ----

	s.AddTool(mcp.NewTool(p+"list_messages",
		mcp.WithDescription("Get WhatsApp messages matching specified criteria with optional context. When a query is provided, uses BM25 keyword search for relevance-ranked results."),
		mcp.WithString("after", mcp.Description("Optional ISO-8601 date to only return messages after")),
		mcp.WithString("before", mcp.Description("Optional ISO-8601 date to only return messages before")),
		mcp.WithString("sender_phone_number", mcp.Description("Optional phone number to filter by sender")),
		mcp.WithString("chat_jid", mcp.Description("Optional chat JID to filter by chat")),
		mcp.WithString("query", mcp.Description("Optional search term to filter messages by content")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of messages to return (default 20)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
		mcp.WithBoolean("include_context", mcp.Description("Include messages before and after matches (default true)")),
		mcp.WithNumber("context_before", mcp.Description("Number of messages to include before each match (default 1)")),
		mcp.WithNumber("context_after", mcp.Description("Number of messages to include after each match (default 1)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		after, _ := args["after"].(string)
		before, _ := args["before"].(string)
		sender, _ := args["sender_phone_number"].(string)
		chatJID, _ := args["chat_jid"].(string)
		query, _ := args["query"].(string)
		limit := intArg(args, "limit", 20)
		page := intArg(args, "page", 0)
		includeCtx := boolArg(args, "include_context", true)
		ctxBefore := intArg(args, "context_before", 1)
		ctxAfter := intArg(args, "context_after", 1)
		result, err := svc.ListMessages(after, before, sender, chatJID, query, limit, page, includeCtx, ctxBefore, ctxAfter)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool(p+"get_message_context",
		mcp.WithDescription("Get context around a specific WhatsApp message."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The ID of the message to get context for")),
		mcp.WithNumber("before", mcp.Description("Number of messages before (default 5)")),
		mcp.WithNumber("after", mcp.Description("Number of messages after (default 5)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msgID, _ := req.RequireString("message_id")
		args := req.GetArguments()
		before := intArg(args, "before", 5)
		after := intArg(args, "after", 5)
		result, err := svc.GetMessageContext(msgID, before, after)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)
	})

	s.AddTool(mcp.NewTool(p+"get_last_interaction",
		mcp.WithDescription("Get most recent WhatsApp message involving the contact."),
		mcp.WithString("jid", mcp.Required(), mcp.Description("The JID of the contact")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, _ := req.RequireString("jid")
		result, err := svc.GetLastInteraction(jid)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	// ---- Sending ----

	s.AddTool(mcp.NewTool(p+"send_message",
		mcp.WithDescription("Send a WhatsApp message to a person or group. For group chats use the JID."),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("message", mcp.Required(), mcp.Description("The message text to send")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, _ := req.RequireString("recipient")
		message, _ := req.RequireString("message")
		success, msg, err := svc.SendMessage(recipient, message)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	s.AddTool(mcp.NewTool(p+"send_file",
		mcp.WithDescription("Send a file via WhatsApp. For group messages use the JID."),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("media_path", mcp.Required(), mcp.Description("Absolute path to the file to send")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, _ := req.RequireString("recipient")
		path, _ := req.RequireString("media_path")
		success, msg, err := svc.SendFile(recipient, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	s.AddTool(mcp.NewTool(p+"send_audio_message",
		mcp.WithDescription("Send an audio file as a WhatsApp voice message. Non-ogg files require ffmpeg for conversion."),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("media_path", mcp.Required(), mcp.Description("Absolute path to the audio file")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, _ := req.RequireString("recipient")
		path, _ := req.RequireString("media_path")
		success, msg, err := svc.SendAudioMessage(recipient, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	// ---- Media ----

	s.AddTool(mcp.NewTool(p+"download_media",
		mcp.WithDescription("Download media from a WhatsApp message and get the local file path."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The ID of the message containing the media")),
		mcp.WithString("chat_jid", mcp.Required(), mcp.Description("The JID of the chat containing the message")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msgID, _ := req.RequireString("message_id")
		chatJID, _ := req.RequireString("chat_jid")
		path, err := svc.DownloadMedia(msgID, chatJID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"success": true, "message": "Media downloaded successfully", "file_path": path})
	})
}
