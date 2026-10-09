package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type dummyClient struct {
	uploaded map[string][]byte
}

func (d *dummyClient) Upload(ctx context.Context, files map[string][]byte, tags []string, platform string) (int, error) {
	for k, v := range files {
		d.uploaded[k] = v
	}
	return len(files), nil
}

func TestFindQQNTEmojiDirs(t *testing.T) {
	tmp := t.TempDir()
	// create fake QQ root structure
	acc1 := filepath.Join(tmp, "12345678", "nt_qq", "nt_data", "Emoji", "personal_emoji", "Ori")
	acc2 := filepath.Join(tmp, "87654321", "nt_qq", "nt_data", "Emoji", "personal_emoji", "Ori")
	if err := os.MkdirAll(acc1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(acc2, 0o755); err != nil {
		t.Fatal(err)
	}

	accs, err := FindQQNTEmojiDirs(tmp)
	if err != nil {
		t.Fatalf("find failed: %v", err)
	}
	if len(accs) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accs))
	}

	// test direct Ori dir
	directAccs, err := FindQQNTEmojiDirs(acc1)
	if err != nil {
		t.Fatalf("direct find failed: %v", err)
	}
	if len(directAccs) != 1 {
		t.Fatalf("expected 1 direct account, got %d", len(directAccs))
	}
}

func TestImportQQNTEmoji(t *testing.T) {
	tmp := t.TempDir()
	// fake png file
	pngHdr := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4")
	if err := os.WriteFile(filepath.Join(tmp, "emoji1"), pngHdr, 0o644); err != nil {
		t.Fatal(err)
	}
	// fake text file (should be skipped)
	if err := os.WriteFile(filepath.Join(tmp, "junk.txt"), []byte("not image"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &dummyClient{uploaded: make(map[string][]byte)}
	res, err := ImportQQNTEmoji(context.Background(), client, tmp, []string{"QQ"}, 10)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	if res.Scanned != 2 {
		t.Errorf("expected 2 scanned, got %d", res.Scanned)
	}
	if res.Imported != 1 {
		t.Errorf("expected 1 imported, got %d", res.Imported)
	}
	if res.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", res.Skipped)
	}
	if len(client.uploaded) != 1 {
		t.Errorf("client uploaded len mismatch: %d", len(client.uploaded))
	}
}
