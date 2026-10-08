package imageutil

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

func pngBytes(w, h int, c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func gifBytes(frames int) []byte {
	pal := color.Palette{color.NRGBA{}, color.NRGBA{R: 10, G: 200, B: 10, A: 255}}
	g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: 8, Height: 8}}
	for i := 0; i < frames; i++ {
		fr := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
		fr.SetColorIndex(i%8, 0, 1)
		g.Image = append(g.Image, fr)
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	_ = gif.EncodeAll(&buf, g)
	return buf.Bytes()
}

func TestSniff(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", pngBytes(4, 4, color.NRGBA{A: 255}), "image/png"},
		{"gif", gifBytes(2), "image/gif"},
		{"jpeg-ish", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0, 0, 0, 0, 0}, "image/jpeg"},
		{"webp-ish", append([]byte("RIFF....WEBP"), make([]byte, 20)...), "image/webp"},
		{"text", []byte("hello world, not an image"), ""},
		{"short", []byte("ab"), ""},
	}
	for _, c := range cases {
		if got := Sniff(c.data); got != c.want {
			t.Errorf("%s: Sniff = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestInspectPNGAndGIF(t *testing.T) {
	m, err := Inspect(pngBytes(30, 20, color.NRGBA{R: 1, A: 255}), "image/png")
	if err != nil {
		t.Fatalf("inspect png: %v", err)
	}
	if m.Width != 30 || m.Height != 20 || m.Animated || m.Frames != 1 {
		t.Errorf("png meta = %+v", m)
	}

	m, err = Inspect(gifBytes(4), "image/gif")
	if err != nil {
		t.Fatalf("inspect gif: %v", err)
	}
	if m.Width != 8 || m.Height != 8 || !m.Animated || m.Frames != 4 {
		t.Errorf("gif meta = %+v", m)
	}
}

func TestInspectRejectsGarbage(t *testing.T) {
	if _, err := Inspect([]byte("not an image at all"), "image/png"); err == nil {
		t.Error("expected error for garbage png")
	}
}

func TestThumbShrinksAndKeepsTransparency(t *testing.T) {
	// alpha 0 = transparent pixel → PNG output, longest side clamped to 320
	out, ext, ct, err := Thumb(pngBytes(600, 300, color.NRGBA{R: 10, G: 20, B: 30, A: 0}), "image/png", 320)
	if err != nil {
		t.Fatalf("thumb: %v", err)
	}
	if ext != "png" || ct != "image/png" {
		t.Errorf("transparent image should produce png, got %s/%s", ext, ct)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode thumb: %v", err)
	}
	if img.Bounds().Dx() != 320 || img.Bounds().Dy() != 160 {
		t.Errorf("thumb size = %v, want 320x160", img.Bounds())
	}
}

func TestThumbTransparentUsesPNG(t *testing.T) {
	_, ext, _, err := Thumb(pngBytes(500, 500, color.NRGBA{R: 255, A: 0}), "image/png", 320)
	if err != nil {
		t.Fatalf("thumb: %v", err)
	}
	if ext != "png" {
		t.Errorf("transparent image should produce png, got %s", ext)
	}
}

func TestThumbOpaqueUsesJPEG(t *testing.T) {
	_, ext, ct, err := Thumb(pngBytes(500, 500, color.NRGBA{R: 200, G: 100, B: 50, A: 255}), "image/png", 320)
	if err != nil {
		t.Fatalf("thumb: %v", err)
	}
	if ext != "jpg" || ct != "image/jpeg" {
		t.Errorf("opaque image should produce jpeg, got %s/%s", ext, ct)
	}
}

func TestFirstFramePNGAnimatedGIF(t *testing.T) {
	out, err := FirstFramePNG(gifBytes(3), "image/gif")
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("\x89PNG")) {
		t.Error("expected PNG output")
	}
	small, err := DownscalePNG(out, 4)
	if err != nil {
		t.Fatalf("downscale: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(small))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Width != 4 || cfg.Height != 4 {
		t.Errorf("downscaled to %dx%d, want 4x4", cfg.Width, cfg.Height)
	}
}

func TestDownscale(t *testing.T) {
	// upscaling must be a no-op
	src := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	if got := Downscale(src, 100); got.Bounds().Dx() != 10 {
		t.Errorf("upscale should not enlarge: %v", got.Bounds())
	}
	// extreme aspect ratio keeps both sides >= 1px
	tall := image.NewNRGBA(image.Rect(0, 0, 1000, 3))
	if got := Downscale(tall, 10); got.Bounds().Dx() != 10 || got.Bounds().Dy() != 1 {
		t.Errorf("tall downscale = %v", got.Bounds())
	}
}
