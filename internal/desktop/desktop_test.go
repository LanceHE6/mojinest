package desktop

import (
	"strings"
	"testing"
)

func TestParseHotkey(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Ctrl+Alt+M", "Ctrl+Alt+M", false},
		{"ctrl+shift+space", "Ctrl+Shift+SPACE", false},
		{"cmd+d", "Super+D", false},
		{"super+f12", "Super+F12", false},
		{"F12", "F12", false},
		{"ctrl+.", "Ctrl+PERIOD", false},
		{"CTRL + ALT + K", "Ctrl+Alt+K", false},
		{"", "", true},
		{"Ctrl", "", true},
		{"Ctrl+Nope", "", true},
		{"Ctrl+A+B", "", true},
		{"ctrl+f99", "", true},
	}
	for _, c := range cases {
		got, err := ParseHotkey(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("%q → %q, want %q", c.in, got.String(), c.want)
		}
	}
}

func TestParseHotkeyModifierOrderIsStable(t *testing.T) {
	a, _ := ParseHotkey("shift+ctrl+alt+v")
	b, _ := ParseHotkey("Ctrl+Alt+Shift+V")
	if a.String() != b.String() || a.String() != "Ctrl+Alt+Shift+V" {
		t.Errorf("unstable modifiers: %s vs %s", a, b)
	}
}

func TestConfigRoundTripAndPerms(t *testing.T) {
	path := t.TempDir() + "/desktop.json"
	c := NewConfig()
	c.ServerURL = "http://192.168.1.4:18642/"
	c.Token = "tok-abc"
	c.Hotkey = "Ctrl+Shift+M"
	c.AutoPaste = true
	if err := c.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Normalized() != "http://192.168.1.4:18642" {
		t.Errorf("normalized = %q", got.Normalized())
	}
	if got.Token != "tok-abc" || got.Hotkey != "Ctrl+Shift+M" || !got.AutoPaste {
		t.Errorf("roundtrip = %+v", got)
	}
}

func TestLoadDefaultsWhenMissing(t *testing.T) {
	c, err := Load(t.TempDir() + "/nope.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.ServerURL != "http://127.0.0.1:18642" || c.Hotkey != "Ctrl+Alt+M" {
		t.Errorf("defaults = %+v", c)
	}
	if c.AutoPaste {
		t.Error("auto paste should default to off")
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("MOJINEST_SERVER", "http://server.local:9000")
	t.Setenv("MOJINEST_TOKEN", "env-token")
	t.Setenv("MOJINEST_HOTKEY", "Ctrl+Alt+J")
	c, err := Load(t.TempDir() + "/nope.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.ServerURL != "http://server.local:9000" || c.Token != "env-token" || c.Hotkey != "Ctrl+Alt+J" {
		t.Errorf("env overrides = %+v", c)
	}
}

func TestAuthedURL(t *testing.T) {
	c := NewConfig()
	c.ServerURL = "http://127.0.0.1:18642/"
	c.Token = "abc123"
	p := NewPicker(c, nil)
	if got := p.AuthedURL(); got != "http://127.0.0.1:18642/#token=abc123" {
		t.Errorf("authed url = %q", got)
	}
	c.Token = ""
	if got := p.AuthedURL(); got != "http://127.0.0.1:18642/" {
		t.Errorf("tokenless url = %q", got)
	}
}

func TestPngForModes(t *testing.T) {
	pngData := []byte("\x89PNG\r\n\x1a\nrest")
	if out, mime, err := pngFor("image/png", pngData); err != nil || mime != "image/png" || len(out) != len(pngData) {
		t.Errorf("png passthrough failed: %v %s", err, mime)
	}
	gifData := []byte("GIF89a...")
	if out, mime, err := pngFor("image/gif", gifData); err != nil || mime != "image/gif" {
		t.Errorf("gif must keep animation: %v %s", err, mime)
	} else if string(out) != string(gifData) {
		t.Error("gif bytes were altered")
	}
	if _, _, err := pngFor("image/webp", []byte("not-a-webp")); err == nil {
		t.Error("broken webp should error out")
	}
	if _, _, err := pngFor("application/pdf", []byte("x")); err == nil {
		t.Error("unsupported type should error")
	}
}

func TestDetectBrowserRespectsOverride(t *testing.T) {
	if got := DetectBrowser("/definitely/not/here"); got != "" {
		t.Errorf("bogus override should return empty, got %q", got)
	}
	// An existing binary path must be returned as-is.
	got := DetectBrowser("/bin/sh")
	if got == "" {
		t.Error("existing path should be detected")
	}
}

func TestHotkeyInstructionsMentionCommand(t *testing.T) {
	lines := HotkeyInstructions("Ctrl+Alt+M")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Ctrl+Alt+M") {
		t.Errorf("instructions should mention the hotkey: %s", joined)
	}
}
