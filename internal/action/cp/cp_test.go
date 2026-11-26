package cp

import (
	"archive/tar"
	"bytes"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestParseSpec(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantRemote bool
		wantNS     string
		wantPod    string
		wantPath   string
		wantErr    bool
	}{
		{name: "local relative", in: "./file.txt", wantPath: "./file.txt"},
		{name: "local absolute", in: "/etc/hosts", wantPath: "/etc/hosts"},
		{name: "local plain name", in: "notes.md", wantPath: "notes.md"},
		{name: "windows drive lower", in: `c:\Users\me\a.txt`, wantPath: `c:\Users\me\a.txt`},
		{name: "windows drive upper", in: `C:\tmp\x`, wantPath: `C:\tmp\x`},
		{name: "pod path", in: "web:/etc/nginx.conf", wantRemote: true, wantPod: "web", wantPath: "/etc/nginx.conf"},
		{name: "ns pod path", in: "prod/web:/var/log/app.log", wantRemote: true, wantNS: "prod", wantPod: "web", wantPath: "/var/log/app.log"},
		{name: "relative remote path", in: "db:data/dump.sql", wantRemote: true, wantPod: "db", wantPath: "data/dump.sql"},
		{name: "empty", in: "", wantErr: true},
		{name: "missing path", in: "web:", wantErr: true},
		{name: "ns missing path", in: "prod/web:", wantErr: true},
		{name: "missing pod", in: ":/tmp/x", wantErr: true},
		{name: "ns only missing pod", in: "prod/:/tmp/x", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			remote, ns, pod, p, err := ParseSpec(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParseSpec(%q): expected error, got remote=%v ns=%q pod=%q path=%q", c.in, remote, ns, pod, p)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSpec(%q): unexpected error %v", c.in, err)
			}
			if remote != c.wantRemote || ns != c.wantNS || pod != c.wantPod || p != c.wantPath {
				t.Errorf("ParseSpec(%q) = (remote=%v ns=%q pod=%q path=%q), want (remote=%v ns=%q pod=%q path=%q)",
					c.in, remote, ns, pod, p, c.wantRemote, c.wantNS, c.wantPod, c.wantPath)
			}
		})
	}
}

// TestWriteTarFileRoundTrip verifies the pure tar primitive: an entry written by
// writeTarFile reads back through archive/tar with the exact same name and bytes.
func TestWriteTarFileRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	const (
		name = "config.yaml"
		mode = int64(0o644)
	)
	data := []byte("apiVersion: v1\nkind: ConfigMap\n")

	if err := writeTarFile(tw, name, mode, data); err != nil {
		t.Fatalf("writeTarFile: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tr := tar.NewReader(&buf)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if hdr.Name != name {
		t.Errorf("name = %q, want %q", hdr.Name, name)
	}
	if hdr.Mode != mode {
		t.Errorf("mode = %o, want %o", hdr.Mode, mode)
	}
	if hdr.Typeflag != tar.TypeReg {
		t.Errorf("typeflag = %v, want %v (reg)", hdr.Typeflag, tar.TypeReg)
	}
	got, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data = %q, want %q", got, data)
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Errorf("expected exactly one entry, got err=%v", err)
	}
}

