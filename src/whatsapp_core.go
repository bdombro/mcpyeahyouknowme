package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// StartCore runs the WhatsApp client as a persistent daemon.
// This maintains a WebSocket connection to WhatsApp, syncs messages,
// and runs a REST API server.
func (w *WhatsAppSource) StartCore(ctx context.Context) error {
	// Set up logger
	logger := waLog.Stdout("Client", "INFO", true)
	logger.Infof("Starting WhatsApp client...")

	// Create database connection for storing session data
	dbLog := waLog.Stdout("Database", "INFO", true)

	// Create data directory if it doesn't exist
	dir := dataDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	container, err := sqlstore.New(context.Background(), "sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=30000", filepath.Join(dir, "whatsapp.db")), dbLog)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get device store - This contains session information
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			// No device exists, create one
			deviceStore = container.NewDevice()
			logger.Infof("Created new device")
		} else {
			return fmt.Errorf("failed to get device: %w", err)
		}
	}

	// Create client instance
	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		return fmt.Errorf("failed to create WhatsApp client")
	}

	// Initialize message store
	messageStore, err := NewMessageStore()
	if err != nil {
		return fmt.Errorf("failed to initialize message store: %w", err)
	}
	defer messageStore.Close()

	// Setup event handling for messages and history sync
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			// Process regular messages
			handleMessage(client, messageStore, v, logger)

		case *events.HistorySync:
			// Process history sync events
			handleHistorySync(client, messageStore, v, logger)

		case *events.Connected:
			logger.Infof("Connected to WhatsApp")

		case *events.LoggedOut:
			logger.Warnf("Device logged out, please scan QR code to log in again")
		}
	})

	// Create channel to track connection success
	connected := make(chan bool, 1)

	// Connect to WhatsApp
	if client.Store.ID == nil {
		// No ID stored, this is a new client, need to pair with phone
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		// Print QR code for pairing with phone
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("\nScan this QR code with your WhatsApp app:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else if evt.Event == "success" {
				connected <- true
				break
			}
		}

		// Wait for connection
		select {
		case <-connected:
			fmt.Println("\nSuccessfully connected and authenticated!")
		case <-time.After(3 * time.Minute):
			return fmt.Errorf("timeout waiting for QR code scan")
		}
	} else {
		// Already logged in, just connect
		err = client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		connected <- true
	}

	// Wait a moment for connection to stabilize
	time.Sleep(2 * time.Second)

	if !client.IsConnected() {
		return fmt.Errorf("failed to establish stable connection")
	}

	fmt.Println("\n✓ Connected to WhatsApp! Type 'help' for commands.")

	// Start REST API server
	startRESTServer(client, messageStore, 8080)

	// Create a channel to keep the goroutine alive
	exitChan := make(chan os.Signal, 1)
	signal.Notify(exitChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("REST server is running. Press Ctrl+C to disconnect and exit.")

	// Wait for termination signal or context cancellation
	select {
	case <-exitChan:
		fmt.Println("Disconnecting...")
	case <-ctx.Done():
		fmt.Println("Stopping WhatsApp client (context cancelled)...")
	}

	// Disconnect client
	client.Disconnect()
	return nil
}

// RequiresAuth returns true because WhatsApp needs authentication before running.
func (w *WhatsAppSource) RequiresAuth() bool {
	return true
}

