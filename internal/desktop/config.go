// Package desktop implements the Mojinest desktop client: a global hotkey,
// a picker window and native "copy to clipboard" for the current machine.
package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the desktop client configuration (separate from the server's).
type Config struct {
	// ServerURL is the Mojinest server, e.g. http://127.0.0.1:18642
	ServerURL string `json:"server_url"`
	// Token is the server's access token.
	Token string `json:"token"`
	// Hotkey is the global shortcut that opens the picker, e.g. Ctrl+Alt+M.
	Hotkey string `json:"hotkey"`
	// Browser overrides the browser used for the picker window ("" = auto).
	Browser string `json:"browser"`
	// WindowMode is "app" (chromeless app window) or "browser" (normal tab).
	WindowMode string `json:"window_mode"`
	// PasteDelayMs is how long to wait before sending Ctrl+V.
	PasteDelayMs int `json:"paste_delay_ms"`
	// AutoPaste sends Ctrl+V into the previously focused window after copying.
	AutoPaste bool `json:"auto_paste"`
}

// DefaultPath returns the desktop config path.
func DefaultPath() string {
	if p := os.Getenv("MOJINEST_DESKTOP_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "mojinest-desktop.json"
	}
	return filepath.Join(home, ".config", "mojinest", "desktop.json")
}

// NewConfig returns the built-in defaults.
func NewConfig() *Config {
	return &Config{
		ServerURL:    "http://127.0.0.1:18642",
		Hotkey:       "Ctrl+Alt+M",
		WindowMode:   "app",
		PasteDelayMs: 250,
		AutoPaste:    false,
	}
}

// Load reads the desktop config, applying a few env overrides.
func Load(path string) (*Config, error) {
	c := NewConfig()
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if v := os.Getenv("MOJINEST_SERVER"); v != "" {
		c.ServerURL = v
	}
	if v := os.Getenv("MOJINEST_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("MOJINEST_HOTKEY"); v != "" {
		c.Hotkey = v
	}
	return c, nil
}

// Save writes the desktop config with 0600 (it holds the server token).
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// Normalized returns the server URL without a trailing slash.
func (c *Config) Normalized() string { return strings.TrimRight(c.ServerURL, "/") }