// TestExtractTarToDir writes a two-entry archive and extracts it beneath an
// existing directory, asserting both files land at their archive-relative paths
// with the right contents.
func TestExtractTarToDir(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := writeTarFile(tw, "bundle/a.txt", 0o644, []byte("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := writeTarFile(tw, "bundle/sub/b.txt", 0o600, []byte("bravo")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := extractTar(&buf, dir, "bundle", runtime.GOOS); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	assertFile(t, filepath.Join(dir, "bundle", "a.txt"), "alpha")
	assertFile(t, filepath.Join(dir, "bundle", "sub", "b.txt"), "bravo")
}

// TestExtractTarToFile extracts a single-entry archive to a non-directory
// destination and asserts it is written to that exact path (the archive's internal
// name is ignored).
func TestExtractTarToFile(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := writeTarFile(tw, "dump.sql", 0o644, []byte("SELECT 1;")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "downloaded.sql")
	if _, err := extractTar(&buf, dst, "dump.sql", runtime.GOOS); err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	assertFile(t, dst, "SELECT 1;")
}

// TestExtractTarNeutralizesTraversal confirms the tar-slip guard: an entry that
// tries to escape the destination via a leading ".." is neutralized (clamped
// inside the destination) rather than being written to a parent directory. The
// file must appear beneath dst, and never above it. Clamped, the name is the
// requested source itself, so it is accepted as a single-file download.
func TestExtractTarNeutralizesTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Craft a header directly; writeTarFile would path.Join-sanitize the name.
	body := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "../escape.txt",
		Mode:     0o644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := extractTar(&buf, dir, "escape.txt", runtime.GOOS); err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	// The traversal is clamped: the file lands inside dst as escape.txt, never in
	// the parent directory.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("traversal wrote a file outside the destination")
	}
	assertFile(t, filepath.Join(dir, "escape.txt"), "pwned")
}

// TestResolveExtractTargetRejectsEscape exercises the containment check directly.
// archiveRel cleans every name before it gets here, so ".." never arrives from an
// archive; handing one in uncleaned proves the filepath.Rel guard behind it fires.
func TestResolveExtractTargetRejectsEscape(t *testing.T) {
	if _, _, err := resolveExtractTarget("/base", "sub/ok.txt", "linux"); err != nil {
		t.Errorf("in-dir name unexpectedly rejected: %v", err)
	}
	for _, rel := range []string{"../evil", "sub/../../evil"} {
		if got, _, err := resolveExtractTarget("/base", rel, "linux"); err == nil {
			t.Errorf("resolveExtractTarget(%q) = %q, want an escape error", rel, got)
		}
	}
}

// TestResolveExtractTargetWindowsNames pins the Windows renaming end to end
// through to the joined path, and that other systems keep names as they are.
// Nothing touches the disk, so the Linux cases are safe to run on Windows.
func TestResolveExtractTargetWindowsNames(t *testing.T) {
	dst := filepath.Join("base", "out")
	cases := []struct {
		rel, goos, want string
		renamed         bool
	}{
		{rel: "logs/app.log", goos: "windows", want: "logs/app.log"},
		{rel: "logs/backup_10:30.sql", goos: "windows", want: "logs/backup_10_30.sql", renamed: true},
		{rel: "con/notes./aux.c", goos: "windows", want: "_con/notes/_aux.c", renamed: true},
		{rel: "logs/backup_10:30.sql", goos: "linux", want: "logs/backup_10:30.sql"},
		{rel: "con/notes./aux.c", goos: "darwin", want: "con/notes./aux.c"},
	}
	for _, c := range cases {
		got, renamed, err := resolveExtractTarget(dst, c.rel, c.goos)
		if err != nil {
			t.Errorf("resolveExtractTarget(%q, %s): %v", c.rel, c.goos, err)
			continue
		}
		if want := filepath.Join(dst, filepath.FromSlash(c.want)); got != want || renamed != c.renamed {
			t.Errorf("resolveExtractTarget(%q, %s) = %q, %v; want %q, %v", c.rel, c.goos, got, renamed, want, c.renamed)
		}
	}
}

// TestWindowsSanitizingCannotEscape feeds the sanitizer the names most likely to
// turn into a traversal once trailing dots and spaces are trimmed, plus raw ".."
// (which archiveRel would have cleaned away), and checks every result stays
// beneath dst with no "." or ".." component left to climb with.
func TestWindowsSanitizingCannotEscape(t *testing.T) {
	dst := filepath.Join("base", "out")
	for _, rel := range []string{
		"..", "../evil", "a/../../evil", "...", ". .", ".. ", " ..", "... /x",
		`..\..\evil`, `a\..\..\evil`, "./x", "x/./y",
	} {
		name, _ := windowsEntryPath(rel)
		for _, part := range strings.Split(name, "/") {
			if part == "." || part == ".." || part == "" {
				t.Errorf("windowsEntryPath(%q) = %q, has component %q", rel, name, part)
			}
		}
		got, _, err := resolveExtractTarget(dst, rel, "windows")
		if err != nil {
			t.Errorf("resolveExtractTarget(%q): %v", rel, err)
			continue
		}
		if r, err := filepath.Rel(dst, got); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			t.Errorf("resolveExtractTarget(%q) = %q, outside %q", rel, got, dst)
		}
	}
}

