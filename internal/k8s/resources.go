package k8s

import "k8s.io/apimachinery/pkg/runtime/schema"

// ResourceInfo describes how to address a kind through the dynamic client and
// the RBAC (SelfSubjectAccessReview) API: its GVR, whether it is namespaced, and
// the singular resource name used in access reviews.
type ResourceInfo struct {
	GVR        schema.GroupVersionResource
	Namespaced bool
	Kind       string // singular Kind, e.g. "Pod"
}

// resources is the single registry mapping our kind keys to their GVR + scope.
// Delete, access-review preflight, and any generic dynamic access read from here,
// so adding a kind updates one table.
var resources = map[string]ResourceInfo{
	"pods":        {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, Namespaced: true, Kind: "Pod"},
	"services":    {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, Namespaced: true, Kind: "Service"},
	"events":      {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}, Namespaced: true, Kind: "Event"},
	"deployments": {GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Namespaced: true, Kind: "Deployment"},
	"nodes":       {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}, Namespaced: false, Kind: "Node"},
	"namespaces":  {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}, Namespaced: false, Kind: "Namespace"},
}

// ResourceFor returns the ResourceInfo for a kind key, or ok=false if unknown.
func ResourceFor(kind string) (ResourceInfo, bool) {
	r, ok := resources[kind]
	return r, ok
}
