package desktop

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHostPortFromURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://192.168.1.4:18642", "192.168.1.4:18642"},
		{"http://192.168.1.4", "192.168.1.4:80"},
		{"https://emoji.example.com", "emoji.example.com:443"},
		{"https://emoji.example.com/", "emoji.example.com:443"},
		{"http://127.0.0.1:9000/api", "127.0.0.1:9000"},
		{"192.168.1.4:18642", "192.168.1.4:18642"},
	}
	for _, c := range cases {
		got, err := HostPortFromURL(c.in)
		if err != nil || got != c.want {
			t.Errorf("HostPortFromURL(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := HostPortFromURL(""); err == nil {
		t.Error("empty URL should error")
	}
}

// The relay only matters for plain-http LAN addresses; everything else is
// already a secure context or already loopback.
func TestNeedsLoopbackRelay(t *testing.T) {
	cases := []struct {
		server string
		want   bool
	}{
		{"http://192.168.1.4:18642", true},
		{"http://userver.local:18642", false}, // hostname, not an IP: browser may already trust it
		{"http://127.0.0.1:18642", false},
		{"http://localhost:18642", false},
		{"https://emoji.example.com", false},
		{"https://192.168.1.4:18642", false},
	}
	for _, c := range cases {
		cfg := NewConfig()
		cfg.ServerURL = c.server
		if got := cfg.NeedsLoopbackRelay(); got != c.want {
			t.Errorf("NeedsLoopbackRelay(%q) = %v, want %v", c.server, got, c.want)
		}
	}
}

// The whole point of the relay: bytes must survive the round trip unmodified,
// including image payloads and the headers the server's CSRF check relies on.
func TestForwarderRelaysHTTPFaithfully(t *testing.T) {
	var seenHost, seenOrigin string
	upstream := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		seenOrigin = r.Header.Get("Origin")
		switch r.URL.Path {
		case "/healthz":
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/img":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNGDATA-1234"))
		case "/api/items":
			if r.Method != http.MethodPost {
				w.WriteHeader(405)
				return
			}
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"echo":%q}`, string(body))
		}
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() { _ = upstream.Serve(ln) }()
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := NewForwarder(ln.Addr().String(), time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := f.Start(); err != nil {
		t.Fatalf("forwarder start: %v", err)
	}
	defer f.Close()
	go func() { _ = f.Serve(ctx) }()

	if f.Port() == 0 {
		t.Fatal("forwarder did not pick a port")
	}
	if !strings.HasPrefix(f.BaseURL(), "http://127.0.0.1:") {
		t.Errorf("base url = %q, want loopback", f.BaseURL())
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(f.BaseURL() + "/healthz")
	if err != nil {
		t.Fatalf("GET healthz through relay: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != `{"ok":true}` {
		t.Errorf("healthz body = %q", b)
	}

	resp, err = client.Get(f.BaseURL() + "/img")
	if err != nil {
		t.Fatalf("GET img: %v", err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "PNGDATA-1234" {
		t.Errorf("image bytes altered: %q", b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type lost: %q", ct)
	}

	// POST with a body + the headers that matter for same-origin checks
	req, _ := http.NewRequest(http.MethodPost, f.BaseURL()+"/api/items", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", f.BaseURL())
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	// The stub echoes the body back JSON-quoted; escaping is expected.
	if !strings.Contains(string(b), `a`) || !strings.Contains(string(b), `1`) {
		t.Errorf("request body did not survive the relay: %q", b)
	}
	// Host must stay as the browser sent it (127.0.0.1:port), because the
	// server's Origin check compares Host with Origin.
	wantHost := strings.TrimPrefix(f.BaseURL(), "http://")
	if seenHost != wantHost {
		t.Errorf("upstream saw Host %q, want %q (relay must not rewrite it)", seenHost, wantHost)
	}
	if seenOrigin != f.BaseURL() {
		t.Errorf("upstream saw Origin %q, want %q", seenOrigin, f.BaseURL())
	}
}

func TestForwarderReportsDeadUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 127.0.0.1:1 refuses connections.
	f := NewForwarder("127.0.0.1:1", time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := f.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer f.Close()
	go func() { _ = f.Serve(ctx) }()

	client := &http.Client{Timeout: 2 * time.Second}
	if _, err := client.Get(f.BaseURL() + "/healthz"); err == nil {
		t.Error("expected an error when the upstream is down")
	}
	// The relay survives: it should still accept the next connection.
	if !PingForwarder(f.Port(), time.Second) {
		t.Error("relay should still be listening after a failed upstream dial")
	}
}

func TestForwarderPortFileRoundTrip(t *testing.T) {
	path := t.TempDir() + "/forward.port"
	if got := ReadPortFile(path); got != 0 {
		t.Errorf("missing file should read as 0, got %d", got)
	}
	if err := WritePortFile(path, 41234); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := ReadPortFile(path); got != 41234 {
		t.Errorf("read back = %d", got)
	}
}

func TestReuseRelayIgnoresStalePortFile(t *testing.T) {
	cfg := NewConfig()
	cfg.ServerURL = "http://192.168.1.4:18642"
	t.Setenv("MOJINEST_PORT_FILE", t.TempDir()+"/f.port")
	// No file yet: nothing to reuse.
	if got := ReuseRelay(cfg); got != "" {
		t.Errorf("expected empty reuse, got %q", got)
	}
	// A stale port nobody listens on must not be reused either.
	if err := WritePortFile(PortFilePath(), 1); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := ReuseRelay(cfg); got != "" {
		t.Errorf("dead port must not be reused, got %q", got)
	}
	// Loopback configs never need a relay at all.
	cfg.ServerURL = "http://127.0.0.1:18642"
	if got := ReuseRelay(cfg); got != "" {
		t.Errorf("loopback server needs no relay, got %q", got)
	}
}

func TestStartRelaySkipsWhenUnnecessary(t *testing.T) {
	cfg := NewConfig()
	cfg.ServerURL = "http://127.0.0.1:18642"
	f, err := StartRelay(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if f != nil {
		t.Error("no relay should be started for a loopback server")
	}
}

func TestPickerWithBaseOverridesWindowURL(t *testing.T) {
	cfg := NewConfig()
	cfg.ServerURL = "http://192.168.1.4:18642"
	cfg.Token = "tok"
	p := NewPicker(cfg, nil)
	want := "http://192.168.1.4:18642/#token=tok"
	if got := p.AuthedURL(); got != want {
		t.Errorf("default url = %q, want %q", got, want)
	}
	relayed := p.WithBase("http://127.0.0.1:40000")
	if got := relayed.AuthedURL(); got != "http://127.0.0.1:40000/#token=tok" {
		t.Errorf("relayed url = %q", got)
	}
	// The original picker must be unaffected.
	if got := p.AuthedURL(); got != want {
		t.Errorf("WithBase mutated the receiver: %q", got)
	}
}