// TestExtractTarWindowsNames extracts, with Windows rules, an archive whose names
// Windows would mangle or refuse, and checks they land renamed and counted. The
// renamed names are valid everywhere, so this runs the same on every host.
func TestExtractTarWindowsNames(t *testing.T) {
	entries := []tarEntry{
		{name: "logs/"},
		{name: "logs/app-2026-11-26T10:00:00.log", body: "A"},
		{name: "logs/con", body: "C"},
		{name: "logs/notes.", body: "N"},
		{name: "logs/what?.txt", body: "Q"},
		{name: "logs/ok.txt", body: "OK"},
	}
	dst := filepath.Join(t.TempDir(), "out")
	res, err := extractTar(buildTar(t, entries...), dst, "logs", "windows")
	if err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	want := map[string]string{
		"app-2026-11-26T10_00_00.log": "A",
		"_con":                        "C",
		"notes":                       "N",
		"what_.txt":                   "Q",
		"ok.txt":                      "OK",
	}
	if got := collectFiles(t, dst); !maps.Equal(got, want) {
		t.Errorf("files under dst = %v, want %v", got, want)
	}
	if res.Renamed != 4 {
		t.Errorf("Renamed = %d, want 4", res.Renamed)
	}

	// A single file downloaded into a directory is named from the archive, so it
	// is renamed too; one downloaded to a path the user typed is left alone.
	dir := t.TempDir()
	res, err = extractTar(buildTar(t, tarEntry{name: "backup_10:30.sql", body: "SQL"}), dir, "backup_10:30.sql", "windows")
	if err != nil {
		t.Fatalf("extractTar into dir: %v", err)
	}
	assertFile(t, filepath.Join(dir, "backup_10_30.sql"), "SQL")
	if res.Renamed != 1 {
		t.Errorf("Renamed into dir = %d, want 1", res.Renamed)
	}
	typed := filepath.Join(t.TempDir(), "backup.sql")
	res, err = extractTar(buildTar(t, tarEntry{name: "backup_10:30.sql", body: "SQL"}), typed, "backup_10:30.sql", "windows")
	if err != nil {
		t.Fatalf("extractTar to typed path: %v", err)
	}
	assertFile(t, typed, "SQL")
	if res.Renamed != 0 {
		t.Errorf("Renamed to typed path = %d, want 0", res.Renamed)
	}
}

// TestExtractTarEmpty confirms an archive with no regular files is reported as an
// error rather than silently succeeding.
func TestExtractTarEmpty(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTar(&buf, t.TempDir(), "x", runtime.GOOS); err == nil {
		t.Errorf("expected error for empty archive, got nil")
	}
}

// TestWriteTarDirRoundTrip tars a real directory tree with writeTarDir, then
// extracts it back and asserts the tree is reproduced under the given prefix.
func TestWriteTarDirRoundTrip(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "top.txt"), "top")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "nested", "inner.txt"), "inner")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	skipped, err := writeTarDir(tw, src, "payload", runtime.GOOS)
	if err != nil {
		t.Fatalf("writeTarDir: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %q, want nothing skipped", skipped)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	if _, err := extractTar(&buf, out, "payload", runtime.GOOS); err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	assertFile(t, filepath.Join(out, "payload", "top.txt"), "top")
	assertFile(t, filepath.Join(out, "payload", "nested", "inner.txt"), "inner")
}

