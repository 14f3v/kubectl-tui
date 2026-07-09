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

	// Workloads (#3).
	"statefulsets": {GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, Namespaced: true, Kind: "StatefulSet"},
	"daemonsets":   {GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, Namespaced: true, Kind: "DaemonSet"},
	"replicasets":  {GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, Namespaced: true, Kind: "ReplicaSet"},
	"jobs":         {GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespaced: true, Kind: "Job"},
	"cronjobs":     {GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, Namespaced: true, Kind: "CronJob"},

	// Config & storage (#4) and secrets (#2).
	"configmaps":             {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}, Namespaced: true, Kind: "ConfigMap"},
	"secrets":                {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}, Namespaced: true, Kind: "Secret"},
	"persistentvolumeclaims": {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}, Namespaced: true, Kind: "PersistentVolumeClaim"},
	"persistentvolumes":      {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumes"}, Namespaced: false, Kind: "PersistentVolume"},
	"storageclasses":         {GVR: schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}, Namespaced: false, Kind: "StorageClass"},

	// Networking (#5).
	"ingresses":       {GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, Namespaced: true, Kind: "Ingress"},
	"networkpolicies": {GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, Namespaced: true, Kind: "NetworkPolicy"},
	"endpointslices":  {GVR: schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, Namespaced: true, Kind: "EndpointSlice"},

	// RBAC (#6).
	"serviceaccounts":     {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}, Namespaced: true, Kind: "ServiceAccount"},
	"roles":               {GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, Namespaced: true, Kind: "Role"},
	"rolebindings":        {GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, Namespaced: true, Kind: "RoleBinding"},
	"clusterroles":        {GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}, Namespaced: false, Kind: "ClusterRole"},
	"clusterrolebindings": {GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}, Namespaced: false, Kind: "ClusterRoleBinding"},

	// Autoscaling & policy (#7).
	"horizontalpodautoscalers": {GVR: schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}, Namespaced: true, Kind: "HorizontalPodAutoscaler"},
	"poddisruptionbudgets":     {GVR: schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}, Namespaced: true, Kind: "PodDisruptionBudget"},
	"resourcequotas":           {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "resourcequotas"}, Namespaced: true, Kind: "ResourceQuota"},
	"limitranges":              {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "limitranges"}, Namespaced: true, Kind: "LimitRange"},

	"certificatesigningrequests": {GVR: schema.GroupVersionResource{Group: "certificates.k8s.io", Version: "v1", Resource: "certificatesigningrequests"}, Namespaced: false, Kind: "CertificateSigningRequest"},
}

// ResourceFor returns the ResourceInfo for a kind key, or ok=false if unknown.
func ResourceFor(kind string) (ResourceInfo, bool) {
	r, ok := resources[kind]
	return r, ok
}
