package worker

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gifAsPNG builds a tiny PNG payload for the stub store.
func gifAsPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 30, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// animatedGIFAsBytes is used to confirm the worker sends only the first frame.
func animatedGIFAsBytes(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.NRGBA{}, color.NRGBA{R: 1, G: 2, B: 3, A: 255}}
	g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: 6, Height: 6}}
	for i := 0; i < 3; i++ {
		fr := image.NewPaletted(image.Rect(0, 0, 6, 6), pal)
		fr.SetColorIndex(i, 0, 1)
		g.Image = append(g.Image, fr)
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

var _ = io.Discard
var _ = os.TempDir
var _ = filepath.Join
var _ = strings.TrimSpace
