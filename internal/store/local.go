package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Local stores blobs on the local filesystem under root.
type Local struct {
	root string
}

// NewLocal creates a local store rooted at root (created on demand).
func NewLocal(root string) (*Local, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Local{root: root}, nil
}

func (l *Local) Kind() string { return "local" }

// Path returns the absolute filesystem path for a key.
func (l *Local) Path(key string) (string, error) {
	trimmed := strings.TrimPrefix(key, "/")
	if trimmed == "" || strings.Contains(trimmed, "..") || strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("invalid key %q", key)
	}
	return filepath.Join(l.root, filepath.FromSlash(trimmed)), nil
}

func (l *Local) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	p, err := l.Path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func (l *Local) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	p, err := l.Path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

func (l *Local) Delete(ctx context.Context, key string) error {
	p, err := l.Path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Local) Exists(ctx context.Context, key string) (bool, error) {
	p, err := l.Path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// URL returns "" so the API proxies the bytes (single-user auth stays in force).
func (l *Local) URL(ctx context.Context, key string) (string, int) { return "", 0 }

// ErrNotFound is returned when a blob does not exist.
var ErrNotFound = errors.New("blob not found")
