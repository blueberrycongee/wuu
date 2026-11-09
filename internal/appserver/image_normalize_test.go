package appserver

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// TestNormalizeTurnStartImagesCompressesLargeImage verifies that a TurnStartImage
// carrying an oversized JPEG is routed through imageproc and returned at the
// 2048x2048 budget, mirroring what `wuu exec --image` does on the CLI path.
// This is the app-server's third (and last) entry point that needed wiring
// before every shell compressed images by default.
func TestNormalizeTurnStartImagesCompressesLargeImage(t *testing.T) {
	large := encodeTestJPEG(t, 3000, 3000, 95)
	in := TurnStartImage{
		MediaType: "image/jpeg",
		Data:      base64.StdEncoding.EncodeToString(large),
	}

	out, err := normalizeTurnStartImages([]TurnStartImage{in})
	if err != nil {
		t.Fatalf("normalizeTurnStartImages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 normalized image, got %d", len(out))
	}
	if out[0].MediaType != "image/jpeg" {
		t.Fatalf("media type = %q, want image/jpeg", out[0].MediaType)
	}
	decoded, err := base64.StdEncoding.DecodeString(out[0].Data)
	if err != nil {
		t.Fatalf("decode result base64: %v", err)
	}
	if len(decoded) >= len(large) {
		t.Fatalf("expected compressed output (%d bytes) smaller than input (%d bytes)", len(decoded), len(large))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if cfg.Width != 2048 || cfg.Height != 2048 {
		t.Fatalf("output dims = %dx%d, want 2048x2048", cfg.Width, cfg.Height)
	}
	if out[0].Width != uint32(cfg.Width) || out[0].Height != uint32(cfg.Height) {
		t.Fatalf("metadata dims = %dx%d, want %dx%d", out[0].Width, out[0].Height, cfg.Width, cfg.Height)
	}
}

// TestNormalizeTurnStartImagesOriginalBypassesResize verifies the Original
// flag on TurnStartImage is honored. Even at 3000x3000 the bytes must round-trip
// unchanged when the caller asks for original resolution.
func TestNormalizeTurnStartImagesOriginalBypassesResize(t *testing.T) {
	large := encodeTestJPEG(t, 3000, 3000, 90)
	in := TurnStartImage{
		MediaType: "image/jpeg",
		Data:      base64.StdEncoding.EncodeToString(large),
		Original:  true,
	}

	out, err := normalizeTurnStartImages([]TurnStartImage{in})
	if err != nil {
		t.Fatalf("normalizeTurnStartImages: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(out[0].Data)
	if err != nil {
		t.Fatalf("decode result base64: %v", err)
	}
	if !bytes.Equal(decoded, large) {
		t.Fatalf("Original=true must return original bytes unchanged (got %d bytes, want %d)", len(decoded), len(large))
	}
	if out[0].Width != 3000 || out[0].Height != 3000 {
		t.Fatalf("metadata dims = %dx%d, want 3000x3000", out[0].Width, out[0].Height)
	}
}

// TestNormalizeTurnStartImagesRejectsNonImageMediaType covers the case where
// the caller's media type is not image/*. normalizeImagePayload rejects the
// payload before imageproc runs, which is the right place for that check.
func TestNormalizeTurnStartImagesRejectsNonImageMediaType(t *testing.T) {
	in := TurnStartImage{
		MediaType: "application/pdf",
		Data:      base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\n%fake\n")),
	}
	_, err := normalizeTurnStartImages([]TurnStartImage{in})
	if err == nil {
		t.Fatalf("expected error for non-image media type")
	}
	if !contains(err.Error(), "unsupported media type") {
		t.Fatalf("error %q should surface normalizeImagePayload's media-type rejection", err)
	}
}

// TestNormalizeTurnStartImagesAcceptsDataURL ensures callers may submit the
// `data:image/jpeg;base64,...` envelope in addition to raw base64. Both
// shapes must reach imageproc.
func TestNormalizeTurnStartImagesAcceptsDataURL(t *testing.T) {
	small := encodeTestJPEG(t, 800, 600, 90)
	envelope := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(small)
	in := TurnStartImage{
		MediaType: "image/jpeg",
		Data:      envelope,
	}
	out, err := normalizeTurnStartImages([]TurnStartImage{in})
	if err != nil {
		t.Fatalf("normalizeTurnStartImages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 image, got %d", len(out))
	}
}

func encodeTestJPEG(t *testing.T, w, h, quality int) []byte {
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

// contains avoids pulling strings into the test file just for one check.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
