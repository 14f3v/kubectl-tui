package inspect

// secret.go keeps a Secret's values out of the text kubetui renders for it. The
// yaml view shows each data value as a placeholder that names its size and
// nothing else. The reveal drill-in (enter on a secret row) reads the real
// values straight from the cache and shows them only on demand.

import (
	"encoding/base64"
	"fmt"

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

// secretYAML renders a Secret, already deep-copied, with its values masked. It
// goes through the object's map form. A typed Secret's data is
// map[string][]byte, which YAML encodes as base64, so a placeholder set on the
// typed object showed as the base64 of its own text: a value that looks real
// and would be applied as the new value if the buffer were saved. In the map
// form each value is a string and the placeholder shows as itself.
// sigs.k8s.io/yaml routes both forms through JSON, so the rest of the text is
// what the typed object renders.
func secretYAML(obj runtime.Object) (string, error) {
	m, err := toMap(obj)
	if err != nil {
		return "", err
	}
	maskSecret(m)
	b, err := yaml.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
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

// maskSecret masks the values in a Secret's map form, in place. Each data value
// becomes placeholder(its size). stringData is dropped: the API server never
// returns it, and a placeholder there would be stored as the literal text if
// applied. The last-applied annotation is dropped too, because client-side
// kubectl apply keeps every value in it again. The engine already strips that
// annotation from the cache, so this matters for a Secret read straight from
// the API.
func maskSecret(m map[string]any) {
	delete(m, "stringData")
	if md, ok := m["metadata"].(map[string]any); ok {
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
			data[k] = placeholder(decodedLen(enc))
		}
	}
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
