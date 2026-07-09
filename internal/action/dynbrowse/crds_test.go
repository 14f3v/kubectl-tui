package dynbrowse

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestCRDInfoFrom builds a namespaced cert-manager Certificate CRD as
// unstructured data and asserts crdInfoFrom lifts out group/plural/kind/scope,
// picks the served+storage version, sets Namespaced true, and produces the
// expected GVR. A second case with a Cluster scope confirms Namespaced flips to
// false.
func TestCRDInfoFrom(t *testing.T) {
	u := crdUnstructured("cert-manager.io", "certificates", "Certificate", "Namespaced",
		[]interface{}{
			map[string]interface{}{"name": "v1", "served": true, "storage": true},
		})

	info := crdInfoFrom(u)
	if info.Group != "cert-manager.io" {
		t.Errorf("Group = %q, want cert-manager.io", info.Group)
	}
	if info.Plural != "certificates" {
		t.Errorf("Plural = %q, want certificates", info.Plural)
	}
	if info.Kind != "Certificate" {
		t.Errorf("Kind = %q, want Certificate", info.Kind)
	}
	if info.Version != "v1" {
		t.Errorf("Version = %q, want v1", info.Version)
	}
	if info.Scope != "Namespaced" || !info.Namespaced {
		t.Errorf("scope = %q namespaced = %v, want Namespaced/true", info.Scope, info.Namespaced)
	}

	want := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	if info.GVR() != want {
		t.Errorf("GVR() = %+v, want %+v", info.GVR(), want)
	}

	// A cluster-scoped CRD must report Namespaced false.
	cluster := crdUnstructured("example.com", "widgets", "Widget", "Cluster",
		[]interface{}{
			map[string]interface{}{"name": "v1alpha1", "served": true, "storage": true},
		})
	ci := crdInfoFrom(cluster)
	if ci.Namespaced || ci.Scope != "Cluster" {
		t.Errorf("cluster CRD namespaced = %v scope = %q, want false/Cluster", ci.Namespaced, ci.Scope)
	}
}

// TestServedVersionFallback confirms the version-selection rules: a served
// non-storage version is chosen over an unserved one, and when nothing is marked
// served we fall back to the first listed version's name.
func TestServedVersionFallback(t *testing.T) {
	// v1beta1 is served (but not storage); v1alpha1 is neither → pick v1beta1.
	served := crdUnstructured("example.com", "things", "Thing", "Namespaced",
		[]interface{}{
			map[string]interface{}{"name": "v1alpha1", "served": false, "storage": false},
			map[string]interface{}{"name": "v1beta1", "served": true, "storage": false},
		})
	if got := crdInfoFrom(served).Version; got != "v1beta1" {
		t.Errorf("Version = %q, want v1beta1 (first served)", got)
	}

	// Nothing served → fall back to the first listed name.
	none := crdUnstructured("example.com", "things", "Thing", "Namespaced",
		[]interface{}{
			map[string]interface{}{"name": "v2", "served": false, "storage": false},
		})
	if got := crdInfoFrom(none).Version; got != "v2" {
		t.Errorf("Version = %q, want v2 (fallback to first)", got)
	}
}

// TestFindResource hand-builds a discovery result containing a cert-manager
// Certificate and asserts findResource returns it (with version from the
// GroupVersion and Namespaced carried through), while a miss returns ok=false.
func TestFindResource(t *testing.T) {
	lists := []*metav1.APIResourceList{
		{
			GroupVersion: "cert-manager.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "certificates", Kind: "Certificate", Namespaced: true},
				{Name: "issuers", Kind: "Issuer", Namespaced: true},
			},
		},
	}

	info, ok := findResource(lists, "certificates", "cert-manager.io")
	if !ok {
		t.Fatal("findResource returned ok=false for a present resource")
	}
	if info.Version != "v1" {
		t.Errorf("Version = %q, want v1", info.Version)
	}
	if info.Kind != "Certificate" {
		t.Errorf("Kind = %q, want Certificate", info.Kind)
	}
	if info.Name != "certificates.cert-manager.io" {
		t.Errorf("Name = %q, want certificates.cert-manager.io", info.Name)
	}
	if !info.Namespaced || info.Scope != "Namespaced" {
		t.Errorf("namespaced = %v scope = %q, want true/Namespaced", info.Namespaced, info.Scope)
	}
	if info.GVR() != (schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}) {
		t.Errorf("GVR() = %+v, unexpected", info.GVR())
	}

	if _, ok := findResource(lists, "nonexistent", "cert-manager.io"); ok {
		t.Error("findResource returned ok=true for a missing resource")
	}
	// Right plural, wrong group must also miss.
	if _, ok := findResource(lists, "certificates", "other.io"); ok {
		t.Error("findResource matched across the wrong group")
	}
}

func TestFindResourceAny(t *testing.T) {
	lists := []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "endpoints", Kind: "Endpoints", Namespaced: true},
			{Name: "componentstatuses", Kind: "ComponentStatus", Namespaced: false},
		}},
		{GroupVersion: "coordination.k8s.io/v1", APIResources: []metav1.APIResource{
			{Name: "leases", Kind: "Lease", Namespaced: true},
		}},
	}

	// Core-group resource: empty group, name is the bare plural (no dot).
	got, ok := findResourceAny(lists, "endpoints")
	if !ok || got.Group != "" || got.Kind != "Endpoints" || got.Name != "endpoints" || !got.Namespaced {
		t.Fatalf("endpoints = %+v ok=%v", got, ok)
	}
	// Cluster-scoped core-group resource.
	if got, ok := findResourceAny(lists, "componentstatuses"); !ok || got.Namespaced || got.Scope != "Cluster" {
		t.Fatalf("componentstatuses = %+v ok=%v", got, ok)
	}
	// Grouped resource: group filled, dotted name.
	if got, ok := findResourceAny(lists, "leases"); !ok || got.Group != "coordination.k8s.io" || got.Name != "leases.coordination.k8s.io" {
		t.Fatalf("leases = %+v ok=%v", got, ok)
	}
	if _, ok := findResourceAny(lists, "nope"); ok {
		t.Error("findResourceAny matched a missing resource")
	}
}

// crdUnstructured builds a minimal CustomResourceDefinition as unstructured data
// with the fields crdInfoFrom reads.
func crdUnstructured(group, plural, kind, scope string, versions []interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]interface{}{
				"name": plural + "." + group,
			},
			"spec": map[string]interface{}{
				"group": group,
				"names": map[string]interface{}{
					"plural": plural,
					"kind":   kind,
				},
				"scope":    scope,
				"versions": versions,
			},
		},
	}
}
