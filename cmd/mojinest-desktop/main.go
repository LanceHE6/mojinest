// Command mojinest-desktop is the Mojinest desktop client: a global hotkey,
// a picker window and native clipboard integration.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"mojinest/internal/desktop"
)

const usage = `Mojinest 桌面客户端

用法:
  mojinest-desktop init            生成配置（含默认快捷键）
  mojinest-desktop pick            打开选图窗口（供快捷键调用）
  mojinest-desktop copy [关键词...]  直接按关键词复制第一张（可配合快捷键）
  mojinest-desktop run             后台常驻：注册全局快捷键
  mojinest-desktop check           自检：服务端连通性、快捷键、剪贴板工具
  mojinest-desktop config          打印当前配置路径与内容

常用参数:
  -config <path>   配置文件（默认 ~/.config/mojinest/desktop.json）
  -server <url>    覆盖服务端地址
  -token <token>   覆盖访问令牌
  -hotkey <keys>   覆盖快捷键，例如 Ctrl+Alt+M
  -quiet           不打印日志
`

func main() {
	fs := flag.NewFlagSet("mojinest-desktop", flag.ContinueOnError)
	cfgPath := fs.String("config", desktop.DefaultPath(), "配置文件路径")
	server := fs.String("server", "", "覆盖服务端地址")
	token := fs.String("token", "", "覆盖访问令牌")
	hotkey := fs.String("hotkey", "", "覆盖全局快捷键")
	quiet := fs.Bool("quiet", false, "不打印日志")
	top := fs.Int("limit", 0, "copy 子命令的结果条数")
	fs.Usage = func() { fmt.Print(usage) }
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	cmd := "pick"
	if fs.NArg() > 0 {
		cmd = fs.Arg(0)
	}
	args := []string{}
	if fs.NArg() > 1 {
		args = fs.Args()[1:]
	}

	logLevel := slog.LevelInfo
	if *quiet {
		logLevel = slog.LevelError
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return
	}

	cfg, err := desktop.Load(*cfgPath)
	if err != nil {
		logger.Error("读取配置失败", "err", err, "path", *cfgPath)
		os.Exit(1)
	}
	if *server != "" {
		cfg.ServerURL = *server
	}
	if *token != "" {
		cfg.Token = *token
	}
	if *hotkey != "" {
		cfg.Hotkey = *hotkey
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := desktop.NewClient(cfg.Normalized(), cfg.Token)
	clip := desktop.NewClipboard(logger)
	picker := desktop.NewPicker(cfg, logger)

	switch cmd {
	case "init":
		if cfg.Token == "" {
			logger.Warn("还没配置 Token：编辑配置文件或加 -token 参数")
		}
		if err := cfg.Save(*cfgPath); err != nil {
			logger.Error("写入配置失败", "err", err)
			os.Exit(1)
		}
		fmt.Printf("配置已写入 %s\n", *cfgPath)
		fmt.Printf("  服务端: %s\n  快捷键: %s\n", cfg.Normalized(), cfg.Hotkey)
		for _, line := range desktop.HotkeyInstructions(cfg.Hotkey) {
			fmt.Println("  " + line)
		}

	case "config":
		fmt.Printf("配置文件: %s\n", *cfgPath)
		fmt.Printf("  服务端: %s\n  快捷键: %s\n  窗口模式: %s\n  自动粘贴: %v\n  Token: %s\n",
			cfg.Normalized(), cfg.Hotkey, cfg.WindowMode, cfg.AutoPaste, mask(cfg.Token))

	case "pick", "open":
		if err := picker.WaitServer(ctx, client, 5*time.Second); err != nil {
			logger.Warn("服务端不可达，仍然打开窗口", "err", err)
		}
		if err := picker.Open(ctx); err != nil {
			logger.Error("打开选图窗口失败", "err", err)
			os.Exit(1)
		}

	case "copy":
		query := strings.TrimSpace(strings.Join(args, " "))
		if err := copyBest(ctx, client, clip, query, *top); err != nil {
			logger.Error("复制失败", "err", err)
			os.Exit(1)
		}

	case "run",
		"daemon":
		if err := runDaemon(ctx, cfg, client, clip, picker, logger); err != nil {
			logger.Error("运行失败", "err", err)
			os.Exit(1)
		}

	case "check":
		os.Exit(selfCheck(ctx, cfg, client, logger))

	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

// copyBest fetches the top match and puts it on the clipboard.
func copyBest(ctx context.Context, c *desktop.Client, clip *desktop.Clipboard, query string, limit int) error {
	q := desktop.SearchQuery{Q: query, Sort: "used"}
	items, err := c.Search(ctx, q)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		if query == "" {
			return errors.New("库还是空的：先用网页版或导入功能放几张图进去")
		}
		// Fall back to the newest match if nothing was used yet.
		items, err = c.Search(ctx, desktop.SearchQuery{Q: query, Sort: "newest", Limit: limit})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("没找到匹配 %q 的表情", query)
		}
	}
	it := items[0]
	data, err := c.Download(ctx, it)
	if err != nil {
		return err
	}
	if err := clip.CopyImage(it.MIME, data); err != nil {
		// Last resort: leave the file on disk and tell the user where.
		p, werr := desktop.SavePNG("", "mojinest-"+it.Sha256[:8]+"."+it.Ext, data)
		if werr != nil {
			return err
		}
		return fmt.Errorf("%w（已保存到 %s）", err, p)
	}
	_ = c.MarkUsed(ctx, it.ID)
	label := it.Name
	if label == "" {
		label = it.AIDesc
	}
	fmt.Printf("已复制：%s（%s）\n", short(label), it.Ext)
	return nil
}

func runDaemon(ctx context.Context, cfg *desktop.Config, client *desktop.Client, clip *desktop.Clipboard, picker *desktop.Picker, logger *slog.Logger) error {
	hk, err := desktop.ParseHotkey(cfg.Hotkey)
	if err != nil {
		return err
	}
	if err := picker.WaitServer(ctx, client, 10*time.Second); err != nil {
		logger.Warn("服务端暂时不可达，快捷键仍会重试", "err", err)
	}

	presses := make(chan struct{}, 1)
	press := func() {
		select {
		case presses <- struct{}{}:
		default: // already queued
		}
	}

	hotkeyErr := make(chan error, 1)
	go func() { hotkeyErr <- desktop.ListenHotkey(ctx, hk, press) }()

	logger.Info("Mojinest 桌面客户端已启动", "hotkey", hk.String(), "server", cfg.Normalized(),
		"platform", runtime.GOOS)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-hotkeyErr:
			if err != nil {
				if errors.Is(err, desktop.ErrHotkeyUnsupported) {
					// Linux/Wayland: the desktop environment owns the binding, so
					// there is nothing to keep running.
					for _, line := range desktop.HotkeyInstructions(cfg.Hotkey) {
						logger.Info(line)
					}
					return nil
				}
				return err
			}
			return nil
		case <-presses:
			if err := picker.Open(ctx); err != nil {
				logger.Error("打开选图窗口失败", "err", err)
			}
		}
	}
}

