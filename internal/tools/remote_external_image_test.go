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
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

func TestRemoteImageRequiresValidatedNativeImage(t *testing.T) {
	ctx := context.Background()
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	scope, _, err := kit.PrepareRemoteExternalRead(ctx, &readContinuationRemote{}, `{"path":"folder/image.png"}`)
	require.NoError(t, err)
	data := readImagePNG(t, 4, 2)
	view, _ := json.Marshal(map[string]any{"action": "read_image", "path": "folder/image.png", "media_type": "image/png"})
	native := toolresult.Result{Content: []toolresult.ContentPart{{Type: "text", Text: string(view)}, {Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(data)}}}
	_, err = scope.AcceptRemoteNativeImage(ctx, native, false)
	require.Error(t, err, "text-only providers must not receive native media")
	_, err = scope.Render(ctx, toolresult.Result{Content: native.Content[1:]})
	require.Error(t, err, "an extension cannot promote an unvalidated read into an image")
	invalid := native
	invalid.Content = append([]toolresult.ContentPart(nil), native.Content...)
	invalid.Content[0].Text = `{"action":"read_image","path":"other.png","media_type":"image/png"}`
	_, err = scope.AcceptRemoteNativeImage(ctx, invalid, true)
	require.Error(t, err)
	invalid.Content[0] = native.Content[0]
	invalid.Content[1].MIMEType = "image/jpeg"
	_, err = scope.AcceptRemoteNativeImage(ctx, invalid, true)
	require.Error(t, err)
	invalid.Content[1] = native.Content[1]
	invalid.Content[1].Data = base64.StdEncoding.EncodeToString([]byte("bad"))
	_, err = scope.AcceptRemoteNativeImage(ctx, invalid, true)
	require.Error(t, err)
	bomb := append([]byte(nil), data...)
	binary.BigEndian.PutUint32(bomb[16:20], 100000)
	binary.BigEndian.PutUint32(bomb[20:24], 100000)
	binary.BigEndian.PutUint32(bomb[29:33], crc32.ChecksumIEEE(bomb[12:29]))
	invalid.Content[1].Data = base64.StdEncoding.EncodeToString(bomb)
	_, err = scope.AcceptRemoteNativeImage(ctx, invalid, true)
	require.ErrorContains(t, err, "pixels")
	tooLarge := make([]byte, toolresult.MaxInlineDataBytes+1)
	invalid.Content[1].Data = base64.StdEncoding.EncodeToString(tooLarge)
	_, err = scope.AcceptRemoteNativeImage(ctx, invalid, true)
	require.Error(t, err)
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var animated bytes.Buffer
	require.NoError(t, gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{0, 0}}))
	invalid.Content[0].Text = `{"action":"read_image","path":"folder/image.png","media_type":"image/gif"}`
	invalid.Content[1].MIMEType = "image/gif"
	invalid.Content[1].Data = base64.StdEncoding.EncodeToString(animated.Bytes())
	_, err = scope.AcceptRemoteNativeImage(ctx, invalid, true)
	require.ErrorContains(t, err, "animated GIF")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = scope.AcceptRemoteNativeImage(canceled, native, true)
	require.ErrorIs(t, err, context.Canceled)
	passed, err := scope.AcceptRemoteNativeImage(ctx, native, true)
	require.NoError(t, err)
	require.Len(t, passed.Content, 1)
	require.Equal(t, toolresult.ContentTypeImage, passed.Content[0].Type)
	altered := readImagePNG(t, 5, 3)
	rendered, err := scope.Render(ctx, toolresult.Result{Content: []toolresult.ContentPart{{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(altered)}}})
	require.NoError(t, err)
	require.Contains(t, rendered.Content[0].Text, `"source":"extension"`)
	require.Equal(t, base64.StdEncoding.EncodeToString(altered), rendered.Content[1].Data)
}
