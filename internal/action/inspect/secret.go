package inspect

// secret.go keeps a Secret's values out of the text kubetui renders for it. The
// yaml view and the edit buffer show each data value as a placeholder that names
// its size and nothing else. An edit gets the real values back from memory just
// before it is applied, so they are never written to the editor's temp file. The
// reveal drill-in (enter on a secret row) reads the real values straight from
// the cache and shows them only on demand.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// isSecret reports whether obj is a core v1 Secret: typed, as the informer cache
// holds one, or unstructured, as the dynamic client returns one. Only group ""
// counts. A custom resource that happens to be called Secret has a schema of its
// own, so its fields mean something else and are left alone.
func isSecret(obj runtime.Object) bool {
	switch o := obj.(type) {
	case *corev1.Secret:
		return true
	case *unstructured.Unstructured:
		gvk := o.GroupVersionKind()
		return gvk.Group == "" && gvk.Kind == "Secret"
	}
	return false
}

// secretYAML renders a Secret, already deep-copied, with its values masked, and
// returns what it masked. It goes through the object's map form. A typed
// Secret's data is map[string][]byte, which YAML encodes as base64, so a
// placeholder set on the typed object showed as the base64 of its own text: a
// value that looks real and would be applied as the new value if the buffer
// were saved. In the map form each value is a string and the placeholder shows
// as itself. sigs.k8s.io/yaml routes both forms through JSON, so the rest of
// the text is what the typed object renders.
func secretYAML(obj runtime.Object) (string, *secretValues, error) {
	m, err := toMap(obj)
	if err != nil {
		return "", nil, err
	}
	masked := maskSecret(m)
	b, err := yaml.Marshal(m)
	if err != nil {
		return "", nil, err
	}
	return string(b), masked, nil
}

// toMap returns obj's map form. An unstructured object already is one, and the
// caller's copy is used as it is.
func toMap(obj runtime.Object) (map[string]any, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u.Object, nil
	}
	return runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
}

// placeholder is what a Secret value of n bytes shows as. It is plain text, not
// base64, so it reads as what it is. '<', ':' and ' ' are outside the base64
// alphabet, so the API server rejects it as a data value if one is ever sent
// back.
func placeholder(n int) string { return fmt.Sprintf("<redacted: %d bytes>", n) }

// placeholderRE matches any text placeholder can produce.
var placeholderRE = regexp.MustCompile(`^<redacted: [0-9]+ bytes>$`)

// secretValues is what maskSecret took out of a Secret: each data value as the
// API carries it (base64), with the placeholder that replaced it, and the
// resourceVersion they were read at. It is held in memory only, for as long as
// the edit that needs it.
type secretValues struct {
	resourceVersion string
	data            map[string]maskedValue
}

// maskedValue is one masked data entry.
type maskedValue struct {
	encoded     string // the original value, base64
	placeholder string // the text written in its place
}

// maskSecret masks the values in a Secret's map form, in place, and returns
// them. Each data value becomes placeholder(its size). stringData is dropped:
// the API server never returns it, and a placeholder there would be stored as
// the literal text if applied. The last-applied annotation is dropped too,
// because client-side kubectl apply keeps every value in it again. The engine
// already strips that annotation from the cache, so this matters for a Secret
// read straight from the API.
func maskSecret(m map[string]any) *secretValues {
	masked := &secretValues{data: map[string]maskedValue{}}
	delete(m, "stringData")
	if md, ok := m["metadata"].(map[string]any); ok {
		masked.resourceVersion, _ = md["resourceVersion"].(string)
		if ann, ok := md["annotations"].(map[string]any); ok {
			delete(ann, corev1.LastAppliedConfigAnnotation)
			if len(ann) == 0 {
				delete(md, "annotations")
			}
		}
	}
	data, _ := m["data"].(map[string]any)
	for k, v := range data {
		if enc, ok := v.(string); ok {
			ph := placeholder(decodedLen(enc))
			data[k] = ph
			masked.data[k] = maskedValue{encoded: enc, placeholder: ph}
		}
	}
	return masked
}

// decodedLen is the size of the value a base64 data entry holds. The API server
// only returns valid base64. Anything else is sized as if it were valid, since
// the size is only a label.
func decodedLen(enc string) int {
	if b, err := base64.StdEncoding.DecodeString(enc); err == nil {
		return len(b)
	}
	return base64.StdEncoding.DecodedLen(len(enc))
}

// Restore takes an edited object, as the JSON the YAML buffer converts to, and
// returns the JSON to apply in its place, or an error if it must not be applied.
type Restore func(edited []byte) ([]byte, error)

// restore is the Restore for a masked Secret. A data value that is still the
// placeholder written for its key gets the original value back. Any other value
// is the user's and is sent as typed: a changed value is applied, a deleted key
// is left out, and a new key with real base64 is added. stringData, which the
// server merges into data as plain text, passes through.
//
// A placeholder that cannot be put back is an error, and nothing is applied.
// That covers a placeholder under a key that had no value (a renamed or new
// key), one that is not the placeholder its key was written with (two swapped,
// or a size retyped), and one moved into stringData, where it would be stored
// as the literal text. Errors name the first such key in sorted order.
//
// When any value is put back, metadata.resourceVersion is set to the version
// the values were read at, even if the line was deleted from the buffer. A
// server-side apply that carries a resourceVersion is refused with a conflict
// unless it matches the stored object, so a restored value can never be
// written over one that changed since the edit began. When nothing is put
// back, nothing stale is sent and the JSON goes as it is.
func (v *secretValues) restore(edited []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(edited))
	dec.UseNumber() // keep integers exact instead of rounding them through float64
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil {
		// Not an object (an emptied buffer is null): there is nothing to put
		// back, and the server rejects it with its own message.
		return edited, nil
	}

	if sd, ok := obj["stringData"].(map[string]any); ok {
		for _, k := range slices.Sorted(maps.Keys(sd)) {
			if s, ok := sd[k].(string); ok && placeholderRE.MatchString(s) {
				return nil, fmt.Errorf("stringData.%s is a redacted placeholder — enter the plain value", k)
			}
		}
	}

	data, _ := obj["data"].(map[string]any)
	restored := false
	for _, k := range slices.Sorted(maps.Keys(data)) {
		s, ok := data[k].(string)
		if !ok || !placeholderRE.MatchString(s) {
			continue // the user's own value
		}
		orig, had := v.data[k]
		switch {
		case !had:
			return nil, fmt.Errorf("data.%s is still redacted — enter a base64 value, or use stringData", k)
		case s != orig.placeholder:
			return nil, fmt.Errorf("data.%s has a placeholder that is not its own — enter a base64 value, or use stringData", k)
		}
		data[k] = orig.encoded
		restored = true
	}
	if !restored {
		return edited, nil
	}

	if v.resourceVersion != "" {
		md, _ := obj["metadata"].(map[string]any)
		if md == nil {
			md = map[string]any{}
			obj["metadata"] = md
		}
		md["resourceVersion"] = v.resourceVersion
	}
	return json.Marshal(obj)
}
