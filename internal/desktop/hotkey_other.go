//go:build !windows

package desktop

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

func hotkeySupportedPlatform() bool { return false }

// ListenHotkey on Linux/macOS: global grabs are either Wayland-forbidden or
// need compositor cooperation, so the client relies on the desktop environment
// binding a shortcut to `mojinest-desktop pick` instead.
func ListenHotkey(ctx context.Context, hk *Hotkey, onPress func()) error {
	if os.Getenv("MOJINEST_ASSUME_HOTKEY") == "1" { // for tests only
		<-ctx.Done()
		return nil
	}
	return fmt.Errorf("%w：请在系统设置里把快捷键 %s 绑定到 `mojinest-desktop pick`", ErrHotkeyUnsupported, hk.String())
}

// HotkeyInstructions returns user-facing setup guidance per platform.
func HotkeyInstructions(hotkey string) []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			"Windows 下客户端会自行注册全局快捷键 " + hotkey + "。",
			"若注册失败（被别的程序占用），换一个组合即可，例如 Ctrl+Alt+K。",
		}
	case "darwin":
		return []string{
			"macOS 需要授予「辅助功能 / 输入监控」权限才能注册全局快捷键。",
			"系统设置 → 隐私与安全性 → 辅助功能，把本程序加进去。",
			"也可以在「键盘 → 快捷键」里把 " + hotkey + " 绑到 `mojinest-desktop pick`。",
		}
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return []string{
				"Wayland 不允许程序自行抢占全局快捷键。",
				"请在「系统设置 → 键盘 → 自定义快捷键」新增一条：",
				"    命令： mojinest-desktop pick",
				"    快捷键： " + hotkey,
			}
		}
		return []string{
			"当前系统未启用内置全局快捷键（X11 下程序抢键并不可靠）。",
			"请在桌面环境的键盘设置里新增自定义快捷键：",
			"    命令： mojinest-desktop pick",
			"    快捷键： " + hotkey,
			"（也可以把快捷键绑到 `mojinest-desktop copy 关键词`，一键复制常用表情）",
		}
	}
}
