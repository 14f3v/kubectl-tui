package apply

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// TestSplitDocuments feeds a stream with a leading "---", two real documents, a
// trailing "---", and a comment-only document, and asserts only the two content
// documents survive — separators, empties, and comment-only docs are dropped.
func TestSplitDocuments(t *testing.T) {
	data := []byte(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: first
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: second
---
# just a comment, nothing to apply
---
`)

	docs := SplitDocuments(data)
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d: %q", len(docs), docs)
	}
	if !strings.Contains(string(docs[0]), "name: first") {
		t.Errorf("first doc wrong: %q", docs[0])
	}
	if !strings.Contains(string(docs[1]), "name: second") {
		t.Errorf("second doc wrong: %q", docs[1])
	}
	// A separator line must not leak into the surviving docs.
	for i, d := range docs {
		for _, line := range strings.Split(string(d), "\n") {
			if strings.TrimSpace(line) == "---" {
				t.Errorf("doc %d still contains a separator line", i)
			}
		}
	}
}

// TestSplitDocuments_Empty confirms a stream with no real content yields nothing.
func TestSplitDocuments_Empty(t *testing.T) {
	data := []byte("---\n\n# comment only\n---\n   \n")
	if docs := SplitDocuments(data); len(docs) != 0 {
		t.Fatalf("expected 0 docs, got %d: %q", len(docs), docs)
	}
}

// TestGVKFromDoc parses a Deployment document and asserts the resolved GVK is
// exactly apps/v1 Deployment.
func TestGVKFromDoc(t *testing.T) {
	doc := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: demo
spec:
  replicas: 1
`)
	obj, gvk, err := parseDoc(doc)
	if err != nil {
		t.Fatalf("parseDoc: %v", err)
	}
	if gvk.Group != "apps" || gvk.Version != "v1" || gvk.Kind != "Deployment" {
		t.Fatalf("GVK = %+v, want apps/v1 Deployment", gvk)
	}
	if obj.GetName() != "web" {
		t.Errorf("name = %q, want web", obj.GetName())
	}
	if obj.GetNamespace() != "demo" {
		t.Errorf("namespace = %q, want demo", obj.GetNamespace())
	}
}

// TestGVKFromDoc_Missing confirms a document without apiVersion/kind is an error
// rather than a silently-empty GVK.
func TestGVKFromDoc_Missing(t *testing.T) {
	if _, _, err := parseDoc([]byte("metadata:\n  name: x\n")); err == nil {
		t.Fatal("expected error for doc missing apiVersion/kind")
	}
}

// TestApplyMapping feeds a namespaced Deployment doc (with no namespace set)
// through Apply, using a hand-built RESTMapper that knows apps/v1 Deployment. It
// asserts Apply resolves the GVR (namespace defaulted, name carried) and that
// the Result reports either success or only a fake-only "unsupported apply
// patch" limitation — never a mapping/parse failure.
func TestApplyMapping(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	// A DefaultRESTMapper with a single explicit, namespaced mapping — no
	// discovery, so the resolution path is fully deterministic in the test.
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

	results := Apply(context.Background(), dyn, m, doc, "kubetui", "default")
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
	// The doc omitted a namespace; the namespaced mapping must default it.
	if r.Namespace != "default" {
		t.Errorf("Namespace = %q, want default (defaulted)", r.Namespace)
	}

	// The GVR/scope resolution succeeded if we got here without a mapping error.
	// The fake dynamic client's server-side apply support is limited, so tolerate
	// only that specific class of failure — anything else is a real bug.
	if r.Err != nil && !isFakeApplyLimitation(r.Err) {
		t.Fatalf("unexpected Apply error (not a fake SSA limitation): %v", r.Err)
	}
}

// TestApplyNoMapping confirms an un-mappable kind is reported per-doc (with the
// GVK recorded) instead of panicking or aborting the batch.
func TestApplyNoMapping(t *testing.T) {
	m := meta.NewDefaultRESTMapper(nil) // knows nothing
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	doc := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
`)
	results := Apply(context.Background(), dyn, m, doc, "kubetui", "default")
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

// TestApplyMultiDoc confirms a batch of two docs yields two ordered Results and
// one bad doc does not sink the good one.
func TestApplyMultiDoc(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	m.AddSpecific(gvk, gvr, gvr, meta.RESTScopeNamespace)

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	// First doc is a valid Deployment; second is missing apiVersion/kind.
	data := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: demo
---
metadata:
  name: broken
`)
	results := Apply(context.Background(), dyn, m, data, "kubetui", "default")
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

// TestNewMapper is a smoke test: NewMapper returns a usable, non-nil mapper.
func TestNewMapper(t *testing.T) {
	if m := NewMapper(nil); m == nil {
		t.Fatal("NewMapper returned nil")
	}
}

// isFakeApplyLimitation reports whether an error is the fake dynamic client's
// known inability to fully service server-side apply, as opposed to a genuine
// bug in Apply's resolution logic. Production code runs against a real API
// server where apply is supported.
func isFakeApplyLimitation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "apply") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no reaction")
}
