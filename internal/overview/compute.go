// Package overview builds the cluster overview dashboard: KPI tiles, capacity
// bars, a node summary, pod-phase breakdown, workload readiness, top CPU
// consumers, and recent events. Data is gathered on a timer (like tenants) with
// one-shot lists rather than a permanent informer set, then reduced by the pure
// compute helpers here (which are unit-tested).
package overview

import (
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

// PodBucket is a coarse pod phase used by the dashboard.
type PodBucket int

const (
	BucketRunning PodBucket = iota
	BucketSucceeded
	BucketPending
	BucketFailed
)

// KPIs are the four headline tiles.
type KPIs struct {
	NodesReady, NodesTotal, NodesCordoned int
	PodsRunning, PodsTotal                int
	PodsPending, PodsFailed               int
	Tenants, TenantsOverQuota             int
	TenantsAvailable                      bool
	Alerts, AlertsWarning, AlertsCritical int
}

// Capacity is the CPU/MEM/PODS utilization panel.
type Capacity struct {
	CPUPct, MemPct, PodsPct    float64
	CPUText, MemText, PodsText string
	HasMetrics                 bool
}

// NodeRow is one row of the node summary.
type NodeRow struct {
	Name        string
	Status      string
	StatusClass columns.StatusClass
	CPUPct      float64
	MemPct      float64
	HasMetrics  bool
	Pods        string
}

// PhaseRow is one segment of the pod-phase breakdown.
type PhaseRow struct {
	Label string
	N     int
	Pct   float64
	Class columns.StatusClass
}

// Workload is one workload-kind readiness row.
type Workload struct {
	Name  string
	Ready int
	Total int
	Class columns.StatusClass
}

// Consumer is one top-CPU-consumer row.
type Consumer struct {
	Name   string
	Millis int64
}

// EventRow is one recent-event row.
type EventRow struct {
	Reason  string
	Message string
	Age     string
	Class   columns.StatusClass
}

// Snapshot is the fully computed dashboard, delivered to the UI as a tea.Msg.
type Snapshot struct {
	KPIs      KPIs
	Capacity  Capacity
	Nodes     []NodeRow
	Phases    []PhaseRow
	Workloads []Workload
	TopCPU    []Consumer
	Events    []EventRow
	Loaded    bool
}

// classifyPod buckets a pod for the phase breakdown and KPI counts.
func classifyPod(pod *corev1.Pod) PodBucket {
	if podFailed(pod) {
		return BucketFailed
	}
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		return BucketSucceeded
	case corev1.PodRunning:
		return BucketRunning
	default:
		return BucketPending
	}
}

func podFailed(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodFailed {
		return true
	}
	switch pod.Status.Reason {
	case "Evicted", "OOMKilled", "NodeLost":
		return true
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError":
				return true
			}
		}
		if t := cs.State.Terminated; t != nil && t.Reason == "OOMKilled" {
			return true
		}
	}
	return false
}

// PodStats reduces a pod list into phase counts and the phase breakdown.
func PodStats(pods []corev1.Pod) (running, pending, failed, succeeded int, phases []PhaseRow) {
	for i := range pods {
		switch classifyPod(&pods[i]) {
		case BucketRunning:
			running++
		case BucketSucceeded:
			succeeded++
		case BucketPending:
			pending++
		case BucketFailed:
			failed++
		}
	}
	total := len(pods)
	phases = []PhaseRow{
		{Label: "Running", N: running, Class: columns.StatusOK},
		{Label: "Succeeded", N: succeeded, Class: columns.StatusInfo},
		{Label: "Pending", N: pending, Class: columns.StatusWarn},
		{Label: "Failed", N: failed, Class: columns.StatusError},
	}
	for i := range phases {
		phases[i].Pct = pctOf(phases[i].N, total)
	}
	return running, pending, failed, succeeded, phases
}

