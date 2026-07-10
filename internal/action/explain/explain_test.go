package explain

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// sampleDoc is a small hand-built OpenAPI v3 document: a Pod schema whose
// properties include a scalar (spec.nodeName once dereferenced), a $ref to
// PodSpec, and an array of $ref (containers -> Container). It exercises $ref
// following, array-items descent, and object field listing without a cluster.
const sampleDoc = `{
  "components": {
    "schemas": {
      "io.k8s.api.core.v1.Pod": {
        "type": "object",
        "description": "Pod is a collection of containers.",
        "x-kubernetes-group-version-kind": [
          {"group": "", "version": "v1", "kind": "Pod"}
        ],
        "properties": {
          "apiVersion": {"type": "string", "description": "APIVersion of the object.\nsecond line"},
          "spec": {"$ref": "#/components/schemas/io.k8s.api.core.v1.PodSpec"}
        }
      },
      "io.k8s.api.core.v1.PodSpec": {
        "type": "object",
        "description": "PodSpec is the desired state of a Pod.",
        "properties": {
          "nodeName": {"type": "string", "description": "NodeName is the node this pod runs on."},
          "replicas": {"type": "integer", "description": "Replica count."},
          "containers": {
            "type": "array",
            "description": "List of containers.",
            "items": {"$ref": "#/components/schemas/io.k8s.api.core.v1.Container"}
          },
          "nodeSelector": {
            "type": "object",
            "description": "Node selector labels.",
            "additionalProperties": {"type": "string"}
          }
        }
      },
      "io.k8s.api.core.v1.Container": {
        "type": "object",
        "description": "A single container.",
        "properties": {
          "name": {"type": "string", "description": "Name of the container."},
          "image": {"type": "string", "description": "Container image."}
        }
      }
    }
  }
}`

// mustParse parses sampleDoc for the tests, failing loudly on error.
func mustParse(t *testing.T) doc {
	t.Helper()
	d, err := parseDoc([]byte(sampleDoc))
	if err != nil {
		t.Fatalf("parseDoc: %v", err)
	}
	return d
}

