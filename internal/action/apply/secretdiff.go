package apply

// secretdiff.go keeps a Secret's values out of the :apply preview. The preview
// renders the live object, read straight from the API, and the dry-run result
// as YAML, and prints every unchanged line as context. For a Secret that put
// every data value on screen as base64, however small the change, and the
// last-applied annotation client-side kubectl apply leaves, which repeats the
// applied manifest, stringData in plain text included.

import (
	"encoding/base64"
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// isCoreSecret reports whether obj is a core v1 Secret. A custom resource that
// happens to be called Secret has a schema of its own and is left alone.
func isCoreSecret(obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}
	gvk := obj.GroupVersionKind()
	return gvk.Group == "" && gvk.Kind == "Secret"
}

// maskSecretDiff returns copies of a live and proposed Secret with each value
// shown as its size: every data and stringData value, and the last-applied
// annotation. A value on the proposed side that differs from the live one is
// marked changed. Without the mark a change that keeps a value's size would
// render the same line on both sides, the diff would come out empty, and the
// preview would report "no changes to apply" and apply nothing. The mark says
// only that the value changed, which the manifest being applied already says.
// Objects that are not core Secrets are returned as they are. live may be nil,
// for a Secret not yet created.
func maskSecretDiff(live, proposed *unstructured.Unstructured) (*unstructured.Unstructured, *unstructured.Unstructured) {
	if !isCoreSecret(proposed) && !isCoreSecret(live) {
		return live, proposed
	}
	var lo, po map[string]any
	if live != nil {
		live = live.DeepCopy()
		lo = live.Object
	}
	if proposed != nil {
		proposed = proposed.DeepCopy()
		po = proposed.Object
	}
	maskPair(stringMap(lo, "data"), stringMap(po, "data"), "", decodedSize)
	maskPair(stringMap(lo, "stringData"), stringMap(po, "stringData"), "", textSize)
	maskPair(stringMap(lo, "metadata", "annotations"), stringMap(po, "metadata", "annotations"), corev1.LastAppliedConfigAnnotation, textSize)
	return live, proposed
}

// maskPair replaces each string value in live and proposed with redacted(its
// size), marking a proposed value that differs from the live one. If only is
// set, only that key is masked. Either map may be nil.
func maskPair(live, proposed map[string]any, only string, size func(string) int) {
	before := maps.Clone(live)
	for k, v := range live {
		if s, ok := v.(string); ok && (only == "" || k == only) {
			live[k] = redacted(size(s), false)
		}
	}
	for k, v := range proposed {
		if s, ok := v.(string); ok && (only == "" || k == only) {
			old, had := before[k]
			proposed[k] = redacted(size(s), had && old != s)
		}
	}
}

// redacted is how the preview shows a value of n bytes. It matches the yaml
// view's placeholder in form, without the key, since the preview is never
// edited and applied back.
func redacted(n int, changed bool) string {
	if changed {
		return fmt.Sprintf("<redacted: %d bytes, changed>", n)
	}
	return fmt.Sprintf("<redacted: %d bytes>", n)
}

// stringMap returns the map at path in obj, or nil if there is none.
func stringMap(obj map[string]any, path ...string) map[string]any {
	if obj == nil {
		return nil
	}
	v, ok, _ := unstructured.NestedFieldNoCopy(obj, path...)
	if !ok {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

// decodedSize is the size of the value a base64 data entry holds. Anything
// that is not valid base64 is sized as if it were, since the size is a label.
func decodedSize(enc string) int {
	if b, err := base64.StdEncoding.DecodeString(enc); err == nil {
		return len(b)
	}
	return base64.StdEncoding.DecodedLen(len(enc))
}

// textSize is the size of a plain-text value.
func textSize(s string) int { return len(s) }
