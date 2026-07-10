package apply

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// TestUnifiedDiffIdentical confirms that identical inputs produce no diff at all,
// which callers rely on to mean "no change".
func TestUnifiedDiffIdentical(t *testing.T) {
	text := "a\nb\nc\n"
	if d := unifiedDiff(text, text, "obj"); d != "" {
		t.Fatalf("expected empty diff for identical input, got:\n%s", d)
	}
}

// TestUnifiedDiffAddedLine checks a single inserted line renders as one "+" line
// with the surrounding lines kept as context.
func TestUnifiedDiffAddedLine(t *testing.T) {
	old := "a\nb\n"
	new := "a\nx\nb\n"
	d := unifiedDiff(old, new, "obj")
	if !strings.Contains(d, "+x") {
		t.Errorf("expected added line '+x', got:\n%s", d)
	}
	if strings.Contains(d, "-a") || strings.Contains(d, "-b") {
		t.Errorf("unchanged lines must not appear as removals, got:\n%s", d)
	}
	// The context lines a and b should survive as unchanged (" " prefixed) lines.
	if !strings.Contains(d, " a") || !strings.Contains(d, " b") {
		t.Errorf("expected context lines ' a' and ' b', got:\n%s", d)
	}
}

// TestUnifiedDiffRemovedLine checks a single deleted line renders as one "-" line.
func TestUnifiedDiffRemovedLine(t *testing.T) {
	old := "a\nx\nb\n"
	new := "a\nb\n"
	d := unifiedDiff(old, new, "obj")
	if !strings.Contains(d, "-x") {
		t.Errorf("expected removed line '-x', got:\n%s", d)
	}
	if strings.Contains(d, "+x") {
		t.Errorf("removed line must not appear as an addition, got:\n%s", d)
	}
}

// TestUnifiedDiffChangedLine checks a modified line renders as the old line
// removed and the new line added (a line-based diff has no notion of in-place
// edits).
func TestUnifiedDiffChangedLine(t *testing.T) {
	old := "a\nreplicas: 1\nc\n"
	new := "a\nreplicas: 3\nc\n"
	d := unifiedDiff(old, new, "obj")
	if !strings.Contains(d, "-replicas: 1") {
		t.Errorf("expected '-replicas: 1', got:\n%s", d)
	}
	if !strings.Contains(d, "+replicas: 3") {
		t.Errorf("expected '+replicas: 3', got:\n%s", d)
	}
	// The unchanged surrounding lines must not be flagged.
	if strings.Contains(d, "-a") || strings.Contains(d, "-c") {
		t.Errorf("unchanged lines wrongly removed, got:\n%s", d)
	}
}

// TestUnifiedDiffHeader confirms the hunk carries the live/proposed banner naming
// the object.
func TestUnifiedDiffHeader(t *testing.T) {
	d := unifiedDiff("a\n", "b\n", "demo/web")
	if !strings.Contains(d, "--- live: demo/web") || !strings.Contains(d, "+++ proposed: demo/web") {
		t.Errorf("missing or malformed header, got:\n%s", d)
	}
}

// TestRenderAddDiff confirms a new object (no live counterpart) renders every
// proposed line as an addition and nothing as a removal.
func TestRenderAddDiff(t *testing.T) {
	proposed := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n"
	d := renderAddDiff(proposed, "v1, Kind=ConfigMap default/cfg")

	for _, want := range []string{
		"+apiVersion: v1",
		"+kind: ConfigMap",
		"+metadata:",
		"+  name: cfg",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("expected addition %q, got:\n%s", want, d)
		}
	}
	if strings.Contains(d, "\n-") {
		t.Errorf("new-object diff must contain no removals, got:\n%s", d)
	}
	if !strings.Contains(d, "+++ proposed: v1, Kind=ConfigMap default/cfg") {
		t.Errorf("missing add-diff header, got:\n%s", d)
	}
}

// TestRenderAddDiffEmpty confirms an empty proposed body yields an empty string
// rather than a lone header.
func TestRenderAddDiffEmpty(t *testing.T) {
	if d := renderAddDiff("", "obj"); d != "" {
		t.Errorf("expected empty diff for empty input, got:\n%s", d)
	}
}

// TestRenderDiffNewObject drives the pure render path with live=nil to confirm the
// new-object branch produces an add-only diff of the proposed object.
func TestRenderDiffNewObject(t *testing.T) {
	proposed := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]interface{}{"name": "cfg", "namespace": "default"},
		"data":       map[string]interface{}{"key": "value"},
	}}

	d, err := renderDiff(nil, proposed, true, "v1, Kind=ConfigMap default/cfg")
	if err != nil {
		t.Fatalf("renderDiff: %v", err)
	}
	if !strings.Contains(d, "+kind: ConfigMap") {
		t.Errorf("expected proposed object rendered as additions, got:\n%s", d)
	}
	if strings.Contains(d, "\n-") {
		t.Errorf("new object must have no removals, got:\n%s", d)
	}
}

