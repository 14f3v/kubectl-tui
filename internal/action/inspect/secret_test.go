package inspect

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
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
				// Readable placeholders, not the base64 of one, each naming
				// its key.
				"password: '<redacted: 7 bytes of password>'",
				"token: '<redacted: 18 bytes of token>'",
				"tls.crt: '<redacted: 27 bytes of tls.crt>'",
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

// editAndRestore renders obj for editing, changes the buffer text with edit as a
// user would, converts it to JSON as the edit action does, and runs the Restore.
// It returns the JSON before and after the Restore.
func editAndRestore(t *testing.T, obj runtime.Object, edit func(string) string) (in, out []byte, err error) {
	t.Helper()
	text, restore, err := EditYAML(obj)
	if err != nil {
		t.Fatalf("EditYAML: %v", err)
	}
	if restore == nil {
		t.Fatal("EditYAML returned no Restore for a Secret")
	}
	in, err = yaml.YAMLToJSON([]byte(edit(text)))
	if err != nil {
		t.Fatalf("edited buffer is not YAML: %v\n%s", err, edit(text))
	}
	out, err = restore(in)
	return in, out, err
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("restored JSON does not parse: %v\n%s", err, b)
	}
	return m
}

func TestRestore(t *testing.T) {
	// Placeholder lines exactly as the buffer holds them for typedSecret.
	const (
		password = "  password: '<redacted: 7 bytes of password>'\n"
		tlsCrt   = "  tls.crt: '<redacted: 27 bytes of tls.crt>'\n"
		token    = "  token: '<redacted: 18 bytes of token>'\n"
		rv       = "  resourceVersion: \"41\"\n"
	)
	original := map[string]any{}
	for k, v := range secretValuesUnderTest {
		original[k] = b64(v)
	}
	with := func(changes map[string]any) map[string]any {
		m := maps.Clone(original)
		for k, v := range changes {
			if v == nil {
				delete(m, k)
			} else {
				m[k] = v
			}
		}
		return m
	}
	replace := func(old, new string) func(string) string {
		return func(s string) string {
			if !strings.Contains(s, old) {
				panic("fixture text not in the buffer: " + old)
			}
			return strings.Replace(s, old, new, 1)
		}
	}

	cases := []struct {
		name           string
		edit           func(string) string
		wantData       map[string]any // nil: no data in the result
		wantStringData map[string]any
		wantRV         string
		wantErr        string
		sentAsTyped    bool // nothing restored, so the JSON must go through byte for byte
	}{
		{
			name:     "a label added keeps every value",
			edit:     replace("    app: db\n", "    app: db\n    tier: backend\n"),
			wantData: original, wantRV: "41",
		},
		{
			name:     "a changed value is sent as typed",
			edit:     replace(password, "  password: "+b64("correct-horse")+"\n"),
			wantData: with(map[string]any{"password": b64("correct-horse")}), wantRV: "41",
		},
		{
			name:     "a deleted key is left out",
			edit:     replace(tlsCrt, ""),
			wantData: with(map[string]any{"tls.crt": nil}), wantRV: "41",
		},
		{
			name:     "a new key with real base64 is added",
			edit:     replace("data:\n", "data:\n  extra: "+b64("more")+"\n"),
			wantData: with(map[string]any{"extra": b64("more")}), wantRV: "41",
		},
		{
			name:           "stringData added passes through",
			edit:           func(s string) string { return s + "stringData:\n  plain: hello\n" },
			wantData:       original,
			wantStringData: map[string]any{"plain": "hello"},
			wantRV:         "41",
		},
		{
			name:     "a deleted resourceVersion is put back",
			edit:     replace(rv, ""),
			wantData: original, wantRV: "41",
		},
		{
			name:     "a retyped resourceVersion is put back",
			edit:     replace(rv, "  resourceVersion: \"99\"\n"),
			wantData: original, wantRV: "41",
		},
		{
			name: "every value replaced is sent as typed",
			edit: func(s string) string {
				s = replace(password, "  password: "+b64("a")+"\n")(s)
				s = replace(tlsCrt, "  tls.crt: "+b64("b")+"\n")(s)
				s = replace(token, "  token: "+b64("c")+"\n")(s)
				return replace(rv, "")(s)
			},
			wantData:    map[string]any{"password": b64("a"), "tls.crt": b64("b"), "token": b64("c")},
			sentAsTyped: true,
		},
		{
			name:        "data removed altogether is sent as typed",
			edit:        replace("data:\n"+password+tlsCrt+token, ""),
			wantRV:      "41",
			sentAsTyped: true,
		},
		{
			name:    "a renamed key that kept its placeholder",
			edit:    replace("  password:", "  passwd:"),
			wantErr: "data.passwd is still redacted — set base64 or stringData",
		},
		{
			name:    "a new key with a copy of a placeholder",
			edit:    replace("data:\n", "data:\n  extra: '<redacted: 7 bytes of password>'\n"),
			wantErr: "data.extra is still redacted",
		},
		{
			name:    "a placeholder with its size retyped",
			edit:    replace("<redacted: 7 bytes of password>", "<redacted: 8 bytes of password>"),
			wantErr: "data.password has a wrong placeholder — set base64 or stringData",
		},
		{
			name:    "a placeholder with its key retyped",
			edit:    replace("<redacted: 7 bytes of password>", "<redacted: 7 bytes of passwd>"),
			wantErr: "data.password has a wrong placeholder",
		},
		{
			name:    "the older placeholder form, without the key",
			edit:    replace("<redacted: 7 bytes of password>", "<redacted: 7 bytes>"),
			wantErr: "data.password has a wrong placeholder",
		},
		{
			name: "two placeholders swapped",
			edit: func(s string) string {
				s = replace("<redacted: 7 bytes of password>", "<swap>")(s)
				s = replace("<redacted: 18 bytes of token>", "<redacted: 7 bytes of password>")(s)
				return replace("<swap>", "<redacted: 18 bytes of token>")(s)
			},
			wantErr: "data.password has a wrong placeholder",
		},
		{
			name: "a placeholder moved into stringData",
			edit: func(s string) string {
				return replace(password, "")(s) + "stringData:\n  password: '<redacted: 7 bytes of password>'\n"
			},
			wantErr: "stringData.password is a placeholder — enter the plain value",
		},
		{
			name: "the older placeholder form in stringData",
			edit: func(s string) string {
				return s + "stringData:\n  password: '<redacted: 7 bytes>'\n"
			},
			wantErr: "stringData.password is a placeholder",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, out, err := editAndRestore(t, typedSecret(), c.edit)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("Restore error = %v, want %q", err, c.wantErr)
				}
				if out != nil {
					t.Errorf("Restore returned a body to apply alongside its error: %s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if c.sentAsTyped && !bytes.Equal(in, out) {
				t.Errorf("nothing was restored, yet the JSON changed:\n in  %s\n out %s", in, out)
			}
			got := decode(t, out)
			gotData, _ := got["data"].(map[string]any)
			if c.wantData == nil && gotData != nil || !maps.Equal(gotData, c.wantData) {
				t.Errorf("data = %v, want %v", gotData, c.wantData)
			}
			if gotSD, _ := got["stringData"].(map[string]any); !maps.Equal(gotSD, c.wantStringData) {
				t.Errorf("stringData = %v, want %v", gotSD, c.wantStringData)
			}
			if gotRV, _, _ := unstructured.NestedString(got, "metadata", "resourceVersion"); gotRV != c.wantRV {
				t.Errorf("resourceVersion = %q, want %q", gotRV, c.wantRV)
			}
			if bytes.Contains(out, []byte("<redacted")) {
				t.Errorf("a placeholder survived into the body to apply: %s", out)
			}
		})
	}
}

