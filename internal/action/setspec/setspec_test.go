package setspec

import (
	"encoding/json"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/types"
)

// decode unmarshals a patch body into a generic map and fails the test on invalid
// JSON. It first asserts json.Valid so a malformed body is caught explicitly.
func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	if !json.Valid(body) {
		t.Fatalf("patch body is not valid JSON: %s", body)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	return m
}

// podSpec walks to the pod spec map, honoring inTemplate nesting. For inTemplate
// it descends spec.template.spec; otherwise spec.
func podSpec(t *testing.T, m map[string]any, inTemplate bool) map[string]any {
	t.Helper()
	spec, ok := m["spec"].(map[string]any)
	if !ok {
		t.Fatalf("missing spec: %#v", m)
	}
	if !inTemplate {
		return spec
	}
	tmpl, ok := spec["template"].(map[string]any)
	if !ok {
		t.Fatalf("missing spec.template: %#v", m)
	}
	inner, ok := tmpl["spec"].(map[string]any)
	if !ok {
		t.Fatalf("missing spec.template.spec: %#v", m)
	}
	return inner
}

// firstContainer returns containers[0] from a pod spec map.
func firstContainer(t *testing.T, spec map[string]any) map[string]any {
	t.Helper()
	list, ok := spec["containers"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("missing containers: %#v", spec)
	}
	c, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("container[0] not an object: %#v", list[0])
	}
	return c
}

func TestSetImage(t *testing.T) {
	for _, inTemplate := range []bool{true, false} {
		pt, body := SetImage(inTemplate, "app", "nginx:1.27")
		if pt != types.StrategicMergePatchType {
			t.Fatalf("patch type = %v, want strategic merge", pt)
		}
		m := decode(t, body)
		c := firstContainer(t, podSpec(t, m, inTemplate))
		if c["name"] != "app" {
			t.Errorf("inTemplate=%v: container name = %v, want app", inTemplate, c["name"])
		}
		if c["image"] != "nginx:1.27" {
			t.Errorf("inTemplate=%v: image = %v, want nginx:1.27", inTemplate, c["image"])
		}
	}
}

func TestSetEnvSorted(t *testing.T) {
	env := map[string]string{"ZED": "3", "ALPHA": "1", "MID": "2"}
	pt, body := SetEnv(true, "app", env)
	if pt != types.StrategicMergePatchType {
		t.Fatalf("patch type = %v, want strategic merge", pt)
	}
	m := decode(t, body)
	c := firstContainer(t, podSpec(t, m, true))
	list, ok := c["env"].([]any)
	if !ok {
		t.Fatalf("env not a list: %#v", c)
	}
	var gotKeys []string
	got := map[string]string{}
	for _, e := range list {
		em := e.(map[string]any)
		gotKeys = append(gotKeys, em["name"].(string))
		got[em["name"].(string)] = em["value"].(string)
	}
	wantKeys := []string{"ALPHA", "MID", "ZED"}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Errorf("env keys = %v, want sorted %v", gotKeys, wantKeys)
	}
	if !reflect.DeepEqual(got, env) {
		t.Errorf("env values = %v, want %v", got, env)
	}
}

func TestSetEnvBarePod(t *testing.T) {
	pt, body := SetEnv(false, "app", map[string]string{"A": "1"})
	if pt != types.StrategicMergePatchType {
		t.Fatalf("patch type = %v", pt)
	}
	m := decode(t, body)
	spec := podSpec(t, m, false)
	// A bare pod must NOT wrap under template.
	if _, hasTemplate := spec["template"]; hasTemplate {
		t.Errorf("bare pod patch should not contain template: %#v", spec)
	}
	c := firstContainer(t, spec)
	if c["name"] != "app" {
		t.Errorf("container name = %v, want app", c["name"])
	}
}

func TestSetResources(t *testing.T) {
	requests := map[string]string{"cpu": "100m", "memory": "128Mi", "bad": "not-a-qty"}
	limits := map[string]string{"cpu": "500m", "memory": "256Mi"}
	pt, body := SetResources(true, "app", requests, limits)
	if pt != types.StrategicMergePatchType {
		t.Fatalf("patch type = %v", pt)
	}
	m := decode(t, body)
	c := firstContainer(t, podSpec(t, m, true))
	res, ok := c["resources"].(map[string]any)
	if !ok {
		t.Fatalf("resources not an object: %#v", c)
	}
	req, ok := res["requests"].(map[string]any)
	if !ok {
		t.Fatalf("requests not an object: %#v", res)
	}
	if req["cpu"] != "100m" {
		t.Errorf("requests.cpu = %v, want 100m", req["cpu"])
	}
	if req["memory"] != "128Mi" {
		t.Errorf("requests.memory = %v, want 128Mi", req["memory"])
	}
	// The invalid quantity must be skipped, not carried through.
	if _, present := req["bad"]; present {
		t.Errorf("invalid quantity should be skipped, got %v", req["bad"])
	}
	lim, ok := res["limits"].(map[string]any)
	if !ok {
		t.Fatalf("limits not an object: %#v", res)
	}
	if lim["cpu"] != "500m" || lim["memory"] != "256Mi" {
		t.Errorf("limits = %v, want cpu=500m memory=256Mi", lim)
	}
}

func TestSetResourcesEmptyOmitsSection(t *testing.T) {
	// Only requests supplied: limits must be absent, not an empty object.
	_, body := SetResources(false, "app", map[string]string{"cpu": "1"}, nil)
	m := decode(t, body)
	c := firstContainer(t, podSpec(t, m, false))
	res := c["resources"].(map[string]any)
	if _, has := res["limits"]; has {
		t.Errorf("limits should be omitted when empty: %#v", res)
	}
	if _, has := res["requests"]; !has {
		t.Errorf("requests should be present: %#v", res)
	}
}

func TestSetServiceAccount(t *testing.T) {
	for _, inTemplate := range []bool{true, false} {
		pt, body := SetServiceAccount(inTemplate, "deployer")
		if pt != types.StrategicMergePatchType {
			t.Fatalf("patch type = %v", pt)
		}
		m := decode(t, body)
		spec := podSpec(t, m, inTemplate)
		if spec["serviceAccountName"] != "deployer" {
			t.Errorf("inTemplate=%v: serviceAccountName = %v, want deployer", inTemplate, spec["serviceAccountName"])
		}
		// Must not accidentally place the field at the top or wrong level.
		if _, has := spec["containers"]; has {
			t.Errorf("serviceAccount patch should not include containers: %#v", spec)
		}
	}
}

// TestPatchRoundTrip is intentionally omitted: the fake dynamic client's
// ObjectTracker does not implement strategic-merge patching for unstructured
// objects, so a round-trip through Patch would fail for reasons unrelated to this
// package's logic. The builders above are the tested surface; Patch is a thin
// namespaced/cluster-scoped dispatch mirroring write.Delete.
