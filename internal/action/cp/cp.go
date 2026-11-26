// Package cp copies files and directories between the local filesystem and a pod,
// mirroring `kubectl cp`. Like kubectl, it works by exec'ing tar inside the target
// container: to upload it pipes a tar stream to `tar -xmf -` on the pod's stdin;
// to download it reads the stdout of `tar -cf -` from the pod. It therefore
// depends on a `tar` binary being present in the container image — exactly the
// same requirement (and failure mode) as kubectl cp. The exec transport reuses the
// WebSocket-primary/SPDY-secondary fallback executor proven by the execshell
// package; that helper is duplicated here (unexported) so this package stays a
// self-contained backend with no TUI dependencies.
package cp

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// ParseSpec parses a copy endpoint into its parts. A remote endpoint is written
// "pod:/path" or "namespace/pod:/path"; the colon separates the pod reference from
// the in-container path. A plain filesystem path with no such colon is local, and
// is returned verbatim as path with remote=false.
//
// The colon test deliberately ignores a Windows drive-letter colon (e.g. the "C:"
// in "C:\dir"): a single ASCII letter immediately followed by ':' at the start of
// the string is treated as a drive marker, not a pod/path separator, so local
// Windows paths are not mistaken for pod references. This matches kubectl's own
// heuristic.
//
// The function is pure and does no I/O; callers validate the resolved pod against
// the cluster.
func ParseSpec(s string) (remote bool, namespace, pod, path string, err error) {
	if s == "" {
		return false, "", "", "", fmt.Errorf("empty copy path")
	}

	// A leading "X:" where X is a single letter is a Windows drive, not a pod:path
	// separator. Only the very first colon can be a drive colon, so we look at it in
	// isolation before scanning for a pod/path separator.
	if isWindowsDriveColon(s) {
		return false, "", "", s, nil
	}

	idx := strings.Index(s, ":")
	if idx < 0 {
		// No colon at all: a local filesystem path.
		return false, "", "", s, nil
	}

	// Remote spec: everything before the colon is the pod reference (optionally
	// namespace-qualified), everything after is the in-container path.
	ref := s[:idx]
	p := s[idx+1:]
	if p == "" {
		return false, "", "", "", fmt.Errorf("remote spec %q is missing a path after ':'", s)
	}

	ns, podName := splitPodRef(ref)
	if podName == "" {
		return false, "", "", "", fmt.Errorf("remote spec %q is missing a pod name", s)
	}
	return true, ns, podName, p, nil
}

