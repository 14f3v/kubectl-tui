package apidisco

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestFlattenResources hand-builds a core "v1" list (pods, namespaced, short name
// "po", plus a "pods/log" subresource that must be dropped) and an "apps/v1"
// list (deployments) and asserts flattenResources: drops the subresource, sorts
// by (group, name) so the core "pods" precedes the "apps" group's "deployments",
// joins ShortNames, and carries APIVersion/Namespaced through.
func TestFlattenResources(t *testing.T) {
	lists := []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true, ShortNames: []string{"po"}},
				{Name: "pods/log", Kind: "Pod", Namespaced: true},
			},
		},
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", Kind: "Deployment", Namespaced: true, ShortNames: []string{"deploy"}},
			},
		},
	}

	got := flattenResources(lists)

	// Subresource dropped: two rows remain (pods, deployments).
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (subresource dropped), got %+v", len(got), got)
	}

	// Sort order: core group ("") sorts before "apps", so pods precedes deployments.
	if got[0].Name != "pods" {
		t.Errorf("got[0].Name = %q, want pods (core group sorts first)", got[0].Name)
	}
	if got[1].Name != "deployments" {
		t.Errorf("got[1].Name = %q, want deployments", got[1].Name)
	}

	pods := got[0]
	if pods.APIVersion != "v1" {
		t.Errorf("pods.APIVersion = %q, want v1", pods.APIVersion)
	}
	if pods.ShortNames != "po" {
		t.Errorf("pods.ShortNames = %q, want po", pods.ShortNames)
	}
	if pods.Kind != "Pod" {
		t.Errorf("pods.Kind = %q, want Pod", pods.Kind)
	}
	if !pods.Namespaced {
		t.Errorf("pods.Namespaced = false, want true")
	}

	deploy := got[1]
	if deploy.APIVersion != "apps/v1" {
		t.Errorf("deployments.APIVersion = %q, want apps/v1", deploy.APIVersion)
	}
	if deploy.ShortNames != "deploy" {
		t.Errorf("deployments.ShortNames = %q, want deploy", deploy.ShortNames)
	}
}

// TestFlattenResourcesMultipleShortNamesAndDedup confirms multiple short names
// join with a comma and that a resource repeated under the same GroupVersion is
// deduped to a single row.
func TestFlattenResourcesMultipleShortNamesAndDedup(t *testing.T) {
	lists := []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "services", Kind: "Service", Namespaced: true, ShortNames: []string{"svc", "s"}},
				{Name: "services", Kind: "Service", Namespaced: true, ShortNames: []string{"svc"}},
			},
		},
	}

	got := flattenResources(lists)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (deduped on APIVersion+Name)", len(got))
	}
	if got[0].ShortNames != "svc,s" {
		t.Errorf("ShortNames = %q, want svc,s", got[0].ShortNames)
	}
}

// TestFlattenGroups hand-builds a core group (v1) and an "apps" group (v1
// preferred, v1beta1 not) and asserts flattenGroups emits one row per version,
// marks Preferred only on the group's preferred GroupVersion, uses "" as the core
// group name, and sorts by (Group, Version).
func TestFlattenGroups(t *testing.T) {
	groups := &metav1.APIGroupList{
		Groups: []metav1.APIGroup{
			{
				Name: "",
				Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: "v1", Version: "v1"},
				},
				PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "v1", Version: "v1"},
			},
			{
				Name: "apps",
				Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: "apps/v1beta1", Version: "v1beta1"},
					{GroupVersion: "apps/v1", Version: "v1"},
				},
				PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "apps/v1", Version: "v1"},
			},
		},
	}

	got := flattenGroups(groups)

	// Three rows: core v1, apps/v1, apps/v1beta1.
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3, got %+v", len(got), got)
	}

	// Sort order: core group ("") first, then apps sorted by version (v1 < v1beta1).
	want := []APIGroupVersion{
		{Group: "", Version: "v1", Preferred: true},
		{Group: "apps", Version: "v1", Preferred: true},
		{Group: "apps", Version: "v1beta1", Preferred: false},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestFlattenGroupsNil confirms a nil list yields an empty (non-nil) slice.
func TestFlattenGroupsNil(t *testing.T) {
	if got := flattenGroups(nil); got == nil || len(got) != 0 {
		t.Errorf("flattenGroups(nil) = %+v, want empty non-nil slice", got)
	}
}
