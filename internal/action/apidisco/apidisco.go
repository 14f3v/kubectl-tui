// apidisco backs the "api-resources" and "api-versions" views: it turns the API
// server's discovery documents into flat, sorted lists the UI can render. The
// network calls (ServerPreferredResources, ServerGroups) are kept separate from
// the flatten/sort transformations so the latter — the interesting logic — can be
// unit-tested against hand-built discovery documents without a live cluster.
package apidisco

import (
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// APIResource is one served resource as shown by "api-resources": its plural
// Name, comma-joined ShortNames, the APIVersion string it is served under (e.g.
// "v1" or "apps/v1"), its Kind, and whether it is namespaced.
type APIResource struct {
	Name       string
	ShortNames string
	APIVersion string
	Kind       string
	Namespaced bool
}

// APIGroupVersion is one group/version pair as shown by "api-versions": Group is
// "" for the core group, Version is the bare version, and Preferred marks the
// version the API server prefers for that group.
type APIGroupVersion struct {
	Group     string
	Version   string
	Preferred bool
}

// Resources lists every served (non-subresource) API resource via discovery,
// flattened and sorted for the "api-resources" view. It tolerates a partial
// discovery failure: some clusters return a groupDiscoveryFailedError for a
// subset of groups while still returning usable data for the rest, so as long as
// the returned slice is non-empty we proceed even when err is non-nil.
func Resources(disco discovery.DiscoveryInterface) ([]APIResource, error) {
	lists, err := disco.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return nil, err
	}
	return flattenResources(lists), nil
}

// Versions lists every group/version pair the API server serves, flattened and
// sorted for the "api-versions" view.
func Versions(disco discovery.DiscoveryInterface) ([]APIGroupVersion, error) {
	groups, err := disco.ServerGroups()
	if err != nil {
		return nil, err
	}
	return flattenGroups(groups), nil
}

// flattenResources turns discovery's per-GroupVersion resource lists into a flat
// APIResource slice. It is pure so it can be unit-tested against hand-built
// lists. Subresources (Name containing "/", e.g. "pods/log") are dropped, the
// APIVersion is the list's GroupVersion string, ShortNames are comma-joined, the
// result is sorted by (group, Name), and duplicates keyed on APIVersion+Name are
// removed (the first occurrence wins).
func flattenResources(lists []*metav1.APIResourceList) []APIResource {
	out := make([]APIResource, 0)
	seen := make(map[string]struct{})
	for _, list := range lists {
		if list == nil {
			continue
		}
		if _, err := schema.ParseGroupVersion(list.GroupVersion); err != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue // subresource, e.g. "pods/log".
			}
			key := list.GroupVersion + "\x00" + r.Name
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, APIResource{
				Name:       r.Name,
				ShortNames: strings.Join(r.ShortNames, ","),
				APIVersion: list.GroupVersion,
				Kind:       r.Kind,
				Namespaced: r.Namespaced,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		gi := groupOf(out[i].APIVersion)
		gj := groupOf(out[j].APIVersion)
		if gi != gj {
			return gi < gj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// flattenGroups turns a discovery APIGroupList into a flat APIGroupVersion slice,
// one row per version of each group. It is pure so it can be unit-tested against
// a hand-built list. Group is the group name ("" for core), Preferred marks the
// version whose GroupVersion equals the group's PreferredVersion.GroupVersion,
// and the result is sorted by (Group, Version).
func flattenGroups(groups *metav1.APIGroupList) []APIGroupVersion {
	out := make([]APIGroupVersion, 0)
	if groups == nil {
		return out
	}
	for _, g := range groups.Groups {
		for _, gv := range g.Versions {
			out = append(out, APIGroupVersion{
				Group:     g.Name,
				Version:   gv.Version,
				Preferred: gv.GroupVersion == g.PreferredVersion.GroupVersion,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// groupOf returns the group portion of a discovery GroupVersion string: "" for a
// core version like "v1", or the group for "apps/v1". A malformed value sorts by
// the whole string.
func groupOf(apiVersion string) string {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return apiVersion
	}
	return gv.Group
}
