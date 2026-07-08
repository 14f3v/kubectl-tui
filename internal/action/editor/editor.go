// Package editor implements the edit action: dump an object to a temp YAML file,
// hand it to $EDITOR, and apply the result with server-side apply — unless the
// file is byte-identical to what was opened, in which case it is a no-op.
package editor

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// WriteTemp writes content to a temp .yaml file and returns its path and the
// SHA-256 of the original content (for the unchanged-file no-op check).
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
	return f.Name(), sha256.Sum256([]byte(content)), nil
}

// Process builds the $EDITOR command for a file. It honors KUBE_EDITOR, then
// EDITOR, falling back to vi, and splits a multi-word editor into program+args.
// Bubble Tea wires stdin/stdout/stderr, so they are left unset here.
func Process(path string) *exec.Cmd {
	ed := os.Getenv("KUBE_EDITOR")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "vi"
	}
	parts := strings.Fields(ed)
	args := append(parts[1:], path)
	return exec.Command(parts[0], args...)
}

// Apply reads the edited file, and if it differs from the original applies it
// with server-side apply (field manager "kubetui"). It reports whether a change
// was applied. The caller is responsible for removing the temp file.
func Apply(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespaced bool, namespace, name, path string, origSum [32]byte) (changed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if sha256.Sum256(data) == origSum {
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