// TestRenderDiffChange drives the pure render path with a live and proposed object
// that differ in one field, confirming only that field is flagged.
func TestRenderDiffChange(t *testing.T) {
	live := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]interface{}{"name": "web", "namespace": "demo"},
		"spec":       map[string]interface{}{"replicas": int64(1)},
	}}
	proposed := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]interface{}{"name": "web", "namespace": "demo"},
		"spec":       map[string]interface{}{"replicas": int64(3)},
	}}

	d, err := renderDiff(live, proposed, false, "apps/v1, Kind=Deployment demo/web")
	if err != nil {
		t.Fatalf("renderDiff: %v", err)
	}
	if !strings.Contains(d, "-  replicas: 1") {
		t.Errorf("expected old replicas removed, got:\n%s", d)
	}
	if !strings.Contains(d, "+  replicas: 3") {
		t.Errorf("expected new replicas added, got:\n%s", d)
	}
	if strings.Contains(d, "-kind:") || strings.Contains(d, "+kind:") {
		t.Errorf("unchanged kind line wrongly flagged, got:\n%s", d)
	}
}

// TestRenderDiffNoChange confirms identical live and proposed objects yield an
// empty diff (the "no change" signal).
func TestRenderDiffNoChange(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]interface{}{"name": "cfg"},
	}}
	// Distinct copies with identical content must still diff to nothing.
	clone := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]interface{}{"name": "cfg"},
	}}
	d, err := renderDiff(obj, clone, false, "obj")
	if err != nil {
		t.Fatalf("renderDiff: %v", err)
	}
	if d != "" {
		t.Errorf("expected empty diff for identical objects, got:\n%s", d)
	}
}

// TestSplitLines confirms the trailing-newline element is dropped and empty input
// yields no lines.
func TestSplitLines(t *testing.T) {
	if got := splitLines("a\nb\n"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("splitLines('a\\nb\\n') = %#v, want [a b]", got)
	}
	if got := splitLines(""); got != nil {
		t.Errorf("splitLines('') = %#v, want nil", got)
	}
	// A line without a trailing newline keeps its single element.
	if got := splitLines("only"); len(got) != 1 || got[0] != "only" {
		t.Errorf("splitLines('only') = %#v, want [only]", got)
	}
}

// TestDiffLabel confirms the namespace is included for namespaced objects and
// omitted (no stray slash) for cluster-scoped ones.
func TestDiffLabel(t *testing.T) {
	if got := diffLabel("apps/v1, Kind=Deployment", "demo", "web"); got != "apps/v1, Kind=Deployment demo/web" {
		t.Errorf("namespaced label = %q", got)
	}
	if got := diffLabel("v1, Kind=Node", "", "node1"); got != "v1, Kind=Node node1" {
		t.Errorf("cluster-scoped label = %q", got)
	}
}

// TestDiffMapping runs a doc through Diff against a fake dynamic client with a
// hand-built RESTMapper, asserting the resolution path (GVK, defaulted namespace,
// name) succeeds. As with the Apply tests, the fake client's server-side apply
// support is limited, so a dry-run apply failure is tolerated as a fake-only
// limitation — anything else (a mapping or parse failure) is a real bug.
func TestDiffMapping(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	m.AddSpecific(gvk, gvr, gvr, meta.RESTScopeNamespace)

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	doc := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 2
`)

	results := Diff(context.Background(), dyn, m, doc, "kubetui", "default")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.GVK != gvk.String() {
		t.Errorf("GVK = %q, want %q", r.GVK, gvk.String())
	}
	if r.Name != "web" {
		t.Errorf("Name = %q, want web", r.Name)
	}
	if r.Namespace != "default" {
		t.Errorf("Namespace = %q, want default (defaulted)", r.Namespace)
	}
	// The GVR/scope resolution succeeded if we got here. The fake dynamic client
	// cannot fully service a dry-run server-side apply, so tolerate only that
	// class of failure.
	if r.Err != nil && !isFakeApplyLimitation(r.Err) {
		t.Fatalf("unexpected Diff error (not a fake SSA limitation): %v", r.Err)
	}
}

// TestDiffNoMapping confirms an un-mappable kind is reported per-doc (with its GVK
// recorded) rather than aborting the batch.
func TestDiffNoMapping(t *testing.T) {
	m := meta.NewDefaultRESTMapper(nil) // knows nothing
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	doc := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
`)
	results := Diff(context.Background(), dyn, m, doc, "kubetui", "default")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected a no-mapping error")
	}
	if results[0].GVK != "apps/v1, Kind=Deployment" {
		t.Errorf("GVK = %q, want the deployment GVK even on mapping failure", results[0].GVK)
	}
}

// TestDiffMultiDocResilience confirms a batch of two docs yields two ordered
// DiffResults and one bad doc (missing apiVersion/kind) does not sink the good
// one — mirroring Apply's per-doc resilience.
func TestDiffMultiDocResilience(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	m.AddSpecific(gvk, gvr, gvr, meta.RESTScopeNamespace)

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	data := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: demo
---
metadata:
  name: broken
`)
	results := Diff(context.Background(), dyn, m, data, "kubetui", "default")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Name != "web" || results[0].Namespace != "demo" {
		t.Errorf("result[0] = %+v, want name=web ns=demo", results[0])
	}
	if results[0].Err != nil && !isFakeApplyLimitation(results[0].Err) {
		t.Errorf("result[0] unexpected error: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Error("result[1] should carry a parse error for the broken doc")
	}
}
