package overview

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/14f3v/kubectl-tui/internal/tenant"
)

// Aggregator gathers the overview dashboard from one-shot cluster lists plus
// metrics. Every list is best-effort: an RBAC-forbidden or absent API just leaves
// that panel empty rather than failing the whole dashboard.
type Aggregator struct {
	cs      kubernetes.Interface
	mc      metricsclient.Interface
	tenants *tenant.Capsule
	sink    func(tea.Msg)
}

// NewAggregator builds an aggregator. mc and tenants may be nil.
func NewAggregator(cs kubernetes.Interface, mc metricsclient.Interface, tenants *tenant.Capsule, sink func(tea.Msg)) *Aggregator {
	return &Aggregator{cs: cs, mc: mc, tenants: tenants, sink: sink}
}

// Refresh gathers and emits a Snapshot. Intended to run from a command goroutine
// (it performs blocking API calls), not from Update.
func (a *Aggregator) Refresh(ctx context.Context) {
	now := time.Now()
	all := metav1.ListOptions{}

	var nodes []corev1.Node
	if nl, err := a.cs.CoreV1().Nodes().List(ctx, all); err == nil {
		nodes = nl.Items
	}
	var pods []corev1.Pod
	if pl, err := a.cs.CoreV1().Pods("").List(ctx, all); err == nil {
		pods = pl.Items
	}

	nodeCPUPct, nodeMemPct, usedCPU, usedMem, podUsage, hasMetrics := a.metrics(ctx, nodes)

	podsByNode := map[string]int{}
	for i := range pods {
		if n := pods[i].Spec.NodeName; n != "" {
			podsByNode[n]++
		}
	}

	running, pending, failed, _, phases := PodStats(pods)
	ready, total, cordoned, nodeRows := NodeStats(nodes, nodeCPUPct, nodeMemPct, hasMetrics, podsByNode)
	capacity := CapacityOf(nodes, usedCPU, usedMem, running+pending, hasMetrics)

	deploys := a.deployments(ctx)
	sts := a.statefulSets(ctx)
	ds := a.daemonSets(ctx)
	jobs := a.jobs(ctx)
	crons := a.cronJobs(ctx)

	var events []corev1.Event
	if el, err := a.cs.CoreV1().Events("").List(ctx, metav1.ListOptions{Limit: 300}); err == nil {
		events = el.Items
	}
	warningEvents := 0
	for i := range events {
		if events[i].Type == corev1.EventTypeWarning {
			warningEvents++
		}
	}

	tAvail, tTotal, tOver := false, 0, 0
	if a.tenants != nil {
		tAvail, tTotal, tOver = a.tenants.Summary(ctx)
	}

	alerts, aw, ac := AlertsFrom(failed, total-ready, warningEvents)

	a.sink(Snapshot{
		Loaded: true,
		KPIs: KPIs{
			NodesReady: ready, NodesTotal: total, NodesCordoned: cordoned,
			PodsRunning: running, PodsTotal: len(pods), PodsPending: pending, PodsFailed: failed,
			Tenants: tTotal, TenantsOverQuota: tOver, TenantsAvailable: tAvail,
			Alerts: alerts, AlertsWarning: aw, AlertsCritical: ac,
		},
		Capacity:  capacity,
		Nodes:     nodeRows,
		Phases:    phases,
		Workloads: Workloads(deploys, sts, ds, jobs, crons),
		TopCPU:    TopConsumers(podUsage, 5),
		Events:    RecentEvents(events, now, 6),
	})
}

// metrics collects node and pod usage; returns per-node utilization percentages,
// cluster totals, per-pod CPU (millis) keyed by ns/name, and availability.
func (a *Aggregator) metrics(ctx context.Context, nodes []corev1.Node) (cpuPct, memPct map[string]float64, usedCPU, usedMem int64, podUsage map[string]int64, ok bool) {
	cpuPct, memPct, podUsage = map[string]float64{}, map[string]float64{}, map[string]int64{}
	if a.mc == nil {
		return cpuPct, memPct, 0, 0, podUsage, false
	}
	allocCPU, allocMem := map[string]int64{}, map[string]int64{}
	for i := range nodes {
		allocCPU[nodes[i].Name] = nodes[i].Status.Allocatable.Cpu().MilliValue()
		allocMem[nodes[i].Name] = nodes[i].Status.Allocatable.Memory().Value()
	}
	nm, err := a.mc.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return cpuPct, memPct, 0, 0, podUsage, false
	}
	ok = true
	for _, m := range nm.Items {
		c := m.Usage.Cpu().MilliValue()
		mem := m.Usage.Memory().Value()
		usedCPU += c
		usedMem += mem
		if a := allocCPU[m.Name]; a > 0 {
			cpuPct[m.Name] = float64(c) / float64(a) * 100
		}
		if a := allocMem[m.Name]; a > 0 {
			memPct[m.Name] = float64(mem) / float64(a) * 100
		}
	}
	if pm, err := a.mc.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, m := range pm.Items {
			var c int64
			for _, ct := range m.Containers {
				c += ct.Usage.Cpu().MilliValue()
			}
			podUsage[m.Namespace+"/"+m.Name] = c
		}
	}
	return cpuPct, memPct, usedCPU, usedMem, podUsage, ok
}

func (a *Aggregator) deployments(ctx context.Context) []appsv1.Deployment {
	l, err := a.cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return l.Items
}

func (a *Aggregator) statefulSets(ctx context.Context) []appsv1.StatefulSet {
	l, err := a.cs.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return l.Items
}

func (a *Aggregator) daemonSets(ctx context.Context) []appsv1.DaemonSet {
	l, err := a.cs.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return l.Items
}

func (a *Aggregator) jobs(ctx context.Context) []batchv1.Job {
	l, err := a.cs.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return l.Items
}

func (a *Aggregator) cronJobs(ctx context.Context) []batchv1.CronJob {
	l, err := a.cs.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return l.Items
}
