package desktop

import (
	"fmt"
	"os"
)

// NotifyStderr writes a user-facing message to stderr. It is the fallback used
// whenever a dialog would be wrong (scripts) or impossible (Linux/macOS).
func NotifyStderr(title, text string, isError bool) {
	prefix := "提示"
	if isError {
		prefix = "错误"
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n%s\n", prefix, title, text)
}