// extractTextContent returns the human-readable text content of a message.
// For non-text message types (stickers, contacts, locations, etc.) it returns
// a bracketed placeholder so the message is not silently dropped.
func extractTextContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}

	if text := msg.GetConversation(); text != "" {
		return text
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if msg.GetStickerMessage() != nil {
		return "sticker"
	}
	if c := msg.GetContactMessage(); c != nil {
		if dn := c.GetDisplayName(); dn != "" {
			return "contact: " + dn
		}
		return "contact"
	}
	if msg.GetContactsArrayMessage() != nil {
		return "contacts"
	}
	if loc := msg.GetLocationMessage(); loc != nil {
		return fmt.Sprintf("location: %.4f, %.4f", loc.GetDegreesLatitude(), loc.GetDegreesLongitude())
	}
	if msg.GetLiveLocationMessage() != nil {
		return "live location"
	}
	if inv := msg.GetGroupInviteMessage(); inv != nil {
		if gn := inv.GetGroupName(); gn != "" {
			return "group invite: " + gn
		}
		return "group invite"
	}
	if vo := msg.GetViewOnceMessage(); vo != nil && vo.GetMessage() != nil {
		inner := extractTextContent(vo.GetMessage())
		if inner != "" {
			return inner
		}
		return "view-once message"
	}
	if eph := msg.GetEphemeralMessage(); eph != nil && eph.GetMessage() != nil {
		inner := extractTextContent(eph.GetMessage())
		if inner != "" {
			return inner
		}
	}
	if msg.GetListMessage() != nil {
		return "list"
	}
	if msg.GetListResponseMessage() != nil {
		return "list response"
	}
	if r := msg.GetReactionMessage(); r != nil {
		return "reaction: " + r.GetText()
	}
	if msg.GetButtonsMessage() != nil {
		return "buttons"
	}
	if msg.GetButtonsResponseMessage() != nil {
		return "button response"
	}
	if msg.GetPollCreationMessage() != nil || msg.GetPollCreationMessageV2() != nil || msg.GetPollCreationMessageV3() != nil {
		return "poll"
	}
	if msg.GetPollUpdateMessage() != nil {
		return "poll update"
	}

	return ""
}

// SendMessageResponse represents the response for the send message API
type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SendMessageRequest represents the request body for the send message API
type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

