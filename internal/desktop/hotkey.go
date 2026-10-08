package desktop

import (
	"errors"
	"fmt"
	"strings"
)

// ErrHotkeyUnsupported means the platform cannot grab global shortcuts itself.
var ErrHotkeyUnsupported = errors.New("hotkey: 该平台不支持内置全局快捷键")

// Mod is a hotkey modifier.
type Mod int

const (
	ModCtrl Mod = iota + 1
	ModAlt
	ModShift
	ModSuper
)

func (m Mod) String() string {
	switch m {
	case ModCtrl:
		return "Ctrl"
	case ModAlt:
		return "Alt"
	case ModShift:
		return "Shift"
	case ModSuper:
		return "Super"
	}
	return "?"
}

// Hotkey is a parsed global shortcut.
type Hotkey struct {
	Raw  string
	Mods []Mod
	Key  string // canonical key name: A..Z, 0..9, F1..F24, SPACE, ENTER, ...
}

func (h Hotkey) String() string {
	parts := make([]string, 0, len(h.Mods)+1)
	for _, m := range h.Mods {
		parts = append(parts, m.String())
	}
	return strings.Join(append(parts, h.Key), "+")
}

// keyAliases maps user input to canonical key names.
var keyAliases = map[string]string{
	"space": "SPACE", "spacebar": "SPACE", " ": "SPACE",
	"enter": "ENTER", "return": "ENTER",
	"tab": "TAB", "esc": "ESC", "escape": "ESC",
	"backspace": "BACKSPACE", "bs": "BACKSPACE",
	"delete": "DELETE", "del": "DELETE", "insert": "INSERT",
	"home": "HOME", "end": "END", "pageup": "PAGEUP", "pagedown": "PAGEDOWN",
	"up": "UP", "down": "DOWN", "left": "LEFT", "right": "RIGHT",
	"-": "MINUS", "=": "EQUAL", "[": "LBRACKET", "]": "RBRACKET",
	";": "SEMICOLON", "'": "QUOTE", ",": "COMMA", ".": "PERIOD",
	"/": "SLASH", "\\": "BACKSLASH", "`": "GRAVE",
}

// ParseHotkey understands "Ctrl+Alt+M", "cmd+shift+space", "super+d",
// "ctrl+f12" and single keys such as "F12".
func ParseHotkey(s string) (*Hotkey, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return nil, fmt.Errorf("快捷键为空")
	}
	seen := map[Mod]bool{}
	hk := &Hotkey{Raw: raw}
	for _, part := range strings.Split(raw, "+") {
		p := strings.ToLower(strings.TrimSpace(part))
		switch p {
		case "":
			continue
		case "ctrl", "control", "ctl":
			seen[ModCtrl] = true
			continue
		case "alt", "option", "opt":
			seen[ModAlt] = true
			continue
		case "shift":
			seen[ModShift] = true
			continue
		case "super", "cmd", "command", "meta", "win", "windows":
			seen[ModSuper] = true
			continue
		}
		if hk.Key != "" {
			return nil, fmt.Errorf("快捷键只能有一个主键（收到 %q 和 %q）", hk.Key, part)
		}
		key, err := canonicalKey(p)
		if err != nil {
			return nil, err
		}
		hk.Key = key
	}
	if hk.Key == "" {
		return nil, fmt.Errorf("快捷键缺少主键（示例：Ctrl+Alt+M）")
	}
	for _, m := range []Mod{ModCtrl, ModAlt, ModShift, ModSuper} {
		if seen[m] {
			hk.Mods = append(hk.Mods, m)
		}
	}
	return hk, nil
}

func canonicalKey(p string) (string, error) {
	if len(p) == 1 {
		c := p[0]
		switch {
		case c >= 'a' && c <= 'z':
			return strings.ToUpper(p), nil
		case c >= '0' && c <= '9':
			return p, nil
		}
	}
	if alias, ok := keyAliases[p]; ok {
		return alias, nil
	}
	if strings.HasPrefix(p, "f") {
		if n, ok := atoiSafe(p[1:]); ok && n >= 1 && n <= 24 {
			return fmt.Sprintf("F%d", n), nil
		}
	}
	return "", fmt.Errorf("不认识的按键 %q（示例：Ctrl+Alt+M）", p)
}

func atoiSafe(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// hotkeySupported reports whether this build can register global hotkeys.
func hotkeySupported() bool { return hotkeySupportedPlatform() }
