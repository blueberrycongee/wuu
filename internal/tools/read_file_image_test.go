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
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

func readImagePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 180, A: 255})
		}
	}
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, img))
	return out.Bytes()
}

func TestReadFileImageContentAndReplay(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	require.NoError(t, err)
	kit.SetSessionDir(t.TempDir())
	data := readImagePNG(t, 32, 16)
	// Detection follows the bytes, including extensionless/generated files.
	require.NoError(t, os.WriteFile(filepath.Join(root, "capture"), data, 0600))
	call := providers.ToolCall{ID: "read-image", Name: "read_file", Arguments: `{"path":"capture"}`}
	for i := 0; i < 2; i++ {
		result, err := kit.ExecuteResult(context.Background(), call)
		require.NoError(t, err)
		require.NoError(t, result.Validate())
		projected := providers.ProjectToolResult(result)
		require.Len(t, projected.ObservationImages, 1)
		require.Equal(t, "image/png", projected.ObservationImages[0].MediaType)
		require.Equal(t, base64.StdEncoding.EncodeToString(data), projected.ObservationImages[0].Data)
		require.NotContains(t, projected.ToolText, projected.ObservationImages[0].Data)
		require.Empty(t, kit.env.ReadEntries(), "image reads must not pollute text edit/dedup state")
		// Persist/reload the canonical result, then run the same admission boundary
		// providers use. The model switch must not destroy the stored visual evidence.
		raw, err := json.Marshal(result)
		require.NoError(t, err)
		var restored toolresult.Result
		require.NoError(t, json.Unmarshal(raw, &restored))
		history := []providers.ChatMessage{
			{Role: "assistant", ToolCalls: []providers.ToolCall{call}},
			{Role: "tool", Name: call.Name, ToolCallID: call.ID, ToolResult: &restored},
		}
		messages, err := providers.PrepareMessagesForModelRequest("gpt-5", history)
		require.NoError(t, err)
		require.Len(t, messages, 3)
		require.Equal(t, projected.ObservationImages, messages[2].Images)
		filtered, err := providers.PrepareMessagesForProviderRequestWithPolicy("test", "text-only", history, providers.MediaInputPolicy{ImageKnown: true})
		require.NoError(t, err)
		require.Empty(t, filtered[2].Images)
		require.Contains(t, filtered[2].Content, "omitted: unsupported")
		require.Equal(t, result, restored)
	}
}

