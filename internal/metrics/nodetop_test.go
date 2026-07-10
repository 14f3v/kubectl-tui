package metrics

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// nodeMetricsGVR is the resource the metrics fake serves NodeMetricses from. The
// generated client lists them under group metrics.k8s.io, resource "nodes" (not
// the kind-derived "nodemetrics"), so tracker fixtures must be added under this
// GVR for NodeMetricses().List to find them.
var nodeMetricsGVR = schema.GroupVersionResource{
	Group:    metricsv1beta1.SchemeGroupVersion.Group,
	Version:  metricsv1beta1.SchemeGroupVersion.Version,
	Resource: "nodes",
}

// newMetricsClient builds a metrics fake seeded with the given NodeMetrics under
// the GVR its lister queries.
func newMetricsClient(t *testing.T, items ...*metricsv1beta1.NodeMetrics) *metricsfake.Clientset {
	t.Helper()
	mc := metricsfake.NewSimpleClientset()
	for _, nm := range items {
		if err := mc.Tracker().Create(nodeMetricsGVR, nm, ""); err != nil {
			t.Fatalf("seed NodeMetrics %q: %v", nm.Name, err)
		}
	}
	return mc
}

// approxEq compares two percentages within a small tolerance.
func approxEq(a, b float64) bool {
	d := a - b
	return d < 0.05 && d > -0.05
}

func TestNodeUsageFrom_Normal(t *testing.T) {
	alloc := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"),      // 4000m
		corev1.ResourceMemory: resource.MustParse("8192Mi"), // 8 Gi
	}
	// 1000m of 4000m = 25%; 2048Mi of 8192Mi = 25%.
	u := nodeUsageFrom("node-a", 1000, 2048*1024*1024, alloc)

	if u.Name != "node-a" {
		t.Errorf("Name = %q, want node-a", u.Name)
	}
	if u.CPUMillis != 1000 {
		t.Errorf("CPUMillis = %d, want 1000", u.CPUMillis)
	}
	if u.MemBytes != 2048*1024*1024 {
		t.Errorf("MemBytes = %d, want %d", u.MemBytes, 2048*1024*1024)
	}
	if !approxEq(u.CPUPct, 25) {
		t.Errorf("CPUPct = %.2f, want 25", u.CPUPct)
	}
	if !approxEq(u.MemPct, 25) {
		t.Errorf("MemPct = %.2f, want 25", u.MemPct)
	}
}

func TestNodeUsageFrom_ZeroAllocatableGuard(t *testing.T) {
	// Allocatable present but zero must not divide-by-zero; pct guards it to 0.
	alloc := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("0"),
		corev1.ResourceMemory: resource.MustParse("0"),
	}
	u := nodeUsageFrom("node-z", 500, 1024*1024, alloc)
	if u.CPUPct != 0 {
		t.Errorf("CPUPct = %.2f, want 0", u.CPUPct)
	}
	if u.MemPct != 0 {
		t.Errorf("MemPct = %.2f, want 0", u.MemPct)
	}
	// Raw usage is still reported even when percentages can't be computed.
	if u.CPUMillis != 500 || u.MemBytes != 1024*1024 {
		t.Errorf("usage not preserved: cpu=%d mem=%d", u.CPUMillis, u.MemBytes)
	}
}

func TestNodeUsageFrom_MissingResource(t *testing.T) {
	// Only CPU is declared; memory absent from Allocatable -> MemPct 0, CPUPct real.
	alloc := corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"), // 2000m
	}
	u := nodeUsageFrom("node-m", 1000, 4096, alloc)
	if !approxEq(u.CPUPct, 50) {
		t.Errorf("CPUPct = %.2f, want 50", u.CPUPct)
	}
	if u.MemPct != 0 {
		t.Errorf("MemPct = %.2f, want 0 (memory allocatable missing)", u.MemPct)
	}
}

