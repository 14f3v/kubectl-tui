package metaedit

import (
	"encoding/json"
	"testing"
)

// decodeMeta unmarshals a metaedit patch and returns the inner
// metadata.<field> object, failing the test if the body is not valid JSON or is
// not shaped as {"metadata":{field:{...}}}.
func decodeMeta(t *testing.T, body []byte, field string) map[string]any {
	t.Helper()
	if !json.Valid(body) {
		t.Fatalf("patch is not valid JSON: %s", body)
	}
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	meta, ok := top["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("missing metadata object in %s", body)
	}
	inner, ok := meta[field].(map[string]any)
	if !ok {
		t.Fatalf("missing metadata.%s object in %s", field, body)
	}
	return inner
}

func TestLabelPatch(t *testing.T) {
	// Set: the key must carry the string value.
	set := LabelPatch("env", "prod", false)
	labels := decodeMeta(t, set, "labels")
	v, ok := labels["env"]
	if !ok {
		t.Fatalf("set patch missing key env: %s", set)
	}
	if s, ok := v.(string); !ok || s != "prod" {
		t.Fatalf("set patch labels.env = %v, want string \"prod\"", v)
	}

	// Remove: the key must be present and mapped to JSON null (nil after decode).
	rm := LabelPatch("env", "prod", true)
	rmLabels := decodeMeta(t, rm, "labels")
	rv, present := rmLabels["env"]
	if !present {
		t.Fatalf("remove patch must still contain key env (mapped to null): %s", rm)
	}
	if rv != nil {
		t.Fatalf("remove patch labels.env = %v, want JSON null", rv)
	}
}

func TestAnnotationPatch(t *testing.T) {
	// Set: the key must carry the string value.
	set := AnnotationPatch("team", "core", false)
	anns := decodeMeta(t, set, "annotations")
	v, ok := anns["team"]
	if !ok {
		t.Fatalf("set patch missing key team: %s", set)
	}
	if s, ok := v.(string); !ok || s != "core" {
		t.Fatalf("set patch annotations.team = %v, want string \"core\"", v)
	}

	// Remove: the key must be present and mapped to JSON null (nil after decode).
	rm := AnnotationPatch("team", "core", true)
	rmAnns := decodeMeta(t, rm, "annotations")
	rv, present := rmAnns["team"]
	if !present {
		t.Fatalf("remove patch must still contain key team (mapped to null): %s", rm)
	}
	if rv != nil {
		t.Fatalf("remove patch annotations.team = %v, want JSON null", rv)
	}
}
