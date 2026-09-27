package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/imageproc"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Bound both compressed input and decoded pixels before imageproc allocates a
// bitmap. The output must also fit the shared inline tool-result contract.
const (
	maxReadImageBytes  = 20 * 1024 * 1024
	maxReadImagePixels = 40_000_000
)

func readFileImage(ctx context.Context, file *os.File, path string, size int64) (toolresult.Result, error) {
	if size > maxReadImageBytes {
		return toolresult.Result{}, fmt.Errorf("image too large (%d bytes, max %d); resize or crop it before reading", size, maxReadImageBytes)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return toolresult.Result{}, fmt.Errorf("seek image: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxReadImageBytes+1))
	if err != nil {
		return toolresult.Result{}, fmt.Errorf("read image: %w", err)
	}
	if len(data) > maxReadImageBytes {
		return toolresult.Result{}, fmt.Errorf("image exceeds %d bytes; resize or crop it before reading", maxReadImageBytes)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return toolresult.Result{}, fmt.Errorf("cannot decode image: %w; use PNG, JPEG, static GIF, or WebP", err)
	}
	if uint64(config.Width)*uint64(config.Height) > maxReadImagePixels {
		return toolresult.Result{}, fmt.Errorf("image dimensions %dx%d exceed %d pixels; resize or crop it before reading", config.Width, config.Height, maxReadImagePixels)
	}
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	encoded, err := imageproc.Encode(path, data, imageproc.Options{})
	if err != nil {
		return toolresult.Result{}, err
	}
	if len(encoded.Bytes) > toolresult.MaxInlineDataBytes {
		return toolresult.Result{}, fmt.Errorf("normalized image exceeds the %d-byte tool attachment limit; resize or crop it before reading", toolresult.MaxInlineDataBytes)
	}
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	text, err := mustJSON(map[string]any{
		"action": "read_image", "path": path, "media_type": encoded.MediaType,
		"width": encoded.Width, "height": encoded.Height,
		"source_width": config.Width, "source_height": config.Height, "resized": encoded.Resized,
	})
	if err != nil {
		return toolresult.Result{}, err
	}
	// Images are evidence, not source-edit context: do not add them to the text
	// read cache or replace repeat reads with an unchanged-file stub.
	return toolresult.Result{Content: []toolresult.ContentPart{
		{Type: toolresult.ContentTypeText, Text: text},
		{Type: toolresult.ContentTypeImage, MIMEType: encoded.MediaType, Data: base64.StdEncoding.EncodeToString(encoded.Bytes), Name: filepath.Base(path)},
	}}, nil
}
