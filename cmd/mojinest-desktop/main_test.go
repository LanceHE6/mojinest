package main

import (
	"fmt"
	"os"
	"testing"
)

// captureStdout runs fn with stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

// The bug the user hit: double-clicking the exe (no arguments) opened a browser
// window out of nowhere. It must never do that again — no args means help /
// menu, never `pick`.
func TestNoArgsDoesNotLaunchPicker(t *testing.T) {
	cfgPath := t.TempDir() + "/desktop.json"
	out := captureStdout(t, func() {
		code := run([]string{"-config", cfgPath})
		if code != 0 {
			t.Errorf("no-arg run should exit 0, got %d", code)
		}
	})
	// Without a GUI it prints the help; with one it shows a menu. Either way the
	// user must see *something* and no window may pop up on its own.
	if len(out) == 0 {
		t.Error("no-arg run printed nothing: the user would see nothing happen")
	}
	if !contains(out, "pick") {
		t.Errorf("no-arg output should tell the user about the pick command:\n%s", out)
	}
}

func TestUnknownSubcommandExitsTwo(t *testing.T) {
	cfgPath := t.TempDir() + "/desktop.json"
	if code := run([]string{"-config", cfgPath, "definitely-not-a-command", "-quiet"}); code != 2 {
		t.Errorf("unknown subcommand exit = %d, want 2", code)
	}
}

func TestHelpAndVersion(t *testing.T) {
	for _, argv := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		out := captureStdout(t, func() {
			if code := run(argv); code != 0 {
				t.Errorf("%v exit = %d, want 0", argv, code)
			}
		})
		if !contains(out, "setup") || !contains(out, "pick") {
			t.Errorf("%v output missing subcommands:\n%s", argv, out)
		}
	}
	out := captureStdout(t, func() {
		if code := run([]string{"-version"}); code != 0 {
			t.Errorf("version exit != 0")
		}
	})
	if !contains(out, "mojinest-desktop") {
		t.Errorf("version output = %q", out)
	}
}

func TestConfigSubcommandPrintsMaskedToken(t *testing.T) {
	cfgPath := t.TempDir() + "/desktop.json"
	out := captureStdout(t, func() {
		if code := run([]string{"-config", cfgPath, "-token", "supersecrettoken", "config"}); code != 0 {
			t.Errorf("config exit != 0")
		}
	})
	if contains(out, "supersecrettoken") {
		t.Errorf("config must not print the raw token:\n%s", out)
	}
	if !contains(out, "sup") || !contains(out, "***") {
		t.Errorf("expected a masked token like sup****ken:\n%s", out)
	}
}

// setup must write a usable config even with no server configured.
func TestSetupWritesConfigWithGeneratedToken(t *testing.T) {
	cfgPath := t.TempDir() + "/sub/desktop.json"
	out := captureStdout(t, func() {
		code := run([]string{"-config", cfgPath, "-server", "http://127.0.0.1:1", "setup"})
		// The health check will fail (nothing listens on port 1): exit 1 is right.
		if code != 1 {
			t.Errorf("setup against a dead server exit = %d, want 1", code)
		}
	})
	if !contains(out, "自检") {
		t.Errorf("setup should run the health check:\n%s", out)
	}
	// config file must exist and be readable by Load
	cfg, err := loadDesktopConfig(cfgPath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if cfg.Token == "" {
		t.Error("setup must generate a placeholder token")
	}
	if cfg.ServerURL != "http://127.0.0.1:1" {
		t.Errorf("server override not persisted: %s", cfg.ServerURL)
	}
}

func TestCopyWithoutQueryOnEmptyLibraryIsAClearError(t *testing.T) {
	// No server: the client fails on the network call, which is also a non-zero
	// exit with a readable error rather than a panic.
	cfgPath := t.TempDir() + "/desktop.json"
	if code := run([]string{"-config", cfgPath, "-server", "http://127.0.0.1:1", "-quiet", "copy", "猫"}); code != 1 {
		t.Errorf("copy against a dead server exit = %d, want 1", code)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

var _ = fmt.Sprintf
