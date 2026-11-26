package inspect

import (
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// secretValuesUnderTest are the values every fixture Secret holds. None of them,
// raw or base64, may appear in anything rendered for the Secret.
var secretValuesUnderTest = map[string]string{
	"password": "hunter2",
	"token":    "s3cr3t-token-value",
	"tls.crt":  "-----BEGIN CERTIFICATE-----",
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// lastApplied is what client-side kubectl apply leaves on a Secret: the applied
// object again, values and all.
func lastApplied() string {
	return `{"apiVersion":"v1","data":{"password":"` + b64("hunter2") + `"},"kind":"Secret"}`
}

func typedSecret() *corev1.Secret {
	data := map[string][]byte{}
	for k, v := range secretValuesUnderTest {
		data[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "db", Namespace: "demo", ResourceVersion: "41", UID: "u1",
			Labels:      map[string]string{"app": "db"},
			Annotations: map[string]string{"team": "core", corev1.LastAppliedConfigAnnotation: lastApplied()},
		},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
		StringData: map[string]string{"plain": "hunter2"},
	}
}

// unstructuredSecret is the same Secret as the dynamic client returns it: data
// as base64 strings, apiVersion/kind set.
func unstructuredSecret() *unstructured.Unstructured {
	data := map[string]any{}
	for k, v := range secretValuesUnderTest {
		data[k] = b64(v)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name": "db", "namespace": "demo", "resourceVersion": "41", "uid": "u1",
			"labels":      map[string]any{"app": "db"},
			"annotations": map[string]any{"team": "core", corev1.LastAppliedConfigAnnotation: lastApplied()},
		},
		"type":       "Opaque",
		"data":       data,
		"stringData": map[string]any{"plain": "hunter2"},
	}}
}

// assertNoValues fails if any fixture value, raw or base64, shows in out.
func assertNoValues(t *testing.T, out string) {
	t.Helper()
	for k, v := range secretValuesUnderTest {
		if strings.Contains(out, v) || strings.Contains(out, b64(v)) {
			t.Errorf("the value of %s leaked into the rendered text:\n%s", k, out)
		}
	}
	if strings.Contains(out, "last-applied-configuration") {
		t.Errorf("the last-applied annotation, which repeats the values, was rendered:\n%s", out)
	}
}

func TestYAML_MasksSecretValues(t *testing.T) {
	for name, obj := range map[string]runtime.Object{
		"typed, from the cache":                 typedSecret(),
		"unstructured, from the dynamic client": unstructuredSecret(),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := YAML(obj)
			if err != nil {
				t.Fatal(err)
			}
			assertNoValues(t, out)
			for _, want := range []string{
				"apiVersion: v1", "kind: Secret", "name: db", "resourceVersion: \"41\"", "app: db", "team: core", "type: Opaque",
				// Readable placeholders, not the base64 of one.
				"password: '<redacted: 7 bytes>'",
				"token: '<redacted: 18 bytes>'",
				"tls.crt: '<redacted: 27 bytes>'",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("yaml missing %q:\n%s", want, out)
				}
			}
			// "<redacted" is 9 bytes, so its base64 starts the base64 of every
			// placeholder.
			if strings.Contains(out, b64("<redacted")) {
				t.Errorf("a placeholder was rendered as base64:\n%s", out)
			}
			if strings.Contains(out, "stringData") {
				t.Errorf("stringData was rendered:\n%s", out)
			}
		})
	}
}

// Masking works on a deep copy: the cached object keeps its values, its
// annotations and its empty TypeMeta.
func TestYAML_SecretSourceIsNotMutated(t *testing.T) {
	s := typedSecret()
	if _, err := YAML(s); err != nil {
		t.Fatal(err)
	}
	if string(s.Data["password"]) != "hunter2" || s.StringData["plain"] != "hunter2" {
		t.Errorf("YAML mutated the cached Secret's values: %q", s.Data)
	}
	if _, ok := s.Annotations[corev1.LastAppliedConfigAnnotation]; !ok || s.Kind != "" {
		t.Errorf("YAML mutated the cached Secret's metadata: %v kind=%q", s.Annotations, s.Kind)
	}

	u := unstructuredSecret()
	if _, err := YAML(u); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := unstructured.NestedString(u.Object, "data", "password"); got != b64("hunter2") {
		t.Errorf("YAML mutated the unstructured Secret's data: password=%q", got)
	}
}

func TestYAML_SecretWithoutData(t *testing.T) {
	for name, data := range map[string]map[string][]byte{"nil data": nil, "empty data": {}} {
		t.Run(name, func(t *testing.T) {
			s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "demo"}, Data: data}
			out, err := YAML(s)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "kind: Secret") || !strings.Contains(out, "name: empty") || strings.Contains(out, "redacted") {
				t.Errorf("yaml for a Secret without data:\n%s", out)
			}
		})
	}
}

// Only a core Secret is masked. A ConfigMap's values are not secret, and a custom
// resource called Secret has a schema of its own, so both render as they are,
// even where a value happens to look like a placeholder.
func TestYAML_LeavesOtherKindsAlone(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "demo"},
		Data:       map[string]string{"motd": "hello", "note": "<redacted: 3 bytes>"},
	}
	out, err := YAML(cm)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "motd: hello") || !strings.Contains(out, "note: '<redacted: 3 bytes>'") {
		t.Errorf("ConfigMap yaml changed:\n%s", out)
	}

	custom := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "vault.example.com/v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "ref", "namespace": "demo"},
		"data":       map[string]any{"path": "kv/db"},
	}}
	out, err = YAML(custom)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "path: kv/db") {
		t.Errorf("a custom kind named Secret was masked:\n%s", out)
	}
}
