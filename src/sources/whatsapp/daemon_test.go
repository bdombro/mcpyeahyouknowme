package whatsapp

import (
	"bytes"
	"testing"

	//lint:ignore SA1019 whatsmeow/binary/proto is deprecated; keep until migrated to waE2E
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// Verifies text extraction handles the message shapes the daemon stores as searchable text.
func TestExtractTextContent(t *testing.T) {
	tests := []struct {
		name     string
		//lint:ignore SA1019 waProto.Message is a deprecated type alias
		msg      *waProto.Message
		expected string
	}{
		{
			name: "conversation message",
			//lint:ignore SA1019 waProto.Message is a deprecated type alias
			msg: &waProto.Message{
				Conversation: proto.String("Hello World"),
			},
			expected: "Hello World",
		},
		{
			name: "extended text message",
			//lint:ignore SA1019 waProto.Message is a deprecated type alias
			msg: &waProto.Message{
				//lint:ignore SA1019 waProto.ExtendedTextMessage is a deprecated type alias
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String("Extended hello"),
				},
			},
			expected: "Extended hello",
		},
		{
			name: "contact",
			//lint:ignore SA1019 waProto.Message is a deprecated type alias
			msg: &waProto.Message{
				//lint:ignore SA1019 waProto.ContactMessage is a deprecated type alias
				ContactMessage: &waProto.ContactMessage{
					DisplayName: proto.String("John Doe"),
				},
			},
			expected: "contact: John Doe",
		},
		{
			name: "sticker",
			//lint:ignore SA1019 waProto.Message is a deprecated type alias
			msg: &waProto.Message{
				//lint:ignore SA1019 waProto.StickerMessage is a deprecated type alias
				StickerMessage: &waProto.StickerMessage{},
			},
			expected: "sticker",
		},
		{
			name: "empty message",
			//lint:ignore SA1019 waProto.Message is a deprecated type alias
			msg:      &waProto.Message{},
			expected: "",
		},
		{
			name:     "nil message",
			msg:      nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTextContent(tt.msg)
			if result != tt.expected {
				t.Errorf("extractTextContent() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

// Verifies media extraction returns the stored metadata fields needed for later download requests.
func TestExtractMediaInfo(t *testing.T) {
	tests := []struct {
		name         string
		//lint:ignore SA1019 waProto.Message is a deprecated type alias
		msg          *waProto.Message
		expectedType string
		hasURL       bool
	}{
		{
			name: "image message",
			//lint:ignore SA1019 waProto.Message is a deprecated type alias
			msg: &waProto.Message{
				//lint:ignore SA1019 waProto.ImageMessage is a deprecated type alias
				ImageMessage: &waProto.ImageMessage{
					Mimetype:      proto.String("image/jpeg"),
					URL:           proto.String("https://example.com/image.jpg"),
					DirectPath:    proto.String("/v1/some/path"),
					MediaKey:      []byte("key123"),
					FileSHA256:    []byte("sha256"),
					FileEncSHA256: []byte("encsha256"),
					FileLength:    proto.Uint64(12345),
				},
			},
			expectedType: "image",
			hasURL:       true,
		},
		{
			name: "document message with filename",
			//lint:ignore SA1019 waProto.Message is a deprecated type alias
			msg: &waProto.Message{
				//lint:ignore SA1019 waProto.DocumentMessage is a deprecated type alias
				DocumentMessage: &waProto.DocumentMessage{
					FileName: proto.String("document.pdf"),
					URL:           proto.String("https://example.com/doc.pdf"),
					MediaKey:      []byte("dockey"),
					FileSHA256:    []byte("docsha"),
					FileEncSHA256: []byte("docencsha"),
					FileLength:    proto.Uint64(54321),
				},
			},
			expectedType: "document",
			hasURL:       true,
		},
		{
			name: "no media message",
			//lint:ignore SA1019 waProto.Message is a deprecated type alias
			msg:          &waProto.Message{Conversation: proto.String("Just text")},
			expectedType: "",
			hasURL:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mediaType, _, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(tt.msg)

			if mediaType != tt.expectedType {
				t.Errorf("mediaType = %q, expected %q", mediaType, tt.expectedType)
			}

			if tt.hasURL && url == "" {
				t.Error("expected non-empty URL for media message")
			}

			// For messages with media, verify non-empty keys
			if tt.expectedType != "" {
				if len(mediaKey) == 0 {
					t.Error("expected non-empty mediaKey")
				}
				if len(fileSHA256) == 0 {
					t.Error("expected non-empty fileSHA256")
				}
				if len(fileEncSHA256) == 0 {
					t.Error("expected non-empty fileEncSHA256")
				}
				if fileLength == 0 {
					t.Error("expected non-zero fileLength")
				}
			}
		})
	}
}

// Verifies direct-path extraction derives a non-empty path from WhatsApp media URLs and stays empty for blanks.
func TestExtractDirectPathFromURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		notEmpty bool
	}{
		{
			name:     "full URL",
			url:      "https://mmg.whatsapp.net/v/t62.7118-24/12345678_abcdef.enc?ccb=11-4",
			notEmpty: true,
		},
		{
			name:     "empty URL",
			url:      "",
			notEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractDirectPathFromURL(tt.url)
			if tt.notEmpty && result == "" {
				t.Error("expected non-empty result for non-empty URL")
			}
			if !tt.notEmpty && result != "" {
				t.Error("expected empty result for empty URL")
			}
		})
	}
}

// Verifies placeholder waveform generation always returns data so audio sends have a fallback waveform.
func TestPlaceholderWaveform(t *testing.T) {
	// Test that it returns non-nil waveform data
	waveform := placeholderWaveform(5000)
	if len(waveform) == 0 {
		t.Error("expected non-empty waveform")
	}
	
	// Zero duration should still return data
	waveform0 := placeholderWaveform(0)
	if len(waveform0) == 0 {
		t.Error("expected non-empty waveform even for zero duration")
	}
}

// Verifies the media downloader exposes the metadata accessors expected by the media download path.
func TestMediaDownloader_interface(t *testing.T) {
	// Test that mediaDownloader implements the required interface methods
	d := &mediaDownloader{
		directPath:     "/path/to/media",
		url:            "https://example.com/media",
		mediaKey:       []byte("key"),
		fileLength:     12345,
		fileSHA256:     []byte("sha256"),
		fileEncSHA256:  []byte("encsha256"),
	}

	if d.GetDirectPath() != "/path/to/media" {
		t.Errorf("GetDirectPath() = %q, expected %q", d.GetDirectPath(), "/path/to/media")
	}
	if d.GetURL() != "https://example.com/media" {
		t.Errorf("GetURL() = %q, expected %q", d.GetURL(), "https://example.com/media")
	}
	if !bytes.Equal(d.GetMediaKey(), []byte("key")) {
		t.Error("GetMediaKey() mismatch")
	}
	if d.GetFileLength() != 12345 {
		t.Errorf("GetFileLength() = %d, expected %d", d.GetFileLength(), 12345)
	}
	if !bytes.Equal(d.GetFileSHA256(), []byte("sha256")) {
		t.Error("GetFileSHA256() mismatch")
	}
	if !bytes.Equal(d.GetFileEncSHA256(), []byte("encsha256")) {
		t.Error("GetFileEncSHA256() mismatch")
	}
}

// Verifies invalid Ogg/Opus data does not panic the waveform analysis helper.
func TestAnalyzeOggOpus_invalidData(_ *testing.T) {
	// Test with empty data
	duration, waveform, err := analyzeOggOpus([]byte{})
	// Function may not error on invalid data, just check it returns something
	_ = duration
	_ = waveform
	_ = err
}

// Verifies the WhatsApp source always reports that interactive authentication is required.
func TestWhatsAppSource_RequiresAuth(t *testing.T) {
	w := &Source{dataDir: t.TempDir()}
	if !w.RequiresAuth() {
		t.Error("WhatsApp source should require auth")
	}
}