// isWindowsDriveColon reports whether s begins with a single-letter Windows drive
// designator ("C:", "d:\\", …). Such a colon is part of a local path and must not
// be read as the pod/path separator.
func isWindowsDriveColon(s string) bool {
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// splitPodRef splits a "namespace/pod" (or bare "pod") reference into its parts.
// An empty namespace means the caller's current/default namespace applies.
func splitPodRef(ref string) (namespace, pod string) {
	if i := strings.Index(ref, "/"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

// Result reports what a copy did beyond succeeding, so the caller can say so when
// the files on disk are not a byte-for-byte mirror of the source.
type Result struct {
	// Skipped lists, relative to the uploaded directory, the entries an upload
	// left out because they are not regular files: symlinks, sockets, devices,
	// and placeholders that could not be read.
	Skipped []string

	// Renamed counts downloaded files written under a different name because
	// theirs is not valid on Windows (see WindowsFileName).
	Renamed int
	// Overwritten lists the archive names of downloaded files that replaced an
	// earlier, different entry because Windows sees the two as one file: their
	// names differ only in case ("Makefile", "makefile") or became equal when
	// renamed ("a:b", "a_b"). Only the later one is on disk.
	Overwritten []string
}

// CopyToPod uploads a local file or directory tree to dstRemote inside the
// container. It builds a tar archive whose top-level entry name is the basename of
// dstRemote, then unpacks it with `tar -xmf - -C <dir(dstRemote)>` so the content
// lands exactly at dstRemote. An empty container targets the pod's first container
// (resolved server-side). It blocks until the transfer completes. Entries of a
// directory that are not regular files are left out and listed in Result.Skipped.
func CopyToPod(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, ns, pod, container, srcLocal, dstRemote string) (Result, error) {
	var res Result
	info, err := os.Stat(srcLocal)
	if err != nil {
		return res, fmt.Errorf("cannot read local source %q: %w", srcLocal, err)
	}

	// The archive is named by the destination's basename and extracted into the
	// destination's parent directory, so a file "a.txt" copied to "/etc/b.txt" lands
	// as "/etc/b.txt" regardless of the source name — matching `kubectl cp`.
	destDir := toSlashDir(dstRemote)
	destBase := path.Base(filepath.ToSlash(strings.TrimRight(dstRemote, "/")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if info.IsDir() {
		if res.Skipped, err = writeTarDir(tw, srcLocal, destBase, runtime.GOOS); err != nil {
			tw.Close()
			return res, err
		}
	} else {
		data, rerr := os.ReadFile(srcLocal)
		if rerr != nil {
			tw.Close()
			return res, fmt.Errorf("cannot read %q: %w", srcLocal, rerr)
		}
		if werr := writeTarFile(tw, destBase, uploadMode(info.Mode(), data, runtime.GOOS), data); werr != nil {
			tw.Close()
			return res, werr
		}
	}
	if err := tw.Close(); err != nil {
		return res, fmt.Errorf("finalize tar: %w", err)
	}

	cmd := []string{"tar", "-xmf", "-", "-C", destDir}
	var stderr bytes.Buffer
	err = execStream(ctx, cfg, cs, ns, pod, container, cmd, remotecommand.StreamOptions{
		Stdin:  &buf,
		Stdout: io.Discard,
		Stderr: &stderr,
	})
	if err != nil {
		return res, wrapTarErr("upload", err, stderr.String())
	}
	return res, nil
}

// CopyFromPod downloads srcRemote from the container into dstLocal. It runs
// `tar -cf - -C <dir(srcRemote)> <base(srcRemote)>` in the pod, reads the tar off
// stdout, and extracts it according to dstLocal: into an existing directory the
// copy lands beneath it under its own name; a file is written to dstLocal itself;
// and a directory copied to a path that does not exist yet becomes that path (see
// extractTar). An empty container targets the pod's first container. It blocks
// until the transfer completes.
func CopyFromPod(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, ns, pod, container, srcRemote, dstLocal string) (Result, error) {
	srcDir := toSlashDir(srcRemote)
	srcBase := path.Base(filepath.ToSlash(strings.TrimRight(srcRemote, "/")))
	// ".." would make tar emit names that climb out of srcDir, which no archive
	// entry can be matched against; it names no file to copy any more than "." does.
	if srcBase == "" || srcBase == "." || srcBase == ".." || srcBase == "/" {
		return Result{}, fmt.Errorf("remote source %q has no file name to copy", srcRemote)
	}

	cmd := []string{"tar", "-cf", "-", "-C", srcDir, srcBase}
	var stdout, stderr bytes.Buffer
	err := execStream(ctx, cfg, cs, ns, pod, container, cmd, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return Result{}, wrapTarErr("download", err, stderr.String())
	}

	return extractTar(&stdout, dstLocal, srcBase, runtime.GOOS)
}

// writeTarFile writes a single regular-file entry (name, mode, data) into tw. It
// is the pure primitive the round-trip test exercises: given the same name and
// bytes it always produces a byte-for-byte reproducible entry. mode is the Unix
// permission bits (e.g. 0o644).
func writeTarFile(tw *tar.Writer, name string, mode int64, data []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header for %q: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar data for %q: %w", name, err)
	}
	return nil
}

// writeTarDir walks a local directory tree and writes every regular file into tw
// under prefix (the archive-relative name of the directory root). Anything else
// (symlinks, sockets, devices, pipes) is left out, the conservative subset that
// unpacks the same with every container tar, and returned as slash-separated
// paths relative to root so the caller can say so rather than let it vanish.
//
// A file that Go reports as only ModeIrregular is tried as a regular one (see
// uploadable), and left out only if it cannot be read. goos selects the header
// modes (see uploadMode).
//
// root itself is followed when it is a symlink (or, on Windows, a junction) to a
// directory, as CopyToPod's os.Stat already did to call it one. filepath.Walk
// would otherwise Lstat it as a link and stop there, uploading nothing. A
// trailing separator makes Lstat resolve the last element on Unix (POSIX path
// resolution) and on Windows (os/stat_windows.go lstatNolog), so the walk starts
// in the directory it points to. Links further down are still left out.
func writeTarDir(tw *tar.Writer, root, prefix, goos string) (skipped []string, err error) {
	if li, lerr := os.Lstat(root); lerr == nil && !li.IsDir() {
		if si, serr := os.Stat(root); serr == nil && si.IsDir() {
			root += string(filepath.Separator)
		}
	}
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		// tar paths are always slash-separated regardless of the host OS.
		rel = filepath.ToSlash(rel)
		shown := rel
		if rel == "." {
			// root itself, when it is not a directory even once followed;
			// name it rather than report ".".
			shown = filepath.Base(root)
		}
		if !uploadable(info.Mode()) {
			skipped = append(skipped, shown)
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			if !info.Mode().IsRegular() {
				skipped = append(skipped, shown)
				return nil
			}
			return fmt.Errorf("cannot read %q: %w", p, err)
		}
		return writeTarFile(tw, path.Join(prefix, rel), uploadMode(info.Mode(), data, goos), data)
	})
	return skipped, err
}

// uploadMode is the permission written into the tar header for an uploaded entry
// whose content is data. A Windows mode says nothing about Unix permissions: Go
// synthesizes it from the read-only attribute alone (os/types_windows.go), as
// 0666, or 0444 when read-only, and adds 0111 only to directories. GNU and
// BusyBox tar running as root in the pod apply header modes as they are, so an
// upload from Windows would land world-writable. On Windows it is therefore the
// conventional 0644 for a file and 0755 for a directory (directories get no
// header of their own today; the pod's tar creates them), and 0755 for a file the
// pod's kernel would run (see linuxExecutable), so a script or a binary
// cross-compiled for Linux can be run without a chmod +x first. Elsewhere the
// entry's own bits are kept, as before.
func uploadMode(m fs.FileMode, data []byte, goos string) int64 {
	switch {
	case goos != "windows":
		return int64(m.Perm())
	case m.IsDir(), linuxExecutable(data):
		return 0o755
	default:
		return 0o644
	}
}

// linuxExecutable reports whether data starts the way Linux's execve requires
// of a file it runs: an ELF header or a "#!" interpreter line. It only tells a
// Windows upload, which has no exec bit to copy, which files to mark
// executable; file names and extensions are not consulted.
func linuxExecutable(data []byte) bool {
	return bytes.HasPrefix(data, []byte("\x7fELF")) || bytes.HasPrefix(data, []byte("#!"))
}

// uploadable reports whether an entry found while walking an upload should be
// read and archived as a regular file. Besides regular files that includes a
// file whose only type bit is ModeIrregular. That is how Windows reports every
// reparse point it has no more specific mode for (os/types_windows.go): OneDrive
// and other Cloud Files placeholders, WOF-compressed system files and HSM stubs
// among them. They read like any file (opening a placeholder downloads it), so
// skipping them dropped files from a directory upload in a OneDrive-backed
// Documents or Desktop folder without a word. One that cannot be read (an app
// execution alias, a junction) is still left out. Other systems do not report
// ordinary files as ModeIrregular.
func uploadable(m fs.FileMode) bool {
	return m.IsRegular() || m.Type() == fs.ModeIrregular
}

// extractTar reads the archive that `tar -cf - -C <dir> <srcBase>` produced in the
// pod and writes its regular files and directories to disk; a directory is
// created even when empty, as kubectl cp does. Whether the copy is a single
// file or a tree is decided from the archive, not from dst: srcBase is a tree when
// it arrives as a directory entry or has entries beneath it. Where it lands then
// depends on dst:
//
//   - dst is an existing directory: entries are placed beneath it by their archive
//     names, so the copy lands at dst/<srcBase>.
//   - dst does not exist: a file is written to dst itself, and a tree becomes dst,
//     with the leading srcBase stripped from every name, as kubectl cp does.
//   - dst is an existing file: a file overwrites it; a tree is refused before
//     anything is written, so the file is never truncated.
//
// dst alone cannot decide it: a tree copied to a path that does not exist yet
// looks like a single file, and every entry would be written to dst itself, each
// truncating the last, leaving one file behind a success toast.
//
// Entry names are sanitized so a malicious archive cannot escape the destination
// via ".." or an absolute path (the classic tar-slip guard), and an entry that is
// not srcBase or beneath it is rejected as a corrupted archive, as kubectl does.
// When goos is "windows", names taken from the archive are also made valid
// Windows file names (see resolveExtractTarget); dst itself, which the user
// typed, is never altered.
func extractTar(r io.Reader, dst, srcBase, goos string) (Result, error) {
	fi, statErr := os.Stat(dst)
	dstIsDir := statErr == nil && fi.IsDir()
	dstIsFile := statErr == nil && !fi.IsDir()

	tr := tar.NewReader(r)
	shape := shapeUnknown
	var res Result
	wrote := false
	// Windows file names ignore case, so two distinct Linux names can be one file
	// there and the later write silently replaces the earlier. Remember which
	// entry each case-folded target came from to tell the user when that happens.
	// The same name appearing twice is tar's own "later replaces earlier", not a
	// clash.
	var written map[string]string
	if goos == "windows" {
		written = map[string]string{}
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return res, fmt.Errorf("read tar: %w", err)
		}
		isDir := hdr.Typeflag == tar.TypeDir
		if !isDir && hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA { //nolint:staticcheck // TypeRegA kept for pre-POSIX archives
			continue
		}

		rel, err := archiveRel(hdr.Name, srcBase)
		if err != nil {
			return res, err
		}
		entryShape := shapeFile
		if isDir || rel != "" {
			entryShape = shapeTree
		}
		switch {
		case shape == shapeUnknown:
			shape = entryShape
		case shape != entryShape:
			return res, fmt.Errorf("archive from pod has both a file and a directory named %q", srcBase)
		}
		if shape == shapeTree && dstIsFile {
			return res, fmt.Errorf("destination %q is a file but %q is a directory; choose a directory or a new path", dst, srcBase)
		}

		// A tree copied to a new path becomes dst, so its srcBase entry is dst
		// itself; a file copied anywhere but into a directory is written to dst.
		target, renamed := dst, false
		switch {
		case dstIsDir:
			target, renamed, err = resolveExtractTarget(dst, path.Join(srcBase, rel), goos)
		case shape == shapeTree && rel != "":
			target, renamed, err = resolveExtractTarget(dst, rel, goos)
		}
		if err != nil {
			return res, err
		}
		if isDir {
			// The parent dirs of each file are created as needed as well, so this
			// only adds the directories that hold no files.
			if err := os.MkdirAll(target, 0o755); err != nil {
				return res, fmt.Errorf("create dir %q: %w", target, err)
			}
			wrote = true
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return res, fmt.Errorf("create dir for %q: %w", target, err)
		}
		if goos == "windows" {
			clearReadOnly(target)
		}
		if err := writeExtractedFile(target, extractMode(hdr.Mode, goos), tr); err != nil {
			return res, err
		}
		wrote = true
		if renamed {
			res.Renamed++
		}
		if written != nil {
			name, key := path.Join(srcBase, rel), strings.ToUpper(target)
			if prev, ok := written[key]; ok && prev != name {
				res.Overwritten = append(res.Overwritten, name)
			}
			written[key] = name
		}
	}
	if !wrote {
		return res, fmt.Errorf("archive from pod was empty")
	}
	return res, nil
}

// archiveShape is what a download turned out to be, learned from its first entry.
type archiveShape int

const (
	shapeUnknown archiveShape = iota
	shapeFile                 // srcBase is a single regular file
	shapeTree                 // srcBase is a directory
)

// archiveRel cleans a tar entry name and returns it relative to the archive root
// srcBase: "" for srcBase itself, "a/b" for "srcBase/a/b". Cleaning forces the
// name absolute before stripping the leading slash, so ".." can never climb above
// the root; a name that is then neither srcBase nor beneath it is an error.
func archiveRel(name, srcBase string) (string, error) {
	clean := path.Clean("/" + filepath.ToSlash(name)) // force-absolute, then strip the leading slash
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" {
		return "", fmt.Errorf("tar entry has empty name")
	}
	if clean == srcBase {
		return "", nil
	}
	if rest, ok := strings.CutPrefix(clean, srcBase+"/"); ok {
		return rest, nil
	}
	return "", fmt.Errorf("tar entry %q is not under %q; the archive from the pod looks corrupted", name, srcBase)
}

