package cualinux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"math/bits"
	"time"

	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/image/draw"
)

func (b *backend) capture(ctx context.Context, w window) ([]byte, int, int, error) {
	if !b.composite {
		return nil, 0, 0, fail("unsupported_capture", "XComposite 0.2 or newer is required for target-only capture")
	}
	if w.Width <= 0 || w.Height <= 0 || w.Width > 8192 || w.Height > 8192 || w.Width*w.Height > 16_000_000 {
		return nil, 0, 0, fail("unsupported_capture", "window exceeds capture size limit")
	}
	if err := checkpoint(ctx); err != nil {
		return nil, 0, 0, err
	}
	pixmap, err := xproto.NewPixmapId(b.conn)
	if err != nil {
		return nil, 0, 0, err
	}
	// Name and read the pixmap while identity/geometry are stable. A named
	// pixmap survives resize/remap, so naming before this guard could return
	// pixels from an obsolete backing store even when dimensions match again.
	if err = xproto.GrabServerChecked(b.conn).Check(); err != nil {
		return nil, 0, 0, err
	}
	var raw *xproto.GetImageReply
	func() {
		defer xproto.UngrabServer(b.conn)
		fresh, e := b.validate(w)
		if e != nil {
			err = e
			return
		}
		if !sameGeometry(w, fresh) {
			err = fail("stale_snapshot", "window moved or resized during capture; observe again")
			return
		}
		// Respect existing compositor redirection; acquire automatic redirection
		// only if no backing pixmap is currently available.
		if err = composite.NameWindowPixmapChecked(b.conn, w.XID, pixmap).Check(); err != nil {
			if err = composite.RedirectWindowChecked(b.conn, w.XID, composite.RedirectAutomatic).Check(); err != nil {
				err = fail("unsupported_capture", "cannot redirect target window: %v", err)
				return
			}
			// Cookie construction sends the X11 request immediately. Defer the
			// entire call, not only Check, so redirection survives readback.
			defer func() { _ = composite.UnredirectWindowChecked(b.conn, w.XID, composite.RedirectAutomatic).Check() }()
			if err = composite.NameWindowPixmapChecked(b.conn, w.XID, pixmap).Check(); err != nil {
				err = fail("capture_failed", "cannot name target pixmap: %v", err)
				return
			}
		}
		defer func() { _ = xproto.FreePixmapChecked(b.conn, pixmap).Check() }()
		geom, e := xproto.GetGeometry(b.conn, xproto.Drawable(pixmap)).Reply()
		if e != nil {
			err = e
			return
		}
		if int(geom.Width) < w.Width+w.border*2 || int(geom.Height) < w.Height+w.border*2 {
			err = fail("capture_failed", "target pixmap geometry changed")
			return
		}
		raw, err = xproto.GetImage(b.conn, xproto.ImageFormatZPixmap, xproto.Drawable(pixmap), int16(w.border), int16(w.border), uint16(w.Width), uint16(w.Height), 0xffffffff).Reply()
	}()
	if err != nil {
		return nil, 0, 0, err
	}
	setup := xproto.Setup(b.conn)
	bpp, pad := 0, 0
	for _, f := range setup.PixmapFormats {
		if f.Depth == raw.Depth {
			bpp = int(f.BitsPerPixel)
			pad = int(f.ScanlinePad)
			break
		}
	}
	if bpp != 16 && bpp != 24 && bpp != 32 || pad == 0 {
		return nil, 0, 0, fail("unsupported_capture", "unsupported X11 pixel format")
	}
	var visual *xproto.VisualInfo
	for _, d := range b.screen.AllowedDepths {
		for i := range d.Visuals {
			if d.Visuals[i].VisualId == w.visual {
				v := d.Visuals[i]
				visual = &v
			}
		}
	}
	if visual == nil || visual.Class != xproto.VisualClassTrueColor {
		return nil, 0, 0, fail("unsupported_capture", "target must use a TrueColor X11 visual")
	}
	stride := ((w.Width*bpp + pad - 1) / pad) * (pad / 8)
	if len(raw.Data) < stride*w.Height {
		return nil, 0, 0, fail("capture_failed", "incomplete target pixel data")
	}
	out := image.NewNRGBA(image.Rect(0, 0, w.Width, w.Height))
	component := func(pixel, mask uint32) uint8 {
		if mask == 0 {
			return 0
		}
		shift := bits.TrailingZeros32(mask)
		value := (pixel & mask) >> shift
		max := mask >> shift
		return uint8((uint64(value)*255 + uint64(max)/2) / uint64(max))
	}
	for y := 0; y < w.Height; y++ {
		for x := 0; x < w.Width; x++ {
			data := raw.Data[y*stride+x*(bpp/8):]
			var pixel uint32
			if setup.ImageByteOrder == xproto.ImageOrderLSBFirst {
				for i := 0; i < bpp/8; i++ {
					pixel |= uint32(data[i]) << uint(i*8)
				}
			} else {
				for i := 0; i < bpp/8; i++ {
					pixel = pixel<<8 | uint32(data[i])
				}
			}
			out.SetNRGBA(x, y, color.NRGBA{R: component(pixel, visual.RedMask), G: component(pixel, visual.GreenMask), B: component(pixel, visual.BlueMask), A: 255})
		}
	}
	width, height := w.Width, w.Height
	if width > 1600 || height > 1600 {
		if width >= height {
			height = max(1, height*1600/width)
			width = 1600
		} else {
			width = max(1, width*1600/height)
			height = 1600
		}
		resized := image.NewNRGBA(image.Rect(0, 0, width, height))
		draw.ApproxBiLinear.Scale(resized, resized.Bounds(), out, out.Bounds(), draw.Src, nil)
		out = resized
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, out); err != nil {
		return nil, 0, 0, err
	}
	return encoded.Bytes(), width, height, nil
}

