package imageproc

import (
	"encoding/binary"
	"image"
	"image/color"
)

// exifOrientation extracts the EXIF orientation tag from a JPEG byte stream.
// Returns 1 (no rotation) for non-JPEG inputs, inputs without EXIF, or any
// malformed segment. Errors are deliberately swallowed because the worst
// case is a sideways photo, not a hard failure: most JPEGs have no EXIF
// at all, and a parse failure on a malformed segment should not block the
// rest of the pipeline.
//
// EXIF lives in an APP1 segment (marker 0xFFE1) prefixed with the six-byte
// signature "Exif\0\0". The TIFF header that follows declares byte order
// (II/MM) and the offset of IFD0. We walk IFD0 looking for tag 0x0112
// (Orientation), which is always type SHORT (3), count 1, and fits in the
// 4-byte value field.
//
// Only JPEG is parsed here. PNG (eXIf chunk) and WebP (EXIF chunk) are
// deferred; the dominant real-world case is JPEG from phone cameras, and
// Go's stdlib decoders don't surface EXIF for any of the three formats.
func exifOrientation(data []byte) uint16 {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+3 < len(data) {
		if data[i] != 0xFF {
			return 1
		}
		// Skip padding 0xFF bytes between markers.
		if data[i+1] == 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		// Skip APPn segments transparently; only APP1 carries EXIF.
		if marker >= 0xE0 && marker <= 0xEF {
			segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
			if segLen < 2 || i+2+segLen > len(data) {
				return 1
			}
			seg := data[i+4 : i+2+segLen]
			if marker == 0xE1 && hasExifSignature(seg) {
				return parseOrientationFromTIFF(seg[6:])
			}
			i += 2 + segLen
			continue
		}
		// Stop at the first non-APP marker (DQT, DHT, SOF, SOS, etc.).
		return 1
	}
	return 1
}

func hasExifSignature(seg []byte) bool {
	return len(seg) >= 6 &&
		seg[0] == 'E' && seg[1] == 'x' && seg[2] == 'i' && seg[3] == 'f' &&
		seg[4] == 0 && seg[5] == 0
}

func parseOrientationFromTIFF(tiff []byte) uint16 {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		bo = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(tiff[2:4]) != 0x002A {
		return 1
	}
	ifdOffset := int(bo.Uint32(tiff[4:8]))
	if ifdOffset+2 > len(tiff) {
		return 1
	}
	numEntries := int(bo.Uint16(tiff[ifdOffset : ifdOffset+2]))
	pos := ifdOffset + 2
	for i := 0; i < numEntries; i++ {
		if pos+12 > len(tiff) {
			return 1
		}
		tag := bo.Uint16(tiff[pos : pos+2])
		if tag == 0x0112 {
			// Orientation: SHORT (type 3), count 1; value is in low 2 bytes.
			return bo.Uint16(tiff[pos+8 : pos+10])
		}
		pos += 12
	}
	return 1
}

