package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mojinest/internal/desktop"
)

func TestImportQQCommand(t *testing.T) {
	var uploadedFiles []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/items" && r.Method == http.MethodPost {
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			files := r.MultipartForm.File["file"]
			var resItems []desktop.Item
			for _, f := range files {
				uploadedFiles = append(uploadedFiles, f.Filename)
				resItems = append(resItems, desktop.Item{Name: f.Filename, SourcePlat: "qq"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": resItems, "skipped": 0})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	tmp := t.TempDir()
	// Fake QQ structure
	oriDir := filepath.Join(tmp, "10001", "nt_qq", "nt_data", "Emoji", "personal_emoji", "Ori")
	if err := os.MkdirAll(oriDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 写入一张合法的 png 和一张文本垃圾文件
	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4")
	if err := os.WriteFile(filepath.Join(oriDir, "emoji_a"), pngData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oriDir, "junk.txt"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "desktop.json")
	cfg := desktop.NewConfig()
	cfg.ServerURL = ts.URL
	cfg.Token = "testtoken"
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// 运行 import-qq 命令
	code := run([]string{"-config", cfgPath, "import-qq", tmp})
	if code != 0 {
		t.Fatalf("import-qq returned %d", code)
	}

	if len(uploadedFiles) != 1 {
		t.Fatalf("expected 1 file uploaded, got %d (%v)", len(uploadedFiles), uploadedFiles)
	}
	if uploadedFiles[0] != "emoji_a.png" {
		t.Errorf("expected emoji_a.png, got %s", uploadedFiles[0])
	}
}

var _ = context.Background