// TestExtractTarLayouts pins where a download lands for every combination of a
// file or a directory in the pod and a destination that is missing, an existing
// directory, or an existing file. want maps slash paths relative to dst (with "."
// for dst itself) to contents, and must match the files on disk exactly.
func TestExtractTarLayouts(t *testing.T) {
	file := []tarEntry{{name: "dump.sql", body: "SELECT 1;"}}
	tree := []tarEntry{
		{name: "nginx/"},
		{name: "nginx/nginx.conf", body: "CONF"},
		{name: "nginx/conf.d/"},
		{name: "nginx/conf.d/default.conf", body: "DEFAULT"},
		{name: "nginx/mime.types", body: "MIME"},
	}
	// A directory holding one file is still a directory, not a file download.
	oneFileTree := []tarEntry{{name: "certs/"}, {name: "certs/tls.crt", body: "CRT"}}

	cases := []struct {
		name    string
		entries []tarEntry
		srcBase string
		dst     string // "missing", "dir" or "file" (a file holding "KEEP")
		want    map[string]string
		wantErr bool
	}{
		{name: "file to new path", entries: file, srcBase: "dump.sql", dst: "missing",
			want: map[string]string{".": "SELECT 1;"}},
		{name: "file into existing dir", entries: file, srcBase: "dump.sql", dst: "dir",
			want: map[string]string{"dump.sql": "SELECT 1;"}},
		{name: "file over existing file", entries: file, srcBase: "dump.sql", dst: "file",
			want: map[string]string{".": "SELECT 1;"}},
		{name: "dir to new path", entries: tree, srcBase: "nginx", dst: "missing",
			want: map[string]string{"nginx.conf": "CONF", "conf.d/default.conf": "DEFAULT", "mime.types": "MIME"}},
		{name: "one-file dir to new path", entries: oneFileTree, srcBase: "certs", dst: "missing",
			want: map[string]string{"tls.crt": "CRT"}},
		{name: "dir into existing dir", entries: tree, srcBase: "nginx", dst: "dir",
			want: map[string]string{"nginx/nginx.conf": "CONF", "nginx/conf.d/default.conf": "DEFAULT", "nginx/mime.types": "MIME"}},
		{name: "dir over existing file", entries: tree, srcBase: "nginx", dst: "file",
			want: map[string]string{".": "KEEP"}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out")
			switch c.dst {
			case "dir":
				if err := os.Mkdir(dst, 0o755); err != nil {
					t.Fatal(err)
				}
			case "file":
				mustWrite(t, dst, "KEEP")
			}

			_, err := extractTar(buildTar(t, c.entries...), dst, c.srcBase, runtime.GOOS)
			if c.wantErr != (err != nil) {
				t.Fatalf("extractTar err = %v, wantErr %v", err, c.wantErr)
			}
			if got := collectFiles(t, dst); !maps.Equal(got, c.want) {
				t.Errorf("files under dst = %v, want %v", got, c.want)
			}
		})
	}
}

// TestExtractTarRejectsForeignEntries confirms an archive holding anything other
// than the requested source is refused rather than extracted, including a ".."
// name that cleaning clamps to something outside the source, and that none of it
// reaches the disk around dst.
func TestExtractTarRejectsForeignEntries(t *testing.T) {
	cases := map[string][]tarEntry{
		"sibling":          {{name: "nginx/a", body: "A"}, {name: "other/b", body: "B"}},
		"climbs out":       {{name: "nginx/../../evil.txt", body: "pwned"}},
		"climbs out later": {{name: "nginx/a", body: "A"}, {name: "nginx/sub/../../../evil.txt", body: "pwned"}},
		"file then tree":   {{name: "nginx", body: "F"}, {name: "nginx/a", body: "A"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dst := filepath.Join(parent, "out")
			if _, err := extractTar(buildTar(t, entries...), dst, "nginx", runtime.GOOS); err == nil {
				t.Fatal("extractTar succeeded, want an error")
			}
			if _, err := os.Stat(filepath.Join(parent, "evil.txt")); !os.IsNotExist(err) {
				t.Error("a traversal entry was written outside the destination")
			}
		})
	}
}