func (b *backend) observe(ctx context.Context, w window, cmd command) (result, error) {
	if cmd.Mode == "ax" || cmd.After == "ax" {
		return result{}, fail("unsupported_action", "Linux X11 helper provides vision only; accessibility-tree mode is not available")
	}
	started := time.Now()
	data, width, height, err := b.capture(ctx, w)
	if err != nil {
		return result{}, err
	}
	settling := ""
	if cmd.Action != "observe" && cmd.After != "" {
		// XTEST acknowledgement precedes the target handling the input and
		// painting. Like the macOS backend, wait for a quiet visual boundary,
		// not an application postcondition. Animation must not wait forever.
		deadline := started.Add(1500 * time.Millisecond)
		quietSince := time.Now()
		for {
			if !time.Now().Before(deadline) {
				settling = "timed_out"
				break
			}
			// Never hold the X server grab while waiting for another client to
			// repaint. Sampling is capped at 10 Hz even for small windows.
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return result{}, checkpoint(ctx)
			case <-timer.C:
			}
			if err = checkpoint(ctx); err != nil {
				return result{}, err
			}
			latest, e := b.validate(w)
			if e != nil {
				return result{}, e
			}
			next, nextWidth, nextHeight, e := b.capture(ctx, latest)
			if e != nil {
				return result{}, e
			}
			now := time.Now()
			if !bytes.Equal(data, next) || !sameGeometry(w, latest) {
				quietSince = now
			}
			data, width, height, w = next, nextWidth, nextHeight, latest
			if !now.Before(deadline) {
				settling = "timed_out"
				break
			}
			if now.Sub(quietSince) >= 200*time.Millisecond {
				settling = "quiet"
				break
			}
		}
	}
	if err = checkpoint(ctx); err != nil {
		return result{}, err
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return result{}, err
	}
	ticket := snapshot{id: hex.EncodeToString(id), epoch: cmd.Epoch, target: w, width: width, height: height, created: time.Now()}
	b.snapshots[w.ID] = ticket
	r := result{Text: "Observed target X11 window. Coordinates are relative to this image; input requires explicit foreground permission.", PNG: data, Data: map[string]any{
		"app": w.ID, "resolved_app": w, "process_id": w.PID, "window_id": uint32(w.XID), "snapshot_id": ticket.id, "mode": "vision", "accessibility_supported": false,
		"screenshot": map[string]any{"width": width, "height": height, "coordinate_space": "screenshot", "capture_source": "xcomposite_window", "window_width": w.Width, "window_height": w.Height},
	}}
	if settling != "" {
		r.Data["settling"] = settling
	}
	return r, nil
}
