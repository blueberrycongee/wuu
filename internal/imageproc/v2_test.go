package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeSaltedJPEG builds a JPEG whose pixel values depend on the salt byte.
// Used by cache tests that need distinct inputs (and therefore distinct
// sha1 digests) without resorting to mutating raw JPEG bytes — which would
// break the EOI marker and corrupt the file.
func makeSaltedJPEG(t *testing.T, w, h, quality int, salt byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x + int(salt)) % 256),
				G: uint8((y + int(salt)) % 256),
				B: 0x80,
				A: 0xFF,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// makeJPEGWithExif returns a minimal JPEG byte stream whose APP1 segment
// carries an EXIF block with the given Orientation tag value. The bytes
// after the APP1 segment are intentionally not a valid JPEG image body;
// these fixtures are only used to exercise exifOrientation, which walks
// markers without decoding pixels.
func makeJPEGWithExif(t *testing.T, orientation uint16) []byte {
	t.Helper()

	// TIFF block (little-endian):
	//   8 bytes header
	//   2 bytes numEntries (=1)
	//   12 bytes for the Orientation entry
	//   4 bytes next-IFD offset (=0)
	tiff := []byte{
		'I', 'I', // byte order
		0x2A, 0x00, // TIFF magic
		0x08, 0x00, 0x00, 0x00, // IFD0 offset (relative to TIFF start)
		0x01, 0x00, // numEntries
		0x12, 0x01, // tag 0x0112 = Orientation
		0x03, 0x00, // type SHORT
		0x01, 0x00, 0x00, 0x00, // count 1
		byte(orientation), byte(orientation >> 8), 0x00, 0x00, // value
		0x00, 0x00, 0x00, 0x00, // next IFD = none
	}

	segContent := append([]byte{'E', 'x', 'i', 'f', 0x00, 0x00}, tiff...)
	segLen := uint16(len(segContent) + 2) // +2 for the length field itself
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	app1 = append(app1, segContent...)

	out := []byte{0xFF, 0xD8} // SOI
	out = append(out, app1...)
	return out
}

func TestExifOrientationParsedForAllValidValues(t *testing.T) {
	for orient := uint16(1); orient <= 8; orient++ {
		data := makeJPEGWithExif(t, orient)
		assert.Equal(t, orient, exifOrientation(data), "orientation %d round-trip", orient)
	}
}

func TestExifOrientationMissingEXIFReturnsOne(t *testing.T) {
	// JPEG SOI + DQT-ish filler, no APP1 EXIF.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xDB, 0x00, 0x04, 0x00, 0x00}
	assert.Equal(t, uint16(1), exifOrientation(jpeg))
}

func TestExifOrientationNonJPEGReturnsOne(t *testing.T) {
	// PNG magic should short-circuit before marker walk.
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
	assert.Equal(t, uint16(1), exifOrientation(png))
}

func TestExifOrientationTooShortReturnsOne(t *testing.T) {
	assert.Equal(t, uint16(1), exifOrientation(nil))
	assert.Equal(t, uint16(1), exifOrientation([]byte{0xFF}))
	assert.Equal(t, uint16(1), exifOrientation([]byte{0xFF, 0xD8}))
}

func TestExifOrientationAPP1WithoutExifSignatureReturnsOne(t *testing.T) {
	// XMP uses APP1 with "http://ns.adobe.com/xap/1.0/\0" — not Exif.
	xmpSig := []byte("http://ns.adobe.com/xap/1.0/\x00")
	segContent := append(xmpSig, []byte("minimal xmp payload")...)
	segLen := uint16(len(segContent) + 2)
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	app1 = append(app1, segContent...)
	jpeg := append([]byte{0xFF, 0xD8}, app1...)
	assert.Equal(t, uint16(1), exifOrientation(jpeg))
}

func TestExifOrientationTruncatedTIFFReturnsOne(t *testing.T) {
	// APP1 declares Exif signature but TIFF body is too short to be valid.
	segContent := []byte{'E', 'x', 'i', 'f', 0, 0, 0x49, 0x49} // only 2 bytes after sig
	segLen := uint16(len(segContent) + 2)
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	app1 = append(app1, segContent...)
	jpeg := append([]byte{0xFF, 0xD8}, app1...)
	assert.Equal(t, uint16(1), exifOrientation(jpeg))
}

