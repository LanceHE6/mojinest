// Package imageutil sniffs image metadata and renders thumbnails.
//
// Supported formats: PNG, JPEG, GIF (incl. animated), WebP (incl. animated).
// Everything is pure Go (no cgo, no external binaries).
package imageutil

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/webp"
)

// AllowedMIME is the set of accepted upload types.
var AllowedMIME = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// Ext returns the canonical file extension for a MIME type.
func Ext(mime string) string {
	switch mime {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	return ""
}

func extToMIME(ext string) string {
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	}
	return ""
}

// Sniff determines the MIME type from the file header.
func Sniff(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// Meta is basic image metadata.
type Meta struct {
	Width    int
	Height   int
	Animated bool
	Frames   int
}

// Inspect returns metadata for a supported image; it also validates decodability.
func Inspect(data []byte, mime string) (Meta, error) {
	var m Meta
	switch mime {
	case "image/gif":
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return m, err
		}
		if len(g.Image) == 0 {
			return m, fmt.Errorf("gif has no frames")
		}
		m.Width = g.Config.Width
		m.Height = g.Config.Height
		m.Frames = len(g.Image)
		m.Animated = m.Frames > 1
		return m, nil
	case "image/png", "image/jpeg":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return m, err
		}
		m.Width, m.Height, m.Frames = cfg.Width, cfg.Height, 1
		return m, nil
	case "image/webp":
		return inspectWebP(data)
	}
	return m, fmt.Errorf("unsupported mime %q", mime)
}

// inspectWebP parses the RIFF container: dimensions, animation flag, frame count.
func inspectWebP(data []byte) (Meta, error) {
	var m Meta
	if len(data) < 30 {
		return m, fmt.Errorf("webp too short")
	}
	// Validate decodability of the first frame (catches truncated/corrupt files).
	if _, err := webp.Decode(bytes.NewReader(data)); err != nil {
		return m, err
	}
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		if body+size > len(data) {
			size = len(data) - body
		}
		switch id {
		case "VP8X":
			if size >= 10 {
				flags := data[body]
				m.Animated = flags&0x02 != 0
				m.Width = 1 + int(data[body+4]) + int(data[body+5])<<8 + int(data[body+6])<<16
				m.Height = 1 + int(data[body+7]) + int(data[body+8])<<8 + int(data[body+9])<<16
			}
		case "ANMF":
			m.Frames++
		case "VP8 ":
			if m.Width == 0 && size >= 10 {
				m.Width = int(binary.LittleEndian.Uint16(data[body+6:body+8]) & 0x3fff)
				m.Height = int(binary.LittleEndian.Uint16(data[body+8:body+10]) & 0x3fff)
			}
		case "VP8L":
			if m.Width == 0 && size >= 5 {
				bits := binary.LittleEndian.Uint32(data[body+1 : body+5])
				m.Width = int(bits&0x3fff) + 1
				m.Height = int(bits>>14&0x3fff) + 1
			}
		}
		pos = body + size
		if size%2 == 1 {
			pos++ // RIFF padding
		}
	}
	if m.Frames == 0 {
		m.Frames = 1
	}
	return m, nil
}

// Thumb renders a thumbnail whose longest side is at most maxSide.
// It returns the encoded bytes plus their extension (jpg or png) and MIME type;
// PNG keeps transparency, JPEG is used for opaque images.
func Thumb(data []byte, mime string, maxSide int) (out []byte, ext, contentType string, err error) {
	img, err := firstFrame(data, mime)
	if err != nil {
		return nil, "", "", err
	}
	dst := Downscale(img, maxSide)
	var buf bytes.Buffer
	if HasAlpha(dst) {
		if err := png.Encode(&buf, dst); err != nil {
			return nil, "", "", err
		}
		return buf.Bytes(), "png", "image/png", nil
	}
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, "", "", err
	}
	return buf.Bytes(), "jpg", "image/jpeg", nil
}

// HasAlpha reports whether any pixel is not fully opaque.
func HasAlpha(img image.Image) bool {
	if opaque, ok := img.(interface{ Opaque() bool }); ok {
		return !opaque.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xffff {
				return true
			}
		}
	}
	return false
}

// FirstFramePNG returns the first frame of an image re-encoded as PNG
// (used when handing an animated image to a vision model).
func FirstFramePNG(data []byte, mime string) ([]byte, error) {
	img, err := firstFrame(data, mime)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DownscalePNG decodes PNG bytes, shrinks them to maxSide and re-encodes.
func DownscalePNG(pngBytes []byte, maxSide int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, err
	}
	out := Downscale(img, maxSide)
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func firstFrame(data []byte, mime string) (image.Image, error) {
	switch mime {
	case "image/gif":
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if len(g.Image) == 0 {
			return nil, fmt.Errorf("gif has no frames")
		}
		return g.Image[0], nil
	case "image/webp":
		return webp.Decode(bytes.NewReader(data))
	case "image/png":
		img, err := png.Decode(bytes.NewReader(data))
		return img, err
	case "image/jpeg":
		img, err := jpeg.Decode(bytes.NewReader(data))
		return img, err
	}
	return nil, fmt.Errorf("unsupported mime %q", mime)
}

// Downscale box-filters src down so its longest side is <= maxSide.
func Downscale(src image.Image, maxSide int) image.Image {
	if maxSide <= 0 {
		return toNRGBA(src)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return toNRGBA(src)
	}
	if w <= maxSide && h <= maxSide {
		return toNRGBA(src)
	}
	scale := float64(maxSide) / float64(w)
	if h > w {
		scale = float64(maxSide) / float64(h)
	}
	nw := int(float64(w)*scale + 0.5)
	nh := int(float64(h)*scale + 0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	nrgba := toNRGBA(src)
	for y := 0; y < nh; y++ {
		y0 := y * h / nh
		y1 := (y + 1) * h / nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0 := x * w / nw
			x1 := (x + 1) * w / nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sr, sg, sb, sa, n uint32
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					c := nrgba.NRGBAAt(xx, yy)
					sr += uint32(c.R)
					sg += uint32(c.G)
					sb += uint32(c.B)
					sa += uint32(c.A)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			a := uint8(sa / n)
			out := color.NRGBA{A: a}
			if a > 0 {
				// Un-premultiply the averaged colour.
				out.R = clamp8(uint32(sr/n) * 255 / uint32(a))
				out.G = clamp8(uint32(sg/n) * 255 / uint32(a))
				out.B = clamp8(uint32(sb/n) * 255 / uint32(a))
			}
			dst.SetNRGBA(x, y, out)
		}
	}
	return dst
}

func clamp8(v uint32) uint8 {
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok {
		return n
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			if a == 0 {
				continue
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r * 255 / a),
				G: uint8(g * 255 / a),
				B: uint8(bl * 255 / a),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}

// extToMIME is used by callers that only know the stored extension.
func ExtToMIME(ext string) string { return extToMIME(ext) }
