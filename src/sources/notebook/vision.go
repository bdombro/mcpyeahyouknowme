package notebook

// #cgo CFLAGS: -x objective-c
// #cgo LDFLAGS: -framework Vision -framework Foundation -framework CoreGraphics -framework AppKit
// #include <stdlib.h>
//
// // analyze_image runs VNRecognizeTextRequest and VNClassifyImageRequest on an image file.
// // Returns a malloc'd JSON string: {"ocr_text":"...","labels":["...",...]} or an error prefix.
// // Caller must free the returned string.
// char* analyze_image(const char* path);
//
// // ocr_pdf_pages renders each PDF page to a CGImage and runs VNRecognizeTextRequest on each.
// // Returns a malloc'd concatenated OCR string, or empty on failure. Caller must free.
// char* ocr_pdf_pages(const char* path);
import "C"

import (
	"encoding/json"
	"strings"
	"unsafe"
)

// #include implementations are in vision_objc.m (same package, compiled by cgo)

// ImageAnalyzer abstracts Vision OCR and classification so tests can inject a fake without calling the real framework.
type ImageAnalyzer interface {
	// AnalyzeImage runs OCR and image classification on an image file, returning extracted text and label strings.
	AnalyzeImage(path string) (ocrText string, labels []string, err error)
	// OCRPDFPages renders each PDF page via CoreGraphics and runs OCR, returning concatenated page text.
	OCRPDFPages(path string) (string, error)
}

// VisionAnalyzer calls the macOS Vision framework via CGO Objective-C.
type VisionAnalyzer struct{}

// AnalyzeImage runs VNRecognizeTextRequest and VNClassifyImageRequest on an image file via the macOS Vision framework.
func (VisionAnalyzer) AnalyzeImage(path string) (ocrText string, labels []string, err error) { // nocov — calls real CGO Vision framework
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cResult := C.analyze_image(cPath)
	if cResult == nil {
		return "", nil, nil
	}
	defer C.free(unsafe.Pointer(cResult))
	result := C.GoString(cResult)

	if strings.HasPrefix(result, "error:") {
		return "", nil, nil
	}

	var payload struct {
		OCRText string   `json:"ocr_text"`
		Labels  []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return "", nil, nil
	}
	return payload.OCRText, payload.Labels, nil
}

// OCRPDFPages renders each page of a PDF via CoreGraphics then runs VNRecognizeTextRequest on each page image.
func (VisionAnalyzer) OCRPDFPages(path string) (string, error) { // nocov — calls real CGO Vision framework
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cResult := C.ocr_pdf_pages(cPath)
	if cResult == nil {
		return "", nil
	}
	defer C.free(unsafe.Pointer(cResult))
	return C.GoString(cResult), nil
}
