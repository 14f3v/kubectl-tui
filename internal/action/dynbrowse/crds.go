// crds.go covers the discovery half of dynbrowse: enumerate the
// CustomResourceDefinitions installed on the cluster (so the UI can offer custom
// kinds as browsable resources) and resolve a plural+group back to the concrete
// GVR, scope, and kind the API server serves. Both paths yield a CRDInfo, the
// small descriptor the UI needs to fetch a Table for that kind.
package dynbrowse

import (
	"context"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

// crdGVR addresses the CustomResourceDefinition list itself. We read CRDs through
// the dynamic client (rather than a typed apiextensions client) so dynbrowse
// stays free of the apiextensions type dependency.
var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// CRDInfo is the compact descriptor of a browsable custom (or, via discovery,
// any) resource: enough to build its GVR and know its scope. Name is the CRD's
// object name ("<plural>.<group>"); Plural/Group/Version identify the served
// resource; Scope is "Namespaced" or "Cluster" with Namespaced as the boolean
// shorthand.
type CRDInfo struct {
	Name       string
	Group      string
	Kind       string
	Plural     string
	Scope      string
	Version    string
	Namespaced bool
	Created    time.Time
}

// GVR returns the GroupVersionResource the info describes, ready to hand to
// FetchTable or the dynamic client.
func (c CRDInfo) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: c.Group, Version: c.Version, Resource: c.Plural}
}

// crdInfoFrom extracts a CRDInfo from a CustomResourceDefinition read as
// unstructured data. It is pure so it can be unit-tested without a live cluster.
// The served version is chosen from spec.versions: the first entry that is
// served, preferring one that is also the storage version; if none is marked
// served we fall back to the first listed version's name so we always have
// something to address.
func crdInfoFrom(u *unstructured.Unstructured) CRDInfo {
	group, _, _ := unstructured.NestedString(u.Object, "spec", "group")
	plural, _, _ := unstructured.NestedString(u.Object, "spec", "names", "plural")
	kind, _, _ := unstructured.NestedString(u.Object, "spec", "names", "kind")
	scope, _, _ := unstructured.NestedString(u.Object, "spec", "scope")

	info := CRDInfo{
		Name:       u.GetName(),
		Group:      group,
		Kind:       kind,
		Plural:     plural,
		Scope:      scope,
		Namespaced: scope == "Namespaced",
		Version:    servedVersion(u),
		Created:    u.GetCreationTimestamp().Time,
	}
	return info
}

// servedVersion picks the version name to browse from spec.versions. It prefers a
// version that is both served and the storage version, then any served version,
// then the first listed version's name as a last resort.
func servedVersion(u *unstructured.Unstructured) string {
	versions, _, _ := unstructured.NestedSlice(u.Object, "spec", "versions")

	first := ""
	servedName := ""
	for _, v := range versions {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if first == "" {
			first = name
		}
		served, _ := m["served"].(bool)
		if !served {
			continue
		}
		storage, _ := m["storage"].(bool)
		if storage {
			return name // best match: served + storage.
		}
		if servedName == "" {
			servedName = name
		}
	}
	if servedName != "" {
		return servedName
	}
	return first
}

// ListCRDs lists every CustomResourceDefinition on the cluster and returns them
// as CRDInfo, sorted by group then name so the UI's kind picker is stable.
func ListCRDs(ctx context.Context, dyn dynamic.Interface) ([]CRDInfo, error) {
	list, err := dyn.Resource(crdGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	infos := make([]CRDInfo, 0, len(list.Items))
	for i := range list.Items {
		infos = append(infos, crdInfoFrom(&list.Items[i]))
	}
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Group != infos[j].Group {
			return infos[i].Group < infos[j].Group
		}
		return infos[i].Name < infos[j].Name
	})
	return infos, nil
}

// findResource scans discovery results for the resource matching a plural and
// group, returning its CRDInfo. It is pure so it can be unit-tested against a
// hand-built resource list. The version comes from the list's GroupVersion, and
// the scope is derived from the APIResource's Namespaced flag.
func findResource(lists []*metav1.APIResourceList, plural, group string) (CRDInfo, bool) {
	for _, list := range lists {
		if list == nil {
			continue
		}
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			if r.Name != plural || gv.Group != group {
				continue
			}
			scope := "Cluster"
			if r.Namespaced {
				scope = "Namespaced"
			}
			return CRDInfo{
				Name:       plural + "." + group,
				Group:      group,
				Version:    gv.Version,
				Kind:       r.Kind,
				Plural:     plural,
				Namespaced: r.Namespaced,
				Scope:      scope,
			}, true
		}
	}
	return CRDInfo{}, false
}

// ResolvePluralGroup resolves a plural+group to the concrete resource the API
// server serves (version, kind, scope) via discovery. It tolerates a partial
// discovery failure: some clusters return a groupDiscoveryFailedError for a
// subset of groups while still returning usable data for the rest, so as long as
// we got a non-empty list we proceed even when err is non-nil. If the resource is
// not found, that is a hard error.
func ResolvePluralGroup(ctx context.Context, disco discovery.DiscoveryInterface, plural, group string) (CRDInfo, error) {
	lists, err := disco.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return CRDInfo{}, err
	}
	info, ok := findResource(lists, plural, group)
	if !ok {
		return CRDInfo{}, fmt.Errorf("no served resource %q in group %q", plural, group)
	}
	return info, nil
}

// findResourceAny scans discovery for the first non-subresource matching a bare
// plural in any group. Pure, for unit testing.
func findResourceAny(lists []*metav1.APIResourceList, plural string) (CRDInfo, bool) {
	for _, list := range lists {
		if list == nil {
			continue
		}
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			if r.Name != plural {
				continue
			}
			scope := "Cluster"
			if r.Namespaced {
				scope = "Namespaced"
			}
			name := plural
			if gv.Group != "" {
				name = plural + "." + gv.Group
			}
			return CRDInfo{
				Name:       name,
				Group:      gv.Group,
				Version:    gv.Version,
				Kind:       r.Kind,
				Plural:     plural,
				Namespaced: r.Namespaced,
				Scope:      scope,
			}, true
		}
	}
	return CRDInfo{}, false
}

// ResolveResource resolves a bare plural (any group) to its served resource — for
// opening core-group and other built-in kinds that have no first-class view
// (e.g. :endpoints, :componentstatuses). Tolerates partial discovery failure.
func ResolveResource(ctx context.Context, disco discovery.DiscoveryInterface, plural string) (CRDInfo, error) {
	lists, err := disco.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return CRDInfo{}, err
	}
	info, ok := findResourceAny(lists, plural)
	if !ok {
		return CRDInfo{}, fmt.Errorf("no served resource %q", plural)
	}
	return info, nil
}
