package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"mojinest/internal/desktop"
)

// ensureRelay makes sure a loopback relay is running and returns the base URL
// the picker window should use ("" when the configured server is already a
// secure context, e.g. https or 127.0.0.1).
//
// Why a relay at all: browsers only allow clipboard writes in a secure context,
// and http://192.168.x.x is not one — so the web UI's copy button silently
// fails on a LAN. Relaying the same bytes through 127.0.0.1 fixes it without
// touching the server or the frontend.
//
// The relay must outlive this process (a hotkey binding runs `pick` and exits),
// so it is started detached from a hidden `relay` subcommand.
func ensureRelay(ctx context.Context, exe, cfgPath string, cfg *desktop.Config, logger *slog.Logger) string {
	if !cfg.NeedsLoopbackRelay() {
		return ""
	}
	if base := desktop.ReuseRelay(cfg); base != "" {
		return base
	}
	if exe == "" {
		logger.Warn("无法确定自身路径，跳过本地转发（剪贴板复制可能不可用）")
		return ""
	}
	cmd := exec.Command(exe, "relay",
		"-config", cfgPath,
		"-server", cfg.Normalized(),
		"-port-file", desktop.PortFilePath(),
		"-quiet")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = desktop.DetachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		logger.Warn("启动本地转发进程失败", "err", err)
		return ""
	}
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(150 * time.Millisecond):
		}
		if base := desktop.ReuseRelay(cfg); base != "" {
			logger.Info("已启动本地转发，剪贴板复制可用", "base", base)
			return base
		}
	}
	logger.Warn("本地转发未在 5 秒内就绪，将直接打开服务端地址")
	return ""
}

// runRelay is the hidden subcommand: it relays loopback traffic to the server
// and exits when idle (see desktop.Forwarder.Idle).
func runRelay(ctx context.Context, cfg *desktop.Config, portFile string, logger *slog.Logger) int {
	remote, err := desktop.HostPortFromURL(cfg.Normalized())
	if err != nil {
		logger.Error("无法解析服务端地址", "err", err, "server", cfg.Normalized())
		return 1
	}
	if portFile == "" {
		portFile = desktop.PortFilePath()
	}
	f := desktop.NewForwarder(remote, 30*time.Minute, logger)
	if err := f.Start(); err != nil {
		logger.Error("本地端口监听失败", "err", err)
		return 1
	}
	defer f.Close()
	if err := desktop.WritePortFile(portFile, f.Port()); err != nil {
		logger.Warn("无法记录端口", "err", err, "path", portFile)
	} else {
		defer os.Remove(portFile)
	}
	logger.Info("本地转发就绪", "local", f.BaseURL(), "remote", remote, "空闲退出", "30m")
	err = f.Serve(ctx)
	if err == nil || ctx.Err() != nil {
		return 0
	}
	logger.Warn("本地转发结束", "err", err)
	return 1
}
