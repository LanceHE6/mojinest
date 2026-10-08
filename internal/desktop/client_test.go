package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeServer mimics the Mojinest API surface the desktop client uses.
func fakeServer(t *testing.T, token string, items []Item, payloads map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if token == "" {
			return true
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return false
		}
		return true
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "kind": "local"})
	})
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int64{"items": int64(len(items)), "tags": 3})
	})
	mux.HandleFunc("/api/items", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method == http.MethodPost {
			_ = r.ParseMultipartForm(4 << 20)
			n := len(r.MultipartForm.File["file"])
			_ = json.NewEncoder(w).Encode(map[string]any{"items": make([]Item, n), "skipped": 0})
			return
		}
		q := r.URL.Query()
		out := []Item{}
		for _, it := range items {
			if q.Get("animated") == "1" && !it.Animated {
				continue
			}
			if want := q.Get("q"); want != "" && !strings.Contains(it.Name+it.AIDesc+it.AIOCR, want) {
				continue
			}
			out = append(out, it)
		}
		sortMode := q.Get("sort")
		if sortMode == "used" && len(out) > 1 && out[0].UseCount < out[1].UseCount {
			out[0], out[1] = out[1], out[0]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": out, "total": len(out)})
	})
	mux.HandleFunc("/api/items/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/use") {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		w.WriteHeader(404)
	})
	mux.HandleFunc("/api/files/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		sha := strings.TrimPrefix(r.URL.Path, "/api/files/")
		p, ok := payloads[sha]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(p))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestClientSearchAndDownload(t *testing.T) {
	items := []Item{
		{ID: 1, Sha256: strings.Repeat("a", 64), Ext: "png", MIME: "image/png", Name: "猫猫大笑", UseCount: 1},
		{ID: 2, Sha256: strings.Repeat("b", 64), Ext: "gif", MIME: "image/gif", Name: "无语青蛙", Animated: true, UseCount: 9},
	}
	ts := fakeServer(t, "tok", items, map[string]string{strings.Repeat("b", 64): "GIF89a-data"})
	c := NewClient(ts.URL, "tok")

	got, err := c.Search(context.Background(), SearchQuery{Sort: "used"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 2 || got[0].ID != 2 {
		t.Errorf("usage sort wrong: %+v", got)
	}

	got, err = c.Search(context.Background(), SearchQuery{Q: "青蛙"})
	if err != nil || len(got) != 1 || got[0].ID != 2 {
		t.Errorf("query filter: %v %+v", err, got)
	}

	got, err = c.Search(context.Background(), SearchQuery{Animate: "1"})
	if err != nil || len(got) != 1 || !got[0].Animated {
		t.Errorf("animated filter: %v %+v", err, got)
	}

	data, err := c.Download(context.Background(), items[1])
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if string(data) != "GIF89a-data" {
		t.Errorf("payload = %q", data)
	}

	// 404 must be a clear error, not empty bytes
	_, err = c.Download(context.Background(), Item{Sha256: strings.Repeat("f", 64), Ext: "png"})
	if err == nil {
		t.Error("expected error for missing blob")
	}
}

func TestClientAuthErrorsAreClear(t *testing.T) {
	ts := fakeServer(t, "right-token", nil, nil)
	bad := NewClient(ts.URL, "wrong-token")
	_, err := bad.Stats(context.Background())
	if err == nil {
		t.Fatal("expected unauthorized")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error should mention unauthorized: %v", err)
	}
	if err := bad.Ping(context.Background()); err == nil {
		t.Error("ping should fail with a wrong token")
	}

	good := NewClient(ts.URL, "right-token")
	if err := good.Ping(context.Background()); err != nil {
		t.Errorf("ping with right token: %v", err)
	}
	stats, err := good.Stats(context.Background())
	if err != nil || stats["items"] != 0 {
		t.Errorf("stats = %v %v", stats, err)
	}
}

func TestClientUpload(t *testing.T) {
	ts := fakeServer(t, "tok", nil, nil)
	c := NewClient(ts.URL, "tok")
	n, err := c.Upload(context.Background(), map[string][]byte{
		"a.png": {1, 2, 3},
		"b.gif": {4, 5},
	}, []string{"导入"}, "qq")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if n != 2 {
		t.Errorf("uploaded %d, want 2", n)
	}
}

func TestClientNeverSendsTokenOnRedirectToOtherHost(t *testing.T) {
	// A hostile redirect target must not see the Authorization header.
	var leaked bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
		w.WriteHeader(200)
	}))
	defer evil.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/x", http.StatusFound)
	}))
	defer relay.Close()

	c := NewClient(relay.URL, "secret-token")
	resp, err := c.do(context.Background(), http.MethodGet, "/api/stats", nil, "")
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if leaked {
		t.Error("token leaked to redirect target")
	}
}

func TestPickerWaitServer(t *testing.T) {
	ts := fakeServer(t, "", nil, nil)
	c := NewClient(ts.URL, "")
	p := NewPicker(NewConfig(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.WaitServer(ctx, c, 2e9); err != nil {
		t.Errorf("wait should succeed against a live server: %v", err)
	}
	dead := NewClient("http://127.0.0.1:1", "")
	if err := p.WaitServer(ctx, dead, 300_000_000); err == nil {
		t.Error("expected timeout against an unreachable server")
	}
}

func TestAPIErrorMessages(t *testing.T) {
	if got := apiError([]byte(`{"error":"boom"}`), 500); got != "boom" {
		t.Errorf("apiError = %q", got)
	}
	if got := apiError(nil, 401); !strings.Contains(got, "Token") {
		t.Errorf("401 message = %q", got)
	}
	if got := apiError([]byte("plain text"), 400); got != "plain text" {
		t.Errorf("fallback = %q", got)
	}
}

var _ = fmt.Sprintf
