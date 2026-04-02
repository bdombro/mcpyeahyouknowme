package notebook

import (
	"errors"
	"testing"
)

// Verifies fakeAnalyzer implements ImageAnalyzer so test doubles can replace VisionAnalyzer.
func TestFakeAnalyzer_implementsInterface(_ *testing.T) {
	var _ ImageAnalyzer = &fakeAnalyzer{}
}

// Verifies VisionAnalyzer implements ImageAnalyzer at compile time.
func TestVisionAnalyzer_implementsInterface(_ *testing.T) {
	var _ ImageAnalyzer = VisionAnalyzer{}
}

// Verifies fakeAnalyzer AnalyzeImage returns configured OCR text and labels.
func TestFakeAnalyzer_AnalyzeImage(t *testing.T) {
	fa := &fakeAnalyzer{
		ocrText: "hello from image",
		labels:  []string{"document", "text"},
	}
	text, labels, err := fa.AnalyzeImage("/some/image.png")
	if err != nil {
		t.Fatalf("AnalyzeImage: %v", err)
	}
	if text != "hello from image" {
		t.Fatalf("ocrText = %q", text)
	}
	if len(labels) != 2 || labels[0] != "document" {
		t.Fatalf("labels = %v", labels)
	}
}

// Verifies fakeAnalyzer AnalyzeImage propagates the configured error.
func TestFakeAnalyzer_AnalyzeImage_error(t *testing.T) {
	fa := &fakeAnalyzer{ocrErr: errors.New("vision unavailable")}
	_, _, err := fa.AnalyzeImage("/path.png")
	if err == nil || err.Error() != "vision unavailable" {
		t.Fatalf("expected 'vision unavailable', got %v", err)
	}
}

// Verifies fakeAnalyzer OCRPDFPages returns configured PDF text.
func TestFakeAnalyzer_OCRPDFPages(t *testing.T) {
	fa := &fakeAnalyzer{pdfText: "page one text\npage two text"}
	text, err := fa.OCRPDFPages("/doc.pdf")
	if err != nil {
		t.Fatalf("OCRPDFPages: %v", err)
	}
	if text != "page one text\npage two text" {
		t.Fatalf("text = %q", text)
	}
}

// Verifies fakeAnalyzer OCRPDFPages propagates the configured error.
func TestFakeAnalyzer_OCRPDFPages_error(t *testing.T) {
	fa := &fakeAnalyzer{pdfErr: errors.New("pdf ocr failed")}
	_, err := fa.OCRPDFPages("/doc.pdf")
	if err == nil || err.Error() != "pdf ocr failed" {
		t.Fatalf("expected 'pdf ocr failed', got %v", err)
	}
}