// resolveExtractTarget maps a slash-separated, archive-relative path to a path
// under dst and rejects any that would escape dst (absolute paths or ".."
// traversal).
//
// When goos is "windows" every component is first passed through WindowsFileName,
// and renamed reports whether that changed anything. A Linux name is not
// necessarily a Windows one: a ':' would put the data in a hidden alternate data
// stream, '?' or '*' would abort the extraction part-way, and "con" or "nul"
// would open a device. filepath.Localize then turns the result into an OS path;
// on Windows it rejects ':', '\' and reserved names itself, so it is a final
// guard that nothing WindowsFileName missed reaches the disk. Other systems take
// the name as it is, so their behaviour is unchanged.
func resolveExtractTarget(dst, rel, goos string) (target string, renamed bool, err error) {
	local := filepath.FromSlash(rel)
	if goos == "windows" {
		var name string
		name, renamed = windowsEntryPath(rel)
		if local, err = filepath.Localize(name); err != nil {
			return "", false, fmt.Errorf("tar entry %q cannot be written on Windows: %w", rel, err)
		}
	}
	target = filepath.Join(dst, local)
	// Defense in depth: archiveRel and Join+Clean already collapse "..", but verify
	// the result is still contained in dst before touching the filesystem.
	r, err := filepath.Rel(dst, target)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("tar entry %q escapes destination directory", rel)
	}
	return target, renamed, nil
}