// Function to send a WhatsApp message
func sendWhatsAppMessage(client *whatsmeow.Client, recipient string, message string, mediaPath string) (bool, string) {
	if !client.IsConnected() {
		return false, "Not connected to WhatsApp"
	}

	// Create JID for recipient
	var recipientJID types.JID
	var err error

	// Check if recipient is a JID
	isJID := strings.Contains(recipient, "@")

	if isJID {
		// Parse the JID string
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return false, fmt.Sprintf("Error parsing JID: %v", err)
		}
	} else {
		// Create JID from phone number
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net", // For personal chats
		}
	}

	msg := &waProto.Message{}

	// Check if we have media to send
	if mediaPath != "" {
		// Read media file
		mediaData, err := os.ReadFile(mediaPath)
		if err != nil {
			return false, fmt.Sprintf("Error reading media file: %v", err)
		}

		// Determine media type and mime type based on file extension
		fileExt := strings.ToLower(mediaPath[strings.LastIndex(mediaPath, ".")+1:])
		var mediaType whatsmeow.MediaType
		var mimeType string

		// Handle different media types
		switch fileExt {
		// Image types
		case "jpg", "jpeg":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/jpeg"
		case "png":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/png"
		case "gif":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/gif"
		case "webp":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/webp"

		// Audio types
		case "ogg":
			mediaType = whatsmeow.MediaAudio
			mimeType = "audio/ogg; codecs=opus"

		// Video types
		case "mp4":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/mp4"
		case "avi":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/avi"
		case "mov":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/quicktime"

		// Document types (for any other file type)
		default:
			mediaType = whatsmeow.MediaDocument
			mimeType = "application/octet-stream"
		}

		// Upload media to WhatsApp servers
		resp, err := client.Upload(context.Background(), mediaData, mediaType)
		if err != nil {
			return false, fmt.Sprintf("Error uploading media: %v", err)
		}

		fmt.Println("Media uploaded", resp)

		// Create the appropriate message type based on media type
		switch mediaType {
		case whatsmeow.MediaImage:
			msg.ImageMessage = &waProto.ImageMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaAudio:
			// Handle ogg audio files
			var seconds uint32 = 30 // Default fallback
			var waveform []byte = nil

			// Try to analyze the ogg file
			if strings.Contains(mimeType, "ogg") {
				analyzedSeconds, analyzedWaveform, err := analyzeOggOpus(mediaData)
				if err == nil {
					seconds = analyzedSeconds
					waveform = analyzedWaveform
				} else {
					return false, fmt.Sprintf("Failed to analyze Ogg Opus file: %v", err)
				}
			} else {
				fmt.Printf("Not an Ogg Opus file: %s\n", mimeType)
			}

			msg.AudioMessage = &waProto.AudioMessage{
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				Seconds:       proto.Uint32(seconds),
				PTT:           proto.Bool(true),
				Waveform:      waveform,
			}
		case whatsmeow.MediaVideo:
			msg.VideoMessage = &waProto.VideoMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaDocument:
			msg.DocumentMessage = &waProto.DocumentMessage{
				Title:         proto.String(mediaPath[strings.LastIndex(mediaPath, "/")+1:]),
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		}
	} else {
		msg.Conversation = proto.String(message)
	}

	// Send message
	_, err = client.SendMessage(context.Background(), recipientJID, msg)

	if err != nil {
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// Extract media info from a message
func extractMediaInfo(msg *waProto.Message) (mediaType string, filename string, url string, mediaKey []byte, fileSHA256 []byte, fileEncSHA256 []byte, fileLength uint64) {
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}

	if img := msg.GetImageMessage(); img != nil {
		return "image", "image.jpg", img.GetURL(), img.MediaKey, img.FileSHA256, img.FileEncSHA256, img.GetFileLength()
	}
	if audio := msg.GetAudioMessage(); audio != nil {
		return "audio", "audio.ogg", audio.GetURL(), audio.MediaKey, audio.FileSHA256, audio.FileEncSHA256, audio.GetFileLength()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", "video.mp4", vid.GetURL(), vid.MediaKey, vid.FileSHA256, vid.FileEncSHA256, vid.GetFileLength()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		filename := doc.GetTitle()
		if filename == "" {
			filename = "document"
		}
		return "document", filename, doc.GetURL(), doc.MediaKey, doc.FileSHA256, doc.FileEncSHA256, doc.GetFileLength()
	}
	if sticker := msg.GetStickerMessage(); sticker != nil {
		return "sticker", "sticker.webp", sticker.GetURL(), sticker.MediaKey, sticker.FileSHA256, sticker.FileEncSHA256, sticker.GetFileLength()
	}

	return "", "", "", nil, nil, nil, 0
}

// Handle incoming messages
func handleMessage(client *whatsmeow.Client, messageStore *MessageStore, msg *events.Message, logger waLog.Logger) {
	if msg.Message == nil {
		return
	}

	chatJID := msg.Info.Chat.String()

	// Extract sender information
	var sender string
	if msg.Info.IsFromMe {
		sender = client.Store.ID.User
	} else if msg.Info.Sender.User != "" {
		sender = msg.Info.Sender.User
	} else {
		sender = msg.Info.Chat.User
	}

	// Extract content
	content := extractTextContent(msg.Message)

	// Extract media info
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(msg.Message)

	// Skip if there's no content or media
	if content == "" && mediaType == "" {
		return
	}

	// Get or create chat name
	// For DMs, msg.Info.PushName is the contact's display name at the time of message.
	// For groups, we query the group info.
	name := GetChatName(client, messageStore, msg.Info.Chat, chatJID, nil, msg.Info.PushName, logger)

	// If it's a group, attempt to refresh group participant list
	if msg.Info.IsGroup {
		go func() {
			groupInfo, err := client.GetGroupInfo(context.Background(), msg.Info.Chat)
			if err == nil && len(groupInfo.Participants) > 0 {
				var jids []string
				for _, p := range groupInfo.Participants {
					jids = append(jids, p.JID.String())
				}
				messageStore.StoreGroupParticipants(chatJID, jids)
			}
		}()
	}

	// Store chat metadata
	messageStore.StoreChat(chatJID, name, msg.Info.Timestamp)

	// Store message
	err := messageStore.StoreMessage(
		msg.Info.ID,
		chatJID,
		sender,
		content,
		msg.Info.Timestamp,
		msg.Info.IsFromMe,
		mediaType,
		filename,
		url,
		mediaKey,
		fileSHA256,
		fileEncSHA256,
		fileLength,
	)

	if err != nil {
		logger.Warnf("Failed to store message: %v", err)
	}
}

// downloadMedia downloads media from WhatsApp servers.
type mediaDownloader struct {
	url           string
	directPath    string
	mediaKey      []byte
	fileLength    uint64
	fileSHA256    []byte
	fileEncSHA256 []byte
}

func (d *mediaDownloader) GetDirectPath() string       { return d.directPath }
func (d *mediaDownloader) GetURL() string              { return d.url }
func (d *mediaDownloader) GetMediaKey() []byte         { return d.mediaKey }
func (d *mediaDownloader) GetFileLength() uint64       { return d.fileLength }
func (d *mediaDownloader) GetFileSHA256() []byte      { return d.fileSHA256 }
func (d *mediaDownloader) GetFileEncSHA256() []byte   { return d.fileEncSHA256 }

// Download media from WhatsApp
func downloadMedia(client *whatsmeow.Client, messageStore *MessageStore, messageID, chatJID string) (bool, string, string, string, error) {
	// Get media info from message store
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64

	err := messageStore.db.QueryRow(`
		SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length
		FROM messages 
		WHERE id = ? AND chat_jid = ?
	`, messageID, chatJID).Scan(&mediaType, &filename, &url, &mediaKey, &fileSHA256, &fileEncSHA256, &fileLength)

	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to find message: %w", err)
	}

	if mediaType == "" || url == "" {
		return false, "", "", "", fmt.Errorf("message does not contain media")
	}

	// Download media
	downloader := &mediaDownloader{
		url:           url,
		directPath:    extractDirectPathFromURL(url),
		mediaKey:      mediaKey,
		fileSHA256:    fileSHA256,
		fileEncSHA256: fileEncSHA256,
		fileLength:    fileLength,
	}

	data, err := client.Download(context.Background(), downloader)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to download media: %w", err)
	}

	// Save to file
	downloadDir := filepath.Join(dataDir(), "downloads")
	os.MkdirAll(downloadDir, 0755)

	// Generate a unique filename with the message ID to avoid conflicts
	ext := filepath.Ext(filename)
	if ext == "" {
		// Add extension based on media type if filename doesn't have one
		switch mediaType {
		case "image":
			ext = ".jpg"
		case "video":
			ext = ".mp4"
		case "audio":
			ext = ".ogg"
		case "document":
			ext = ".bin"
		case "sticker":
			ext = ".webp"
		}
	}

	baseName := strings.TrimSuffix(filename, ext)
	fullFilename := fmt.Sprintf("%s_%s%s", baseName, messageID[:8], ext)
	filePath := filepath.Join(downloadDir, fullFilename)

	err = os.WriteFile(filePath, data, 0644)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to save media: %w", err)
	}

	return true, filePath, mediaType, filename, nil
}

