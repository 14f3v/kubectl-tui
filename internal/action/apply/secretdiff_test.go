package apply

import (
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// liveSecret is demo/db as the API returns it after a client-side kubectl apply
// with stringData: base64 data, and the manifest again, in plain text, in the
// last-applied annotation.
func liveSecret() *unstructured.Unstructured {
	la := `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"db","namespace":"demo"},"stringData":{"password":"OLD-hunter2","user":"admin-user"}}`
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name": "db", "namespace": "demo",
			"annotations": map[string]any{corev1.LastAppliedConfigAnnotation: la, "team": "core"},
		},
		"type": "Opaque",
		"data": map[string]any{"password": b64("OLD-hunter2"), "user": b64("admin-user")},
	}}
}

// secretPlaintexts are the values liveSecret and the proposed edits hold. None
// of them, raw or base64, may appear in a preview.
var secretPlaintexts = []string{"OLD-hunter2", "NEW-hunter2", "admin-user", "longer-new-password", "brand-new"}

func assertNoSecretValues(t *testing.T, d string) {
	t.Helper()
	for _, v := range secretPlaintexts {
		if strings.Contains(d, v) || strings.Contains(d, b64(v)) {
			t.Errorf("the value %q is in the preview:\n%s", v, d)
		}
	}
}

func TestRenderDiffMasksSecret(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(p *unstructured.Unstructured)
		want    []string // lines that must be in the diff
		wantNot []string
	}{
		{
			// The case the preview used to leak most in: a label-only change
			// printed every value as context.
			name: "a label only",
			edit: func(p *unstructured.Unstructured) { p.SetLabels(map[string]string{"tier": "db"}) },
			want: []string{
				"+  labels:", "+    tier: db",
				"   password: '<redacted: 11 bytes>'",
				"   user: '<redacted: 10 bytes>'",
				"     kubectl.kubernetes.io/last-applied-configuration: '<redacted: 139 bytes>'",
			},
			wantNot: []string{"changed"},
		},
		{
			// Same size, so only the mark tells the lines apart. Without it the
			// diff would be empty and the apply skipped as "no changes".
			name: "a value changed to one of the same size",
			edit: func(p *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(p.Object, b64("NEW-hunter2"), "data", "password")
			},
			want: []string{
				"-  password: '<redacted: 11 bytes>'",
				"+  password: '<redacted: 11 bytes, changed>'",
				"   user: '<redacted: 10 bytes>'",
			},
		},
		{
			name: "a value changed to one of another size",
			edit: func(p *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(p.Object, b64("longer-new-password"), "data", "password")
			},
			want: []string{"-  password: '<redacted: 11 bytes>'", "+  password: '<redacted: 19 bytes, changed>'"},
		},
		{
			name: "a key added and a key removed",
			edit: func(p *unstructured.Unstructured) {
				_ = unstructured.SetNestedStringMap(p.Object, map[string]string{"password": b64("OLD-hunter2"), "extra": b64("brand-new")}, "data")
			},
			want:    []string{"+  extra: '<redacted: 9 bytes>'", "-  user: '<redacted: 10 bytes>'", "   password: '<redacted: 11 bytes>'"},
			wantNot: []string{"changed"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			live := liveSecret()
			proposed := liveSecret()
			c.edit(proposed)
			d, err := renderDiff(live, proposed, false, "v1, Kind=Secret demo/db")
			if err != nil {
				t.Fatal(err)
			}
			if d == "" {
				t.Fatal("the change gave an empty diff")
			}
			assertNoSecretValues(t, d)
			for _, want := range c.want {
				if !strings.Contains(d, want) {
					t.Errorf("diff lacks %q:\n%s", want, d)
				}
			}
			for _, not := range c.wantNot {
				if strings.Contains(d, not) {
					t.Errorf("diff has %q:\n%s", not, d)
				}
			}
			// The objects the caller holds keep their values.
			if got, _, _ := unstructured.NestedString(live.Object, "data", "password"); got != b64("OLD-hunter2") {
				t.Errorf("renderDiff changed the live object: password=%q", got)
			}
			if got := proposed.GetAnnotations()[corev1.LastAppliedConfigAnnotation]; !strings.Contains(got, "OLD-hunter2") {
				t.Errorf("renderDiff changed the proposed object's annotation: %q", got)
			}
		})
	}
}

// An apply that changes nothing still diffs to nothing once the values are
// masked, so the preview is skipped as before.
func TestRenderDiffSecretNoChange(t *testing.T) {
	d, err := renderDiff(liveSecret(), liveSecret(), false, "v1, Kind=Secret demo/db")
	if err != nil || d != "" {
		t.Errorf("identical Secrets: diff %q, err %v; want none", d, err)
	}
}

// A Secret that does not exist yet is all additions, masked too.
func TestRenderDiffNewSecret(t *testing.T) {
	proposed := liveSecret()
	_ = unstructured.SetNestedStringMap(proposed.Object, map[string]string{"plain": "brand-new"}, "stringData")
	d, err := renderDiff(nil, proposed, true, "v1, Kind=Secret demo/db")
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecretValues(t, d)
	for _, want := range []string{"+  password: '<redacted: 11 bytes>'", "+  plain: '<redacted: 9 bytes>'", "+kind: Secret", "+    team: core"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff lacks %q:\n%s", want, d)
		}
	}
}

// Only a core Secret is masked: a custom kind called Secret has its own schema.
func TestRenderDiffLeavesCustomSecretKindAlone(t *testing.T) {
	obj := func(path string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "vault.example.com/v1",
			"kind":       "Secret",
			"metadata":   map[string]any{"name": "ref", "namespace": "demo"},
			"data":       map[string]any{"path": path},
		}}
	}
	d, err := renderDiff(obj("kv/old"), obj("kv/new"), false, "vault.example.com/v1, Kind=Secret demo/ref")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "-  path: kv/old") || !strings.Contains(d, "+  path: kv/new") {
		t.Errorf("a custom kind named Secret was masked:\n%s", d)
	}
}