// NodeStats reduces nodes (plus per-node usage and pod counts) into KPI counts
// and the node summary rows.
func NodeStats(nodes []corev1.Node, cpuPct, memPct map[string]float64, hasMetrics bool, podsByNode map[string]int) (ready, total, cordoned int, rows []NodeRow) {
	total = len(nodes)
	for i := range nodes {
		n := &nodes[i]
		status, class := nodeStatus(n)
		if status == "Ready" {
			ready++
		}
		if n.Spec.Unschedulable {
			cordoned++
		}
		podCap := n.Status.Allocatable.Pods().Value()
		pods := itoa(podsByNode[n.Name])
		if podCap > 0 {
			pods += "/" + itoa64(podCap)
		}
		rows = append(rows, NodeRow{
			Name:        n.Name,
			Status:      status,
			StatusClass: class,
			CPUPct:      cpuPct[n.Name],
			MemPct:      memPct[n.Name],
			HasMetrics:  hasMetrics,
			Pods:        pods,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return ready, total, cordoned, rows
}

func nodeStatus(n *corev1.Node) (string, columns.StatusClass) {
	isReady := false
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			isReady = c.Status == corev1.ConditionTrue
			break
		}
	}
	if !isReady {
		return "NotReady", columns.StatusError
	}
	if n.Spec.Unschedulable {
		return "Cordoned", columns.StatusWarn
	}
	return "Ready", columns.StatusOK
}

// Workloads reduces the workload kinds into readiness rows.
func Workloads(deploys []appsv1.Deployment, sts []appsv1.StatefulSet, ds []appsv1.DaemonSet, jobs []batchv1.Job, crons []batchv1.CronJob) []Workload {
	out := []Workload{}

	dReady, dTotal := 0, 0
	for i := range deploys {
		dTotal += int(replicas(deploys[i].Spec.Replicas))
		dReady += int(deploys[i].Status.ReadyReplicas)
	}
	out = append(out, workload("Deployments", dReady, dTotal))

	sReady, sTotal := 0, 0
	for i := range sts {
		sTotal += int(replicas(sts[i].Spec.Replicas))
		sReady += int(sts[i].Status.ReadyReplicas)
	}
	out = append(out, workload("StatefulSets", sReady, sTotal))

	dsReady, dsTotal := 0, 0
	for i := range ds {
		dsTotal += int(ds[i].Status.DesiredNumberScheduled)
		dsReady += int(ds[i].Status.NumberReady)
	}
	out = append(out, workload("DaemonSets", dsReady, dsTotal))

	jReady, jTotal := 0, 0
	for i := range jobs {
		jTotal++
		if jobs[i].Status.Succeeded > 0 {
			jReady++
		}
	}
	out = append(out, workload("Jobs", jReady, jTotal))

	out = append(out, workload("CronJobs", len(crons), len(crons)))
	return out
}

func workload(name string, ready, total int) Workload {
	class := columns.StatusOK
	switch {
	case total == 0:
		class = columns.StatusMuted
	case ready < total && float64(ready)/float64(total) < 0.5:
		class = columns.StatusWarn
	case ready < total:
		class = columns.StatusInfo
	}
	return Workload{Name: name, Ready: ready, Total: total, Class: class}
}

// CapacityOf computes CPU/MEM/PODS utilization from node allocatable, node usage,
// and the running pod count.
func CapacityOf(nodes []corev1.Node, usedCPUMillis, usedMemBytes int64, podCount int, hasMetrics bool) Capacity {
	var allocCPU, allocMem, allocPods int64
	for i := range nodes {
		alloc := nodes[i].Status.Allocatable
		allocCPU += alloc.Cpu().MilliValue()
		allocMem += alloc.Memory().Value()
		allocPods += alloc.Pods().Value()
	}
	cap := Capacity{HasMetrics: hasMetrics}
	if hasMetrics {
		cap.CPUPct = pctF(usedCPUMillis, allocCPU)
		cap.MemPct = pctF(usedMemBytes, allocMem)
		cap.CPUText = fmtCores(usedCPUMillis) + " / " + fmtCores(allocCPU) + " cores"
		cap.MemText = fmtGi(usedMemBytes) + " / " + fmtGi(allocMem)
	} else {
		cap.CPUText = "metrics n/a"
		cap.MemText = "metrics n/a"
	}
	cap.PodsPct = pctF(int64(podCount), allocPods)
	cap.PodsText = itoa(podCount) + " / " + itoa64(allocPods)
	return cap
}

// RecentEvents sorts events newest-first and returns the top n as rows.
func RecentEvents(events []corev1.Event, now time.Time, n int) []EventRow {
	sorted := make([]*corev1.Event, len(events))
	for i := range events {
		sorted[i] = &events[i]
	}
	sort.Slice(sorted, func(i, j int) bool { return eventTime(sorted[i]).After(eventTime(sorted[j])) })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	rows := make([]EventRow, 0, len(sorted))
	for _, e := range sorted {
		class := columns.StatusMuted
		if e.Type == corev1.EventTypeWarning {
			class = columns.StatusWarn
		}
		rows = append(rows, EventRow{
			Reason:  e.Reason,
			Message: e.InvolvedObject.Name + " · " + singleLine(e.Message),
			Age:     shortAge(now.Sub(eventTime(e))),
			Class:   class,
		})
	}
	return rows
}

// TopConsumers returns the n pods with the highest CPU usage.
func TopConsumers(podUsageMillis map[string]int64, n int) []Consumer {
	cons := make([]Consumer, 0, len(podUsageMillis))
	for k, v := range podUsageMillis {
		cons = append(cons, Consumer{Name: shortName(k), Millis: v})
	}
	sort.Slice(cons, func(i, j int) bool { return cons[i].Millis > cons[j].Millis })
	if len(cons) > n {
		cons = cons[:n]
	}
	return cons
}

func shortName(nsName string) string {
	if i := strings.IndexByte(nsName, '/'); i >= 0 {
		return nsName[i+1:]
	}
	return nsName
}

// AlertsFrom synthesizes alert counts from failed pods and unhealthy nodes,
// since there is no separate alerting system.
func AlertsFrom(failedPods, notReadyNodes int, warningEvents int) (total, warning, critical int) {
	critical = failedPods + notReadyNodes
	warning = warningEvents
	return warning + critical, warning, critical
}

// ---- small helpers ----

func replicas(r *int32) int32 {
	if r == nil {
		return 1
	}
	return *r
}

func pctOf(n, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

func pctF(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func fmtCores(millis int64) string {
	if millis < 1000 {
		return itoa(int(millis)) + "m"
	}
	return itoa(int(millis / 1000)) // whole cores for the summary
}

func fmtGi(bytes int64) string {
	gi := float64(bytes) / (1024 * 1024 * 1024)
	if gi < 10 {
		return trim1(gi) + "Gi"
	}
	return itoa(int(gi+0.5)) + "Gi"
}

func trim1(f float64) string {
	whole := int(f)
	frac := int((f-float64(whole))*10 + 0.5)
	if frac >= 10 {
		whole++
		frac = 0
	}
	return itoa(whole) + "." + itoa(frac)
}

func eventTime(e *corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if e.Series != nil && !e.Series.LastObservedTime.IsZero() {
		return e.Series.LastObservedTime.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}

func shortAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d.Seconds())
	switch {
	case s < 60:
		return itoa64(s) + "s"
	case s < 3600:
		return itoa64(s/60) + "m"
	case s < 86400:
		return itoa64(s/3600) + "h"
	default:
		return itoa64(s/86400) + "d"
	}
}

func singleLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

func itoa(n int) string { return itoa64(int64(n)) }
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
