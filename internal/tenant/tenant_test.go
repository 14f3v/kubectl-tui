package tenant

import (
	"testing"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

const (
	cpu20 = 20 * 1000 // 20 cores in millicores
	gi64  = 64 * 1024 * 1024 * 1024
)

func TestAggregateStatus(t *testing.T) {
	tenants := []Tenant{
		{Name: "healthy", Tier: "gold", Owners: []string{"fin"}, Size: 3, HardCPU: cpu20, HardMem: gi64},
		{Name: "throttled", HardCPU: cpu20, HardMem: gi64},
		{Name: "over", HardCPU: cpu20, HardMem: gi64},
		{Name: "idle", HardCPU: cpu20, HardMem: gi64},
		{Name: "cordoned", Cordoned: true, HardCPU: cpu20, HardMem: gi64},
		{Name: "terminating", State: "Terminating", HardCPU: cpu20, HardMem: gi64},
		{Name: "unquota'd"}, // no hard limits
	}
	used := map[string]Used{
		"healthy":   {CPUMillis: 5 * 1000, MemBytes: 10 * 1024 * 1024 * 1024, Pods: 12},
		"throttled": {CPUMillis: 17 * 1000, Pods: 20}, // 85% of 20 cores
		"over":      {CPUMillis: 21 * 1000, Pods: 30}, // >100%
		"idle":      {CPUMillis: 0, Pods: 0},          // no pods
		"cordoned":  {CPUMillis: 1 * 1000, Pods: 4},
		"unquota'd": {CPUMillis: 3 * 1000, Pods: 5},
	}

	views := Aggregate(tenants, used)
	byName := map[string]View{}
	for _, v := range views {
		byName[v.Name] = v
	}

	want := map[string]struct {
		status string
		class  columns.StatusClass
	}{
		"healthy":     {"Healthy", columns.StatusOK},
		"throttled":   {"Throttled", columns.StatusWarn},
		"over":        {"Over quota", columns.StatusError},
		"idle":        {"Idle", columns.StatusMuted},
		"cordoned":    {"Cordoned", columns.StatusMuted},
		"terminating": {"Terminating", columns.StatusMuted},
		"unquota'd":   {"Healthy", columns.StatusOK}, // pods>0, no quota to breach
	}
	for name, w := range want {
		got := byName[name]
		if got.Status != w.status || got.StatusClass != w.class {
			t.Errorf("%s: status=%q/%v, want %q/%v", name, got.Status, got.StatusClass, w.status, w.class)
		}
	}

	// Field mapping sanity.
	h := byName["healthy"]
	if h.Tier != "gold" || h.Owner != "fin" || h.NS != 3 || h.Pods != 12 {
		t.Fatalf("healthy view fields = %+v", h)
	}
	if h.CPUUsed != 5*1000 || h.CPUQuota != cpu20 {
		t.Fatalf("healthy cpu = %d/%d", h.CPUUsed, h.CPUQuota)
	}
}

func TestFirstOwnerFallback(t *testing.T) {
	if got := firstOwner(nil); got != "—" {
		t.Fatalf("no owners = %q, want —", got)
	}
}