// The edit buffer is the yaml view's text, so the same masking keeps values off
// disk. Only a Secret gets a Restore; anything else is applied as saved.
func TestEditYAML_MatchesYAML(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "demo"}, Data: map[string]string{"k": "v"}}
	for name, c := range map[string]struct {
		obj     runtime.Object
		restore bool
	}{
		"typed Secret":        {typedSecret(), true},
		"unstructured Secret": {unstructuredSecret(), true},
		"ConfigMap":           {cm, false},
	} {
		t.Run(name, func(t *testing.T) {
			view, err := YAML(c.obj)
			if err != nil {
				t.Fatal(err)
			}
			edit, restore, err := EditYAML(c.obj)
			if err != nil {
				t.Fatal(err)
			}
			if edit != view {
				t.Errorf("edit buffer differs from the yaml view:\n%s\n---\n%s", edit, view)
			}
			if (restore != nil) != c.restore {
				t.Errorf("Restore present = %v, want %v", restore != nil, c.restore)
			}
			assertNoValues(t, edit)
		})
	}
}

// A Secret read from the dynamic client restores the same way.
func TestRestore_UnstructuredSecret(t *testing.T) {
	_, out, err := editAndRestore(t, unstructuredSecret(), func(s string) string {
		return strings.Replace(s, "    app: db\n", "    app: db\n    tier: backend\n", 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := decode(t, out)["data"].(map[string]any)
	for k, v := range secretValuesUnderTest {
		if data[k] != b64(v) {
			t.Errorf("data.%s = %v, want the original %s", k, data[k], b64(v))
		}
	}
}

// Keys of the same size, as generated keys and tokens often are, have
// placeholders that differ only in the key they name. A placeholder moved,
// copied or swapped onto another key of that size must still be refused. If it
// were put back as that key's own, the other key's old value would be applied
// in place of the one the user meant, the edit would report success, and where
// kubetui alone owns the source key, server-side apply would delete it.
func TestRestore_EqualSizeKeys(t *testing.T) {
	cur, next := strings.Repeat("C", 32), strings.Repeat("N", 32)
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "keys", Namespace: "demo", ResourceVersion: "41"},
		Data:       map[string][]byte{"current": []byte(cur), "next": []byte(next)},
	}
	const (
		curLine  = "  current: '<redacted: 32 bytes of current>'\n"
		nextLine = "  next: '<redacted: 32 bytes of next>'\n"
	)
	if text, _, _ := EditYAML(s); !strings.Contains(text, curLine) || !strings.Contains(text, nextLine) {
		t.Fatalf("fixture lines not in the buffer:\n%s", text)
	}

	_, out, err := editAndRestore(t, s, func(buf string) string {
		return strings.Replace(buf, "  name: keys\n", "  labels:\n    a: b\n  name: keys\n", 1)
	})
	if err != nil {
		t.Fatalf("label edit: %v", err)
	}
	if data, _ := decode(t, out)["data"].(map[string]any); data["current"] != b64(cur) || data["next"] != b64(next) {
		t.Errorf("label edit: data = %v, want both originals", data)
	}

	for name, c := range map[string]struct {
		edit    func(string) string
		wantErr string
	}{
		// Rotation: drop current, and rename next to take its place.
		"next renamed onto current": {
			edit: func(buf string) string {
				buf = strings.Replace(buf, curLine, "", 1)
				return strings.Replace(buf, "  next:", "  current:", 1)
			},
			wantErr: "data.current has a wrong placeholder",
		},
		"the two swapped": {
			edit: func(buf string) string {
				buf = strings.Replace(buf, curLine, "  current: '<redacted: 32 bytes of next>'\n", 1)
				return strings.Replace(buf, nextLine, "  next: '<redacted: 32 bytes of current>'\n", 1)
			},
			wantErr: "data.current has a wrong placeholder",
		},
		"current copied over next": {
			edit: func(buf string) string {
				return strings.Replace(buf, nextLine, "  next: '<redacted: 32 bytes of current>'\n", 1)
			},
			wantErr: "data.next has a wrong placeholder",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, out, err := editAndRestore(t, s, c.edit)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("Restore error = %v, want %q; body %s", err, c.wantErr, out)
			}
			if out != nil {
				t.Errorf("Restore returned a body to apply alongside its error: %s", out)
			}
		})
	}
}