// podRoot returns the Pod schema from the sample document.
func podRoot(t *testing.T, d doc) map[string]any {
	t.Helper()
	root, ok := d.schemaForGVK(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
	if !ok {
		t.Fatal("schemaForGVK: Pod not found")
	}
	return root
}

// TestParseDoc confirms parseDoc lifts components.schemas and rejects both
// malformed JSON and a document that omits the schemas map.
func TestParseDoc(t *testing.T) {
	d := mustParse(t)
	if len(d.schemas) != 3 {
		t.Errorf("schemas count = %d, want 3", len(d.schemas))
	}
	if _, err := parseDoc([]byte("{not json")); err == nil {
		t.Error("parseDoc accepted malformed JSON")
	}
	if _, err := parseDoc([]byte(`{"components":{}}`)); err == nil {
		t.Error("parseDoc accepted a document with no schemas")
	}
}

// TestSchemaForGVK checks the group-version-kind extension match and the name
// heuristic fallback (a schema with no extension resolved by its dotted name).
func TestSchemaForGVK(t *testing.T) {
	d := mustParse(t)
	if _, ok := d.schemaForGVK(schema.GroupVersionKind{Version: "v1", Kind: "Pod"}); !ok {
		t.Error("schemaForGVK missed Pod via the gvk extension")
	}
	// Container has no extension, so it must resolve via the name heuristic.
	if _, ok := d.schemaForGVK(schema.GroupVersionKind{Version: "v1", Kind: "Container"}); !ok {
		t.Error("schemaForGVK missed Container via the name heuristic")
	}
	if _, ok := d.schemaForGVK(schema.GroupVersionKind{Version: "v1", Kind: "Ghost"}); ok {
		t.Error("schemaForGVK matched a nonexistent kind")
	}
}

// TestResolveRef confirms a $ref is followed to its target and that a node
// without a $ref is returned unchanged.
func TestResolveRef(t *testing.T) {
	d := mustParse(t)
	root := podRoot(t, d)
	specRef, _ := root["properties"].(map[string]any)["spec"].(map[string]any)
	spec := d.resolveRef(specRef)
	if got := stringOr(spec["description"], ""); got != "PodSpec is the desired state of a Pod." {
		t.Errorf("resolveRef(spec) description = %q, want the PodSpec description", got)
	}
	// A node with no $ref is unchanged.
	plain := map[string]any{"type": "string"}
	if got := d.resolveRef(plain); got["type"] != "string" {
		t.Errorf("resolveRef of a ref-less node changed it: %+v", got)
	}
}

// TestNavigateLeafScalar walks Pod -> spec -> nodeName (through a $ref) and lands
// on the string scalar.
func TestNavigateLeafScalar(t *testing.T) {
	d := mustParse(t)
	node, err := d.navigate(podRoot(t, d), []string{"spec", "nodeName"})
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if typeString(node) != "string" {
		t.Errorf("nodeName type = %q, want string", typeString(node))
	}
}

// TestNavigateThroughRef walks Pod -> spec and lands on the dereferenced PodSpec
// object (proving $ref resolution mid-path).
func TestNavigateThroughRef(t *testing.T) {
	d := mustParse(t)
	node, err := d.navigate(podRoot(t, d), []string{"spec"})
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	props, ok := node["properties"].(map[string]any)
	if !ok || props["containers"] == nil {
		t.Fatalf("navigate to spec did not resolve PodSpec properties: %+v", node)
	}
}

// TestNavigateThroughArray walks Pod -> spec -> containers -> name, descending
// through the array's items ($ref to Container) to the leaf scalar.
func TestNavigateThroughArray(t *testing.T) {
	d := mustParse(t)
	node, err := d.navigate(podRoot(t, d), []string{"spec", "containers", "name"})
	if err != nil {
		t.Fatalf("navigate through array: %v", err)
	}
	if typeString(node) != "string" {
		t.Errorf("containers.name type = %q, want string", typeString(node))
	}
}

// TestNavigateThroughAllOf replicates how Kubernetes wraps object-typed fields:
// `spec: {allOf: [{$ref: PodSpec}], default: {}}` rather than a bare $ref. That is
// the real-world shape that broke `explain pod.spec.containers` against a live
// cluster — the allOf node had no "properties", so descent stopped at spec.
func TestNavigateThroughAllOf(t *testing.T) {
	const k8sDoc = `{
  "components": {"schemas": {
    "Pod": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group": "", "version": "v1", "kind": "Pod"}],
      "properties": {
        "spec": {"default": {}, "allOf": [{"$ref": "#/components/schemas/PodSpec"}]}
      }
    },
    "PodSpec": {
      "type": "object",
      "properties": {
        "containers": {
          "type": "array",
          "description": "List of containers.",
          "items": {"$ref": "#/components/schemas/Container"}
        }
      }
    },
    "Container": {
      "type": "object",
      "properties": {"name": {"type": "string"}, "image": {"type": "string"}}
    }
  }}
}`
	d, err := parseDoc([]byte(k8sDoc))
	if err != nil {
		t.Fatalf("parseDoc: %v", err)
	}
	root, ok := d.schemaForGVK(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	if !ok {
		t.Fatal("Pod schema not found")
	}
	// spec is allOf-wrapped; navigation must resolve through it to reach Container.
	node, err := d.navigate(root, []string{"spec", "containers", "name"})
	if err != nil {
		t.Fatalf("navigate spec.containers.name: %v", err)
	}
	if typeString(node) != "string" {
		t.Errorf("name type = %q, want string", typeString(node))
	}
	// resolveRef must dereference an allOf-wrapped node directly.
	specNode := map[string]any{
		"default": map[string]any{},
		"allOf":   []any{map[string]any{"$ref": "#/components/schemas/PodSpec"}},
	}
	if got := d.resolveRef(specNode); got["properties"] == nil {
		t.Errorf("resolveRef did not resolve allOf-wrapped node: %+v", got)
	}
}

// TestNavigateUnknownField confirms an unknown segment is a clear error naming
// the field.
func TestNavigateUnknownField(t *testing.T) {
	d := mustParse(t)
	_, err := d.navigate(podRoot(t, d), []string{"spec", "bogus"})
	if err == nil {
		t.Fatal("navigate accepted an unknown field")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error %q does not name the offending field", err)
	}
}

// TestUnwrapMap confirms navigating into an object with additionalProperties (a
// map) is well-behaved: nodeSelector is a scalar-valued map, so it has no
// descendable properties and reports its map type.
func TestUnwrapMap(t *testing.T) {
	d := mustParse(t)
	node, err := d.navigate(podRoot(t, d), []string{"spec", "nodeSelector"})
	if err != nil {
		t.Fatalf("navigate to nodeSelector: %v", err)
	}
	if got := fieldType(node); got != "map[string]string" {
		t.Errorf("nodeSelector type = %q, want map[string]string", got)
	}
}

// TestRenderSchema checks the rendered text carries the resolved type, the
// description, and the immediate field names of an object node.
func TestRenderSchema(t *testing.T) {
	d := mustParse(t)
	spec, err := d.navigate(podRoot(t, d), []string{"spec"})
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	out := renderSchema("Pod", "v1", "pod.spec", spec)

	for _, want := range []string{
		"KIND:     Pod",
		"VERSION:  v1",
		"FIELD: pod.spec <Object>",
		"PodSpec is the desired state of a Pod.",
		"FIELDS:",
		"containers",
		"nodeName",
		"replicas",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q\n---\n%s", want, out)
		}
	}
	// The containers field must render as an array type in the FIELDS list.
	if !strings.Contains(out, "[]Object") {
		t.Errorf("render output missing []Object for containers\n---\n%s", out)
	}
}

// TestRenderScalar confirms a leaf scalar renders its type and description and,
// having no properties, omits the FIELDS block.
func TestRenderScalar(t *testing.T) {
	d := mustParse(t)
	node, err := d.navigate(podRoot(t, d), []string{"spec", "nodeName"})
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	out := renderSchema("Pod", "v1", "pod.spec.nodeName", node)
	if !strings.Contains(out, "FIELD: pod.spec.nodeName <string>") {
		t.Errorf("scalar render missing typed FIELD line\n---\n%s", out)
	}
	if !strings.Contains(out, "NodeName is the node this pod runs on.") {
		t.Errorf("scalar render missing description\n---\n%s", out)
	}
	if strings.Contains(out, "FIELDS:") {
		t.Errorf("scalar render should have no FIELDS block\n---\n%s", out)
	}
}

// TestFindKind matches a token against plural name, singular, short name, and
// kind, skips subresources, and misses an unknown token.
func TestFindKind(t *testing.T) {
	lists := []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "pods", SingularName: "pod", Kind: "Pod", ShortNames: []string{"po"}},
			{Name: "pods/status", Kind: "Pod"}, // subresource, must be skipped
		}},
		{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{
			{Name: "deployments", SingularName: "deployment", Kind: "Deployment", ShortNames: []string{"deploy"}},
		}},
	}

	cases := []struct {
		token string
		want  schema.GroupVersionKind
	}{
		{"pods", schema.GroupVersionKind{Version: "v1", Kind: "Pod"}},
		{"Pod", schema.GroupVersionKind{Version: "v1", Kind: "Pod"}},
		{"po", schema.GroupVersionKind{Version: "v1", Kind: "Pod"}},
		{"pod", schema.GroupVersionKind{Version: "v1", Kind: "Pod"}},
		{"DEPLOY", schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}},
		{"deployment", schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}},
	}
	for _, c := range cases {
		got, ok := findKind(lists, c.token)
		if !ok {
			t.Errorf("findKind(%q) = not found", c.token)
			continue
		}
		if got != c.want {
			t.Errorf("findKind(%q) = %+v, want %+v", c.token, got, c.want)
		}
	}
	if _, ok := findKind(lists, "nope"); ok {
		t.Error("findKind matched an unknown token")
	}
}

