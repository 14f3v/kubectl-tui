package metrics

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// NodeUsage is one node's current usage, mirroring "kubectl top node". CPUPct and
// MemPct are usage over that node's Allocatable capacity (0 when Allocatable is
// missing or zero, never a divide-by-zero).
type NodeUsage struct {
	Name      string
	CPUMillis int64
	MemBytes  int64
	CPUPct    float64
	MemPct    float64
}

// CollectNodes fetches per-node usage from metrics.k8s.io and joins it to each
// node's Allocatable capacity to compute utilization percentages. The result is
// sorted by node name. If the metrics list fails the error is returned so the UI
// can fall back to its unavailable state; the UI probes with Probe first, so an
// absent metrics-server never reaches here.
func CollectNodes(ctx context.Context, cs kubernetes.Interface, mc metricsclient.Interface) ([]NodeUsage, error) {
	nodeMetrics, err := mc.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// Index node Allocatable by name so we can join without an O(n*m) scan. A
	// failed node list is tolerated: usage is still reported, just without
	// percentages (empty Allocatable yields 0%, like the zero-total guard in pct).
	alloc := map[string]corev1.ResourceList{}
	if nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		for i := range nodes.Items {
			alloc[nodes.Items[i].Name] = nodes.Items[i].Status.Allocatable
		}
	}

	out := make([]NodeUsage, 0, len(nodeMetrics.Items))
	for i := range nodeMetrics.Items {
		nm := &nodeMetrics.Items[i]
		out = append(out, nodeUsageFrom(
			nm.Name,
			nm.Usage.Cpu().MilliValue(),
			nm.Usage.Memory().Value(),
			alloc[nm.Name],
		))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// nodeUsageFrom builds a NodeUsage from raw usage values and a node's Allocatable
// list, doing the per-node percentage math. It is pure (no cluster access) so it
// can be unit-tested directly. It reuses the package quantity/pct helpers, so a
// missing CPU or memory entry in allocatable is treated as a zero total and its
// percentage is 0 rather than a panic.
func nodeUsageFrom(name string, usageCPUmillis, usageMemBytes int64, allocatable corev1.ResourceList) NodeUsage {
	return NodeUsage{
		Name:      name,
		CPUMillis: usageCPUmillis,
		MemBytes:  usageMemBytes,
		CPUPct:    pct(usageCPUmillis, quantityMillis(allocatable, corev1.ResourceCPU)),
		MemPct:    pct(usageMemBytes, quantityValue(allocatable, corev1.ResourceMemory)),
	}
}
