package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// HotkeySupported reports whether this build can register global shortcuts.
func HotkeySupported() bool { return hotkeySupportedPlatform() }

// DetectBrowser returns the browser binary that will host the picker window
// ("" when none was found).
func DetectBrowser(override string) string {
	if override != "" {
		if p, err := exec.LookPath(override); err == nil {
			return p
		}
		if _, err := os.Stat(override); err == nil {
			return override
		}
		return ""
	}
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{"msedge", "chrome", "brave"}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			candidates = append(candidates,
				filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(pf, `Google\Chrome\Application\chrome.exe`))
		}
		if pf := os.Getenv("ProgramFiles(x86)"); pf != "" {
			candidates = append(candidates,
				filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(pf, `Google\Chrome\Application\chrome.exe`))
		}
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			candidates = append(candidates,
				filepath.Join(la, `Google\Chrome\Application\chrome.exe`))
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		candidates = []string{"google-chrome", "chromium", "chromium-browser",
			"microsoft-edge", "microsoft-edge-stable", "brave-browser",
			"/usr/bin/google-chrome", "/usr/bin/chromium", "/snap/bin/chromium"}
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// HasClipboardTool reports whether a working clipboard CLI is available.
func HasClipboardTool() bool {
	switch runtime.GOOS {
	case "windows":
		_, err := exec.LookPath("powershell")
		return err == nil
	case "darwin":
		_, err := exec.LookPath("osascript")
		return err == nil
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if _, err := exec.LookPath("wl-copy"); err == nil {
				return true
			}
		}
		for _, t := range []string{"xclip", "xsel"} {
			if _, err := exec.LookPath(t); err == nil {
				return true
			}
		}
		return false
	}
}