// TestExtractTarReportsWindowsClashes checks that two entries which are one file
// on Windows are reported, whether they differ only in case or became equal when
// renamed, while an entry repeated under the same name is not. Only Result is
// asserted for the case clash, since the test host's filesystem may itself be
// case-insensitive (Windows, macOS) or not (Linux); the later entry wins on all.
func TestExtractTarReportsWindowsClashes(t *testing.T) {
	entries := []tarEntry{
		{name: "src/"},
		{name: "src/Makefile", body: "UPPER"},
		{name: "src/makefile", body: "lower"},
		{name: "src/a:b", body: "colon"},
		{name: "src/a_b", body: "underscore"},
		{name: "src/again", body: "first"},
		{name: "src/again", body: "second"},
	}
	dst := filepath.Join(t.TempDir(), "out")
	res, err := extractTar(buildTar(t, entries...), dst, "src", "windows")
	if err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	if want := []string{"src/makefile", "src/a_b"}; !slices.Equal(res.Overwritten, want) {
		t.Errorf("Overwritten = %q, want %q", res.Overwritten, want)
	}
	assertFile(t, filepath.Join(dst, "makefile"), "lower")
	assertFile(t, filepath.Join(dst, "a_b"), "underscore")
	assertFile(t, filepath.Join(dst, "again"), "second")

	// Other systems are not checked: there the two names are two files.
	res, err = extractTar(buildTar(t, entries[:3]...), filepath.Join(t.TempDir(), "out"), "src", "linux")
	if err != nil {
		t.Fatalf("extractTar linux: %v", err)
	}
	if len(res.Overwritten) != 0 {
		t.Errorf("Overwritten on linux = %q, want none", res.Overwritten)
	}
}

func TestExtractMode(t *testing.T) {
	cases := []struct {
		hdr  int64
		goos string
		want os.FileMode
	}{
		{0o400, "windows", 0o600},
		{0o444, "windows", 0o644},
		{0o755, "windows", 0o755},
		{0o400, "linux", 0o400},
		{0o444, "darwin", 0o444},
		{0o4755, "linux", 0o755}, // setuid and friends are never applied
	}
	for _, c := range cases {
		if got := extractMode(c.hdr, c.goos); got != c.want {
			t.Errorf("extractMode(%o, %s) = %o, want %o", c.hdr, c.goos, got, c.want)
		}
	}
}

