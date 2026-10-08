package desktop

import (
	"strings"
	"testing"
)

// Windows 的消息循环与键盘常量是手写的，很容易写错一位：
// 这里把字面量钉住，改动时会立刻炸出来。
func TestWindowsVirtualKeyTableIsSane(t *testing.T) {
	want := map[string]uint32{
		"SPACE": 0x20, "ENTER": 0x0D, "TAB": 0x09, "ESC": 0x1B,
		"A": 0x41, "M": 0x4D, "Z": 0x5A, "0": 0x30, "9": 0x39,
		"F1": 0x70, "F12": 0x7B, "F24": 0x87,
	}
	for _, hk := range []string{
		"SPACE", "ENTER", "TAB", "ESC", "A", "M", "Z", "0", "9", "F1", "F12", "F24",
	} {
		got, ok := windowsVKForTest(hk)
		if !ok {
			t.Errorf("no VK code for %s", hk)
			continue
		}
		if got != want[hk] {
			t.Errorf("VK(%s) = %#x, want %#x", hk, got, want[hk])
		}
	}
}

// The hotkey parser must accept everything the UI/picker promise.
func TestHotkeyNamesAcceptedByPickerFlow(t *testing.T) {
	valid := []string{
		"Ctrl+Alt+M", "Ctrl+Shift+M", "Super+D", "Ctrl+Alt+Shift+K",
		"Ctrl+Space", "Alt+F4", "Ctrl+.", "Ctrl+Alt+1", "F12",
	}
	for _, h := range valid {
		if _, err := ParseHotkey(h); err != nil {
			t.Errorf("%q should parse: %v", h, err)
		}
	}
	invalid := []string{"", "Ctrl", "Nope", "Ctrl+A+B", "Ctrl+F25", "Ctrl+Alt+"}
	for _, h := range invalid {
		if _, err := ParseHotkey(h); err == nil {
			t.Errorf("%q should be rejected", h)
		}
	}
}

func TestHotkeyStringRoundTripsThroughParse(t *testing.T) {
	for _, raw := range []string{"Ctrl+Alt+M", "Ctrl+Shift+Space", "Super+F5", "Ctrl+Alt+Shift+Super+D"} {
		hk, err := ParseHotkey(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		again, err := ParseHotkey(hk.String())
		if err != nil {
			t.Fatalf("re-parse %q: %v", hk.String(), err)
		}
		if again.String() != hk.String() {
			t.Errorf("round trip changed %q → %q", hk.String(), again.String())
		}
		if !strings.Contains(hk.String(), "Ctrl") && strings.Contains(raw, "ctrl") {
			t.Errorf("modifier lost: %s", hk.String())
		}
	}
}
