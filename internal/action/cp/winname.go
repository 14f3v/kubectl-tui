package cp

import "strings"

// windowsIllegal lists the printable characters Windows refuses in a file name.
// '/' never reaches a name here (it separates archive components) and '\' is a
// separator on Windows; both are listed so a returned name is always a single
// path element. NTFS reads a lone ':' as the start of an alternate data stream,
// so "backup_10:30.sql" would otherwise become an empty "backup_10" with the data
// hidden in a stream named "30.sql".
const windowsIllegal = `<>:"/\|?*`

// WindowsFileName returns name changed just enough to be created as a file on
// Windows, or name itself when it is already valid. It is pure and does the same
// on every OS, so callers decide when Windows rules apply:
//
//   - each of < > : " / \ | ? * and every byte below 0x20 becomes '_';
//   - trailing dots and spaces, which Win32 silently strips (so "notes." and
//     "notes" would be the same file), are trimmed, and a name left empty
//     becomes "_";
//   - a reserved device name gets a '_' prefix (see isReservedWindowsName), so a
//     file called "nul" is not thrown away and one called "con" is not written
//     into the console the TUI is drawing on;
//   - invalid UTF-8, which has no UTF-16 spelling (Go would substitute U+FFFD,
//     and filepath.Localize refuses it outright), becomes '_'.
//
// The result is never "", "." or "..", so it can neither vanish from nor climb
// out of the path it is joined into.
func WindowsFileName(name string) string {
	b := []byte(strings.ToValidUTF8(name, "_"))
	for i, c := range b {
		if c < 0x20 || strings.IndexByte(windowsIllegal, c) >= 0 {
			b[i] = '_'
		}
	}
	s := strings.TrimRight(string(b), ". ")
	if s == "" {
		return "_"
	}
	if isReservedWindowsName(s) {
		return "_" + s
	}
	return s
}

// isReservedWindowsName reports whether name opens a device rather than a file on
// Windows. It mirrors Go's own check (internal/filepathlite/path_windows.go
// isReservedName): the part before the first '.' or ':', with trailing spaces
// trimmed, is compared case-insensitively against the device names. It differs
// in two deliberate ways, both erring towards renaming. A reserved name with an
// extension ("aux.c", "con.txt") always counts: Go asks RtlIsDosDeviceName_U at
// run time because Windows 11 stopped reserving those, but Windows 10 still opens
// the device. And COM0 and LPT0, which Microsoft's naming rules list but Go does
// not, count too.
func isReservedWindowsName(name string) bool {
	base := name
	if i := strings.IndexAny(base, ".:"); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, " ")
	return isReservedWindowsBaseName(base)
}

func isReservedWindowsBaseName(name string) bool {
	if len(name) == 3 {
		switch asciiUpper(name) {
		case "CON", "PRN", "AUX", "NUL":
			return true
		}
	}
	if len(name) >= 4 {
		switch asciiUpper(name[:3]) {
		case "COM", "LPT":
			if len(name) == 4 && '0' <= name[3] && name[3] <= '9' {
				return true
			}
			// Superscript ¹, ² and ³ count as digits too.
			switch name[3:] {
			case "\u00b9", "\u00b2", "\u00b3":
				return true
			}
			return false
		}
	}
	// CONIN$ and CONOUT$ open the console's input and output buffers just as CON
	// does; Go's check notes that even "./CONIN$" reaches the console.
	switch asciiUpper(name) {
	case "CONIN$", "CONOUT$":
		return true
	}
	return false
}

// asciiUpper upper-cases ASCII letters only, the comparison Windows uses for
// device names; a non-ASCII byte is left alone rather than folded, so no Unicode
// look-alike is mistaken for a device.
func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}

// windowsEntryPath applies WindowsFileName to every component of a slash-separated
// archive path and reports whether any of them changed.
func windowsEntryPath(rel string) (string, bool) {
	parts := strings.Split(rel, "/")
	changed := false
	for i, p := range parts {
		if q := WindowsFileName(p); q != p {
			parts[i], changed = q, true
		}
	}
	return strings.Join(parts, "/"), changed
}
