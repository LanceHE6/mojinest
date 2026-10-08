//go:build windows

package desktop

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hotkeySupportedPlatform() bool { return true }

// HotkeyInstructions returns user-facing setup guidance per platform.
func HotkeyInstructions(hotkey string) []string {
	return []string{
		"Windows 下客户端会自行注册全局快捷键 " + hotkey + "。",
		"若注册失败（被别的程序占用），换一个组合即可，例如 Ctrl+Alt+K。",
		"开机自启：把 mojinest-desktop.exe 的快捷方式放进 shell:startup，参数写 run。",
	}
}

const (
	modAlt     = 0x0001
	modControl = 0x0002
	modShift   = 0x0004
	modWin     = 0x0008
	wmHotkey   = 0x0312
	wmQuit     = 0x0012
	wmApp      = 0x8000
)

// vkCodes maps canonical key names to Windows virtual-key codes.
var vkCodes = func() map[string]uint32 {
	m := map[string]uint32{
		"SPACE": 0x20, "ENTER": 0x0D, "TAB": 0x09, "ESC": 0x1B,
		"BACKSPACE": 0x08, "DELETE": 0x2E, "INSERT": 0x2D,
		"HOME": 0x24, "END": 0x23, "PAGEUP": 0x21, "PAGEDOWN": 0x22,
		"UP": 0x26, "DOWN": 0x28, "LEFT": 0x25, "RIGHT": 0x27,
		"MINUS": 0xBD, "EQUAL": 0xBB, "LBRACKET": 0xDB, "RBRACKET": 0xDD,
		"SEMICOLON": 0xBA, "QUOTE": 0xDE, "COMMA": 0xBC, "PERIOD": 0xBE,
		"SLASH": 0xBF, "BACKSLASH": 0xDC, "GRAVE": 0xC0,
	}
	for c := 'A'; c <= 'Z'; c++ {
		m[string(c)] = uint32(c)
	}
	for c := '0'; c <= '9'; c++ {
		m[string(c)] = uint32(c)
	}
	for i := 1; i <= 24; i++ {
		m[fmt.Sprintf("F%d", i)] = uint32(0x70 + i - 1)
	}
	return m
}()

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procGetMessage       = user32.NewProc("GetMessageW")
	procPeekMessage      = user32.NewProc("PeekMessageW")
	procPostThreadMsg    = user32.NewProc("PostThreadMessageW")
)

type msgStruct struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

var (
	hotkeyIDCounter uint32
	hotkeyIDMu      sync.Mutex
)

// ListenHotkey registers the shortcut and invokes onPress whenever it fires.
// It blocks until ctx is cancelled (run it in its own goroutine).
func ListenHotkey(ctx context.Context, hk *Hotkey, onPress func()) error {
	return ListenHotkeyWithReady(ctx, hk, onPress, nil)
}

// ListenHotkeyWithReady is ListenHotkey plus an onReady callback fired once the
// shortcut is actually registered — callers use it to tell the user that the
// hotkey is live (or that registration failed) instead of guessing.
func ListenHotkeyWithReady(ctx context.Context, hk *Hotkey, onPress func(), onReady func(error)) error {
	if !hotkeySupportedPlatform() {
		return ErrHotkeyUnsupported
	}
	vk, ok := vkCodes[hk.Key]
	if !ok {
		return fmt.Errorf("Windows 不支持按键 %s", hk.Key)
	}
	var mods uint32
	for _, m := range hk.Mods {
		switch m {
		case ModCtrl:
			mods |= modControl
		case ModAlt:
			mods |= modAlt
		case ModShift:
			mods |= modShift
		case ModSuper:
			mods |= modWin
		}
	}

	hotkeyIDMu.Lock()
	hotkeyIDCounter++
	id := hotkeyIDCounter
	hotkeyIDMu.Unlock()

	// The message queue is per-thread: lock the OS thread for the loop's life.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if r, _, err := procRegisterHotKey.Call(0, uintptr(id), uintptr(mods), uintptr(vk)); r == 0 {
		regErr := fmt.Errorf("注册全局快捷键 %s 失败（可能被别的程序占用）: %w", hk.String(), err)
		if onReady != nil {
			onReady(regErr)
		}
		return regErr
	}
	defer procUnregisterHotKey.Call(0, uintptr(id))
	if onReady != nil {
		onReady(nil)
	}

	// Wake the blocking GetMessage when ctx is done.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			tid := windows.GetCurrentThreadId()
			procPostThreadMsg.Call(uintptr(tid), uintptr(wmApp), 0, 0)
		case <-stop:
		}
	}()
	defer close(stop)

	var msg msgStruct
	for {
		r, _, err := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return fmt.Errorf("消息循环失败: %w", err)
		}
		if r == 0 { // WM_QUIT
			return nil
		}
		if msg.message == wmHotkey && msg.wParam == uintptr(id) {
			onPress()
		}
		if msg.message == wmApp {
			return nil
		}
	}
}
