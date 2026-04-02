package whatsapp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	//lint:ignore SA1019 whatsmeow/binary/proto is deprecated; keep aliases until migrated to waE2E
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// SendMessageResponse represents the response for the send message API.
type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SendMessageRequest represents the request body for the send message API.
type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

// sendWhatsAppMessage is the daemon-side live send path: it resolves `recipient`, optionally uploads `mediaPath`, then sends through the connected client.
func sendWhatsAppMessage(client *whatsmeow.Client, recipient string, message string, mediaPath string) (bool, string) {
	if !client.IsConnected() {
		return false, "Not connected to WhatsApp"
	}

	var recipientJID types.JID
	var err error

	isJID := strings.Contains(recipient, "@")

	if isJID {
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return false, fmt.Sprintf("Error parsing JID: %v", err)
		}
	} else {
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net",
		}
	}

	//lint:ignore SA1019 waProto.Message is a deprecated alias to waE2E.Message
	msg := &waProto.Message{}

	if mediaPath != "" {
		mediaData, err := os.ReadFile(mediaPath)
		if err != nil {
			return false, fmt.Sprintf("Error reading media file: %v", err)
		}

		fileExt := strings.ToLower(mediaPath[strings.LastIndex(mediaPath, ".")+1:])
		var mediaType whatsmeow.MediaType
		var mimeType string

		switch fileExt {
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
		case "ogg":
			mediaType = whatsmeow.MediaAudio
			mimeType = "audio/ogg; codecs=opus"
		case "mp4":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/mp4"
		case "avi":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/avi"
		case "mov":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/quicktime"
		default:
			mediaType = whatsmeow.MediaDocument
			mimeType = "application/octet-stream"
		}

		resp, err := client.Upload(context.Background(), mediaData, mediaType)
		if err != nil {
			return false, fmt.Sprintf("Error uploading media: %v", err)
		}

		fmt.Println("Media uploaded", resp)

		switch mediaType {
		case whatsmeow.MediaImage:
			//lint:ignore SA1019 waProto.ImageMessage is a deprecated alias
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
			var seconds uint32 = 30
			var waveform []byte

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

			//lint:ignore SA1019 waProto.AudioMessage is a deprecated alias
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
			//lint:ignore SA1019 waProto.VideoMessage is a deprecated alias
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
			//lint:ignore SA1019 waProto.DocumentMessage is a deprecated alias
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

	_, err = client.SendMessage(context.Background(), recipientJID, msg)
	if err != nil {
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// mediaDownloader implements the whatsmeow Downloadable interface.
type mediaDownloader struct {
	url           string
	directPath    string
	mediaKey      []byte
	fileLength    uint64
	fileSHA256    []byte
	fileEncSHA256 []byte
}

// GetDirectPath returns the WhatsApp direct path so whatsmeow can fetch the encrypted payload.
func (d *mediaDownloader) GetDirectPath() string    { return d.directPath }
// GetURL returns the media URL so whatsmeow can fetch the encrypted payload.
func (d *mediaDownloader) GetURL() string           { return d.url }
// GetMediaKey returns the media key used to decrypt the downloaded payload.
func (d *mediaDownloader) GetMediaKey() []byte      { return d.mediaKey }
// GetFileLength returns the stored media length so whatsmeow can validate the download.
func (d *mediaDownloader) GetFileLength() uint64    { return d.fileLength }
// GetFileSHA256 returns the plaintext SHA so whatsmeow can validate the download.
func (d *mediaDownloader) GetFileSHA256() []byte    { return d.fileSHA256 }
// GetFileEncSHA256 returns the encrypted SHA so whatsmeow can validate the download.
func (d *mediaDownloader) GetFileEncSHA256() []byte { return d.fileEncSHA256 }

// downloadMedia is the daemon-side live download path: it reconstructs media params from SQLite, fetches from WhatsApp, and writes under `dataDir/downloads`.
func downloadMedia(client *whatsmeow.Client, messageStore *MessageStore, messageID, chatJID string, dataDir string) (bool, string, string, string, error) {
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

	downloadDir := filepath.Join(dataDir, "downloads")
	os.MkdirAll(downloadDir, 0755)

	ext := filepath.Ext(filename)
	if ext == "" {
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

// extractDirectPathFromURL extracts the direct path component from a WhatsApp media URL.
func extractDirectPathFromURL(url string) string {
	parts := strings.SplitN(url, "/", 4)
	if len(parts) < 4 {
		return ""
	}
	return "/" + parts[3]
}

// analyzeOggOpus tries to extract duration and generate a simple waveform from an Ogg Opus file.
func analyzeOggOpus(data []byte) (duration uint32, waveform []byte, err error) {
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not a valid Ogg file (missing OggS signature)")
	}

	var lastGranule uint64
	var sampleRate uint32 = 48000
	var preSkip uint16
	var foundOpusHead bool

	for i := 0; i < len(data); {
		if i+27 >= len(data) {
			break
		}

		if string(data[i:i+4]) != "OggS" {
			i++
			continue
		}

		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeqNum := binary.LittleEndian.Uint32(data[i+18 : i+22])
		numSegments := int(data[i+26])

		if i+27+numSegments >= len(data) {
			break
		}
		segmentTable := data[i+27 : i+27+numSegments]

		pageSize := 27 + numSegments
		for _, segLen := range segmentTable {
			pageSize += int(segLen)
		}

		if !foundOpusHead && pageSeqNum <= 1 {
			pageData := data[i : i+pageSize]
			headPos := bytes.Index(pageData, []byte("OpusHead"))
			if headPos >= 0 && headPos+12 < len(pageData) {
				headPos += 8
				if headPos+12 <= len(pageData) {
					preSkip = binary.LittleEndian.Uint16(pageData[headPos+10 : headPos+12])
					sampleRate = binary.LittleEndian.Uint32(pageData[headPos+12 : headPos+16])
					foundOpusHead = true
					fmt.Printf("Found OpusHead: sampleRate=%d, preSkip=%d\n", sampleRate, preSkip)
				}
			}
		}

		if granulePos != 0 {
			lastGranule = granulePos
		}

		i += pageSize
	}

	if !foundOpusHead {
		fmt.Println("Warning: OpusHead not found, using default values")
	}

	if lastGranule > 0 {
		durationSeconds := float64(lastGranule-uint64(preSkip)) / float64(sampleRate)
		duration = uint32(math.Ceil(durationSeconds))
		fmt.Printf("Calculated Opus duration from granule: %f seconds (lastGranule=%d)\n",
			durationSeconds, lastGranule)
	} else {
		fmt.Println("Warning: No valid granule position found, using estimation")
		durationEstimate := float64(len(data)) / 2000.0
		duration = uint32(durationEstimate)
	}

	if duration < 1 {
		duration = 1
	} else if duration > 300 {
		duration = 300
	}

	waveform = placeholderWaveform(duration)

	fmt.Printf("Ogg Opus analysis: size=%d bytes, calculated duration=%d sec, waveform=%d bytes\n",
		len(data), duration, len(waveform))

	return duration, waveform, nil
}

// placeholderWaveform generates a synthetic waveform for WhatsApp voice messages.
func placeholderWaveform(duration uint32) []byte {
	const waveformLength = 64
	waveform := make([]byte, waveformLength)

	baseAmplitude := 35.0
	frequencyFactor := float64(min(int(duration), 120)) / 30.0

	for i := range waveform {
		pos := float64(i) / float64(waveformLength)

		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)

		val += (rand.Float64() - 0.5) * 15

		fadeInOut := math.Sin(pos * math.Pi)
		val = val * (0.7 + 0.3*fadeInOut)

		val = val + 50

		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}

		waveform[i] = byte(val)
	}

	return waveform
}