// extractMode is the permission a downloaded file is created with: the pod's own
// bits, except that on Windows the owner-write bit is always kept. The only
// permission a Windows file carries is the read-only attribute, which Go sets
// whenever that bit is clear (syscall.Open), so a pod file at 0400 or 0444 (a
// Secret mounted with defaultMode 0400, a 0444 file shipped in the image) would
// arrive read-only, and the next download over it would fail with "Access is
// denied", aborting a directory part-way. kubectl cp never sets download modes at
// all (os.Create). Other systems keep the exact bits, as before.
func extractMode(hdrMode int64, goos string) os.FileMode {
	mode := os.FileMode(hdrMode).Perm()
	if goos == "windows" {
		mode |= 0o200
	}
	return mode
}

// clearReadOnly makes an existing file at target owner-writable again so a
// download can replace one that an earlier download left read-only. On Windows
// os.Chmod only toggles FILE_ATTRIBUTE_READONLY (syscall.Chmod), which is exactly
// what blocks the overwrite. A file counts whether it is regular or, like the
// files OneDrive syncs, a placeholder Go reports as ModeIrregular (see
// uploadable); links, devices and directories are left alone. A failure is left
// for the open that follows to report.
func clearReadOnly(target string) {
	fi, err := os.Lstat(target)
	if err != nil || !uploadable(fi.Mode()) || fi.Mode().Perm()&0o200 != 0 {
		return
	}
	_ = os.Chmod(target, fi.Mode().Perm()|0o200)
}

