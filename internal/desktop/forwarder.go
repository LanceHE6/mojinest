package desktop

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Forwarder is a tiny TCP relay used for one reason: browsers only allow
// clipboard writes in a "secure context", and http://127.0.0.1 counts as one
// while http://192.168.x.x does not. Pointing the picker window at
// http://127.0.0.1:<port> — with the bytes relayed to the real (possibly LAN)
// server — makes "click a sticker → it is on your clipboard" actually work.
//
// It is a plain byte relay rather than a reverse proxy: the traffic is already
// plain HTTP (no TLS to strip), and not rewriting the request keeps the
// server's same-origin CSRF check satisfied, since the browser's Origin and
// the Host header stay identical.
type Forwarder struct {
	LocalAddr  string // e.g. 127.0.0.1:18690
	RemoteAddr string // e.g. 192.168.1.4:18642
	Idle       time.Duration

	log     *slog.Logger
	ln      net.Listener
	last    atomic.Int64
	started time.Time
}

// NewForwarder builds a relay to remote (host:port).
func NewForwarder(remote string, idle time.Duration, logger *slog.Logger) *Forwarder {
	if idle <= 0 {
		idle = 30 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Forwarder{RemoteAddr: remote, Idle: idle, log: logger}
}

// HostPortFromURL extracts host:port from a base URL, applying default ports.
func HostPortFromURL(base string) (string, error) {
	s := strings.TrimSpace(base)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "", fmt.Errorf("无法从 %q 解析出主机地址", base)
	}
	if _, _, err := net.SplitHostPort(s); err != nil {
		// No port: infer from the original scheme.
		if strings.HasPrefix(strings.TrimSpace(base), "https://") {
			s += ":443"
		} else {
			s += ":80"
		}
	}
	return s, nil
}

// Start begins listening on loopback and returns immediately.
func (f *Forwarder) Start() error {
	if f.LocalAddr == "" {
		f.LocalAddr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", f.LocalAddr)
	if err != nil {
		return fmt.Errorf("本地端口监听失败: %w", err)
	}
	f.ln = ln
	f.LocalAddr = ln.Addr().String()
	f.last.Store(time.Now().Unix())
	f.started = time.Now()
	return nil
}

// Port is the chosen loopback port.
func (f *Forwarder) Port() int {
	_, portStr, err := net.SplitHostPort(f.LocalAddr)
	if err != nil {
		return 0
	}
	p, _ := strconv.Atoi(portStr)
	return p
}

// BaseURL is what the picker window should open.
func (f *Forwarder) BaseURL() string { return "http://" + f.LocalAddr }

// Serve accepts connections until ctx is done or the relay goes idle.
func (f *Forwarder) Serve(ctx context.Context) error {
	if f.ln == nil {
		return fmt.Errorf("forwarder not started")
	}
	go f.watchIdle(ctx)
	go func() {
		<-ctx.Done()
		_ = f.ln.Close()
	}()
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		f.last.Store(time.Now().Unix())
		go f.handle(conn)
	}
}

func (f *Forwarder) watchIdle(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(time.Unix(f.last.Load(), 0)) > f.Idle {
				f.log.Info("本地转发空闲超时，退出", "idle", f.Idle.String())
				_ = f.ln.Close()
				return
			}
		}
	}
}

func (f *Forwarder) handle(client net.Conn) {
	defer client.Close()
	up, err := net.DialTimeout("tcp", f.RemoteAddr, 10*time.Second)
	if err != nil {
		f.log.Warn("无法连接服务端", "remote", f.RemoteAddr, "err", err)
		return
	}
	defer up.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, client); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, up); closeWrite(client); done <- struct{}{} }()
	<-done
}

// Close stops the listener.
func (f *Forwarder) Close() error {
	if f.ln == nil {
		return nil
	}
	return f.ln.Close()
}

// PingForwarder reports whether something is already serving on 127.0.0.1:port.
func PingForwarder(port int, timeout time.Duration) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// WritePortFile / ReadPortFile let separate CLI invocations share one relay.
func WritePortFile(path string, port int) error {
	return writeFileAtomic(path, []byte(strconv.Itoa(port)+"\n"))
}

// ReadPortFile returns 0 when the file is missing or malformed.
func ReadPortFile(path string) int {
	b, err := readFileLimited(path, 64)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return p
}
