package desktop

import (
	"strings"
	"testing"
)

func TestParseDialogMode(t *testing.T) {
	cases := []struct {
		in      string
		want    DialogMode
		wantErr bool
	}{
		{"", DialogAuto, false},
		{"auto", DialogAuto, false},
		{"AUTO", DialogAuto, false},
		{"always", DialogAlways, false},
		{"never", DialogNever, false},
		{"none", DialogNever, false},
		{" nope ", DialogAuto, true},
	}
	for _, c := range cases {
		got, ok := ParseDialogMode(c.in)
		if c.wantErr {
			if ok {
				t.Errorf("%q should be rejected, got %v", c.in, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("ParseDialogMode(%q) = %v/%v, want %v", c.in, got, ok, c.want)
		}
	}
}

// A modal dialog blocks the process forever when nobody can click OK — that is
// exactly what happened under wine: a scripted `copy` hung. never/always must
// therefore be honoured strictly, independent of platform.
func TestShouldDialogHonoursExplicitModes(t *testing.T) {
	if ShouldDialog(DialogAlways) != true {
		t.Error("always must always show a dialog")
	}
	if ShouldDialog(DialogNever) != false {
		t.Error("never must never show a dialog")
	}
	// auto falls back to "is there a console", which on Unix is always false.
	if got := ShouldDialog(DialogAuto); got != consoleAttached() {
		t.Errorf("auto = %v, want %v", got, consoleAttached())
	}
}

// PowerShell exits 0 even when a stub does nothing, so the client must require
// the script's own confirmation marker before claiming success.
func TestPsCopyConfirmedRequiresMarker(t *testing.T) {
	if err := psCopyConfirmed([]byte("ok\n")); err != nil {
		t.Errorf("explicit ok must pass: %v", err)
	}
	if err := psCopyConfirmed([]byte("OK")); err != nil {
		t.Errorf("marker check should be case-insensitive: %v", err)
	}
	err := psCopyConfirmed(nil)
	if err == nil {
		t.Fatal("silent PowerShell must be reported as a failure, not success")
	}
	if !strings.Contains(err.Error(), "没有确认") {
		t.Errorf("error should explain the missing confirmation: %v", err)
	}
	// Long output must be truncated rather than dumped into a dialog.
	long := []byte(strings.Repeat("x", 500))
	if err := psCopyConfirmed(long); err == nil {
		t.Error("garbage output should fail")
	} else if len(err.Error()) > 300 {
		t.Errorf("error message not truncated: %d chars", len(err.Error()))
	}
}

func TestPasteConfirmed(t *testing.T) {
	if err := pasteConfirmed(nil); err != nil {
		t.Errorf("silent success is normal for SendKeys: %v", err)
	}
	if err := pasteConfirmed([]byte("ok")); err != nil {
		t.Errorf("ok marker should pass: %v", err)
	}
	if err := pasteConfirmed([]byte("ERROR: cannot send keys")); err == nil {
		t.Error("error output should be surfaced")
	}
}