// TestExtractTarRedownloadsReadOnlyOnWindows downloads a 0400 file twice, as a
// user re-fetching a Secret's key would, and over a file an earlier download
// left read-only. With Windows rules both must succeed and leave the file
// writable. os.Chmod and the owner-write bit mean the same thing on every host
// for this purpose, so the test runs everywhere.
func TestExtractTarRedownloadsReadOnlyOnWindows(t *testing.T) {
	dir := t.TempDir()
	for i, body := range []string{"first", "second"} {
		archive := buildTar(t, tarEntry{name: "tls.key", body: body, mode: 0o400})
		if _, err := extractTar(archive, dir, "tls.key", "windows"); err != nil {
			t.Fatalf("download %d: %v", i+1, err)
		}
	}
	target := filepath.Join(dir, "tls.key")
	assertFile(t, target, "second")
	if fi, err := os.Stat(target); err != nil || fi.Mode().Perm()&0o200 == 0 {
		t.Errorf("downloaded file is not writable: %v, %v", fi.Mode(), err)
	}

	stale := filepath.Join(t.TempDir(), "ca.crt")
	mustWrite(t, stale, "old")
	if err := os.Chmod(stale, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTar(buildTar(t, tarEntry{name: "ca.crt", body: "new"}), stale, "ca.crt", "windows"); err != nil {
		t.Fatalf("download over a read-only file: %v", err)
	}
	assertFile(t, stale, "new")
}

// tarEntry is one member of an in-memory test archive. A name ending in "/" is a
// directory marker, as GNU tar writes them; mode defaults to 0o644.
type tarEntry struct {
	name string
	body string
	mode int64
}

// buildTar writes entries verbatim, without writeTarFile's path.Join, so tests can
// craft names a real tar would never produce.
func buildTar(t *testing.T, entries ...tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: tar.TypeReg, Size: int64(len(e.body))}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if strings.HasSuffix(e.name, "/") {
			hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeDir, 0o755, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// collectFiles returns every regular file under root as slash paths relative to
// root ("." when root is itself a file) mapped to their contents.
func collectFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		got[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", root, err)
	}
	return got
}

// TestWriteTarDirReportsSkipped checks that what an upload leaves out is reported
// by path rather than dropped silently, and that only the regular files are
// archived. It needs a symlink, which Windows only lets privileged or developer-
// mode accounts create, so it skips where that fails.
func TestWriteTarDirReportsSkipped(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "real.txt"), "real")
	if err := os.Mkdir(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../real.txt", filepath.Join(src, "sub", "link.txt")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	skipped, err := writeTarDir(tw, src, "payload", runtime.GOOS)
	if err != nil {
		t.Fatalf("writeTarDir: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sub/link.txt"}; !slices.Equal(skipped, want) {
		t.Errorf("skipped = %q, want %q", skipped, want)
	}
	var names []string
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	if want := []string{"payload/real.txt"}; !slices.Equal(names, want) {
		t.Errorf("archived %q, want %q", names, want)
	}
}

func TestUploadMode(t *testing.T) {
	cases := []struct {
		mode fs.FileMode
		goos string
		want int64
	}{
		{0o666, "windows", 0o644},
		{0o444, "windows", 0o644},
		{fs.ModeIrregular | 0o666, "windows", 0o644},
		{fs.ModeDir | 0o777, "windows", 0o755},
		{0o755, "linux", 0o755},
		{0o600, "darwin", 0o600},
		{fs.ModeDir | 0o750, "linux", 0o750},
	}
	for _, c := range cases {
		if got := uploadMode(c.mode, c.goos); got != c.want {
			t.Errorf("uploadMode(%v, %s) = %o, want %o", c.mode, c.goos, got, c.want)
		}
	}
}

// TestWriteTarDirHeaderModes checks the header modes a directory upload carries:
// 0644 under Windows rules whatever the file's own bits, and the file's own bits
// otherwise.
func TestWriteTarDirHeaderModes(t *testing.T) {
	src := t.TempDir()
	file := filepath.Join(src, "run.sh")
	mustWrite(t, file, "#!/bin/sh\n")
	if err := os.Chmod(file, 0o700); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	for goos, want := range map[string]int64{"windows": 0o644, "linux": int64(fi.Mode().Perm())} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if _, err := writeTarDir(tw, src, "payload", goos); err != nil {
			t.Fatalf("writeTarDir(%s): %v", goos, err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		hdr, err := tar.NewReader(&buf).Next()
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Mode != want {
			t.Errorf("%s: header mode = %o, want %o", goos, hdr.Mode, want)
		}
	}
}

// TestUploadable pins which walked entries are read as files. A bare
// ModeIrregular is how Windows reports a OneDrive placeholder, so it must be
// tried; every other special type, alone or with ModeIrregular, is left out.
func TestUploadable(t *testing.T) {
	cases := []struct {
		mode fs.FileMode
		want bool
	}{
		{0o644, true},
		{fs.ModeIrregular | 0o666, true},
		{fs.ModeSymlink | 0o777, false},
		{fs.ModeSymlink | fs.ModeIrregular, false},
		{fs.ModeSocket, false},
		{fs.ModeNamedPipe, false},
		{fs.ModeDevice | fs.ModeCharDevice, false},
	}
	for _, c := range cases {
		if got := uploadable(c.mode); got != c.want {
			t.Errorf("uploadable(%v) = %v, want %v", c.mode, got, c.want)
		}
	}
}

func TestToSlashDir(t *testing.T) {
	cases := map[string]string{
		"/etc/nginx.conf": "/etc",
		"/etc/":           "/",
		"file.txt":        ".",
		"data/dump.sql":   "data",
		"/a/b/c":          "/a/b",
	}
	for in, want := range cases {
		if got := toSlashDir(in); got != want {
			t.Errorf("toSlashDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%q = %q, want %q", path, got, want)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
