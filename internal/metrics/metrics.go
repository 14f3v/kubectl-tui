// Package metrics polls metrics.k8s.io for pod and node usage. Metrics are
// optional: when metrics-server is absent the prober reports unavailable and the
// UI hides the CPU/MEM columns and header gauges rather than showing zeros.
package metrics

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const groupVersion = "metrics.k8s.io/v1beta1"

// PodUsage is a pod's summed container usage.
type PodUsage struct {
	CPUMillis int64
	MemBytes  int64
}

// Snapshot is the poller's output message: per-pod usage keyed by
// "namespace/name", plus cluster-wide CPU/MEM utilization percentages. It doubles
// as the tea.Msg delivered to the UI.
type Snapshot struct {
	Available     bool
	Pods          map[string]PodUsage
	ClusterCPUPct float64
	ClusterMemPct float64
}

// Probe reports whether the metrics API is served. The reason is a short label
// ("not installed") for the UI when it is not.
func Probe(disco discovery.DiscoveryInterface) (bool, string) {
	res, err := disco.ServerResourcesForGroupVersion(groupVersion)
	if err != nil || res == nil || len(res.APIResources) == 0 {
		return false, "not installed"
	}
	return true, ""
}

// Collect fetches one metrics snapshot for the namespace (empty = all). Cluster
// utilization is computed from node usage over node allocatable capacity.
func Collect(ctx context.Context, cs kubernetes.Interface, mc metricsclient.Interface, namespace string) (Snapshot, error) {
	snap := Snapshot{Available: true, Pods: map[string]PodUsage{}}

	podMetrics, err := mc.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Snapshot{Available: false}, err
	}
	for i := range podMetrics.Items {
		pm := &podMetrics.Items[i]
		snap.Pods[pm.Namespace+"/"+pm.Name] = podUsage(pm)
	}

	// Cluster utilization: sum node usage / sum node allocatable.
	nodeMetrics, err := mc.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err == nil {
		var usedCPU, usedMem int64
		for _, nm := range nodeMetrics.Items {
			usedCPU += nm.Usage.Cpu().MilliValue()
			usedMem += nm.Usage.Memory().Value()
		}
		if nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
			var allocCPU, allocMem int64
			for i := range nodes.Items {
				alloc := nodes.Items[i].Status.Allocatable
				allocCPU += quantityMillis(alloc, corev1.ResourceCPU)
				allocMem += quantityValue(alloc, corev1.ResourceMemory)
			}
			snap.ClusterCPUPct = pct(usedCPU, allocCPU)
			snap.ClusterMemPct = pct(usedMem, allocMem)
		}
	}
	return snap, nil
}

// podUsage sums a pod's container CPU (millicores) and memory (bytes).
func podUsage(pm *metricsv1beta1.PodMetrics) PodUsage {
	var cpu, mem int64
	for _, c := range pm.Containers {
		cpu += c.Usage.Cpu().MilliValue()
		mem += c.Usage.Memory().Value()
	}
	return PodUsage{CPUMillis: cpu, MemBytes: mem}
}

// pct is used/total as a percentage, guarding a zero total.
func pct(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
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

// FormatCPU renders millicores the way kubectl top does: "142m", or cores with
// one decimal once past a full core.
func FormatCPU(millis int64) string {
	if millis <= 0 {
		return "0"
	}
	if millis < 1000 {
		return fmt.Sprintf("%dm", millis)
	}
	return fmt.Sprintf("%.1f", float64(millis)/1000)
}

// FormatMem renders bytes in binary units (Mi/Gi), matching kubectl.
func FormatMem(bytes int64) string {
	q := resource.NewQuantity(bytes, resource.BinarySI)
	const mi = 1024 * 1024
	switch {
	case bytes >= 1024*mi:
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(1024*mi))
	case bytes >= mi:
		return fmt.Sprintf("%dMi", bytes/mi)
	default:
		return q.String()
	}
}
