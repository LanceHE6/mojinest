package desktop

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Picker opens the web UI in an app-like window so the hotkey feels native.
// The browser is only a viewer: copying goes through the client's own
// clipboard support (`copy` subcommand) or the web UI's copy button.
type Picker struct {
	cfg *Config
	log *slog.Logger
}

// NewPicker builds a picker for the given config.
func NewPicker(cfg *Config, logger *slog.Logger) *Picker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Picker{cfg: cfg, log: logger}
}

// AuthedURL returns the UI URL with the token attached so the web app can
// bootstrap its session without a manual login. The token travels in the
// fragment (#), which never reaches the server as part of the request line.
func (p *Picker) AuthedURL() string {
	base := p.cfg.Normalized() + "/"
	if p.cfg.Token == "" {
		return base
	}
	return base + "#token=" + p.cfg.Token
}

// Open launches the picker window (app-style when the browser supports it).
func (p *Picker) Open(ctx context.Context) error {
	u := p.AuthedURL()
	switch runtime.GOOS {
	case "windows":
		return p.openWindows(u)
	case "darwin":
		return p.openDarwin(u)
	default:
		return p.openLinux(u)
	}
}

type browserCandidate struct {
	bin    string
	appArg string
}

func (p *Picker) candidates() []browserCandidate {
	if p.cfg.Browser != "" {
		return []browserCandidate{{p.cfg.Browser, "--app=%s"}}
	}
	switch runtime.GOOS {
	case "windows":
		return []browserCandidate{{"msedge", "--app=%s"}, {"chrome", "--app=%s"}, {"brave", "--app=%s"}}
	case "darwin":
		return []browserCandidate{
			{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "--app=%s"},
			{"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "--app=%s"},
			{"/Applications/Chromium.app/Contents/MacOS/Chromium", "--app=%s"},
		}
	default:
		return []browserCandidate{
			{"google-chrome", "--app=%s"}, {"chromium", "--app=%s"},
			{"chromium-browser", "--app=%s"}, {"microsoft-edge", "--app=%s"},
			{"brave-browser", "--app=%s"},
		}
	}
}

func (p *Picker) openLinux(u string) error {
	if p.cfg.WindowMode != "browser" {
		for _, b := range p.candidates() {
			path, err := exec.LookPath(b.bin)
			if err != nil {
				continue
			}
			// --class gives the window its own identity in task bars.
			if err := launchDetached(path, []string{fmt.Sprintf(b.appArg, u), "--class=Mojinest"}); err != nil {
				p.log.Warn("启动浏览器失败", "bin", b.bin, "err", err)
				continue
			}
			p.log.Info("已打开选图窗口", "browser", path)
			return nil
		}
	}
	for _, opener := range [][]string{{"xdg-open"}, {"gio", "open"}} {
		path, err := exec.LookPath(opener[0])
		if err != nil {
			continue
		}
		if err := launchDetached(path, append(opener[1:], u)); err != nil {
			continue
		}
		p.log.Info("已用系统默认浏览器打开", "opener", path)
		return nil
	}
	return fmt.Errorf("找不到可用浏览器：请在配置里指定 browser，或用 -browser 指定路径")
}

func (p *Picker) openWindows(u string) error {
	if p.cfg.WindowMode != "browser" {
		for _, b := range p.candidates() {
			if err := launchDetached(b.bin, []string{fmt.Sprintf(b.appArg, u)}); err == nil {
				p.log.Info("已打开选图窗口", "browser", b.bin)
				return nil
			}
		}
	}
	if err := launchDetached("cmd", []string{"/c", "start", "", u}); err != nil {
		return fmt.Errorf("打开浏览器失败: %w", err)
	}
	p.log.Info("已用默认浏览器打开")
	return nil
}

func (p *Picker) openDarwin(u string) error {
	if p.cfg.WindowMode != "browser" {
		for _, b := range p.candidates() {
			if _, err := os.Stat(b.bin); err != nil {
				continue
			}
			if err := launchDetached(b.bin, []string{fmt.Sprintf(b.appArg, u)}); err == nil {
				p.log.Info("已打开选图窗口", "browser", b.bin)
				return nil
			}
		}
	}
	if err := launchDetached("open", []string{u}); err != nil {
		return fmt.Errorf("打开浏览器失败: %w", err)
	}
	p.log.Info("已用默认浏览器打开")
	return nil
}

// launchDetached starts a process that must outlive this one.
// Plain exec.Command would die with our context, and CommandContext kills the
// child as soon as the context is cancelled (which happens on normal exit) —
// so the child gets its own process group and no tied context.
func launchDetached(bin string, args []string) error {
	path := bin
	if !strings.ContainsRune(bin, os.PathSeparator) {
		p, err := exec.LookPath(bin)
		if err != nil {
			return err
		}
		path = p
	} else if _, err := os.Stat(bin); err != nil {
		return err
	}
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the child in the background; it lives on in its own session.
	go func() { _ = cmd.Wait() }()
	return nil
}

// WaitServer blocks until the server answers (or the timeout expires).
func (p *Picker) WaitServer(ctx context.Context, c *Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := c.Health(hctx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("服务端不可达 (%s): %w", p.cfg.Normalized(), err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
}

// tempFile writes bytes to a temp file for commands that need a path.
func tempFile(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
