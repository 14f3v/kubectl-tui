// Package inspect implements the read-only inspect actions: rendering a cached
// object as YAML and running kubectl-style describe. Both return plain text that
// the caller shows in a scrollable text page.
package inspect

import (
	"fmt"

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
	b, err := yaml.Marshal(clone)
	if err != nil {
		return "", err
	}
	return string(b), nil
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
