// Package editor implements the edit action: dump an object to a temp YAML file,
// hand it to $EDITOR, and apply the result with server-side apply — unless the
// file is unchanged from what was opened (line endings aside), in which case it
// is a no-op.
package editor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"runtime"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// WriteTemp writes content to a temp .yaml file and returns its path and the
// digest of the original content, for Unchanged.
func WriteTemp(content string) (path string, sum [32]byte, err error) {
	f, err := os.CreateTemp("", "kubetui-*.yaml")
	if err != nil {
		return "", [32]byte{}, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", [32]byte{}, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", [32]byte{}, err
	}
	return f.Name(), digest([]byte(content)), nil
}

// Unchanged reports whether data, read back from the temp file, is still the
// content WriteTemp wrote (sum) — line endings aside. An editor that saves with
// CRLF, because it is set to or an .editorconfig asks it to, rewrites every line
// without the user touching one. The YAML parser reads CRLF and LF alike, so
// that is no change, not a server-side apply that sends the same object back
// and claims every field in it for kubetui.
func Unchanged(data []byte, sum [32]byte) bool {
	return digest(data) == sum
}

// digest is the SHA-256 behind Unchanged, taken with CRLF folded to LF.
func digest(b []byte) [32]byte {
	return sha256.Sum256(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
}

// Process builds the $EDITOR command for a file: the editor command picks from
// KUBE_EDITOR, EDITOR or the platform default, split into program and arguments
// with quotes honored, and the file as its last argument. Bubble Tea wires
// stdin/stdout/stderr, so they are left unset here.
func Process(path string) *exec.Cmd {
	argv := command(os.Getenv, runtime.GOOS, isAbsProgram)
	return exec.Command(argv[0], append(argv[1:], path)...)
}

// quickExit is how soon an editor must hand back an untouched file for kubetui to
// conclude it never waited for the user.
const quickExit = time.Second

// QuickExitHint is the toast for an editor that handed the file back untouched
// within quickExit of getting it, or "" if it took longer. Nobody reviews an
// object and quits that fast. What happens instead is a GUI editor passing the
// file to a window it already has open and exiting at once: code without --wait,
// subl without -w, Notepad++ without -multiInst. kubetui then reads the file back
// and removes it before the user has typed anything, and a bare "no changes"
// would hide why the edit went nowhere. A fast :q! in vi earns the hint too,
// which costs nothing.
//
// The example names KUBE_EDITOR because it wins over EDITOR, so it works
// whichever variable holds the editor that did not wait. The toast is a single
// footer line cut to the terminal width, so the hint stays short enough to fit
// 80 columns whole; past that, the example is the part that gets cut.
func QuickExitHint(elapsed time.Duration) string {
	if elapsed >= quickExit {
		return ""
	}
	return `editor exited at once — use a wait flag, e.g. KUBE_EDITOR="code --wait"`
}

// Apply reads the edited file, and if it differs from the original applies it
// with server-side apply (field manager "kubetui"). It reports whether a change
// was applied. The caller is responsible for removing the temp file.
func Apply(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespaced bool, namespace, name, path string, origSum [32]byte) (changed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if Unchanged(data, origSum) {
		return false, nil // unchanged — no-op
	}
	jsonBytes, err := yaml.YAMLToJSON(data)
	if err != nil {
		return true, err
	}
	opts := metav1.PatchOptions{FieldManager: "kubetui"}
	if namespaced {
		_, err = dyn.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.ApplyPatchType, jsonBytes, opts)
	} else {
		_, err = dyn.Resource(gvr).Patch(ctx, name, types.ApplyPatchType, jsonBytes, opts)
	}
	return true, err
}
