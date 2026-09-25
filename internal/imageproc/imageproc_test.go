package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeSolidPNG builds a deterministic solid-color PNG of the given size. The
// color varies with (x, y) so any unintended resize is observable in pixel
// tests; for byte-level tests the exact pixels don't matter.
func makeSolidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func makeSolidJPEG(t *testing.T, w, h int, quality int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}))
	return buf.Bytes()
}

func makeAnimatedGIF(t *testing.T, w, h int) []byte {
	t.Helper()
	pal := color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{255, 255, 255, 255}}
	g := &gif.GIF{
		Image: make([]*image.Paletted, 2),
		Delay: []int{10, 10},
	}
	for i := range g.Image {
		frame := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				frame.SetColorIndex(x, y, uint8(i))
			}
		}
		g.Image[i] = frame
		g.Disposal = append(g.Disposal, uint8(gif.DisposalNone))
	}
	var buf bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buf, g))
	return buf.Bytes()
}

func TestEncodePassthroughJPEG(t *testing.T) {
	data := makeSolidJPEG(t, 800, 600, 90)
	res, err := Encode("photo.jpg", data, Options{})
	require.NoError(t, err)
	assert.True(t, res.Skipped, "small JPEG should pass through")
	assert.False(t, res.Resized)
	assert.Equal(t, "image/jpeg", res.MediaType)
	assert.Equal(t, uint32(800), res.Width)
	assert.Equal(t, uint32(600), res.Height)
	assert.Equal(t, data, res.Bytes, "passthrough must return the original bytes unchanged")
}

func TestEncodePassthroughPNG(t *testing.T) {
	data := makeSolidPNG(t, 100, 100)
	res, err := Encode("tiny.png", data, Options{})
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Equal(t, "image/png", res.MediaType)
	assert.Equal(t, data, res.Bytes)
}

func TestEncodeResizeOversizedJPEG(t *testing.T) {
	data := makeSolidJPEG(t, 4000, 3000, 90)
	res, err := Encode("huge.jpg", data, Options{})
	require.NoError(t, err)
	assert.True(t, res.Resized)
	assert.False(t, res.Skipped)
	assert.Equal(t, "image/jpeg", res.MediaType)
	assert.Equal(t, uint32(2048), res.Width, "long side should be clamped to MaxDimension")
	assert.Equal(t, uint32(1536), res.Height, "short side preserves aspect ratio")
	assert.Less(t, len(res.Bytes), len(data), "resized output should be smaller than input")
}

func TestEncodeResizeOversizedPNG(t *testing.T) {
	data := makeSolidPNG(t, 3000, 3000)
	res, err := Encode("square.png", data, Options{})
	require.NoError(t, err)
	assert.True(t, res.Resized)
	assert.Equal(t, "image/png", res.MediaType, "PNG re-encodes as PNG to preserve alpha")
	assert.Equal(t, uint32(2048), res.Width)
	assert.Equal(t, uint32(2048), res.Height)
}

func TestEncodeResizeWithPatchBudget(t *testing.T) {
	data := makeSolidJPEG(t, 8000, 8000, 90)
	res, err := Encode("patchbudget.jpg", data, Options{Mode: ModeResizeWithLimits})
	require.NoError(t, err)
	assert.True(t, res.Resized)
	assert.LessOrEqual(t, int64(res.Width)*int64(res.Height), int64(DefaultMaxPatches*PatchSize*PatchSize),
		"output area must fit within the patch budget")
}

func TestEncodeResizeWithPatchBudgetAlreadyFits(t *testing.T) {
	data := makeSolidJPEG(t, 800, 600, 90)
	res, err := Encode("fits.jpg", data, Options{Mode: ModeResizeWithLimits})
	require.NoError(t, err)
	assert.True(t, res.Skipped, "images that already fit should pass through")
}

func TestEncodeOriginalBypassesResize(t *testing.T) {
	data := makeSolidJPEG(t, 5000, 4000, 90)
	res, err := Encode("raw.jpg", data, Options{Mode: ModeOriginal})
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Equal(t, data, res.Bytes)
	assert.Equal(t, uint32(5000), res.Width)
	assert.Equal(t, uint32(4000), res.Height)
}

func TestEncodeUnsupportedFormatReturnsTypedError(t *testing.T) {
	// HEIC has the "ftyp" box with "heic" as major brand; we don't need a
	// valid file, just bytes that are neither PNG, JPEG, GIF, nor WebP.
	heic := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}
	_, err := Encode("photo.heic", heic, Options{})
	require.Error(t, err)
	var ipErr *Error
	require.ErrorAs(t, err, &ipErr)
	assert.Equal(t, KindUnsupportedFormat, ipErr.Kind)
}

func TestEncodeAnimatedGIFReturnsTypedError(t *testing.T) {
	data := makeAnimatedGIF(t, 16, 16)
	_, err := Encode("anim.gif", data, Options{})
	require.Error(t, err)
	var ipErr *Error
	require.ErrorAs(t, err, &ipErr)
	assert.Equal(t, KindUnsupportedFormat, ipErr.Kind)
}

func TestEncodeStaticGIFPassthrough(t *testing.T) {
	// Static single-frame GIF; should pass through.
	pal := color.Palette{color.RGBA{0, 0, 0, 255}}
	frame := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	var buf bytes.Buffer
	require.NoError(t, gif.Encode(&buf, frame, nil))
	res, err := Encode("static.gif", buf.Bytes(), Options{})
	require.NoError(t, err)
	assert.Equal(t, "image/gif", res.MediaType)
}

func TestEncodeTooLargeReturnsTypedError(t *testing.T) {
	// Synthetic oversize input that still doesn't match a known signature.
	huge := make([]byte, MaxInputBytes+1)
	_, err := Encode("huge.bin", huge, Options{})
	require.Error(t, err)
	var ipErr *Error
	require.ErrorAs(t, err, &ipErr)
	assert.Equal(t, KindTooLarge, ipErr.Kind)
}

func TestEncodeCorruptJPEGReturnsTypedError(t *testing.T) {
	// Valid JPEG magic, then garbage.
	bad := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00}
	_, err := Encode("bad.jpg", bad, Options{})
	require.Error(t, err)
	var ipErr *Error
	require.ErrorAs(t, err, &ipErr)
	assert.Equal(t, KindDecode, ipErr.Kind)
}
