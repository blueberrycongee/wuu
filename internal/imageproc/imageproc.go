// Package imageproc normalizes image attachments before they reach model
// providers. It mirrors Codex's codex_utils_image crate: detect format, drop
// inputs the model APIs cannot consume, shrink oversized images to a
// documented budget, and re-encode with a stable quality ladder.
//
// The CLI loader, app-server image normalizer, and file-reading tools use this
// package. The Electron renderer
// (desktop/src/renderer/ComposerMessages.ts) keeps its own canvas path as a
// fast pre-compression before the bytes ever cross the IPC boundary; the
// constants there must stay aligned with this package's defaults.
package imageproc

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Defaults align with Codex's codex_utils_image and Anthropic's recommended
// pre-processing for Claude Vision inputs.
const (
	// MaxDimension is the longest side after any resize.
	MaxDimension uint32 = 2048

	// PatchSize matches OpenAI Responses API patch math (32x32 tokens).
	PatchSize uint32 = 32

	// DefaultMaxPatches is OpenAI's documented budget (10_000 ~ 10 MP).
	DefaultMaxPatches = 10_000

	// DefaultQuality matches Codex's JPEG encoder quality for prompt images.
	DefaultQuality = 85

	// MaxInputBytes is a sanity guard, not a protocol requirement. Mirrors
	// Codex's MAX_PROMPT_IMAGE_INPUT_BYTES.
	MaxInputBytes = 1 << 30
)

// Mode controls how Encode handles the input image.
type Mode int

const (
	// ModeDefault shrinks images larger than MaxDimension on either axis and
	// re-encodes; smaller images are passed through byte-for-byte when the
	// format is one we can safely forward (PNG or JPEG).
	ModeDefault Mode = iota

	// ModeOriginal returns the input bytes unchanged. EXIF orientation is
	// not applied; the bytes are forwarded as-is.
	ModeOriginal

	// ModeResizeWithLimits shrinks to satisfy both the dimension and patch
	// budget supplied in Options. Used to align with OpenAI Responses API
	// image sizing where the model is billed per 32x32 patch.
	ModeResizeWithLimits
)

// Options tunes Encode. The zero value picks the package defaults.
type Options struct {
	Mode         Mode
	MaxDimension uint32
	MaxPatches   int
	Quality      int
}

func (o Options) withDefaults() Options {
	if o.MaxDimension == 0 {
		o.MaxDimension = MaxDimension
	}
	if o.MaxPatches == 0 {
		o.MaxPatches = DefaultMaxPatches
	}
	if o.Quality == 0 {
		o.Quality = DefaultQuality
	}
	return o
}

// Result describes the encoded image. Width/Height are the dimensions of the
// returned bytes; they match the source dimensions when Skipped is true.
type Result struct {
	Bytes     []byte
	MediaType string
	Width     uint32
	Height    uint32
	Resized   bool
	Skipped   bool
}

// Error is the typed failure surface for Encode. Callers can branch on Kind
// to decide whether to surface the error to the user or fall back.
type Error struct {
	Kind   ErrorKind
	Path   string
	Reason string
	Cause  error
}

type ErrorKind int

const (
	KindUnknown           ErrorKind = iota
	KindDecode                      // format recognized but decode failed (corrupt/truncated)
	KindUnsupportedFormat           // HEIC, AVIF, TIFF, animated GIF, anything we cannot forward
	KindEncode                      // re-encoding failed (rare)
	KindTooLarge                    // input bytes exceed the sanity guard
)

func (e *Error) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("imageproc %s: %s: %s", e.Path, e.Kind, e.message())
	}
	return fmt.Sprintf("imageproc %s: %s", e.Kind, e.message())
}

func (e *Error) message() string {
	switch {
	case e.Reason != "" && e.Cause != nil:
		return e.Reason + ": " + e.Cause.Error()
	case e.Reason != "":
		return e.Reason
	case e.Cause != nil:
		return e.Cause.Error()
	default:
		return "unknown error"
	}
}

func (k ErrorKind) String() string {
	switch k {
	case KindDecode:
		return "decode failed"
	case KindUnsupportedFormat:
		return "unsupported format"
	case KindEncode:
		return "encode failed"
	case KindTooLarge:
		return "input too large"
	default:
		return "unknown"
	}
}

func (e *Error) Unwrap() error { return e.Cause }

