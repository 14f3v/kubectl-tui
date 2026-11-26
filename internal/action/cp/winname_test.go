package cp

import "testing"

func TestWindowsFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		// Already valid: returned untouched.
		{"plain.txt", "plain.txt"},
		{"日本語.txt", "日本語.txt"},
		{".bashrc", ".bashrc"},
		{"..x", "..x"},
		{"console", "console"},
		{"com10", "com10"},
		{"comx", "comx"},
		{"auxiliary.c", "auxiliary.c"},
		{"_con", "_con"},
		{"CONIN$x", "CONIN$x"},
		{"lpt", "lpt"},

		// Characters Windows refuses, and control bytes.
		{"backup_10:30.sql", "backup_10_30.sql"},
		{"app-2026-11-26T10:00:00.log", "app-2026-11-26T10_00_00.log"},
		{`a<b>c"d|e?f*g`, "a_b_c_d_e_f_g"},
		{`a\b/c`, "a_b_c"},
		{"tab\there\x01", "tab_here_"},
		{"caf\xe9.txt", "caf_.txt"},

		// Trailing dots and spaces, which Win32 strips.
		{"notes.", "notes"},
		{"trailing  ", "trailing"},
		{"mixed. . ", "mixed"},
		{"...", "_"},
		{" ", "_"},
		{".", "_"},
		{"..", "_"},
		{"", "_"},

		// Reserved device names, in any case, with or without an extension.
		{"con", "_con"},
		{"CON", "_CON"},
		{"Prn", "_Prn"},
		{"nul.txt", "_nul.txt"},
		{"aux.c", "_aux.c"},
		{"aux.tar.gz", "_aux.tar.gz"},
		{"con .txt", "_con .txt"},
		{"nul.", "_nul"},
		{"nul ", "_nul"},
		{"COM1", "_COM1"},
		{"com0", "_com0"},
		{"lpt9.log", "_lpt9.log"},
		{"com¹", "_com¹"},
		{"LPT³.txt", "_LPT³.txt"},
		{"conin$", "_conin$"},
		{"CONOUT$", "_CONOUT$"},
		{"CONOUT$.log", "_CONOUT$.log"},
		// ':' is replaced before the device check, so it can't hide a device.
		{"con:stream", "con_stream"},
	}
	for _, c := range cases {
		if got := WindowsFileName(c.in); got != c.want {
			t.Errorf("WindowsFileName(%q) = %q, want %q", c.in, got, c.want)
		}
		// A sanitized name is a fixed point: sanitizing again changes nothing.
		if again := WindowsFileName(c.want); again != c.want {
			t.Errorf("WindowsFileName(%q) = %q, want it unchanged", c.want, again)
		}
	}
}

func TestWindowsEntryPath(t *testing.T) {
	cases := []struct {
		in, want string
		changed  bool
	}{
		{"nginx/conf.d/default.conf", "nginx/conf.d/default.conf", false},
		{"a:b/c./con", "a_b/c/_con", true},
		{"logs/2026-11-26T10:00.log", "logs/2026-11-26T10_00.log", true},
	}
	for _, c := range cases {
		got, changed := windowsEntryPath(c.in)
		if got != c.want || changed != c.changed {
			t.Errorf("windowsEntryPath(%q) = %q, %v; want %q, %v", c.in, got, changed, c.want, c.changed)
		}
	}
}