// writeExtractedFile creates target with mode and copies the tar entry body into
// it, closing the file before returning any error.
func writeExtractedFile(target string, mode os.FileMode, r io.Reader) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create %q: %w", target, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("write %q: %w", target, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q: %w", target, err)
	}
	return nil
}

// toSlashDir returns the slash-separated directory portion of a container path.
// Container paths are POSIX even when kubetui runs on Windows, so we normalize to
// slashes and use path (not filepath) semantics.
func toSlashDir(p string) string {
	dir := path.Dir(filepath.ToSlash(strings.TrimRight(p, "/")))
	if dir == "" {
		return "."
	}
	return dir
}

// wrapTarErr adds the container's stderr (if any) to an exec error so a missing
// `tar`, a bad path, or a permission problem surfaces the container's own message
// rather than a bare stream error.
func wrapTarErr(op string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return fmt.Errorf("%s failed: %v: %s", op, err, stderr)
	}
	if strings.Contains(err.Error(), "exit code 127") {
		return fmt.Errorf("%s failed: 'tar' not found in container — the image must include tar (like `kubectl cp`): %w", op, err)
	}
	return fmt.Errorf("%s failed: %w", op, err)
}

// execStream runs cmd in the target container and wires the supplied stream
// options. Stdin is enabled only when opts.Stdin is non-nil (upload); TTY is never
// used because tar's binary framing must not pass through a pseudo-terminal.
func execStream(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, ns, pod, container string, cmd []string, opts remotecommand.StreamOptions) error {
	req := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(ns).
		Name(pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := newFallbackExecutor(cfg, req.URL())
	if err != nil {
		return err
	}
	return exec.StreamWithContext(ctx, opts)
}

// newFallbackExecutor builds a WebSocket-primary, SPDY-secondary executor,
// mirroring kubectl's exec/cp. Duplicated from the execshell package so this
// backend has no cross-package dependency on TUI code.
func newFallbackExecutor(cfg *rest.Config, u *url.URL) (remotecommand.Executor, error) {
	ws, err := remotecommand.NewWebSocketExecutor(cfg, "GET", u.String())
	if err != nil {
		return nil, err
	}
	spdy, err := remotecommand.NewSPDYExecutor(cfg, "POST", u)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
}
