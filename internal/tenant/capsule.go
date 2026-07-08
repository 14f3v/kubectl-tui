package tenant

import (
	"context"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Capsule label keys used to associate ResourceQuotas with a tenant. The newer
// key is preferred; the legacy key is a fallback for older installs.
const (
	labelTenantNew = "projectcapsule.dev/tenant"
	labelTenantOld = "capsule.clastix.io/tenant"
)

// tenantGVRs are tried in order; v1beta2 is the current storage version.
var tenantGVRs = []schema.GroupVersionResource{
	{Group: "capsule.clastix.io", Version: "v1beta2", Resource: "tenants"},
	{Group: "capsule.clastix.io", Version: "v1beta1", Resource: "tenants"},
}

// Capsule is the Capsule-backed TenantProvider. It never imports the Capsule Go
// module: Tenants are decoded from unstructured, tolerating version skew.
type Capsule struct {
	dyn       dynamic.Interface
	cs        kubernetes.Interface
	sink      func(tea.Msg)
	tierLabel string
	gvr       schema.GroupVersionResource
}

// NewCapsule builds a provider. tierLabel is the label key used for the TIER
// column (default "tier").
func NewCapsule(dyn dynamic.Interface, cs kubernetes.Interface, sink func(tea.Msg), tierLabel string) *Capsule {
	if tierLabel == "" {
		tierLabel = "tier"
	}
	return &Capsule{dyn: dyn, cs: cs, sink: sink, tierLabel: tierLabel}
}

// Available reports whether Capsule's Tenant CRD is served, resolving the GVR.
// The reason distinguishes "not installed" from "forbidden".
func (c *Capsule) Available(disco discovery.DiscoveryInterface) (bool, string) {
	for _, gvr := range tenantGVRs {
		res, err := disco.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
		if err != nil {
			continue
		}
		for _, r := range res.APIResources {
			if r.Name == "tenants" {
				c.gvr = gvr
				return true, ""
			}
		}
	}
	return false, "not installed"
}

// Refresh lists tenants and their quota usage and emits a Snapshot. It is the one
// entry point the page calls on enter and on its 30s tick.
func (c *Capsule) Refresh(ctx context.Context) {
	if c.gvr.Resource == "" {
		if ok, reason := c.Available(c.discovery()); !ok {
			c.sink(Snapshot{Available: false, Reason: reason})
			return
		}
	}
	tenants, err := c.listTenants(ctx)
	if err != nil {
		reason := "unavailable"
		if apierrors.IsForbidden(err) {
			reason = "forbidden"
		} else if apierrors.IsNotFound(err) {
			reason = "not installed"
		}
		c.sink(Snapshot{Available: false, Reason: reason})
		return
	}
	used := c.aggregateQuotas(ctx)
	c.sink(Snapshot{Available: true, Views: Aggregate(tenants, used)})
}

func (c *Capsule) discovery() discovery.DiscoveryInterface {
	return c.cs.Discovery()
}

func (c *Capsule) listTenants(ctx context.Context) ([]Tenant, error) {
	list, err := c.dyn.Resource(c.gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	tenants := make([]Tenant, 0, len(list.Items))
	for i := range list.Items {
		tenants = append(tenants, decodeTenant(&list.Items[i], c.tierLabel))
	}
	return tenants, nil
}

// aggregateQuotas lists Capsule-managed ResourceQuotas cluster-wide and sums
// status.used per tenant. On a cluster-wide 403 it returns an empty map (the
// dashboard then shows tenants without quota columns).
func (c *Capsule) aggregateQuotas(ctx context.Context) map[string]Used {
	used := map[string]Used{}
	for _, label := range []string{labelTenantNew, labelTenantOld} {
		rqs, err := c.cs.CoreV1().ResourceQuotas(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: label})
		if err != nil {
			continue
		}
		if len(rqs.Items) == 0 {
			continue
		}
		for i := range rqs.Items {
			rq := &rqs.Items[i]
			tenant := rq.Labels[label]
			if tenant == "" {
				continue
			}
			u := used[tenant]
			u.CPUMillis += quantityMillis(rq.Status.Used, corev1.ResourceCPU)
			u.MemBytes += quantityValue(rq.Status.Used, corev1.ResourceMemory)
			u.Pods += quantityValue(rq.Status.Used, corev1.ResourcePods)
			used[tenant] = u
		}
		break // the first label that yields quotas wins
	}
	return used
}

// decodeTenant extracts the fields we display from an unstructured Tenant,
// tolerating older status shapes (namespaces vs spaces).
func decodeTenant(u *unstructured.Unstructured, tierLabel string) Tenant {
	obj := u.Object
	t := Tenant{Name: u.GetName()}

	if v, ok := u.GetLabels()[tierLabel]; ok {
		t.Tier = v
	} else if v, ok := u.GetLabels()["capsule.clastix.io/tier"]; ok {
		t.Tier = v
	}

	t.State, _, _ = unstructured.NestedString(obj, "status", "state")
	t.Cordoned, _, _ = unstructured.NestedBool(obj, "spec", "cordoned")

	if ns, ok, _ := unstructured.NestedStringSlice(obj, "status", "namespaces"); ok {
		t.Namespaces = ns
	} else if sp, ok, _ := unstructured.NestedStringSlice(obj, "status", "spaces"); ok {
		t.Namespaces = sp
	}
	if size, ok, _ := unstructured.NestedInt64(obj, "status", "size"); ok {
		t.Size = int(size)
	} else {
		t.Size = len(t.Namespaces)
	}

	if owners, ok, _ := unstructured.NestedSlice(obj, "spec", "owners"); ok {
		for _, o := range owners {
			if om, ok := o.(map[string]any); ok {
				if name, ok := om["name"].(string); ok {
					t.Owners = append(t.Owners, name)
				}
			}
		}
	}

	// Hard limits: sum cpu/memory across spec.resourceQuotas.items[].hard.
	if items, ok, _ := unstructured.NestedSlice(obj, "spec", "resourceQuotas", "items"); ok {
		for _, it := range items {
			im, ok := it.(map[string]any)
			if !ok {
				continue
			}
			hard, ok, _ := unstructured.NestedStringMap(im, "hard")
			if !ok {
				continue
			}
			if q, err := resource.ParseQuantity(hard["cpu"]); err == nil {
				t.HardCPU += q.MilliValue()
			}
			if q, err := resource.ParseQuantity(hard["memory"]); err == nil {
				t.HardMem += q.Value()
			}
		}
	}
	return t
}

func quantityMillis(rl corev1.ResourceList, name corev1.ResourceName) int64 {
	if q, ok := rl[name]; ok {
		return q.MilliValue()
	}
	return 0
}

func quantityValue(rl corev1.ResourceList, name corev1.ResourceName) int64 {
	if q, ok := rl[name]; ok {
		return q.Value()
	}
	return 0
}
