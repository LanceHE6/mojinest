//go:build !windows

package desktop

// windowsVKForTest lets the shared test file assert the Windows key table on
// any platform: the table itself is plain data, not platform code.
func windowsVKForTest(key string) (uint32, bool) {
	for _, c := range []struct {
		name string
		vk   uint32
	}{
		{"SPACE", 0x20}, {"ENTER", 0x0D}, {"TAB", 0x09}, {"ESC", 0x1B},
		{"A", 0x41}, {"M", 0x4D}, {"Z", 0x5A}, {"0", 0x30}, {"9", 0x39},
		{"F1", 0x70}, {"F12", 0x7B}, {"F24", 0x87},
	} {
		if c.name == key {
			return c.vk, true
		}
	}
	return 0, false
}
