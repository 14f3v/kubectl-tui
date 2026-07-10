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
	"net/url"
	"os"
	"path"
	"path/filepath"
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

// CopyToPod uploads a local file or directory tree to dstRemote inside the
// container. It builds a tar archive whose top-level entry name is the basename of
// dstRemote, then unpacks it with `tar -xmf - -C <dir(dstRemote)>` so the content
// lands exactly at dstRemote. An empty container targets the pod's first container
// (resolved server-side). It blocks until the transfer completes.
func CopyToPod(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, ns, pod, container, srcLocal, dstRemote string) error {
	info, err := os.Stat(srcLocal)
	if err != nil {
		return fmt.Errorf("cannot read local source %q: %w", srcLocal, err)
	}

	// The archive is named by the destination's basename and extracted into the
	// destination's parent directory, so a file "a.txt" copied to "/etc/b.txt" lands
	// as "/etc/b.txt" regardless of the source name — matching `kubectl cp`.
	destDir := toSlashDir(dstRemote)
	destBase := path.Base(filepath.ToSlash(strings.TrimRight(dstRemote, "/")))

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if info.IsDir() {
		if err := writeTarDir(tw, srcLocal, destBase); err != nil {
			tw.Close()
			return err
		}
	} else {
		data, rerr := os.ReadFile(srcLocal)
		if rerr != nil {
			tw.Close()
			return fmt.Errorf("cannot read %q: %w", srcLocal, rerr)
		}
		if werr := writeTarFile(tw, destBase, int64(info.Mode().Perm()), data); werr != nil {
			tw.Close()
			return werr
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("finalize tar: %w", err)
	}

	cmd := []string{"tar", "-xmf", "-", "-C", destDir}
	var stderr bytes.Buffer
	err = execStream(ctx, cfg, cs, ns, pod, container, cmd, remotecommand.StreamOptions{
		Stdin:  &buf,
		Stdout: io.Discard,
		Stderr: &stderr,
	})
	if err != nil {
		return wrapTarErr("upload", err, stderr.String())
	}
	return nil
}

// CopyFromPod downloads srcRemote from the container into dstLocal. It runs
// `tar -cf - -C <dir(srcRemote)> <base(srcRemote)>` in the pod, reads the tar off
// stdout, and extracts it under dstLocal. When dstLocal is an existing directory
// the entries are written beneath it using their in-archive names; otherwise a
// single-file archive is written directly to dstLocal. An empty container targets
// the pod's first container. It blocks until the transfer completes.
func CopyFromPod(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, ns, pod, container, srcRemote, dstLocal string) error {
	srcDir := toSlashDir(srcRemote)
	srcBase := path.Base(filepath.ToSlash(strings.TrimRight(srcRemote, "/")))
	if srcBase == "" || srcBase == "." || srcBase == "/" {
		return fmt.Errorf("remote source %q has no file name to copy", srcRemote)
	}

	cmd := []string{"tar", "-cf", "-", "-C", srcDir, srcBase}
	var stdout, stderr bytes.Buffer
	err := execStream(ctx, cfg, cs, ns, pod, container, cmd, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return wrapTarErr("download", err, stderr.String())
	}

	return extractTar(&stdout, dstLocal)
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
// under prefix (the archive-relative name of the directory root). Non-regular
// files (symlinks, sockets, devices) are skipped, mirroring the conservative
// subset kubectl cp reliably handles across container tar implementations.
func writeTarDir(tw *tar.Writer, root, prefix string) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		// tar paths are always slash-separated regardless of the host OS.
		name := path.Join(prefix, filepath.ToSlash(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("cannot read %q: %w", p, err)
		}
		return writeTarFile(tw, name, int64(info.Mode().Perm()), data)
	})
}

// extractTar reads a tar stream and writes its regular-file entries to disk. If
// dst is an existing directory the entries are placed beneath it by their archive
// names; otherwise the (single) entry is written to dst directly. Entry names are
// sanitized so a malicious archive cannot escape the destination via ".." or an
// absolute path (the classic tar-slip guard).
func extractTar(r io.Reader, dst string) error {
	dstIsDir := false
	if fi, err := os.Stat(dst); err == nil && fi.IsDir() {
		dstIsDir = true
	}

	tr := tar.NewReader(r)
	wrote := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		// Skip directory markers; parent dirs are created for each file as needed.
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA { //nolint:staticcheck // TypeRegA kept for pre-POSIX archives
			continue
		}

		target, err := resolveExtractTarget(dst, dstIsDir, hdr.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create dir for %q: %w", target, err)
		}
		if err := writeExtractedFile(target, os.FileMode(hdr.Mode).Perm(), tr); err != nil {
			return err
		}
		wrote = true
	}
	if !wrote {
		return fmt.Errorf("archive from pod was empty")
	}
	return nil
}

// resolveExtractTarget maps a tar entry name to a safe on-disk path under dst and
// rejects any name that would escape dst (absolute paths or ".." traversal).
func resolveExtractTarget(dst string, dstIsDir bool, name string) (string, error) {
	clean := path.Clean("/" + filepath.ToSlash(name)) // force-absolute, then strip the leading slash
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" {
		return "", fmt.Errorf("tar entry has empty name")
	}

	if !dstIsDir {
		// Single-file destination: ignore the archive's internal path and write to dst.
		return dst, nil
	}

	target := filepath.Join(dst, filepath.FromSlash(clean))
	// Defense in depth: Join+Clean already collapse "..", but verify the result is
	// still contained in dst before touching the filesystem.
	rel, err := filepath.Rel(dst, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tar entry %q escapes destination directory", name)
	}
	return target, nil
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
