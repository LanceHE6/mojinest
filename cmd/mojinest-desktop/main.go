// Command mojinest-desktop is the Mojinest desktop client: a global hotkey,
// a picker window and native clipboard integration.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"mojinest/internal/desktop"
	"mojinest/internal/importer"
)

const usage = `Mojinest 桌面客户端

用法:
  mojinest-desktop [选项] <子命令> [参数]

子命令:
  setup             首次配置（写配置 + 体检 + 平台指引）
  pick              打开选图窗口（供系统快捷键调用）
  copy [关键词...]   直接按关键词复制第一张进剪贴板
  run               常驻并注册全局快捷键（Windows / macOS）
  import-qq [路径]  扫描并批量导入电脑端 QQNT 收藏表情
  check             自检：服务端、令牌、快捷键、剪贴板工具、浏览器
  config            打印当前配置
  help              显示本帮助

选项:
  -config <path>    配置文件（默认 ~/.config/mojinest/desktop.json）
  -server <url>     覆盖服务端地址，例如 http://192.168.1.4:18642
  -token <token>    覆盖访问令牌
  -hotkey <keys>    覆盖快捷键，例如 Ctrl+Alt+M
  -browser <path>   覆盖浏览器可执行文件
  -quiet            只输出错误
  -version          打印版本

不带子命令时（例如直接双击 exe）：Windows 弹出一个三选一的小面板，
不会擅自打开浏览器窗口；其他平台打印本帮助。
`

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "0.2.0"

// parseArgs splits argv into flags and positional arguments.
//
// Go's flag package stops parsing at the first positional argument, so
// `mojinest-desktop copy 猫 -quiet` would silently ignore -quiet, and
// `mojinest-desktop relay -port-file x` would ignore its port file (which is
// exactly what bit us: the relay never recorded its port). Both orders are in
// real use — a hotkey binding writes `pick -config …`, a human types
// `-config … pick` — so the arguments are classified here and the flag set is
// parsed from the flag part alone. Returns the positional arguments in order:
// [subcommand, words...].
func parseArgs(fs *flag.FlagSet, argv []string) ([]string, error) {
	var flagArgs, positionals []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		flagArgs = append(flagArgs, a)
		name := strings.TrimLeft(a, "-")
		if !strings.Contains(name, "=") && flagNeedsValue(fs, name) {
			if i+1 < len(argv) {
				i++
				flagArgs = append(flagArgs, argv[i])
			}
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positionals, nil
}

// flagNeedsValue reports whether a flag takes a separate value argument.
func flagNeedsValue(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
		return false
	}
	return true
}