// applyOrientation returns a new image with the EXIF orientation baked into
// the pixels. After this call the orientation tag in any output stream is
// implicitly 1; the caller should not preserve EXIF, because not every
// model API honors it.
//
// Values 1-8 follow the EXIF spec:
//
//	1 = normal                          (identity)
//	2 = flip horizontal                 (mirror left-right)
//	3 = rotate 180                      (identity, but inverted)
//	4 = flip vertical                   (mirror top-bottom)
//	5 = transpose                       (mirror along main diagonal: width/height swap)
//	6 = rotate 90 CW                    (width/height swap; most common for phone photos)
//	7 = transverse                      (mirror along anti-diagonal: width/height swap)
//	8 = rotate 270 CW (a.k.a. 90 CCW)   (width/height swap)
//
// Values outside 1..8 are treated as identity. The returned image has its
// origin at (0, 0) regardless of the source's bounds.
func applyOrientation(img image.Image, orient uint16) image.Image {
	if orient <= 1 || orient > 8 {
		return img
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	switch orient {
	case 2: // flip horizontal
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(x, y, colorAt(img, bounds.Min.X+w-1-x, bounds.Min.Y+y))
			}
		}
		return dst
	case 3: // rotate 180
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(x, y, colorAt(img, bounds.Min.X+w-1-x, bounds.Min.Y+h-1-y))
			}
		}
		return dst
	case 4: // flip vertical
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(x, y, colorAt(img, bounds.Min.X+x, bounds.Min.Y+h-1-y))
			}
		}
		return dst
	case 5: // transpose: width and height swap, dst(x,y) = src(y,x)
		dst := image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < w; y++ {
			for x := 0; x < h; x++ {
				dst.SetNRGBA(x, y, colorAt(img, bounds.Min.X+y, bounds.Min.Y+x))
			}
		}
		return dst
	case 6: // rotate 90 CW: width and height swap, dst(x,y) = src(y, h-1-x)
		dst := image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < w; y++ {
			for x := 0; x < h; x++ {
				dst.SetNRGBA(x, y, colorAt(img, bounds.Min.X+y, bounds.Min.Y+h-1-x))
			}
		}
		return dst
	case 7: // transverse: width and height swap, dst(x,y) = src(w-1-y, h-1-x)
		dst := image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < w; y++ {
			for x := 0; x < h; x++ {
				dst.SetNRGBA(x, y, colorAt(img, bounds.Min.X+w-1-y, bounds.Min.Y+h-1-x))
			}
		}
		return dst
	case 8: // rotate 270 CW (90 CCW): width and height swap, dst(x,y) = src(w-1-y, x)
		dst := image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < w; y++ {
			for x := 0; x < h; x++ {
				dst.SetNRGBA(x, y, colorAt(img, bounds.Min.X+w-1-y, bounds.Min.Y+x))
			}
		}
		return dst
	}
	return img
}

// stripExifSegment removes APP1 segments carrying the "Exif\0\0" signature
// from a JPEG byte stream and returns the cleaned bytes plus whether any
// EXIF was found. We strip before decode because Go's image/jpeg honors
// EXIF orientation on its own; without stripping, our applyOrientation
// would double-rotate the image.
//
// Other APPn segments (APP2 ICC profile, APP14 Adobe marker, etc.) are
// preserved. The cleaned byte stream still has a valid SOI + remaining
// markers; only the EXIF APP1 segment is removed.
func stripExifSegment(data []byte) ([]byte, bool) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return data, false
	}
	out := make([]byte, 0, len(data))
	out = append(out, data[0], data[1])
	i := 2
	found := false
	for i+3 < len(data) {
		if data[i] != 0xFF {
			out = append(out, data[i:]...)
			break
		}
		// Skip padding 0xFF bytes between markers.
		if data[i+1] == 0xFF {
			out = append(out, 0xFF)
			i++
			continue
		}
		marker := data[i+1]
		if marker >= 0xE0 && marker <= 0xEF {
			segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
			if segLen < 2 || i+2+segLen > len(data) {
				out = append(out, data[i:]...)
				break
			}
			content := data[i+4 : i+2+segLen]
			if marker == 0xE1 && hasExifSignature(content) {
				found = true
				i += 2 + segLen
				continue
			}
			out = append(out, data[i:i+2+segLen]...)
			i += 2 + segLen
			continue
		}
		out = append(out, data[i:]...)
		break
	}
	return out, found
}

// colorAt is a thin wrapper that converts image.Image.At to a color.NRGBA so
// we can write directly into an NRGBA destination without going through the
// slow color.Color interface path on each Set.
func colorAt(img image.Image, x, y int) color.NRGBA {
	r, g, b, a := img.At(x, y).RGBA()
	// RGBA() returns 16-bit-per-channel premultiplied; NRGBA stores 8-bit
	// non-premultiplied. Shift and re-premultiply as needed.
	if a == 0 {
		return color.NRGBA{}
	}
	return color.NRGBA{
		R: uint8((r * 0xFF) / a),
		G: uint8((g * 0xFF) / a),
		B: uint8((b * 0xFF) / a),
		A: uint8(a >> 8),
	}
}
