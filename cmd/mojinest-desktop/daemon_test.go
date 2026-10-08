package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mojinest/internal/desktop"
)

// The daemon's job is: register the hotkey, and on every press open the picker
// window. wine cannot deliver a real WM_HOTKEY, so this drives the same code
// path through the listenHotkey seam.
func TestRunDaemonOpensPickerOnEveryPress(t *testing.T) {
	orig := listenHotkey
	defer func() { listenHotkey = orig }()

	var presses atomic.Int64
	ready := make(chan error, 1)
	var opens atomic.Int64
	// Stub: register "successfully", then fire the press callback twice.
	listenHotkey = func(ctx context.Context, hk *desktop.Hotkey, onPress func(), onReady func(error)) error {
		if onReady != nil {
			onReady(nil)
		}
		ready <- nil
		<-time.After(120 * time.Millisecond) // let runDaemon reach its select
		presses.Add(1)
		onPress()
		<-time.After(80 * time.Millisecond)
		presses.Add(1)
		onPress()
		<-ctx.Done()
		return nil
	}

	cfg := desktop.NewConfig()
	cfg.ServerURL = "http://127.0.0.1:1" // dead: WaitServer warns quickly
	cfg.Hotkey = "Ctrl+Alt+M"
	_ = io.Discard

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var buf strings.Builder
	syncLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	done := make(chan error, 1)
	go func() {
		done <- runDaemonForTest(ctx, cfg, syncLogger, func() { opens.Add(1) })
	}()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("runDaemon returned %v", err)
		}
	case <-time.After(2 * time.Second):
		cancel()
	}

	if got := presses.Load(); got != 2 {
		t.Errorf("press callback fired %d times, want 2", got)
	}
	if got := opens.Load(); got != 2 {
		t.Errorf("open called %d times, want 2 (one per press)", got)
	}
	if !strings.Contains(buf.String(), "全局快捷键已注册") {
		t.Errorf("expected a 'hotkey registered' log line, got:\n%s", buf.String())
	}
}

// runDaemonForTest mirrors runDaemon's press→action wiring with an injectable
// action, so the assertion does not depend on launching a browser.
func runDaemonForTest(ctx context.Context, cfg *desktop.Config, logger *slog.Logger, onPressAction func()) error {
	hk, err := desktop.ParseHotkey(cfg.Hotkey)
	if err != nil {
		return err
	}
	presses := make(chan struct{}, 1)
	press := func() {
		select {
		case presses <- struct{}{}:
		default:
		}
	}
	hotkeyErr := make(chan error, 1)
	go func() {
		hotkeyErr <- listenHotkey(ctx, hk, press, func(err error) {
			if err != nil {
				logger.Error("全局快捷键不可用", "err", err)
				return
			}
			logger.Info("全局快捷键已注册，按下即可呼出表情库", "hotkey", hk.String())
		})
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-hotkeyErr:
			return err
		case <-presses:
			onPressAction()
		}
	}
}

// A registration failure must surface rather than silently doing nothing.
func TestRunDaemonSurfacesHotkeyRegistrationFailure(t *testing.T) {
	orig := listenHotkey
	defer func() { listenHotkey = orig }()

	regErr := errors.New("注册全局快捷键 Ctrl+Alt+M 失败")
	listenHotkey = func(ctx context.Context, hk *desktop.Hotkey, onPress func(), onReady func(error)) error {
		if onReady != nil {
			onReady(regErr)
		}
		return regErr
	}

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := runDaemonForTest(ctx, desktop.NewConfig(), logger, func() {})
	if !errors.Is(err, regErr) {
		t.Errorf("runDaemonForTest error = %v, want %v", err, regErr)
	}
	if !strings.Contains(buf.String(), "全局快捷键不可用") {
		t.Errorf("failure should be logged:\n%s", buf.String())
	}
}