func TestNodeUsageFrom_NilAllocatable(t *testing.T) {
	// A node with no Allocatable at all (join miss) still yields usage, 0% both.
	u := nodeUsageFrom("orphan", 250, 512, nil)
	if u.CPUPct != 0 || u.MemPct != 0 {
		t.Errorf("nil allocatable pct = (%.2f,%.2f), want (0,0)", u.CPUPct, u.MemPct)
	}
	if u.CPUMillis != 250 || u.MemBytes != 512 {
		t.Errorf("usage not preserved: cpu=%d mem=%d", u.CPUMillis, u.MemBytes)
	}
}

// nodeMetric is a small helper to build a NodeMetrics fixture.
func nodeMetric(name, cpu, mem string) *metricsv1beta1.NodeMetrics {
	return &metricsv1beta1.NodeMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Usage: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(mem),
		},
	}
}

// node builds a Node fixture with the given Allocatable.
func node(name, cpu, mem string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
			},
		},
	}
}

func TestCollectNodes_JoinsAndSorts(t *testing.T) {
	mc := newMetricsClient(t,
		nodeMetric("node-b", "1000m", "2048Mi"),
		nodeMetric("node-a", "500m", "1024Mi"),
	)
	cs := kubefake.NewClientset(
		node("node-a", "2", "4096Mi"), // 500/2000=25%, 1024/4096=25%
		node("node-b", "4", "8192Mi"), // 1000/4000=25%, 2048/8192=25%
	)

	got, err := CollectNodes(context.Background(), cs, mc)
	if err != nil {
		t.Fatalf("CollectNodes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Sorted by name.
	if got[0].Name != "node-a" || got[1].Name != "node-b" {
		t.Fatalf("order = [%s %s], want [node-a node-b]", got[0].Name, got[1].Name)
	}
	if got[0].CPUMillis != 500 || got[0].MemBytes != 1024*1024*1024 {
		t.Errorf("node-a usage = %dm/%d", got[0].CPUMillis, got[0].MemBytes)
	}
	if !approxEq(got[0].CPUPct, 25) || !approxEq(got[0].MemPct, 25) {
		t.Errorf("node-a pct = (%.2f,%.2f), want (25,25)", got[0].CPUPct, got[0].MemPct)
	}
	if !approxEq(got[1].CPUPct, 25) || !approxEq(got[1].MemPct, 25) {
		t.Errorf("node-b pct = (%.2f,%.2f), want (25,25)", got[1].CPUPct, got[1].MemPct)
	}
}

func TestCollectNodes_MissingNodeYieldsZeroPct(t *testing.T) {
	// Metrics exist for a node that isn't in the Node list (join miss). Usage is
	// still surfaced; percentages fall to 0 instead of panicking.
	mc := newMetricsClient(t, nodeMetric("ghost", "750m", "1500Mi"))
	cs := kubefake.NewClientset() // no nodes

	got, err := CollectNodes(context.Background(), cs, mc)
	if err != nil {
		t.Fatalf("CollectNodes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Name != "ghost" || got[0].CPUMillis != 750 {
		t.Errorf("unexpected usage: %+v", got[0])
	}
	if got[0].CPUPct != 0 || got[0].MemPct != 0 {
		t.Errorf("pct = (%.2f,%.2f), want (0,0)", got[0].CPUPct, got[0].MemPct)
	}
}

func TestCollectNodes_MetricsErrorPropagates(t *testing.T) {
	// Simulate metrics-server list failure: CollectNodes must return the error so
	// the UI can degrade (it already probes first, so this is defense in depth).
	mc := metricsfake.NewSimpleClientset()
	mc.PrependReactor("list", "nodes", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("metrics.k8s.io not available")
	})
	cs := kubefake.NewClientset()

	if _, err := CollectNodes(context.Background(), cs, mc); err == nil {
		t.Fatal("expected error when metrics list fails, got nil")
	}
}
