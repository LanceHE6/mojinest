package desktop

import (
	"io"
	"net"
	"os"
	"path/filepath"
)

// PortFilePath is where the CLI records the running relay's port.
func PortFilePath() string {
	if p := os.Getenv("MOJINEST_PORT_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "mojinest-forward.port"
	}
	return filepath.Join(home, ".config", "mojinest", "forward.port")
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readFileLimited(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(max)))
}

// closeWrite half-closes a TCP connection when the socket supports it, so the
// peer sees EOF instead of waiting for the connection to time out.
func closeWrite(c net.Conn) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}
