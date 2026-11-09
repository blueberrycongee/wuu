package exec

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRunAttachmentsLoadsFilesAndImages(t *testing.T) {
	root := t.TempDir()
	imageBytes := encodePNG(t, 4, 4)
	pdfBytes := []byte("%PDF-1.7\n%test\n")
	if err := os.WriteFile(filepath.Join(root, "shot.png"), imageBytes, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), pdfBytes, 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	attachments, err := resolveRunAttachments(Options{
		Workdir:    root,
		ImagePaths: []string{"shot.png"},
		FilePaths:  []string{"report.pdf"},
	})
	if err != nil {
		t.Fatalf("resolveRunAttachments: %v", err)
	}
	if len(attachments.Images) != 1 || attachments.Images[0].MediaType != "image/png" {
		t.Fatalf("unexpected image attachment: %+v", attachments.Images)
	}
	if got := attachments.Images[0].Data; got != base64.StdEncoding.EncodeToString(imageBytes) {
		t.Fatalf("image data = %q", got)
	}
	if len(attachments.Files) != 1 || attachments.Files[0].MediaType != "application/pdf" || attachments.Files[0].Filename != "report.pdf" {
		t.Fatalf("unexpected file attachment: %+v", attachments.Files)
	}
	if got := attachments.Files[0].Data; got != base64.StdEncoding.EncodeToString(pdfBytes) {
		t.Fatalf("file data = %q", got)
	}
}

func TestResolveRunAttachmentsRejectsNonImageForImageFlag(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write text: %v", err)
	}

	_, err := resolveRunAttachments(Options{Workdir: root, ImagePaths: []string{"notes.txt"}})
	if err == nil || !strings.Contains(err.Error(), "expected image/*") {
		t.Fatalf("expected image media type error, got %v", err)
	}
}

func TestResolveRunAttachmentsRejectsNonPDFFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write text: %v", err)
	}

	_, err := resolveRunAttachments(Options{Workdir: root, FilePaths: []string{"notes.txt"}})
	if err == nil || !strings.Contains(err.Error(), "only PDF files are supported") {
		t.Fatalf("expected PDF media type error, got %v", err)
	}
}

func TestResolveRunAttachmentsDefersImageNormalizationToAppServer(t *testing.T) {
	root := t.TempDir()
	large := encodeJPEG(t, 3000, 3000, 95)
	if err := os.WriteFile(filepath.Join(root, "big.jpg"), large, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	attachments, err := resolveRunAttachments(Options{
		Workdir:    root,
		ImagePaths: []string{"big.jpg"},
	})
	if err != nil {
		t.Fatalf("resolveRunAttachments: %v", err)
	}
	if len(attachments.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(attachments.Images))
	}
	if attachments.Images[0].MediaType != "image/jpeg" {
		t.Fatalf("media type = %q, want image/jpeg", attachments.Images[0].MediaType)
	}
	decoded, err := base64.StdEncoding.DecodeString(attachments.Images[0].Data)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if !bytes.Equal(decoded, large) {
		t.Fatalf("exec must forward original bytes for app-server normalization (got %d bytes, want %d)", len(decoded), len(large))
	}
	if attachments.Images[0].Original {
		t.Fatal("default image attachment must allow app-server normalization")
	}
}

// TestResolveRunAttachmentsOriginalBypassesResize verifies the --image-original
// opt-out flag (Codex ImageDetail::Original equivalent) leaves the bytes
// untouched regardless of input size.
func TestResolveRunAttachmentsOriginalBypassesResize(t *testing.T) {
	root := t.TempDir()
	large := encodeJPEG(t, 3000, 3000, 90)
	if err := os.WriteFile(filepath.Join(root, "raw.jpg"), large, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	attachments, err := resolveRunAttachments(Options{
		Workdir:       root,
		ImagePaths:    []string{"raw.jpg"},
		ImageOriginal: true,
	})
	if err != nil {
		t.Fatalf("resolveRunAttachments: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(attachments.Images[0].Data)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if !bytes.Equal(decoded, large) {
		t.Fatalf("ImageOriginal=true must return the original bytes unchanged (got %d bytes, want %d)", len(decoded), len(large))
	}
	if !attachments.Images[0].Original {
		t.Fatal("ImageOriginal=true must be preserved across the app-server protocol")
	}
}

// TestResolveRunAttachmentsUnsupportedFormatSurfacesTypedError verifies that a
// HEIC input is rejected with a clear error message rather than silently
// forwarded as raw bytes (the previous behavior, which could send 200MB to
// the model).
func TestResolveRunAttachmentsUnsupportedFormatSurfacesTypedError(t *testing.T) {
	root := t.TempDir()
	// HEIC-shaped bytes behind a .jpg extension: MIME detection by extension
	// passes, then imageproc must reject because the content signature is
	// neither PNG/JPEG/GIF/WebP.
	heic := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}
	if err := os.WriteFile(filepath.Join(root, "fake.jpg"), heic, 0o644); err != nil {
		t.Fatalf("write heic-as-jpg: %v", err)
	}

	_, err := resolveRunAttachments(Options{
		Workdir:    root,
		ImagePaths: []string{"fake.jpg"},
	})
	if err == nil {
		t.Fatalf("expected error for HEIC-shaped input")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("error %q should mention unsupported format (this confirms imageproc ran instead of MIME detection bailing earlier)", err)
	}
	if !strings.Contains(err.Error(), "--image") {
		t.Fatalf("error %q should be wrapped with --image prefix so callers see the offending flag", err)
	}
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h, quality int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}