// A Restore error ends the toast "• apply NAME: ERR", one footer line cut to the
// terminal width with no history, and the temp file holding the edit is gone
// by then. So the whole message, the remedy at its end included, must fit 80
// columns for a short name and a key of ten characters.
func TestRestoreErrorsFitTheToast(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "demo"},
		Data:       map[string][]byte{"passwd_key": []byte("x")},
	}
	const ph = "'<redacted: 1 bytes of passwd_key>'"
	for name, edit := range map[string]func(string) string{
		"still redacted": func(buf string) string {
			return strings.Replace(buf, "  passwd_key:", "  passwd_new:", 1)
		},
		"wrong placeholder": func(buf string) string {
			return strings.Replace(buf, ph, "'<redacted: 2 bytes of passwd_key>'", 1)
		},
		"placeholder in stringData": func(buf string) string {
			return buf + "stringData:\n  passwd_key: " + ph + "\n"
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := editAndRestore(t, s, edit)
			if err == nil {
				t.Fatal("the edit was not refused")
			}
			toast := "• apply web: " + err.Error()
			if w := ansi.StringWidth(toast); w > 80 {
				t.Errorf("toast is %d columns, over 80: %s", w, toast)
			}
		})
	}
}

func TestRestore_SecretWithoutData(t *testing.T) {
	for name, data := range map[string]map[string][]byte{"nil data": nil, "empty data": {}} {
		t.Run(name, func(t *testing.T) {
			s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "demo", ResourceVersion: "7"}, Data: data}
			addLabel := func(t string) string {
				return strings.Replace(t, "  name: empty\n", "  labels:\n    a: b\n  name: empty\n", 1)
			}
			in, out, err := editAndRestore(t, s, addLabel)
			if err != nil || !bytes.Equal(in, out) {
				t.Errorf("label edit: err=%v, changed=%v, want it sent as typed", err, !bytes.Equal(in, out))
			}
			_, _, err = editAndRestore(t, s, func(t string) string {
				return t + "data:\n  k: '<redacted: 0 bytes of k>'\n"
			})
			if err == nil || !strings.Contains(err.Error(), "data.k is still redacted") {
				t.Errorf("placeholder on a Secret that had no data: err=%v", err)
			}
		})
	}
}

// Restore re-encodes the object, which must not round integers through float64.
func TestRestore_KeepsIntegersExact(t *testing.T) {
	s := typedSecret()
	s.Generation = 1<<53 + 1 // not representable as a float64
	_, out, err := editAndRestore(t, s, func(t string) string { return t })
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"generation":9007199254740993`)) {
		t.Errorf("generation was not kept exact: %s", out)
	}
}

// A body that is not an object has nothing to restore and goes to the server,
// which rejects it with its own message.
func TestRestore_NotAnObject(t *testing.T) {
	_, restore, err := EditYAML(typedSecret())
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"null", "[]", `"text"`} {
		if out, err := restore([]byte(in)); err != nil || string(out) != in {
			t.Errorf("restore(%s) = %s, %v; want it unchanged", in, out, err)
		}
	}
}
