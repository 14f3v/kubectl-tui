// Package inspect implements the read-only inspect actions: rendering a cached
// object as YAML and running kubectl-style describe. Both return plain text that
// the caller shows in a scrollable text page.
package inspect

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/describe"
	"sigs.k8s.io/yaml"
)

// groupKinds maps our kind keys to the GroupKind kubectl's describer dispatches
// on. Extending the browser to a new kind adds one entry here.
var groupKinds = map[string]schema.GroupKind{
	"pods":        {Group: "", Kind: "Pod"},
	"services":    {Group: "", Kind: "Service"},
	"nodes":       {Group: "", Kind: "Node"},
	"namespaces":  {Group: "", Kind: "Namespace"},
	"events":      {Group: "", Kind: "Event"},
	"deployments": {Group: "apps", Kind: "Deployment"},

	// Workloads (#3).
	"statefulsets": {Group: "apps", Kind: "StatefulSet"},
	"daemonsets":   {Group: "apps", Kind: "DaemonSet"},
	"replicasets":  {Group: "apps", Kind: "ReplicaSet"},
	"jobs":         {Group: "batch", Kind: "Job"},
	"cronjobs":     {Group: "batch", Kind: "CronJob"},

	// Config & storage (#4) and secrets (#2).
	"configmaps":             {Group: "", Kind: "ConfigMap"},
	"secrets":                {Group: "", Kind: "Secret"},
	"persistentvolumeclaims": {Group: "", Kind: "PersistentVolumeClaim"},
	"persistentvolumes":      {Group: "", Kind: "PersistentVolume"},
	"storageclasses":         {Group: "storage.k8s.io", Kind: "StorageClass"},

	// Networking (#5).
	"ingresses":       {Group: "networking.k8s.io", Kind: "Ingress"},
	"networkpolicies": {Group: "networking.k8s.io", Kind: "NetworkPolicy"},
	"endpointslices":  {Group: "discovery.k8s.io", Kind: "EndpointSlice"},

	// RBAC (#6).
	"serviceaccounts":     {Group: "", Kind: "ServiceAccount"},
	"roles":               {Group: "rbac.authorization.k8s.io", Kind: "Role"},
	"rolebindings":        {Group: "rbac.authorization.k8s.io", Kind: "RoleBinding"},
	"clusterroles":        {Group: "rbac.authorization.k8s.io", Kind: "ClusterRole"},
	"clusterrolebindings": {Group: "rbac.authorization.k8s.io", Kind: "ClusterRoleBinding"},

	// Autoscaling & policy (#7).
	"horizontalpodautoscalers": {Group: "autoscaling", Kind: "HorizontalPodAutoscaler"},
	"poddisruptionbudgets":     {Group: "policy", Kind: "PodDisruptionBudget"},
	"resourcequotas":           {Group: "", Kind: "ResourceQuota"},
	"limitranges":              {Group: "", Kind: "LimitRange"},

	"certificatesigningrequests": {Group: "certificates.k8s.io", Kind: "CertificateSigningRequest"},

	"priorityclasses": {Group: "scheduling.k8s.io", Kind: "PriorityClass"},
	"runtimeclasses":  {Group: "node.k8s.io", Kind: "RuntimeClass"},
	"ingressclasses":  {Group: "networking.k8s.io", Kind: "IngressClass"},
	"leases":          {Group: "coordination.k8s.io", Kind: "Lease"},
}

// GroupKindFor returns the GroupKind for a kind key.
func GroupKindFor(kind string) (schema.GroupKind, bool) {
	gk, ok := groupKinds[kind]
	return gk, ok
}

// YAML renders a cached object as YAML. managedFields are already stripped by the
// engine transform; we deep-copy before stamping apiVersion/kind so the shared
// cache object is never mutated.
func YAML(obj any) (string, error) {
	ro, ok := obj.(runtime.Object)
	if !ok {
		return "", fmt.Errorf("object is not a runtime.Object (%T)", obj)
	}
	clone := ro.DeepCopyObject()
	if clone.GetObjectKind().GroupVersionKind().Empty() {
		if gvks, _, err := scheme.Scheme.ObjectKinds(clone); err == nil && len(gvks) > 0 {
			clone.GetObjectKind().SetGroupVersionKind(gvks[0])
		}
	}
	redactSecret(clone)
	b, err := yaml.Marshal(clone)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// redactSecret masks a Secret's values so the yaml view never leaks credentials.
// Each data value becomes "<redacted: N bytes>" and stringData is dropped. The
// argument is always a deep copy, so the shared cache object is unaffected. A
// no-op for every other type. The reveal drill-in (enter on a secret row) reads
// the real values straight from the cache and shows them only on demand.
func redactSecret(obj runtime.Object) {
	s, ok := obj.(*corev1.Secret)
	if !ok {
		return
	}
	for k, v := range s.Data {
		s.Data[k] = []byte(fmt.Sprintf("<redacted: %d bytes>", len(v)))
	}
	for k := range s.StringData {
		s.StringData[k] = "<redacted>"
	}
}

// Describe runs the kubectl describer for a kind and object, returning its text.
// It makes its own API calls (describe aggregates events and related objects), so
// callers should run it as a command, not inline in Update.
func Describe(cfg *rest.Config, kind, namespace, name string) (string, error) {
	gk, ok := GroupKindFor(kind)
	if !ok {
		return "", fmt.Errorf("describe not supported for %q", kind)
	}
	d, ok := describe.DescriberFor(gk, cfg)
	if !ok {
		return "", fmt.Errorf("no describer for %s", gk.Kind)
	}
	return d.Describe(namespace, name, describe.DescriberSettings{ShowEvents: true})
}
