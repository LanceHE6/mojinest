// Package importer implements extracting stickers from local chat apps
// (such as QQNT) and importing them directly into Mojinest.
package importer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"mojinest/internal/imageutil"
)

// QQAccount represents a discovered QQ user profile on disk.
type QQAccount struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	EmojiPath string `json:"emoji_path"`
}

// Result records what happened during an import batch.
type Result struct {
	Scanned  int      `json:"scanned"`
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"`
	Errors   []string `json:"errors,omitempty"`
}

// Client abstracts the destination Mojinest server.
type Client interface {
	Upload(ctx context.Context, files map[string][]byte, tags []string, platform string) (int, error)
}

// FindQQNTEmojiDirs locates candidate personal_emoji/Ori directories across platforms.
// If customRoot is specified, it scans that directory directly.
func FindQQNTEmojiDirs(customRoot string) ([]QQAccount, error) {
	roots := candidateQQRoots(customRoot)
	var accounts []QQAccount
	seen := make(map[string]bool)

	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		// Structure 1: root/<QQ_NUMBER>/nt_qq/nt_data/Emoji/personal_emoji/Ori
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			accountID := e.Name()
			emojiPath := filepath.Join(root, accountID, "nt_qq", "nt_data", "Emoji", "personal_emoji", "Ori")
			if fi, err := os.Stat(emojiPath); err == nil && fi.IsDir() {
				if !seen[emojiPath] {
					seen[emojiPath] = true
					accounts = append(accounts, QQAccount{
						ID:        accountID,
						Path:      filepath.Join(root, accountID),
						EmojiPath: emojiPath,
					})
				}
			}
		}

		// Structure 2: customRoot is already pointing directly to the personal_emoji or Ori folder
		if isOriDir(root) && !seen[root] {
			seen[root] = true
			accounts = append(accounts, QQAccount{
				ID:        filepath.Base(filepath.Dir(filepath.Dir(root))),
				Path:      root,
				EmojiPath: root,
			})
		}
	}

	return accounts, nil
}

func isOriDir(p string) bool {
	if strings.HasSuffix(filepath.ToSlash(p), "/Ori") || strings.HasSuffix(filepath.ToSlash(p), "/personal_emoji") {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir()
	}
	return false
}

func candidateQQRoots(custom string) []string {
	if custom != "" {
		return []string{custom}
	}
	var res []string
	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "windows":
		if home != "" {
			res = append(res, filepath.Join(home, "Documents", "Tencent Files"))
		}
		// Also check user profile Documents if home dir is weird
		if up := os.Getenv("USERPROFILE"); up != "" {
			res = append(res, filepath.Join(up, "Documents", "Tencent Files"))
		}
	case "darwin":
		if home != "" {
			res = append(res, filepath.Join(home, "Library", "Containers", "com.tencent.qq", "Data", "Library", "Application Support", "QQ"))
		}
	default:
		// Linux (Native QQNT or Flatpak/Wine)
		if home != "" {
			res = append(res,
				filepath.Join(home, ".config", "QQ"),
				filepath.Join(home, ".local", "share", "QQ"),
				filepath.Join(home, ".var", "app", "com.tencent.qq", "config", "QQ"),
			)
		}
	}
	return res
}

// ImportQQNTEmoji scans the emoji directory of a QQ account, reads all image files,
// sniffs valid types, and batch-uploads them via Client.
func ImportQQNTEmoji(ctx context.Context, c Client, emojiDir string, tags []string, batchSize int) (*Result, error) {
	if batchSize <= 0 {
		batchSize = 20
	}
	res := &Result{}

	entries, err := os.ReadDir(emojiDir)
	if err != nil {
		return nil, fmt.Errorf("read emoji directory: %w", err)
	}

	batch := make(map[string][]byte)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		imported, err := c.Upload(ctx, batch, tags, "qq")
		if err != nil {
			return fmt.Errorf("upload batch: %w", err)
		}
		res.Imported += imported
		res.Skipped += len(batch) - imported
		batch = make(map[string][]byte)
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		res.Scanned++
		p := filepath.Join(emojiDir, entry.Name())
		f, err := os.Open(p)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: open failed: %v", entry.Name(), err))
			continue
		}
		// Read up to 32MB max per image
		data, err := io.ReadAll(io.LimitReader(f, 32<<20))
		f.Close()
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: read failed: %v", entry.Name(), err))
			continue
		}

		mime := imageutil.Sniff(data)
		if mime == "" {
			// Not a valid image file, skip
			res.Skipped++
			continue
		}

		// Ensure extension reflects mime
		ext := imageutil.Ext(mime)
		cleanName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if cleanName == "" {
			cleanName = "qq_emoji"
		}
		targetName := fmt.Sprintf("%s.%s", cleanName, ext)

		batch[targetName] = data
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}

	if err := flush(); err != nil {
		return res, err
	}
	return res, nil
}