// selfCheck prints a diagnostic report; the exit code is the number of problems.
func selfCheck(ctx context.Context, cfg *desktop.Config, client *desktop.Client, logger *slog.Logger) int {
	problems := 0
	report := func(ok bool, label, detail string) {
		mark := "OK  "
		if !ok {
			mark = "FAIL"
			problems++
		}
		fmt.Printf("[%s] %-22s %s\n", mark, label, detail)
	}

	fmt.Printf("Mojinest 桌面客户端自检 (%s/%s)\n\n", runtime.GOOS, runtime.GOARCH)

	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	health, err := client.Health(hctx)
	if err != nil {
		report(false, "服务端连通", fmt.Sprintf("%s — %v", cfg.Normalized(), err))
	} else {
		report(true, "服务端连通", fmt.Sprintf("%s (%v)", cfg.Normalized(), health["kind"]))
	}

	if cfg.Token == "" {
		report(false, "访问令牌", "未配置（服务端若开了鉴权会 401）")
	} else {
		sctx, cancel2 := context.WithTimeout(ctx, 5*time.Second)
		stats, err := client.Stats(sctx)
		cancel2()
		if err != nil {
			report(false, "令牌鉴权", err.Error())
		} else {
			report(true, "令牌鉴权", fmt.Sprintf("库内 %d 张 / %d 个标签", stats["items"], stats["tags"]))
		}
	}

	if hk, err := desktop.ParseHotkey(cfg.Hotkey); err != nil {
		report(false, "快捷键语法", err.Error())
	} else {
		report(true, "快捷键语法", hk.String())
		if desktop.HotkeySupported() {
			report(true, "内置全局快捷键", "可用（run 模式会自行注册）")
		} else {
			report(false, "内置全局快捷键",
				fmt.Sprintf("本平台不支持；请在系统设置里把 %s 绑到 `mojinest-desktop pick`", hk.String()))
		}
	}

	switch runtime.GOOS {
	case "windows":
		psOk := false
		if _, err := exec.LookPath("powershell"); err == nil {
			psOk = true
		}
		report(psOk, "PowerShell", "剪贴板与自动粘贴依赖它")
	case "darwin":
		ok := false
		if _, err := exec.LookPath("osascript"); err == nil {
			ok = true
		}
		report(ok, "osascript", "剪贴板与自动粘贴依赖它")
	default:
		for _, tool := range []string{"xclip", "xsel", "wl-copy", "xdotool"} {
			if _, err := exec.LookPath(tool); err == nil {
				report(true, "剪贴板工具 "+tool, "已安装")
			}
		}
		if !desktop.HasClipboardTool() {
			report(false, "剪贴板工具", "缺 xclip/xsel —— 安装其一才能复制图片")
		}
	}

	browser := desktop.DetectBrowser(cfg.Browser)
	if browser == "" {
		report(false, "浏览器", "未找到 Chrome/Chromium/Edge，选图窗口无法打开")
	} else {
		report(true, "浏览器", browser)
	}

	fmt.Printf("\n问题数: %d\n", problems)
	return problems
}

func mask(s string) string {
	if s == "" {
		return "(未设置)"
	}
	if len(s) <= 6 {
		return "****"
	}
	return s[:3] + "****" + s[len(s)-3:]
}

func short(s string) string {
	r := []rune(s)
	if len(r) > 24 {
		return string(r[:24]) + "…"
	}
	return s
}
