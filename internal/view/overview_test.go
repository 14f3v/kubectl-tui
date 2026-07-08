package view

import (
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/overview"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func TestOverviewPageRender(t *testing.T) {
	p := &overviewPage{
		theme:  style.Default(),
		loaded: true,
		snap: overview.Snapshot{
			Loaded: true,
			KPIs: overview.KPIs{
				NodesReady: 5, NodesTotal: 6, NodesCordoned: 1,
				PodsRunning: 231, PodsTotal: 260, PodsPending: 6, PodsFailed: 4,
				Tenants: 8, TenantsOverQuota: 1, TenantsAvailable: true,
				Alerts: 3, AlertsWarning: 2, AlertsCritical: 1,
			},
			Capacity: overview.Capacity{
				CPUPct: 52, MemPct: 63, PodsPct: 89, HasMetrics: true,
				CPUText: "41 / 80 cores", MemText: "410 / 650Gi", PodsText: "231 / 260",
			},
			Nodes: []overview.NodeRow{
				{Name: "ip-10-0-1-45", Status: "Ready", StatusClass: columns.StatusOK, CPUPct: 58, MemPct: 71, HasMetrics: true, Pods: "42/60"},
				{Name: "ip-10-0-4-08", Status: "Cordoned", StatusClass: columns.StatusWarn, Pods: "8/60"},
			},
			Phases: []overview.PhaseRow{
				{Label: "Running", N: 231, Pct: 88, Class: columns.StatusOK},
				{Label: "Failed", N: 4, Pct: 2, Class: columns.StatusError},
			},
			Workloads: []overview.Workload{
				{Name: "Deployments", Ready: 32, Total: 34, Class: columns.StatusInfo},
			},
			TopCPU: []overview.Consumer{
				{Name: "fraud-model-serve", Millis: 890},
			},
			Events: []overview.EventRow{
				{Reason: "BackOff", Message: "notifications · restart", Age: "12s", Class: columns.StatusError},
			},
		},
	}
	out := p.View(140, 60)
	for _, want := range []string{
		"NODES READY", "PODS RUNNING", "TENANTS", "ALERTS",
		"CLUSTER CAPACITY", "NODES", "POD PHASES", "WORKLOADS", "TOP CPU", "RECENT EVENTS",
		"231/260", "Deployments", "fraud-model", "BackOff",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("overview render missing %q", want)
		}
	}

	// The Summary maps pod counts for the chrome.
	s := p.Summary()
	if s.Total != 260 || s.OK != 231 || s.Err != 4 {
		t.Fatalf("summary = %+v, want 260/231ok/4err", s)
	}
}

func TestOverviewLoadingRender(t *testing.T) {
	p := &overviewPage{theme: style.Default(), loaded: false}
	if !strings.Contains(p.View(120, 20), "gathering") {
		t.Fatal("loading state should show a gathering message")
	}
}
