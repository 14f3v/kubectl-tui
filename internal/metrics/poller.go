package metrics

import (
	"context"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	pollInterval = 15 * time.Second
	reprobeEvery = 5 * time.Minute
)

// Poller periodically collects metrics for the active namespace and delivers a
// Snapshot to the UI. When metrics-server is absent it emits an unavailable
// snapshot and re-probes every few minutes, so the columns reappear if it is
// installed later without restarting the tool.
type Poller struct {
	cs    kubernetes.Interface
	mc    metricsclient.Interface
	disco discovery.DiscoveryInterface
	sink  func(tea.Msg)
	ns    atomic.Value // string
}

// NewPoller builds a poller. mc may be nil if the metrics client could not be
// constructed, in which case metrics are simply always unavailable.
func NewPoller(cs kubernetes.Interface, mc metricsclient.Interface, disco discovery.DiscoveryInterface, sink func(tea.Msg)) *Poller {
	p := &Poller{cs: cs, mc: mc, disco: disco, sink: sink}
	p.ns.Store("")
	return p
}

// SetNamespace rescopes pod metrics to a namespace ("" = all).
func (p *Poller) SetNamespace(ns string) { p.ns.Store(ns) }

func (p *Poller) namespace() string {
	if v, ok := p.ns.Load().(string); ok {
		return v
	}
	return ""
}

// Run drives the poll/reprobe loop until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	if p.mc == nil {
		p.emitUnavailable()
		return
	}
	for {
		if avail, _ := Probe(p.disco); !avail {
			p.emitUnavailable()
			if !sleep(ctx, reprobeEvery) {
				return
			}
			continue
		}
		// Available: poll on the fast cadence until a collection error demotes us.
		if !p.pollOnce(ctx) {
			// error — treat as unavailable and fall back to re-probing
			p.emitUnavailable()
		}
		if !sleep(ctx, pollInterval) {
			return
		}
	}
}

// pollOnce collects and emits a snapshot; returns false on error.
func (p *Poller) pollOnce(ctx context.Context) bool {
	snap, err := Collect(ctx, p.cs, p.mc, p.namespace())
	if err != nil {
		return false
	}
	p.sink(snap)
	return true
}

func (p *Poller) emitUnavailable() {
	p.sink(Snapshot{Available: false})
}

// sleep waits d or until ctx is cancelled; returns false if cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
