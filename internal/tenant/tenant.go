// Package tenant renders a multi-tenancy dashboard backed by the Capsule operator
// (capsule.clastix.io). It reads Tenant objects through the dynamic client (never
// importing the Capsule Go module, which drags controller-runtime) and aggregates
// per-tenant CPU/MEM usage against quota. Quota usage is refreshed on a timer, not
// on Tenant watch events, because ResourceQuota status.used changes emit no Tenant
// events.
package tenant

import "github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine/columns"

// Tenant is the decoded subset of a Capsule Tenant we care about.
type Tenant struct {
	Name       string
	State      string   // status.state
	Tier       string   // from a label (configurable key)
	Owners     []string // spec.owners[].name
	Namespaces []string // status.namespaces (fallback status.spaces)
	Size       int      // status.size (namespace count)
	Cordoned   bool     // spec.cordoned
	HardCPU    int64    // summed spec.resourceQuotas hard cpu (millicores), 0 = none
	HardMem    int64    // summed spec.resourceQuotas hard memory (bytes), 0 = none
}

// Used is a tenant's aggregated ResourceQuota usage summed across its namespaces.
type Used struct {
	CPUMillis int64
	MemBytes  int64
	Pods      int64
}

// View is a fully computed dashboard row.
type View struct {
	Name        string
	Tier        string
	Owner       string
	NS          int
	Pods        int
	CPUUsed     int64 // millicores
	CPUQuota    int64 // millicores (0 = unquota'd)
	MemUsed     int64 // bytes
	MemQuota    int64 // bytes
	Status      string
	StatusClass columns.StatusClass
}

// Snapshot is the provider's output message: the dashboard rows plus availability.
type Snapshot struct {
	Available bool
	Reason    string // "not installed" | "forbidden" when unavailable
	Views     []View
}

// Aggregate computes dashboard rows from decoded tenants and their summed quota
// usage. It applies the status precedence: Terminating → Cordoned → Over quota →
// Throttled (≥80%) → Idle (no pods) → Healthy.
func Aggregate(tenants []Tenant, usedByTenant map[string]Used) []View {
	out := make([]View, 0, len(tenants))
	for _, t := range tenants {
		used := usedByTenant[t.Name]
		v := View{
			Name:     t.Name,
			Tier:     t.Tier,
			Owner:    firstOwner(t.Owners),
			NS:       t.Size,
			Pods:     int(used.Pods),
			CPUUsed:  used.CPUMillis,
			CPUQuota: t.HardCPU,
			MemUsed:  used.MemBytes,
			MemQuota: t.HardMem,
		}
		v.Status, v.StatusClass = status(t, used)
		out = append(out, v)
	}
	return out
}

func status(t Tenant, used Used) (string, columns.StatusClass) {
	switch {
	case t.State == "Terminating":
		return "Terminating", columns.StatusMuted
	case t.Cordoned:
		return "Cordoned", columns.StatusMuted
	case overQuota(used.CPUMillis, t.HardCPU) || overQuota(used.MemBytes, t.HardMem):
		return "Over quota", columns.StatusError
	case throttled(used.CPUMillis, t.HardCPU) || throttled(used.MemBytes, t.HardMem):
		return "Throttled", columns.StatusWarn
	case used.Pods == 0:
		return "Idle", columns.StatusMuted
	default:
		return "Healthy", columns.StatusOK
	}
}

func overQuota(used, hard int64) bool { return hard > 0 && used >= hard }

func throttled(used, hard int64) bool {
	return hard > 0 && float64(used) >= 0.8*float64(hard)
}

func firstOwner(owners []string) string {
	if len(owners) == 0 {
		return "—"
	}
	return owners[0]
}
