// Package inspect implements the inspect actions: rendering a cached object as
// YAML and running kubectl-style describe. Both return plain text that the
// caller shows in a scrollable text page. EditYAML renders the same YAML for the
// edit buffer, and for a Secret it also returns a Restore that puts the masked
// values back into the saved buffer before the edit is applied.
package inspect

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
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

	// Remaining built-in kinds (#27).
	"validatingwebhookconfigurations":   {Group: "admissionregistration.k8s.io", Kind: "ValidatingWebhookConfiguration"},
	"mutatingwebhookconfigurations":     {Group: "admissionregistration.k8s.io", Kind: "MutatingWebhookConfiguration"},
	"validatingadmissionpolicies":       {Group: "admissionregistration.k8s.io", Kind: "ValidatingAdmissionPolicy"},
	"validatingadmissionpolicybindings": {Group: "admissionregistration.k8s.io", Kind: "ValidatingAdmissionPolicyBinding"},
	"mutatingadmissionpolicies":         {Group: "admissionregistration.k8s.io", Kind: "MutatingAdmissionPolicy"},
	"mutatingadmissionpolicybindings":   {Group: "admissionregistration.k8s.io", Kind: "MutatingAdmissionPolicyBinding"},
	"flowschemas":                       {Group: "flowcontrol.apiserver.k8s.io", Kind: "FlowSchema"},
	"prioritylevelconfigurations":       {Group: "flowcontrol.apiserver.k8s.io", Kind: "PriorityLevelConfiguration"},
	"csidrivers":                        {Group: "storage.k8s.io", Kind: "CSIDriver"},
	"csinodes":                          {Group: "storage.k8s.io", Kind: "CSINode"},
	"volumeattachments":                 {Group: "storage.k8s.io", Kind: "VolumeAttachment"},
	"csistoragecapacities":              {Group: "storage.k8s.io", Kind: "CSIStorageCapacity"},
}

// GroupKindFor returns the GroupKind for a kind key.
func GroupKindFor(kind string) (schema.GroupKind, bool) {
	gk, ok := groupKinds[kind]
	return gk, ok
}

// YAML renders a cached object as YAML. managedFields are already stripped by the
// engine transform; we deep-copy before stamping apiVersion/kind so the shared
// cache object is never mutated. A core Secret, typed from the cache or
// unstructured from the dynamic client, renders with its values masked (see
// secretYAML).
func YAML(obj any) (string, error) {
	s, _, err := render(obj)
	return s, err
}

// EditYAML renders an object for the edit buffer. The text is what YAML shows,
// so a Secret's values are masked there too and never reach the temp file the
// editor gets. For a Secret it also returns a Restore that holds the masked
// values in memory and puts them back into the saved buffer before it is
// applied. For any other object the Restore is nil and the buffer is applied
// as saved.
func EditYAML(obj any) (string, Restore, error) {
	s, masked, err := render(obj)
	if err != nil || masked == nil {
		return s, nil, err
	}
	return s, masked.restore, nil
}

// render is the body of YAML and EditYAML. It returns, for a Secret, the values
// it masked.
func render(obj any) (string, *secretValues, error) {
	ro, ok := obj.(runtime.Object)
	if !ok {
		return "", nil, fmt.Errorf("object is not a runtime.Object (%T)", obj)
	}
	clone := ro.DeepCopyObject()
	if clone.GetObjectKind().GroupVersionKind().Empty() {
		if gvks, _, err := scheme.Scheme.ObjectKinds(clone); err == nil && len(gvks) > 0 {
			clone.GetObjectKind().SetGroupVersionKind(gvks[0])
		}
	}
	if isSecret(clone) {
		return secretYAML(clone)
	}
	b, err := yaml.Marshal(clone)
	if err != nil {
		return "", nil, err
	}
	return string(b), nil, nil
}

// Describe runs the kubectl describer for a kind and object, returning its text.
// It makes its own API calls (describe aggregates events and related objects), so
// callers should run it as a command, not inline in Update. A Secret is
// described with every value shown as its size (see secretDescriber).
func Describe(cfg *rest.Config, kind, namespace, name string) (string, error) {
	gk, ok := GroupKindFor(kind)
	if !ok {
		return "", fmt.Errorf("describe not supported for %q", kind)
	}
	var d describe.ResourceDescriber
	if gk == (schema.GroupKind{Group: "", Kind: "Secret"}) {
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return "", err
		}
		d = secretDescriber(cs)
	} else if d, ok = describe.DescriberFor(gk, cfg); !ok {
		return "", fmt.Errorf("no describer for %s", gk.Kind)
	}
	return d.Describe(namespace, name, describe.DescriberSettings{ShowEvents: true})
}