// TestSplitExpr checks the token/path split and that empty segments are dropped.
func TestSplitExpr(t *testing.T) {
	cases := []struct {
		in    string
		token string
		path  []string
	}{
		{"pods", "pods", nil},
		{"pod.spec.containers", "pod", []string{"spec", "containers"}},
		{"deployments.spec.replicas", "deployments", []string{"spec", "replicas"}},
		{"pod..spec.", "pod", []string{"spec"}},
		{"  pods  ", "pods", nil},
	}
	for _, c := range cases {
		token, path := splitExpr(c.in)
		if token != c.token {
			t.Errorf("splitExpr(%q) token = %q, want %q", c.in, token, c.token)
		}
		if strings.Join(path, ",") != strings.Join(c.path, ",") {
			t.Errorf("splitExpr(%q) path = %v, want %v", c.in, path, c.path)
		}
	}
}

// TestOpenAPIPathKey checks the core-vs-grouped key formatting.
func TestOpenAPIPathKey(t *testing.T) {
	if got := openAPIPathKey("", "v1"); got != "api/v1" {
		t.Errorf("core key = %q, want api/v1", got)
	}
	if got := openAPIPathKey("apps", "v1"); got != "apis/apps/v1" {
		t.Errorf("grouped key = %q, want apis/apps/v1", got)
	}
}