// extractDirectPathFromURL extracts the direct path component from a WhatsApp media URL
func extractDirectPathFromURL(url string) string {
	// Example URL: https://mmg.whatsapp.net/v/t62.7119-24/...
	// We need to extract everything after the domain
	parts := strings.SplitN(url, "/", 4)
	if len(parts) < 4 {
		return ""
	}
	return "/" + parts[3]
}

// startRESTServer starts an HTTP server for sending messages
func startRESTServer(client *whatsmeow.Client, messageStore *MessageStore, port int) {
	mux := http.NewServeMux()

	// Endpoint to send a message
	mux.HandleFunc("/api/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		success, message := sendWhatsAppMessage(client, req.Recipient, req.Message, req.MediaPath)

		resp := SendMessageResponse{
			Success: success,
			Message: message,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Endpoint to download media
	mux.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			MessageID string `json:"message_id"`
			ChatJID   string `json:"chat_jid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.MessageID == "" || req.ChatJID == "" {
			http.Error(w, "Missing message_id or chat_jid parameter", http.StatusBadRequest)
			return
		}

		success, filePath, mediaType, filename, err := downloadMedia(client, messageStore, req.MessageID, req.ChatJID)
		if !success {
			http.Error(w, fmt.Sprintf("Failed to download: %v", err), http.StatusInternalServerError)
			return
		}

		response := map[string]string{
			"success":    "true",
			"file_path":  filePath,
			"media_type": mediaType,
			"filename":   filename,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// Start server in background
	go func() {
		addr := fmt.Sprintf(":%d", port)
		fmt.Printf("Starting REST API server on %s\n", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			fmt.Printf("REST server error: %v\n", err)
		}
	}()
}

// GetChatName determines the appropriate name for a chat based on JID and other info
func GetChatName(client *whatsmeow.Client, messageStore *MessageStore, jid types.JID, chatJID string, conversation interface{}, sender string, logger waLog.Logger) string {
	var existingName string
	err := messageStore.db.QueryRow("SELECT name FROM chats WHERE jid = ?", chatJID).Scan(&existingName)
	hasExisting := err == nil && existingName != ""

	// If we already have a real (non-phone-number, non-placeholder, non-synthesized) name, use it
	if hasExisting && !looksLikePhoneNumber(existingName) && !looksLikeGroupPlaceholder(existingName) && !isSynthesizedName(existingName) {
		logger.Infof("Using existing chat name for %s: %s", chatJID, existingName)
		return existingName
	}

	// Need to determine chat name
	var name string

	if jid.Server == "g.us" {
		// This is a group chat
		logger.Infof("Getting name for group: %s", chatJID)

		// Use conversation data if provided (from history sync)
		if conversation != nil {
			var displayName, convName *string
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				v = v.Elem()

				if displayNameField := v.FieldByName("DisplayName"); displayNameField.IsValid() && displayNameField.Kind() == reflect.Ptr && !displayNameField.IsNil() {
					dn := displayNameField.Elem().String()
					displayName = &dn
				}

				if nameField := v.FieldByName("Name"); nameField.IsValid() && nameField.Kind() == reflect.Ptr && !nameField.IsNil() {
					n := nameField.Elem().String()
					convName = &n
				}
			}

			if displayName != nil && *displayName != "" {
				name = *displayName
			} else if convName != nil && *convName != "" {
				name = *convName
			}
		}

		if name == "" {
			groupInfo, err := client.GetGroupInfo(context.Background(), jid)
			if err == nil && groupInfo.Name != "" {
				name = groupInfo.Name
			} else {
				name = fmt.Sprintf("Group %s", jid.User)
			}
		}

		logger.Infof("Using group name: %s", name)
	} else {
		// This is an individual contact
		logger.Infof("Getting name for contact: %s", chatJID)

		contact, err := client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.FullName != "" {
			name = contact.FullName
		} else if err == nil && contact.PushName != "" {
			name = contact.PushName
		} else if sender != "" {
			name = sender
		} else {
			name = jid.User
		}

		logger.Infof("Using contact name: %s", name)
	}

	// If we resolved a real name and the DB had a placeholder/synthesized name, update it
	isPlaceholder := looksLikePhoneNumber(existingName) || looksLikeGroupPlaceholder(existingName) || isSynthesizedName(existingName)
	if hasExisting && isPlaceholder && !looksLikePhoneNumber(name) && !looksLikeGroupPlaceholder(name) {
		logger.Infof("Upgrading chat name for %s: %s -> %s", chatJID, existingName, name)
		messageStore.db.Exec("UPDATE chats SET name = ? WHERE jid = ?", name, chatJID)
	}

	return name
}

// Handle history sync events
func handleHistorySync(client *whatsmeow.Client, messageStore *MessageStore, historySync *events.HistorySync, logger waLog.Logger) {
	fmt.Printf("Received history sync event with %d conversations\n", len(historySync.Data.Conversations))

	syncedCount := 0
	for _, conversation := range historySync.Data.Conversations {
		if conversation.ID == nil {
			continue
		}

		chatJID := *conversation.ID

		jid, err := types.ParseJID(chatJID)
		if err != nil {
			logger.Warnf("Failed to parse JID %s: %v", chatJID, err)
			continue
		}

		name := GetChatName(client, messageStore, jid, chatJID, conversation, "", logger)

		// Extract group participants directly from conversation metadata.
		// This supplements the later GetGroupInfo calls and gives us data
		// even for groups we've since left.
		if jid.Server == "g.us" && len(conversation.GetParticipant()) > 0 {
			var pJIDs []string
			for _, gp := range conversation.GetParticipant() {
				if gp.GetUserJID() != "" {
					pJIDs = append(pJIDs, gp.GetUserJID())
				}
			}
			if len(pJIDs) > 0 {
				messageStore.StoreGroupParticipants(chatJID, pJIDs)
			}
		}

		messages := conversation.Messages
		if len(messages) > 0 {
			latestMsg := messages[0]
			if latestMsg == nil || latestMsg.Message == nil {
				continue
			}

			timestamp := time.Time{}
			if ts := latestMsg.Message.GetMessageTimestamp(); ts != 0 {
				timestamp = time.Unix(int64(ts), 0)
			} else {
				continue
			}

			messageStore.StoreChat(chatJID, name, timestamp)

			for _, msg := range messages {
				if msg == nil || msg.Message == nil {
					continue
				}

				// Extract text content using the comprehensive extractor
				var content string
				if msg.Message.Message != nil {
					content = extractTextContent(msg.Message.Message)
				}

				var mediaType, filename, url string
				var mediaKey, fileSHA256, fileEncSHA256 []byte
				var fileLength uint64

				if msg.Message.Message != nil {
					mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength = extractMediaInfo(msg.Message.Message)
				}

				if content == "" && mediaType == "" {
					continue
				}

				// Determine sender — check multiple fields for best data.
				// WhatsApp populates participant info inconsistently across
				// Key.Participant, WebMessageInfo.Participant, and PushName.
				var sender string
				isFromMe := false
				if msg.Message.Key != nil {
					if msg.Message.Key.FromMe != nil {
						isFromMe = *msg.Message.Key.FromMe
					}
				}
				if isFromMe {
					sender = client.Store.ID.User
				} else {
					// Try Key.Participant first (most common for group messages)
					if msg.Message.Key != nil && msg.Message.Key.Participant != nil && *msg.Message.Key.Participant != "" {
						sender = *msg.Message.Key.Participant
					}
					// Fall back to WebMessageInfo.Participant (field 5)
					if sender == "" && msg.Message.Participant != nil && *msg.Message.Participant != "" {
						sender = *msg.Message.Participant
					}
					// Fall back to PushName (display name at send time)
					if sender == "" && msg.Message.PushName != nil && *msg.Message.PushName != "" {
						sender = *msg.Message.PushName
					}
					// Final fallback: chat JID user portion
					if sender == "" {
						sender = jid.User
					}
				}

				msgID := ""
				if msg.Message.Key != nil && msg.Message.Key.ID != nil {
					msgID = *msg.Message.Key.ID
				}

				timestamp := time.Time{}
				if ts := msg.Message.GetMessageTimestamp(); ts != 0 {
					timestamp = time.Unix(int64(ts), 0)
				} else {
					continue
				}

				err = messageStore.StoreMessage(
					msgID,
					chatJID,
					sender,
					content,
					timestamp,
					isFromMe,
					mediaType,
					filename,
					url,
					mediaKey,
					fileSHA256,
					fileEncSHA256,
					fileLength,
				)
				if err != nil {
					logger.Warnf("Failed to store history message: %v", err)
				} else {
					syncedCount++
				}
			}
		}
	}

	fmt.Printf("History sync complete. Stored %d messages.\n", syncedCount)
}

// requestHistorySync asks the primary device for additional on-demand history.
func requestHistorySync(client *whatsmeow.Client) {
	if client == nil {
		fmt.Println("Client is not initialized. Cannot request history sync.")
		return
	}

	if !client.IsConnected() {
		fmt.Println("Client is not connected. Please ensure you are connected to WhatsApp first.")
		return
	}

	if client.Store.ID == nil {
		fmt.Println("Client is not logged in. Please scan the QR code first.")
		return
	}

	historyMsg := client.BuildHistorySyncRequest(nil, 500)
	if historyMsg == nil {
		fmt.Println("Failed to build history sync request.")
		return
	}

	_, err := client.SendPeerMessage(context.Background(), historyMsg)
	if err != nil {
		fmt.Printf("Failed to request history sync: %v\n", err)
	} else {
		fmt.Println("History sync requested. Waiting for server response...")
	}
}

// analyzeOggOpus tries to extract duration and generate a simple waveform from an Ogg Opus file
func analyzeOggOpus(data []byte) (duration uint32, waveform []byte, err error) {
	// Try to detect if this is a valid Ogg file by checking for the "OggS" signature
	// at the beginning of the file
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not a valid Ogg file (missing OggS signature)")
	}

	// Parse Ogg pages to find the last page with a valid granule position
	var lastGranule uint64
	var sampleRate uint32 = 48000 // Default Opus sample rate
	var preSkip uint16 = 0
	var foundOpusHead bool

	// Scan through the file looking for Ogg pages
	for i := 0; i < len(data); {
		// Check if we have enough data to read Ogg page header
		if i+27 >= len(data) {
			break
		}

		// Verify Ogg page signature
		if string(data[i:i+4]) != "OggS" {
			// Skip until next potential page
			i++
			continue
		}

		// Extract header fields
		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeqNum := binary.LittleEndian.Uint32(data[i+18 : i+22])
		numSegments := int(data[i+26])

		// Extract segment table
		if i+27+numSegments >= len(data) {
			break
		}
		segmentTable := data[i+27 : i+27+numSegments]

		// Calculate page size
		pageSize := 27 + numSegments
		for _, segLen := range segmentTable {
			pageSize += int(segLen)
		}

		// Check if we're looking at an OpusHead packet (should be in first few pages)
		if !foundOpusHead && pageSeqNum <= 1 {
			// Look for "OpusHead" marker in this page
			pageData := data[i : i+pageSize]
			headPos := bytes.Index(pageData, []byte("OpusHead"))
			if headPos >= 0 && headPos+12 < len(pageData) {
				// Found OpusHead, extract sample rate and pre-skip
				// OpusHead format: Magic(8) + Version(1) + Channels(1) + PreSkip(2) + SampleRate(4) + ...
				headPos += 8 // Skip "OpusHead" marker
				// PreSkip is 2 bytes at offset 10
				if headPos+12 <= len(pageData) {
					preSkip = binary.LittleEndian.Uint16(pageData[headPos+10 : headPos+12])
					sampleRate = binary.LittleEndian.Uint32(pageData[headPos+12 : headPos+16])
					foundOpusHead = true
					fmt.Printf("Found OpusHead: sampleRate=%d, preSkip=%d\n", sampleRate, preSkip)
				}
			}
		}

		// Keep track of last valid granule position
		if granulePos != 0 {
			lastGranule = granulePos
		}

		// Move to next page
		i += pageSize
	}

	if !foundOpusHead {
		fmt.Println("Warning: OpusHead not found, using default values")
	}

	// Calculate duration based on granule position
	if lastGranule > 0 {
		// Formula for duration: (lastGranule - preSkip) / sampleRate
		durationSeconds := float64(lastGranule-uint64(preSkip)) / float64(sampleRate)
		duration = uint32(math.Ceil(durationSeconds))
		fmt.Printf("Calculated Opus duration from granule: %f seconds (lastGranule=%d)\n",
			durationSeconds, lastGranule)
	} else {
		// Fallback to rough estimation if granule position not found
		fmt.Println("Warning: No valid granule position found, using estimation")
		durationEstimate := float64(len(data)) / 2000.0 // Very rough approximation
		duration = uint32(durationEstimate)
	}

	// Make sure we have a reasonable duration (at least 1 second, at most 300 seconds)
	if duration < 1 {
		duration = 1
	} else if duration > 300 {
		duration = 300
	}

	// Generate waveform
	waveform = placeholderWaveform(duration)

	fmt.Printf("Ogg Opus analysis: size=%d bytes, calculated duration=%d sec, waveform=%d bytes\n",
		len(data), duration, len(waveform))

	return duration, waveform, nil
}

// placeholderWaveform generates a synthetic waveform for WhatsApp voice messages
// that appears natural with some variability based on the duration
func placeholderWaveform(duration uint32) []byte {
	// WhatsApp expects a 64-byte waveform for voice messages
	const waveformLength = 64
	waveform := make([]byte, waveformLength)

	// Create a more natural looking waveform with some patterns and variability
	// rather than completely random values

	// Base amplitude and frequency - longer messages get faster frequency
	baseAmplitude := 35.0
	frequencyFactor := float64(min(int(duration), 120)) / 30.0

	for i := range waveform {
		// Position in the waveform (normalized 0-1)
		pos := float64(i) / float64(waveformLength)

		// Create a wave pattern with some randomness
		// Use multiple sine waves of different frequencies for more natural look
		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)

		// Add some randomness to make it look more natural
		val += (rand.Float64() - 0.5) * 15

		// Add some fade-in and fade-out effects
		fadeInOut := math.Sin(pos * math.Pi)
		val = val * (0.7 + 0.3*fadeInOut)

		// Center around 50 (typical voice baseline)
		val = val + 50

		// Ensure values stay within WhatsApp's expected range (0-100)
		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}

		waveform[i] = byte(val)
	}

	return waveform
}
