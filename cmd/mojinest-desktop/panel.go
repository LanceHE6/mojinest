package main

import (
	"fmt"

	"mojinest/internal/desktop"
)

// controlPanel is what double-clicking the exe does: an explicit three-way
// choice instead of silently opening a browser window (which is what it used
// to do, and what surprised the user).
//
// On platforms where a dialog cannot be drawn, it prints usage instead so the
// command still does something visible.
func controlPanel(cfgPath string) int {
	cfg, err := desktop.Load(cfgPath)
	if err != nil {
		desktop.Notify(desktop.DialogAlways, "Mojinest 配置读取失败", fmt.Sprintf("%v\n\n配置文件：%s", err, cfgPath), true)
		return 1
	}

	// No GUI (Linux/macOS): print the CLI help — never open a window.
	if !desktop.CanPrompt() {
		fmt.Print(usage)
		return 0
	}

	state := "还没配置服务端和令牌\n请先运行：mojinest-desktop setup"
	if cfg.Token != "" {
		state = fmt.Sprintf("服务端 %s\n令牌 %s…\n快捷键 %s",
			cfg.Normalized(), safePrefix(cfg.Token), cfg.Hotkey)
	}

	switch desktop.AskYesNoCancel("Mojinest 表情包库", fmt.Sprintf(`当前设置：
%s

请选择要做的事：

  「是」    打开表情库（浏览器窗口）
  「否」    查看如何常驻运行（启用快捷键 %s）
  「取消」  什么都不做
`, state, cfg.Hotkey)) {
	case desktop.AskYes:
		picker := desktop.NewPicker(cfg, nil)
		if err := picker.Open(nil); err != nil {
			desktop.Notify(desktop.DialogAlways, "打不开选图窗口", err.Error(), true)
			return 1
		}
		return 0

	case desktop.AskNo:
		text := "常驻运行需要从命令行启动：\n\n    mojinest-desktop run\n\n" +
			"配置文件：\n    " + cfgPath
		if desktop.HotkeySupported() {
			text += "\n\n开机自启：把本程序快捷方式放进 shell:startup，参数填 run。\n" +
				"运行时按 " + cfg.Hotkey + " 即可叫出表情库。"
		}
		desktop.Notify(desktop.DialogAlways, "如何常驻运行", text, false)
		return 0

	default:
		return 0
	}
}