// Encode normalizes an image for prompt use. path is used only for error
// context and may be empty (for in-memory payloads). data must be the raw
// image bytes; callers are responsible for any base64 decoding before calling.
//
// Encode is deterministic given (data, opts.Mode), so identical inputs
// across the process hit a process-wide LRU keyed by sha1(input)+Mode. The
// returned *Result must be treated as read-only; callers that mutate
// Result.Bytes would corrupt the cache for subsequent callers.
func Encode(path string, data []byte, opts Options) (*Result, error) {
	opts = opts.withDefaults()

	if int64(len(data)) > MaxInputBytes {
		return nil, &Error{Kind: KindTooLarge, Path: path, Reason: fmt.Sprintf("input is %d bytes, max is %d", len(data), MaxInputBytes)}
	}

	key := cacheKey{digest: sha1.Sum(data), mode: opts.Mode}
	if cached, ok := sharedCache.get(key); ok {
		return cached, nil
	}

	result, err := encodeUncached(path, data, opts)
	if err != nil {
		return nil, err
	}
	sharedCache.put(key, result)
	return result, nil
}

func encodeUncached(path string, data []byte, opts Options) (*Result, error) {
	format, err := detectFormat(data)
	if err != nil {
		return nil, &Error{Kind: KindUnsupportedFormat, Path: path, Reason: err.Error()}
	}

	if opts.Mode == ModeOriginal {
		return passthroughResult(path, data, format)
	}

	if format == formatGIF {
		animated, err := isAnimatedGIF(data)
		if err != nil {
			return nil, &Error{Kind: KindDecode, Path: path, Reason: "decode GIF", Cause: err}
		}
		if animated {
			return nil, &Error{Kind: KindUnsupportedFormat, Path: path, Reason: "animated GIF is not supported"}
		}
	}

	img, err := decode(bytes.NewReader(data), format)
	if err != nil {
		return nil, &Error{Kind: KindDecode, Path: path, Cause: err}
	}

	// v2: bake JPEG EXIF orientation into pixels. Go's image/jpeg
	// auto-applies EXIF orientation on decode, so to avoid double rotation
	// we detect the orientation before decode, strip the EXIF APP1 segment
	// from the input, and run our own applyOrientation on the now-pre-
	// rotation pixels. PNG/WebP EXIF is deferred — neither stdlib decoder
	// surfaces it. ModeOriginal forwards raw bytes unchanged.
	needsReencode := false
	if format == formatJPEG {
		if orient := exifOrientation(data); orient != 1 {
			if stripped, ok := stripExifSegment(data); ok {
				if img, err = decode(bytes.NewReader(stripped), format); err != nil {
					return nil, &Error{Kind: KindDecode, Path: path, Cause: err}
				}
			}
			img = applyOrientation(img, orient)
			needsReencode = true
		}
	}

	bounds := img.Bounds()
	width := uint32(bounds.Dx())
	height := uint32(bounds.Dy())

	targetW, targetH, needsResize := computeTarget(width, height, opts)

	if !needsResize && !needsReencode {
		return &Result{
			Bytes:     append([]byte(nil), data...),
			MediaType: formatToMime(format),
			Width:     width,
			Height:    height,
			Skipped:   true,
		}, nil
	}

	var dst *image.NRGBA
	if needsResize {
		dst = image.NewNRGBA(image.Rect(0, 0, int(targetW), int(targetH)))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	} else {
		// needsReencode but no resize: copy pixels into a fresh NRGBA so
		// the encoder receives the post-orientation image. draw.Src is
		// the right op here because we want straight copy, not alpha
		// compositing.
		dst = image.NewNRGBA(bounds)
		draw.Draw(dst, dst.Bounds(), img, bounds.Min, draw.Src)
	}

	outBytes, outMime, err := encodeResized(dst, format, opts.Quality)
	if err != nil {
		return nil, &Error{Kind: KindEncode, Path: path, Cause: err}
	}

	return &Result{
		Bytes:     outBytes,
		MediaType: outMime,
		Width:     uint32(dst.Bounds().Dx()),
		Height:    uint32(dst.Bounds().Dy()),
		Resized:   needsResize || needsReencode,
	}, nil
}

func passthroughResult(path string, data []byte, format format) (*Result, error) {
	img, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, &Error{Kind: KindDecode, Path: path, Cause: err}
	}
	return &Result{
		Bytes:     append([]byte(nil), data...),
		MediaType: formatToMime(format),
		Width:     uint32(img.Width),
		Height:    uint32(img.Height),
		Skipped:   true,
	}, nil
}

type format int

const (
	formatUnknown format = iota
	formatJPEG
	formatPNG
	formatGIF
	formatWebP
)