func TestExifOrientationBigEndianTIFF(t *testing.T) {
	// Same Orientation=6 fixture but with MM (big-endian) byte order.
	tiff := []byte{
		'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08, // TIFF header
		0x00, 0x01, // numEntries
		0x01, 0x12, // tag (big-endian)
		0x00, 0x03, // type SHORT (big-endian)
		0x00, 0x00, 0x00, 0x01, // count 1
		0x00, 0x06, 0x00, 0x00, // value 6
		0x00, 0x00, 0x00, 0x00, // next IFD
	}
	segContent := append([]byte{'E', 'x', 'i', 'f', 0, 0}, tiff...)
	segLen := uint16(len(segContent) + 2)
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	app1 = append(app1, segContent...)
	jpeg := append([]byte{0xFF, 0xD8}, app1...)
	assert.Equal(t, uint16(6), exifOrientation(jpeg))
}

// makeLabeledImage builds a small RGBA image where each pixel encodes its
// (x, y) position in the red channel. Used by applyOrientation tests to
// verify every output pixel lands where the spec says it should.
func makeLabeledImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// R = (y*W + x + 1); never zero so we can tell it from
			// zeroed-out pixels at a glance.
			img.SetRGBA(x, y, color.RGBA{R: uint8(y*w + x + 1), A: 255})
		}
	}
	return img
}

// pixelLabel returns the red-channel value the makeLabeledImage helper
// would have placed at (x, y) for an image of width W.
func pixelLabel(x, y, W int) uint8 {
	return uint8(y*W + x + 1)
}

func TestApplyOrientationOutOfRangeIsIdentity(t *testing.T) {
	src := makeLabeledImage(3, 2)
	for _, orient := range []uint16{0, 9, 100, 65535} {
		out := applyOrientation(src, orient)
		assert.Same(t, src, out,
			"orientation %d should be treated as identity", orient)
	}
}

func TestApplyOrientationFlipHorizontal(t *testing.T) {
	src := makeLabeledImage(3, 2)
	out := applyOrientation(src, 2).(*image.NRGBA)
	b := out.Bounds()
	require.Equal(t, image.Rect(0, 0, 3, 2), b)
	// dst(x, y) = src(W-1-x, y) = src(2-x, y)
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			want := pixelLabel(2-x, y, 3)
			assert.Equal(t, want, out.NRGBAAt(x, y).R,
				"flip-H at (%d, %d)", x, y)
		}
	}
}

func TestApplyOrientationRotate180(t *testing.T) {
	src := makeLabeledImage(3, 2)
	out := applyOrientation(src, 3).(*image.NRGBA)
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			want := pixelLabel(2-x, 1-y, 3)
			assert.Equal(t, want, out.NRGBAAt(x, y).R,
				"180 at (%d, %d)", x, y)
		}
	}
}

func TestApplyOrientationRotate90CW(t *testing.T) {
	// O=6: width/height swap. src 3x2 becomes 2x3. This is the most common
	// orientation tag on phone-camera photos, so get it right.
	src := makeLabeledImage(3, 2)
	out := applyOrientation(src, 6).(*image.NRGBA)
	require.Equal(t, image.Rect(0, 0, 2, 3), out.Bounds())
	// dst(x, y) = src(y, h-1-x) = src(y, 1-x)
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			want := pixelLabel(y, 1-x, 3)
			assert.Equal(t, want, out.NRGBAAt(x, y).R,
				"90 CW at (%d, %d)", x, y)
		}
	}
}

func TestApplyOrientationRotate270CW(t *testing.T) {
	src := makeLabeledImage(3, 2)
	out := applyOrientation(src, 8).(*image.NRGBA)
	require.Equal(t, image.Rect(0, 0, 2, 3), out.Bounds())
	// dst(x, y) = src(w-1-y, x) = src(2-y, x)
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			want := pixelLabel(2-y, x, 3)
			assert.Equal(t, want, out.NRGBAAt(x, y).R,
				"270 CW at (%d, %d)", x, y)
		}
	}
}