// loadWithOverrides reads the config and applies CLI overrides.
func loadWithOverrides(cfgPath, server, token, hotkey, browser string) (*desktop.Config, error) {
	cfg, err := desktop.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if server != "" {
		cfg.ServerURL = server
	}
	if token != "" {
		cfg.Token = token
	}
	if hotkey != "" {
		cfg.Hotkey = hotkey
	}
	if browser != "" {
		cfg.Browser = browser
	}
	return cfg, nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	fs := flag.NewFlagSet("mojinest-desktop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", desktop.DefaultPath(), "配置文件路径")
	server := fs.String("server", "", "覆盖服务端地址")
	token := fs.String("token", "", "覆盖访问令牌")
	hotkey := fs.String("hotkey", "", "覆盖全局快捷键")
	browser := fs.String("browser", "", "覆盖浏览器可执行文件")
	portFile := fs.String("port-file", "", "本地转发端口记录文件（内部使用）")
	dialogs := fs.String("dialogs", "auto", "弹窗策略: auto（有控制台就写 stderr）| always | never")
	quiet := fs.Bool("quiet", false, "只输出错误")
	showVersion := fs.Bool("version", false, "打印版本")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	positionals, err := parseArgs(fs, argv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Print(usage)
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Printf("mojinest-desktop %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return 0
	}
	dialogMode, ok := desktop.ParseDialogMode(*dialogs)
	if !ok {
		fmt.Fprintf(os.Stderr, "无法识别的 -dialogs 值 %q（可选 auto/always/never）\n", *dialogs)
		return 2
	}

	// No subcommand: never silently pop a window. Double-clicking the exe used
	// to open a browser tab, which surprised the user; show a menu instead.
	if len(positionals) == 0 {
		return controlPanel(*cfgPath)
	}

	cmd := positionals[0]
	args := positionals[1:]

	// `relay` is a hidden helper spawned by `pick`; it must run before the
	// logging/flag plumbing below so it can stay quiet and fully detached.
	if cmd == "relay" {
		cfg, err := loadWithOverrides(*cfgPath, *server, *token, *hotkey, *browser)
		if err != nil {
			return 1
		}
		relayLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		if *quiet {
			relayLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
		}
		return runRelay(context.Background(), cfg, *portFile, relayLogger)
	}

	level := slog.LevelInfo
	if *quiet {
		level = slog.LevelError
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return 0
	}

	cfg, err := loadWithOverrides(*cfgPath, *server, *token, *hotkey, *browser)
	if err != nil {
		logger.Error("读取配置失败", "err", err, "path", *cfgPath)
		desktop.Notify(dialogMode, "Mojinest 配置读取失败", fmt.Sprintf("%v\n\n配置文件：%s", err, *cfgPath), true)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := desktop.NewClient(cfg.Normalized(), cfg.Token)
	clip := desktop.NewClipboard(logger)
	picker := desktop.NewPicker(cfg, logger)

	switch cmd {
	case "setup", "init":
		return doSetup(ctx, cfg, client, *cfgPath, logger)

	case "config":
		printConfig(cfg, *cfgPath)
		return 0

	case "pick", "open":
		if err := picker.WaitServer(ctx, client, 5*time.Second); err != nil {
			logger.Warn("服务端不可达，仍然打开窗口", "err", err)
		}
		// On a LAN over plain http the browser refuses clipboard writes; a
		// loopback relay gives the page a secure context so the copy button
		// works. ensureRelay starts (or reuses) that relay.
		if base := ensureRelay(ctx, selfPath(), *cfgPath, cfg, logger); base != "" {
			picker = picker.WithBase(base)
		}
		if err := picker.Open(ctx); err != nil {
			logger.Error("打开选图窗口失败", "err", err)
			desktop.Notify(dialogMode, "Mojinest 打不开选图窗口", err.Error(), true)
			return 1
		}
		return 0

	case "copy":
		query := strings.TrimSpace(strings.Join(args, " "))
		if err := copyBest(ctx, client, clip, query); err != nil {
			logger.Error("复制失败", "err", err)
			if !*quiet {
				desktop.Notify(dialogMode, "Mojinest 复制失败", err.Error(), true)
			}
			return 1
		}
		return 0

	case "run", "daemon":
		if err := runDaemon(ctx, cfg, client, picker, logger, dialogMode); err != nil {
			logger.Error("运行失败", "err", err)
			desktop.Notify(dialogMode, "Mojinest 启动失败", err.Error(), true)
			return 1
		}
		return 0

	case "import-qq":
		customRoot := ""
		if len(args) > 0 {
			customRoot = args[0]
		}
		return doImportQQ(ctx, client, customRoot, logger)

	case "check":
		return selfCheck(ctx, cfg, client, logger)

	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n%s", cmd, usage)
		return 2
	}
}

// doImportQQ handles scanning and importing QQNT stickers into the Mojinest server.
func doImportQQ(ctx context.Context, client *desktop.Client, customRoot string, logger *slog.Logger) int {
	fmt.Println("正在查找本地 QQNT 收藏表情目录...")
	accounts, err := importer.FindQQNTEmojiDirs(customRoot)
	if err != nil {
		logger.Error("扫描 QQNT 目录失败", "err", err)
		return 1
	}
	if len(accounts) == 0 {
		fmt.Println("未找到任何 QQNT 收藏表情目录。")
		fmt.Println("提示：可直接指定路径，例如：mojinest-desktop import-qq \"C:\\...\\nt_data\\Emoji\\personal_emoji\\Ori\"")
		return 1
	}

	totalImported := 0
	totalSkipped := 0
	for _, acc := range accounts {
		fmt.Printf("\n发现 QQ 账号 [%s] 表情目录: %s\n", acc.ID, acc.EmojiPath)
		fmt.Printf("开始批量导入...\n")
		res, err := importer.ImportQQNTEmoji(ctx, client, acc.EmojiPath, []string{"QQ收藏", acc.ID}, 20)
		if err != nil {
			logger.Error("导入失败", "account", acc.ID, "err", err)
			continue
		}
		fmt.Printf("账号 [%s] 导入完成：扫描 %d 个，成功入库 %d 个，跳过/重复 %d 个\n",
			acc.ID, res.Scanned, res.Imported, res.Skipped)
		if len(res.Errors) > 0 {
			for _, e := range res.Errors {
				logger.Warn("文件导入错误", "err", e)
			}
		}
		totalImported += res.Imported
		totalSkipped += res.Skipped
	}

	fmt.Printf("\n总计：成功导入 %d 个表情，跳过/重复 %d 个。\n", totalImported, totalSkipped)
	return 0
}

// doSetup writes the config, runs the health check and prints platform advice.
func doSetup(ctx context.Context, cfg *desktop.Config, client *desktop.Client, cfgPath string, logger *slog.Logger) int {
	_, statErr := os.Stat(cfgPath)
	created := os.IsNotExist(statErr)
	if cfg.Token == "" {
		tok, err := randomToken()
		if err != nil {
			logger.Error("生成令牌失败", "err", err)
			return 1
		}
		cfg.Token = tok
		logger.Warn("没有配置令牌，已生成一个占位值 —— 请改成服务端的 Token", "token", tok)
	}
	if err := cfg.Save(cfgPath); err != nil {
		logger.Error("写入配置失败", "err", err)
		return 1
	}
	action := "已更新"
	if created {
		action = "已创建"
	}
	fmt.Printf("%s配置文件：%s\n", action, cfgPath)
	printConfig(cfg, cfgPath)
	fmt.Println()
	problems := selfCheck(ctx, cfg, client, logger)
	fmt.Println()
	fmt.Println("下一步：")
	if desktop.HotkeySupported() {
		fmt.Println("  1. 常驻运行：  mojinest-desktop run")
		fmt.Println("  2. 开机自启：把本程序快捷方式放进 shell:startup，参数填 run")
	} else {
		for _, line := range desktop.HotkeyInstructions(cfg.Hotkey) {
			fmt.Println("  " + line)
		}
	}
	if problems > 0 {
		return 1
	}
	return 0
}

func printConfig(cfg *desktop.Config, cfgPath string) {
	fmt.Printf("配置文件: %s\n", cfgPath)
	fmt.Printf("  服务端:   %s\n", cfg.Normalized())
	fmt.Printf("  令牌:     %s\n", mask(cfg.Token))
	fmt.Printf("  快捷键:   %s\n", cfg.Hotkey)
	fmt.Printf("  窗口模式: %s\n", cfg.WindowMode)
	fmt.Printf("  浏览器:   %s\n", orDefault(cfg.Browser, "(自动探测)"))
	fmt.Printf("  自动粘贴: %v（延迟 %dms）\n", cfg.AutoPaste, cfg.PasteDelayMs)
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// copyBest fetches the top match and puts it on the clipboard.
func copyBest(ctx context.Context, c *desktop.Client, clip *desktop.Clipboard, query string) error {
	items, err := c.Search(ctx, desktop.SearchQuery{Q: query, Sort: "used"})
	if err != nil {
		return err
	}
	if len(items) == 0 {
		if query == "" {
			return errors.New("库还是空的：先用网页版或导入功能放几张图进去")
		}
		// Nothing has been used yet: fall back to the newest match.
		items, err = c.Search(ctx, desktop.SearchQuery{Q: query, Sort: "newest"})
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
		p, werr := desktop.SavePNG("", "mojinest-"+safePrefix(it.Sha256)+"."+it.Ext, data)
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

// listenHotkey is a seam so tests can drive the press→action path without a
// real global shortcut (wine's RegisterHotKey cannot deliver WM_HOTKEY).
var listenHotkey = desktop.ListenHotkeyWithReady

func runDaemon(ctx context.Context, cfg *desktop.Config, client *desktop.Client, picker *desktop.Picker, logger *slog.Logger, dialogMode desktop.DialogMode) error {
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
		default: // a press is already queued
		}
	}

	// On a LAN over plain http the browser refuses clipboard writes, so the
	// window is served from a loopback relay instead: 127.0.0.1 counts as a
	// secure context. This is what makes "press hotkey → click sticker → paste"
	// actually work end to end.
	relay, err := desktop.StartRelay(ctx, cfg, logger)
	if err != nil {
		logger.Warn("本地转发启动失败，剪贴板复制可能不可用", "err", err)
	}
	if relay != nil {
		defer relay.Close()
		picker = picker.WithBase(relay.BaseURL())
	}

	hotkeyErr := make(chan error, 1)
	go func() {
		hotkeyErr <- listenHotkey(ctx, hk, press, func(err error) {
			if err != nil {
				logger.Error("全局快捷键不可用", "err", err)
				desktop.Notify(dialogMode, "Mojinest 快捷键注册失败", err.Error(), true)
				return
			}
			logger.Info("全局快捷键已注册，按下即可呼出表情库", "hotkey", hk.String())
		})
	}()

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
					// there is nothing for this process to keep doing.
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

// safePrefix returns a short, always-valid prefix of a hex digest.
func safePrefix(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

// selfPath is the absolute path of the running binary, used to spawn helpers
// (the relay) detached from this process.
func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