func detectFormat(data []byte) (format, error) {
	if len(data) < 12 {
		return formatUnknown, errors.New("image data too short to identify format")
	}
	switch {
	case data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return formatJPEG, nil
	case bytes.Equal(data[0:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return formatPNG, nil
	case bytes.Equal(data[0:6], []byte{'G', 'I', 'F', '8', '7', 'a'}) ||
		bytes.Equal(data[0:6], []byte{'G', 'I', 'F', '8', '9', 'a'}):
		return formatGIF, nil
	case bytes.Equal(data[0:4], []byte{'R', 'I', 'F', 'F'}) &&
		bytes.Equal(data[8:12], []byte{'W', 'E', 'B', 'P'}):
		return formatWebP, nil
	default:
		return formatUnknown, errors.New("unsupported image format (PNG, JPEG, GIF, or WebP required)")
	}
}

func decode(r io.Reader, f format) (image.Image, error) {
	switch f {
	case formatJPEG:
		return jpeg.Decode(r)
	case formatPNG:
		return png.Decode(r)
	case formatGIF:
		return gif.Decode(r)
	case formatWebP:
		return webp.Decode(r)
	default:
		return nil, errors.New("unsupported format")
	}
}

func formatToMime(f format) string {
	switch f {
	case formatJPEG:
		return "image/jpeg"
	case formatPNG:
		return "image/png"
	case formatGIF:
		return "image/gif"
	case formatWebP:
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// canPreserveSource reports whether the format's decoder produces pixels in a
// form we can re-emit at the same quality. Used to decide whether to
// re-encode after a resize, or fall back to a lossless PNG.
func canPreserveSource(f format) bool {
	return f == formatJPEG
}

// encodeResized picks an output format. If the source is a format we can
// re-emit losslessly-or-near (JPEG), we keep it. Otherwise we fall back to
// PNG so we never lose information by re-encoding through a worse format.
// WebP and animated-GIF sources land here because the Go toolchain has no
// stable WebP encoder; Codex's crate does, we do not.
func encodeResized(img image.Image, src format, quality int) ([]byte, string, error) {
	if canPreserveSource(src) {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
}

// computeTarget returns (targetWidth, targetHeight, needsResize).
func computeTarget(width, height uint32, opts Options) (uint32, uint32, bool) {
	switch opts.Mode {
	case ModeResizeWithLimits:
		tw, th := patchBasedDimensions(width, height, opts.MaxDimension, opts.MaxPatches)
		return tw, th, tw != width || th != height
	default:
		if width <= opts.MaxDimension && height <= opts.MaxDimension {
			return width, height, false
		}
		scaledW, scaledH := scaleToFit(width, height, opts.MaxDimension)
		return scaledW, scaledH, true
	}
}

// scaleToFit shrinks (width, height) so the longer side equals maxDim,
// preserving aspect ratio. Both output dimensions are at least 1.
func scaleToFit(width, height, maxDim uint32) (uint32, uint32) {
	if width <= maxDim && height <= maxDim {
		return width, height
	}
	if width >= height {
		return maxDim, max1(uint32(uint64(height) * uint64(maxDim) / uint64(width)))
	}
	return max1(uint32(uint64(width) * uint64(maxDim) / uint64(height))), maxDim
}

// patchBasedDimensions matches the math Codex uses to honor OpenAI's patch
// budget. First shrink to maxDim; if that still exceeds maxPatches, scale by
// area so the resulting patch grid stays within budget.
func patchBasedDimensions(width, height uint32, maxDim uint32, maxPatches int) (uint32, uint32) {
	w := max1(width)
	h := max1(height)
	if patchesFit(w, h, maxDim, maxPatches) {
		return w, h
	}
	dimScale := float64(maxDim) / float64(max64(w, h))
	if dimScale > 1 {
		dimScale = 1
	}
	w2 := uint32(float64(w) * dimScale)
	h2 := uint32(float64(h) * dimScale)
	if patchesFit(w2, h2, maxDim, maxPatches) {
		return max1(w2), max1(h2)
	}
	wf := float64(w2)
	hf := float64(h2)
	patchSize := float64(PatchSize)
	scale := (patchSize * patchSize * float64(maxPatches) / wf / hf)
	scaledW := wf * scale / patchSize
	scaledH := hf * scale / patchSize
	floorW := scaledW
	if float64(int64(scaledW)) > scaledW {
		floorW = float64(int64(scaledW))
	}
	floorH := scaledH
	if float64(int64(scaledH)) > scaledH {
		floorH = float64(int64(scaledH))
	}
	if floorW > 0 && floorH > 0 {
		shrink := floorW / scaledW
		if floorH/scaledH < shrink {
			shrink = floorH / scaledH
		}
		scale *= shrink
	}
	return max1(uint32(wf * scale)), max1(uint32(hf * scale))
}

func patchesFit(width, height, maxDim uint32, maxPatches int) bool {
	if width > maxDim || height > maxDim {
		return false
	}
	patchesW := (width + PatchSize - 1) / PatchSize
	patchesH := (height + PatchSize - 1) / PatchSize
	return int64(patchesW)*int64(patchesH) <= int64(maxPatches)
}

func max1(v uint32) uint32 {
	if v < 1 {
		return 1
	}
	return v
}

func max64(a, b uint32) uint32 {
	if a >= b {
		return a
	}
	return b
}

// isAnimatedGIF returns true when the GIF has more than one frame. Mirrors
// Codex's "non-animated GIF support only" stance.
func isAnimatedGIF(data []byte) (bool, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return false, err
	}
	return len(g.Image) > 1, nil
}
