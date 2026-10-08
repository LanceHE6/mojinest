package desktop

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Clipboard puts image bytes on the system clipboard and can paste them into
// the window that had focus before the picker opened.
//
// Per platform:
//
//	windows  PowerShell (System.Windows.Forms) — no external tools, also the
//	         only backend that can synthesise Ctrl+V reliably.
//	linux X11 xclip/xsel for bytes, xdotool for the keystroke.
//	         Wayland needs wl-copy / ydotool (best effort).
//	darwin   osascript.
type Clipboard struct {
	Log *slog.Logger
}

// NewClipboard builds a clipboard helper.
func NewClipboard(logger *slog.Logger) *Clipboard {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Clipboard{Log: logger}
}

// pngFor adapts bytes for clipboard consumers that only accept PNG.
// GIF keeps its animation (browsers/WeChat accept image/gif), while WebP is
// converted to PNG because most IM clients cannot paste WebP.
func pngFor(mime string, data []byte) (out []byte, outMime string, err error) {
	switch mime {
	case "image/png", "image/gif":
		return data, mime, nil
	case "image/jpeg":
		return data, mime, nil
	case "image/webp":
		img, err := decodeWebP(data)
		if err != nil {
			return nil, "", err
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil
	default:
		return nil, "", fmt.Errorf("不支持的图片类型 %s", mime)
	}
}

// CopyImage places the image on the clipboard.
func (c *Clipboard) CopyImage(mime string, data []byte) error {
	data, mime, err := pngFor(mime, data)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		return c.copyWindows(data)
	case "darwin":
		return c.copyDarwin(data)
	default:
		return c.copyLinux(mime, data)
	}
}

// --- windows ---

const psCopyScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
$img = [System.Drawing.Image]::FromStream([System.IO.File]::OpenRead($env:MOJI_FILE))
[System.Windows.Forms.Clipboard]::SetImage($img)
Write-Output 'ok'
`

func (c *Clipboard) copyWindows(data []byte) error {
	f, err := tempFile("", "mojinest-*.png", data)
	if err != nil {
		return err
	}
	defer os.Remove(f)
	script := strings.ReplaceAll(psCopyScript, "\n", ";")
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "MOJI_FILE="+f)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("复制到剪贴板失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- darwin ---

func (c *Clipboard) copyDarwin(data []byte) error {
	f, err := tempFile("", "mojinest-*.png", data)
	if err != nil {
		return err
	}
	defer os.Remove(f)
	script := fmt.Sprintf(`set the clipboard to (read (POSIX file %q) as «class PNGf»)`, f)
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("复制到剪贴板失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- linux ---

func (c *Clipboard) copyLinux(mime string, data []byte) error {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if path, err := exec.LookPath("wl-copy"); err == nil {
			cmd := exec.Command(path, "--type", mime)
			cmd.Stdin = bytes.NewReader(data)
			if out, err := cmd.CombinedOutput(); err == nil {
				return nil
			} else {
				c.Log.Warn("wl-copy 失败，回退 X11", "err", strings.TrimSpace(string(out)))
			}
		}
	}
	// X11: xclip understands arbitrary targets; xsel only text.
	if path, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command(path, "-selection", "clipboard", "-t", mime, "-i")
		cmd.Stdin = bytes.NewReader(data)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("xclip 失败: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if path, err := exec.LookPath("xsel"); err == nil && mime == "image/gif" {
		// xsel cannot carry images; keep the file so the user can drag it.
		_ = path
	}
	return fmt.Errorf("缺少剪贴板工具：安装 xclip（X11）或 wl-clipboard（Wayland）")
}

// Paste sends the copy keystroke into the focused window.
func (c *Clipboard) Paste(delay time.Duration) error {
	if delay > 0 {
		select {
		case <-time.After(delay):
		}
	}
	switch runtime.GOOS {
	case "windows":
		return c.pasteWindows()
	case "darwin":
		return c.pasteDarwin()
	default:
		return c.pasteLinux()
	}
}

func (c *Clipboard) pasteWindows() error {
	const script = `
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait('^v')
`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", strings.ReplaceAll(script, "\n", ";"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("自动粘贴失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Clipboard) pasteDarwin() error {
	script := `tell application "System Events" to keystroke "v" using command down`
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("自动粘贴失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Clipboard) pasteLinux() error {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if path, err := exec.LookPath("ydotool"); err == nil {
			return exec.Command(path, "key", "29:1", "47:1", "47:0", "29:0").Run()
		}
	}
	path, err := exec.LookPath("xdotool")
	if err != nil {
		return fmt.Errorf("缺少 xdotool，无法自动粘贴（可关闭 auto_paste 后手动 Ctrl+V）")
	}
	if out, err := exec.Command(path, "key", "--clearmodifiers", "ctrl+v").CombinedOutput(); err != nil {
		return fmt.Errorf("自动粘贴失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SavePNG writes a PNG copy next to the original: handy fallback for browsers.
func SavePNG(dir string, name string, data []byte) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = home
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := dir + string(os.PathSeparator) + name
	return p, os.WriteFile(p, data, 0o644)
}
