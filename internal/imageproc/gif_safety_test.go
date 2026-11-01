package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGIFAnimationCheckDoesNotDecodeEveryFrame(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 256, 256), color.Palette{color.Black, color.White})
	frames := make([]*image.Paletted, 128)
	delays := make([]int, len(frames))
	for i := range frames {
		frames[i] = frame
	}
	var input bytes.Buffer
	require.NoError(t, gif.EncodeAll(&input, &gif.GIF{Image: frames, Delay: delays}))
	allocations := testing.AllocsPerRun(1, func() {
		animated, err := isAnimatedGIF(input.Bytes())
		if err != nil || !animated {
			t.Fatalf("animation=%v err=%v", animated, err)
		}
	})
	if allocations > 50 {
		t.Fatalf("animation inspection allocated %.0f objects for 128 frames", allocations)
	}
	_, err := Encode("animated.gif", input.Bytes(), Options{})
	require.Error(t, err)
}

func TestGIFInspectionAcceptsExtensionsAndRejectsTruncation(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 4, 3), color.Palette{color.Black, color.White})
	var data bytes.Buffer
	require.NoError(t, gif.Encode(&data, frame, nil))
	original := data.Bytes()
	// A comment and application metadata before the trailer must not be mistaken
	// for image blocks, including bytes that resemble an image descriptor.
	extensions := []byte{0x21, 0xfe, 4, 0x2c, 0x21, 0x3b, 0x00, 0, 0x21, 0xff, 11, 'T', 'E', 'S', 'T', 'A', 'P', 'P', '1', '2', '3', '4', 3, 0x2c, 0x3b, 0x21, 0}
	withMetadata := append(append(append([]byte(nil), original[:len(original)-1]...), extensions...), 0x3b)
	animated, err := isAnimatedGIF(withMetadata)
	require.NoError(t, err)
	require.False(t, animated)
	result, err := Encode("metadata.gif", withMetadata, Options{})
	require.NoError(t, err)
	require.Equal(t, withMetadata, result.Bytes)
	// Metadata after the first frame must still be validated; the standard
	// single-frame decoder itself stops before this malformed extension.
	badControl := append(append([]byte(nil), original[:len(original)-1]...), 0x21, 0xf9, 3, 0, 0, 0, 0, 0x3b)
	_, err = Encode("bad-control.gif", badControl, Options{})
	require.Error(t, err)
	for _, n := range []int{1, 5, 12, 13, len(original) / 2, len(original) - 1} {
		_, err := isAnimatedGIF(original[:n])
		require.Error(t, err, "truncation at %d", n)
	}
}

func TestGIFInspectionRejectsOversizedSingleFrameBeforeDecode(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var data bytes.Buffer
	require.NoError(t, gif.Encode(&data, frame, nil))
	large := append([]byte(nil), data.Bytes()...)
	// Set the logical canvas and frame dimensions to 65535×65535 without
	// expanding the compressed input; the decoder must reject before allocation.
	large[6], large[7], large[8], large[9] = 255, 255, 255, 255
	descriptor := bytes.IndexByte(large[13:], 0x2c)
	require.GreaterOrEqual(t, descriptor, 0)
	descriptor += 13
	large[descriptor+5], large[descriptor+6], large[descriptor+7], large[descriptor+8] = 255, 255, 255, 255
	_, err := Encode("huge.gif", large, Options{})
	require.Error(t, err)
	var imageErr *Error
	require.ErrorAs(t, err, &imageErr)
	require.Equal(t, KindTooLarge, imageErr.Kind)
}
