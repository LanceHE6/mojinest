package store

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLocalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocal(dir)
	if err != nil {
		t.Fatalf("new local: %v", err)
	}
	ctx := context.Background()
	key := KeyFor("abcdef1234567890", "png")

	if ok, _ := s.Exists(ctx, key); ok {
		t.Error("should not exist yet")
	}
	if err := s.Put(ctx, key, strings.NewReader("hello"), 5, "image/png"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if ok, _ := s.Exists(ctx, key); !ok {
		t.Error("should exist after put")
	}
	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello" {
		t.Errorf("got %q", data)
	}
	if _, err := s.Get(ctx, KeyFor("missing", "png")); err != ErrNotFound {
		t.Errorf("missing blob err = %v, want ErrNotFound", err)
	}
	if u, exp := s.URL(ctx, key); u != "" || exp != 0 {
		t.Errorf("local URL should be empty, got %q/%d", u, exp)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok, _ := s.Exists(ctx, key); ok {
		t.Error("should be gone")
	}
	// delete of missing blob is not an error
	if err := s.Delete(ctx, key); err != nil {
		t.Errorf("delete missing: %v", err)
	}
	// sharding puts blobs in a two-char subdirectory
	if p, _ := s.Path(key); filepath.Dir(p) != filepath.Join(dir, "files", "ab") {
		t.Errorf("shard path = %s", p)
	}
	// path traversal is rejected
	if _, err := s.Path("../../etc/passwd"); err == nil {
		t.Error("expected traversal to be rejected")
	}
}

func TestS3RoundTripAgainstStub(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	var sawAuth bool

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// path style: /bucket/key...
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
		key := ""
		if len(parts) > 1 {
			key = parts[1]
		}
		if strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			sawAuth = true
		}
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			objects[key] = b
			mu.Unlock()
			w.WriteHeader(200)
		case http.MethodHead:
			mu.Lock()
			_, ok := objects[key]
			mu.Unlock()
			if ok {
				w.WriteHeader(200)
			} else {
				w.WriteHeader(404)
			}
		case http.MethodGet:
			mu.Lock()
			b, ok := objects[key]
			mu.Unlock()
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Write(b)
		case http.MethodDelete:
			mu.Lock()
			delete(objects, key)
			mu.Unlock()
			w.WriteHeader(204)
		}
	})
	stub := httptest.NewServer(mux)
	defer stub.Close()

	s, err := NewS3(context.Background(), S3Options{
		Endpoint: stub.URL, Region: "auto", Bucket: "mojinest",
		AccessKey: "AKIA", SecretKey: "secret", PathStyle: true,
	})
	if err != nil {
		t.Fatalf("new s3: %v", err)
	}
	ctx := context.Background()
	key := KeyFor("deadbeef", "gif")

	if ok, err := s.Exists(ctx, key); err != nil || ok {
		t.Fatalf("exists before put: ok=%v err=%v", ok, err)
	}
	if err := s.Put(ctx, key, strings.NewReader("gif-bytes"), 9, "image/gif"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if ok, err := s.Exists(ctx, key); err != nil || !ok {
		t.Fatalf("exists after put: ok=%v err=%v", ok, err)
	}
	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "gif-bytes" {
		t.Errorf("roundtrip = %q", data)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok, _ := s.Exists(ctx, key); ok {
		t.Error("still present after delete")
	}
	if !sawAuth {
		t.Error("requests must be SigV4-signed")
	}
	if s.Kind() != "s3" {
		t.Errorf("kind = %s", s.Kind())
	}
	// presigned URL without public_base
	url, exp := s.URL(ctx, key)
	if url == "" || exp != 3600 || !strings.Contains(url, "X-Amz-Signature") {
		t.Errorf("presigned url = %q exp=%d", url, exp)
	}
	// public_base short-circuits signing
	s2, _ := NewS3(context.Background(), S3Options{
		Endpoint: stub.URL, Bucket: "b", AccessKey: "a", SecretKey: "s",
		PathStyle: true, PublicBase: "https://cdn.example.com/",
	})
	u2, e2 := s2.URL(ctx, key)
	if u2 != "https://cdn.example.com/"+key || e2 != 0 {
		t.Errorf("public url = %q/%d", u2, e2)
	}
}

func TestNewS3RequiresBucket(t *testing.T) {
	if _, err := NewS3(context.Background(), S3Options{}); err == nil {
		t.Error("expected error without bucket")
	}
}

func TestThumbKeyNaming(t *testing.T) {
	if got := ThumbKeyFor("1234abcd", "jpg"); got != "thumbs/12/1234abcd.jpg" {
		t.Errorf("thumb key = %s", got)
	}
	if got := KeyFor("ab", "png"); got != "files/ab/ab.png" {
		t.Errorf("short key = %s", got)
	}
	if got := KeyFor("x", "png"); got != "files/00/x.png" {
		t.Errorf("degenerate key = %s", got)
	}
}
