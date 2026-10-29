package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

// The content source and image are deliberately different: the host validates
// and normalizes extension output rather than returning the native file bytes.
func TestExternalReadImageContract(t *testing.T) {
	root := t.TempDir()
	native := readImagePNG(t, 4, 2)
	alternate := readImagePNG(t, 7, 3)
	require.NoError(t, os.WriteFile(filepath.Join(root, "image.png"), native, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "oversize.png"), native, 0600))
	require.NoError(t, os.Truncate(filepath.Join(root, "oversize.png"), maxReadImageBytes+1))
	require.NoError(t, os.WriteFile(filepath.Join(root, "text.txt"), []byte("text"), 0600))
	kit, err := New(root)
	require.NoError(t, err)
	_, err = kit.PrepareExternalRead(context.Background(), `{"path":"image.png"}`, false)
	require.Error(t, err, "old text-only providers must keep refusing images")
	_, err = kit.PrepareExternalRead(context.Background(), `{"path":"oversize.png"}`, true)
	require.Error(t, err, "the native source size bound must be enforced before plugin dispatch")
	for _, args := range []string{`{"path":"image.png","limit":1}`, `{"path":"image.png","offset":2}`} {
		_, err = kit.PrepareExternalRead(context.Background(), args, true)
		require.Error(t, err, "image ranges are unavailable")
	}
	scope, err := kit.PrepareExternalRead(context.Background(), `{"path":"image.png"}`, true)
	require.NoError(t, err)
	result, err := scope.Render(context.Background(), toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(alternate)}}})
	require.NoError(t, err)
	projection := providers.ProjectToolResult(result)
	require.Len(t, projection.ObservationImages, 1)
	require.Equal(t, base64.StdEncoding.EncodeToString(alternate), projection.ObservationImages[0].Data)
	require.NotContains(t, projection.ToolText, projection.ObservationImages[0].Data)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal([]byte(projection.ToolText), &metadata))
	require.Equal(t, "extension", metadata["source"])
	require.Equal(t, false, metadata["content_verified"])
	require.Empty(t, kit.env.ReadEntries())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = scope.Render(canceled, toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(alternate)}}})
	require.ErrorIs(t, err, context.Canceled)
	for _, invalid := range []toolresult.Result{
		{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/jpeg", Data: base64.StdEncoding.EncodeToString(alternate)}}},
		{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("not image"))}}},
		{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", URI: "https://example.test/image"}}},
		{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "fake"}}},
		{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(alternate)}}, StructuredContent: json.RawMessage(`{"text":"fake"}`)},
		{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", toolresult.MaxInlineDataBytes+1)))}}},
	} {
		_, err := scope.Render(context.Background(), invalid)
		require.Error(t, err)
	}
	textScope, err := kit.PrepareExternalRead(context.Background(), `{"path":"text.txt"}`, true)
	require.NoError(t, err)
	_, err = textScope.Render(context.Background(), toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(alternate)}}})
	require.Error(t, err, "text request cannot be turned into an image")
	large := readImagePNG(t, 4096, 4)
	result, err = scope.Render(context.Background(), toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(large)}}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(providers.ProjectToolResult(result).ToolText), &metadata))
	require.Equal(t, float64(2048), metadata["width"])
	require.Equal(t, true, metadata["resized"])
	oversizedHeader := append([]byte(nil), alternate...)
	binary.BigEndian.PutUint32(oversizedHeader[16:20], 100000)
	binary.BigEndian.PutUint32(oversizedHeader[20:24], 100000)
	binary.BigEndian.PutUint32(oversizedHeader[29:33], crc32.ChecksumIEEE(oversizedHeader[12:29]))
	_, err = scope.Render(context.Background(), toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(oversizedHeader)}}})
	require.ErrorContains(t, err, "pixels")
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var animated bytes.Buffer
	require.NoError(t, gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{0, 0}}))
	_, err = scope.Render(context.Background(), toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/gif", Data: base64.StdEncoding.EncodeToString(animated.Bytes())}}})
	require.ErrorContains(t, err, "animated GIF")
}

func TestExternalImageInvalidatesOnlyPriorNativeObservation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(path, []byte("original text\n"), 0600))
	kit, err := New(root)
	require.NoError(t, err)
	_, err = kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"file"}`})
	require.NoError(t, err)
	require.Len(t, kit.env.ReadEntries(), 1)
	img := readImagePNG(t, 4, 2)
	require.NoError(t, os.WriteFile(path, img, 0600))
	scope, err := kit.PrepareExternalRead(context.Background(), `{"path":"file"}`, true)
	require.NoError(t, err)
	valid := toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(img)}}}
	_, err = scope.Render(context.Background(), toolresult.Result{})
	require.Error(t, err)
	require.Len(t, kit.env.ReadEntries(), 1, "failed image must not change observation")
	_, err = scope.Render(context.Background(), valid)
	require.NoError(t, err)
	require.Empty(t, kit.env.ReadEntries(), "successful image must invalidate prior text observation")
	var old ReadFileEntry
	old.ContentSHA256 = "old"
	kit.env.RecordRead(path, old)
	scope, err = kit.PrepareExternalRead(context.Background(), `{"path":"file"}`, true)
	require.NoError(t, err)
	// An identical later observation is still a new observation.
	kit.env.RecordRead(path, old)
	_, err = scope.Render(context.Background(), valid)
	require.NoError(t, err)
	require.Len(t, kit.env.ReadEntries(), 1, "a concurrent newer observation must survive")
}