func TestReadFileImageFormatsAndResize(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 8))
	var jpg, staticGIF bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpg, img, nil))
	require.NoError(t, gif.Encode(&staticGIF, img, nil))
	// Minimal lossless 1x1 WebP, kept inline as a format fixture.
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, mime    string
		data          []byte
		width, height int
		resized       bool
	}{
		{"jpeg", "image/jpeg", jpg.Bytes(), 16, 8, false},
		{"gif", "image/gif", staticGIF.Bytes(), 16, 8, false},
		{"webp", "image/webp", webp, 1, 1, false},
		{"large", "image/png", readImagePNG(t, 4096, 1024), 2048, 512, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			kit, err := New(root)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "image.bin"), tc.data, 0600))
			result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"image.bin"}`})
			require.NoError(t, err)
			require.Len(t, result.Content, 2)
			require.Equal(t, tc.mime, result.Content[1].MIMEType)
			decoded, err := base64.StdEncoding.DecodeString(result.Content[1].Data)
			require.NoError(t, err)
			config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
			require.NoError(t, err)
			require.Equal(t, tc.width, config.Width)
			require.Equal(t, tc.height, config.Height)
			var metadata struct{ Resized bool }
			require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &metadata))
			require.Equal(t, tc.resized, metadata.Resized)
		})
	}
}

func TestReadFileImageFailures(t *testing.T) {
	noisy := image.NewNRGBA(image.Rect(0, 0, 1200, 1200))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(noisy.Pix); i += 4 {
		noisy.Pix[i], noisy.Pix[i+1], noisy.Pix[i+2], noisy.Pix[i+3] = byte(rng.Uint32()), byte(rng.Uint32()), byte(rng.Uint32()), 255
	}
	var noisyPNG bytes.Buffer
	require.NoError(t, png.Encode(&noisyPNG, noisy))
	require.Greater(t, noisyPNG.Len(), toolresult.MaxInlineDataBytes)

	pngData := readImagePNG(t, 16, 8)
	hugeHeader := append([]byte(nil), pngData...)
	binary.BigEndian.PutUint32(hugeHeader[16:20], 100000)
	binary.BigEndian.PutUint32(hugeHeader[20:24], 100000)
	binary.BigEndian.PutUint32(hugeHeader[29:33], crc32.ChecksumIEEE(hugeHeader[12:29]))
	var animated bytes.Buffer
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	require.NoError(t, gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{0, 0}}))
	for _, tc := range []struct {
		name       string
		data       []byte
		args, want string
		size       int64
	}{
		{name: "output budget", data: noisyPNG.Bytes(), want: "tool attachment limit"},
		{name: "unsupported binary", data: []byte("\x00\x00\x00\x18ftypavif"), want: "unsupported binary"},
		{name: "line range", data: pngData, args: `{"path":"image","limit":1}`, want: "text ranges"},
		{name: "byte range", data: pngData, args: `{"path":"image","byte_range":{"offset":0,"limit":10}}`, want: "text ranges"},
		{name: "corrupt", data: pngData[:24], want: "decode"},
		{name: "animated", data: animated.Bytes(), want: "animated GIF"},
		{name: "pixel budget", data: hugeHeader, want: "pixels"},
		{name: "source budget", data: pngData, size: maxReadImageBytes + 1, want: "image too large"},
		{name: "unsupported bmp", data: append([]byte("BM"), make([]byte, 100)...), want: "cannot decode image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "image"), tc.data, 0600))
			if tc.size > 0 {
				require.NoError(t, os.Truncate(filepath.Join(root, "image"), tc.size))
			}
			kit, err := New(root)
			require.NoError(t, err)
			if tc.args == "" {
				tc.args = `{"path":"image"}`
			}
			result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: tc.args})
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, providers.ProjectToolResult(result).ObservationImages)
		})
	}
}

func TestReadFileImageScopeAndWorktree(t *testing.T) {
	kit, parent, worktree, ctx := newWorktreeExecFixture(t)
	parentData, worktreeData := readImagePNG(t, 2, 1), readImagePNG(t, 4, 2)
	require.NoError(t, os.WriteFile(filepath.Join(parent, "screen.png"), parentData, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "screen.png"), worktreeData, 0600))
	result, err := kit.ExecuteResult(ctx, providers.ToolCall{Name: "read_file", Arguments: `{"path":"screen.png"}`})
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString(worktreeData), result.Content[1].Data)
	outside := filepath.Join(t.TempDir(), "outside.png")
	require.NoError(t, os.WriteFile(outside, parentData, 0600))
	// A symlink in the execution worktree must not escape the original guard.
	require.NoError(t, os.Symlink(outside, filepath.Join(worktree, "escape.png")))
	_, err = kit.ExecuteResult(ctx, providers.ToolCall{Name: "read_file", Arguments: `{"path":"escape.png"}`})
	require.Error(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(worktree, "secrets"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "secrets", "key.png"), parentData, 0600))
	require.NoError(t, os.Symlink(filepath.Join(worktree, "secrets", "key.png"), filepath.Join(worktree, "alias.png")))
	_, err = kit.ExecuteResult(ctx, providers.ToolCall{Name: "read_file", Arguments: `{"path":"alias.png"}`})
	require.ErrorContains(t, err, "sensitive path")
	sessionDir := t.TempDir()
	kit.SetSessionDir(sessionDir)
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "capture.png"), parentData, 0600))
	result, err = kit.ExecuteResult(ctx, providers.ToolCall{Name: "read_file", Arguments: `{"path":"$SESSION_DIR/capture.png"}`})
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString(parentData), result.Content[1].Data)
}

func TestReadFileSVGRemainsText(t *testing.T) {
	root := t.TempDir()
	data := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`
	require.NoError(t, os.WriteFile(filepath.Join(root, "icon.svg"), []byte(data), 0600))
	kit, err := New(root)
	require.NoError(t, err)
	result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"icon.svg"}`})
	require.NoError(t, err)
	require.Empty(t, providers.ProjectToolResult(result).ObservationImages)
	require.True(t, strings.Contains(result.TextProjection(), "svg"))
}