// TestEncodeAppliesEXIFOrientationEndToEnd confirms the wiring: a JPEG with
// Orientation=6 and small dimensions comes back with corrected orientation
// baked into pixels (width/height swap) and no longer marked Skipped.
func TestEncodeAppliesEXIFOrientationEndToEnd(t *testing.T) {
	resetCache()

	// Build a 2x3 JPEG, then prepend an APP1 EXIF segment that says
	// "rotate 90 CW". imageproc should detect this, strip the EXIF so
	// the Go decoder does not auto-rotate, and return a 3x2 image.
	jpgBytes := makeSolidJPEG(t, 2, 3, 90)
	exif := makeJPEGWithExif(t, 6)
	// SOI from jpg + APP1-EXIF from exif + rest of jpg (without its SOI).
	full := append(append([]byte{}, jpgBytes[:2]...), exif[2:]...)
	full = append(full, jpgBytes[2:]...)

	res, err := Encode("photo.jpg", full, Options{})
	require.NoError(t, err)
	assert.False(t, res.Skipped, "EXIF orientation forces re-encode")
	assert.True(t, res.Resized)
	assert.Equal(t, uint32(3), res.Width, "width/height must swap after 90 CW rotation")
	assert.Equal(t, uint32(2), res.Height)
}

// TestEncodeModeOriginalSkipsEXIFApplication confirms the opt-out path: the
// caller wants raw bytes, so we do not silently rewrite orientation.
func TestEncodeModeOriginalSkipsEXIFApplication(t *testing.T) {
	resetCache()
	jpgBytes := makeSolidJPEG(t, 2, 3, 90)
	exif := makeJPEGWithExif(t, 6)
	full := append(append([]byte{}, jpgBytes[:2]...), exif[2:]...)
	full = append(full, jpgBytes[2:]...)

	res, err := Encode("photo.jpg", full, Options{Mode: ModeOriginal})
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Equal(t, full, res.Bytes, "ModeOriginal must return input bytes unchanged")
}

func TestEncodeCacheMissOnDifferentMode(t *testing.T) {
	resetCache()
	data := makeSolidJPEG(t, 800, 600, 90)
	r1, err := Encode("a.jpg", data, Options{Mode: ModeDefault})
	require.NoError(t, err)
	r2, err := Encode("a.jpg", data, Options{Mode: ModeOriginal})
	require.NoError(t, err)
	assert.NotSame(t, r1, r2, "different modes must not share cache entries")
}

func TestEncodeCacheLRUEvictsOldest(t *testing.T) {
	resetCache()
	// defaultCacheSize is 32. Insert 33 distinct entries, touch the last
	// to make it MRU, then insert one more and verify the oldest entry
	// was evicted (re-encoding produces a fresh *Result) while the
	// touched entry is still cached.
	inputs := make([][]byte, defaultCacheSize+2)
	results := make([]*Result, defaultCacheSize+2)
	for i := range inputs {
		// Salt the pixel values so each input has a distinct sha1.
		inputs[i] = makeSaltedJPEG(t, 100, 100, 90, byte(i))
		r, err := Encode("c.jpg", inputs[i], Options{})
		require.NoError(t, err)
		results[i] = r
	}
	// Re-fetch the most recently inserted entry; should be the same pointer.
	touched, err := Encode("c.jpg", inputs[defaultCacheSize], Options{})
	require.NoError(t, err)
	assert.Same(t, results[defaultCacheSize], touched, "LRU should promote re-accessed entry")

	// Insert one more — this should evict results[0] (oldest).
	inputs[defaultCacheSize+1] = makeSaltedJPEG(t, 100, 100, 90, 200)
	_, err = Encode("c.jpg", inputs[defaultCacheSize+1], Options{})
	require.NoError(t, err)

	// Oldest entry should have been evicted: a fresh call returns a new pointer.
	r0Again, err := Encode("c.jpg", inputs[0], Options{})
	require.NoError(t, err)
	assert.NotSame(t, results[0], r0Again, "oldest entry should have been evicted")

	// Touched entry should still be cached.
	touchedAgain, err := Encode("c.jpg", inputs[defaultCacheSize], Options{})
	require.NoError(t, err)
	assert.Same(t, results[defaultCacheSize], touchedAgain, "MRU entry should still be cached")
}

func TestEncodeCacheConcurrentAccessIsRaceFree(t *testing.T) {
	resetCache()
	const goroutines = 16
	const calls = 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for c := 0; c < calls; c++ {
				data := makeSaltedJPEG(t, 100, 100, 90, byte(g*calls+c))
				_, err := Encode("c.jpg", data, Options{})
				assert.NoError(t, err)
			}
		}(g)
	}
	wg.Wait()
}
