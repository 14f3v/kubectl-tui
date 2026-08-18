package view

import (
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

func TestTruncNeverExceedsDisplayWidth(t *testing.T) {
	// trunc sits next to pad in the same layouts, and pad is display-width aware.
	// If trunc is not, the two disagree and columns drift apart on any non-ASCII
	// cell — which is ordinary in Kubernetes (annotations, node labels, CJK names).
	for _, s := range []string{
		"plain-ascii-name",
		"日本語のポッド名",
		"café-münchen-service",
		"emoji-🚀-deploy",
	} {
		for _, w := range []int{1, 4, 8, 12, 20} {
			got := trunc(s, w)
			if gw := lipgloss.Width(got); gw > w {
				t.Errorf("trunc(%q, %d) = %q, width %d exceeds %d", s, w, got, gw, w)
			}
			if !utf8.ValidString(got) {
				t.Errorf("trunc(%q, %d) = %q, split a rune", s, w, got)
			}
		}
	}
}

func TestTruncKeepsShortStringsWhole(t *testing.T) {
	for _, s := range []string{"abc", "日本"} {
		if got := trunc(s, 10); got != s {
			t.Errorf("trunc(%q, 10) = %q, want it unchanged", s, got)
		}
	}
	if got := trunc("abc", 0); got != "" {
		t.Errorf("trunc with no room = %q, want empty", got)
	}
}

func TestPadIsDisplayWidthAware(t *testing.T) {
	for _, s := range []string{"abc", "日本語"} {
		got := pad(s, 10)
		if w := lipgloss.Width(got); w != 10 {
			t.Errorf("pad(%q, 10) width = %d, want 10", s, w)
		}
	}
	// Already at or over width: left alone.
	if got := pad("abcdefghij", 5); got != "abcdefghij" {
		t.Errorf("pad over-width = %q, want unchanged", got)
	}
}

func TestWorkloadKindPredicates(t *testing.T) {
	for _, k := range []string{"deployments", "statefulsets", "daemonsets", "replicasets"} {
		if !forwardableWorkload(k) {
			t.Errorf("forwardableWorkload(%q) = false", k)
		}
		if !logsWorkload(k) {
			t.Errorf("logsWorkload(%q) = false", k)
		}
	}
	// Jobs have pods to tail but no stable service to forward to.
	if forwardableWorkload("jobs") {
		t.Error("forwardableWorkload(jobs) = true, want false")
	}
	if !logsWorkload("jobs") {
		t.Error("logsWorkload(jobs) = false, want true")
	}
	for _, k := range []string{"pods", "services", "nodes", "configmaps", ""} {
		if forwardableWorkload(k) || logsWorkload(k) {
			t.Errorf("%q classified as a workload", k)
		}
	}
}

func TestSingularKind(t *testing.T) {
	cases := map[string]string{
		"pods":                        "pod",
		"deployments":                 "deployment",
		"nodes":                       "node",
		"replicasets":                 "replicaset",
		"ingresses":                   "ingress",
		"networkpolicies":             "networkpolicy",
		"storageclasses":              "storageclass",
		"runtimeclasses":              "runtimeclass",
		"ingressclasses":              "ingressclass",
		"validatingadmissionpolicies": "validatingadmissionpolicy",
		"csistoragecapacities":        "csistoragecapacity",
		"endpointslices":              "endpointslice",
		"namespaces":                  "namespace",
		"leases":                      "lease",
	}
	for in, want := range cases {
		if got := singularKind(in); got != want {
			t.Errorf("singularKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPageRegistry(t *testing.T) {
	// The registry is pure and drives every ":" command, but had no test at all.
	if _, ok := ResolveKind("po"); !ok {
		t.Error(`ResolveKind("po") failed; the pods alias is unregistered`)
	}
	if k, _ := ResolveKind("deploy"); k != "deployments" {
		t.Errorf(`ResolveKind("deploy") = %q, want "deployments"`, k)
	}
	if k, _ := ResolveKind("pods"); k != "pods" {
		t.Errorf("a canonical kind must resolve to itself, got %q", k)
	}
	if _, ok := ResolveKind("definitely-not-a-kind"); ok {
		t.Error("an unknown alias resolved")
	}
	if _, ok := NewPage("definitely-not-a-kind", Deps{}); ok {
		t.Error("NewPage built a page for an unknown kind")
	}

	cmds := Commands()
	if len(cmds) < 30 {
		t.Errorf("Commands() returned %d entries, expected the full kind set", len(cmds))
	}
	for _, c := range cmds {
		if c.Name == "" || c.Desc == "" {
			t.Errorf("command %+v is missing a name or description", c)
		}
		if _, ok := ResolveKind(c.Name); !ok {
			t.Errorf("listed command %q does not resolve", c.Name)
		}
	}
	// Sorted, so the palette order is stable.
	for i := 1; i < len(cmds); i++ {
		if cmds[i-1].Name > cmds[i].Name {
			t.Errorf("Commands() not sorted at %d: %q > %q", i, cmds[i-1].Name, cmds[i].Name)
		}
	}
	aliases := Aliases()
	if len(aliases) < len(cmds) {
		t.Errorf("Aliases() = %d, fewer than %d commands", len(aliases), len(cmds))
	}
	if !strings.Contains(strings.Join(aliases, ","), "svc") {
		t.Error("Aliases() is missing a known short alias (svc)")
	}
}

// Every registered kind must singularize to something different and non-empty —
// these strings go straight into user-facing titles like "deployment/web · logs",
// and a missed plural form shows up there as "ingresse/web".
func TestSingularKindCoversEveryRegisteredKind(t *testing.T) {
	for _, k := range columns.Kinds() {
		got := singularKind(k)
		if got == "" || got == k {
			t.Errorf("singularKind(%q) = %q, want a distinct singular", k, got)
		}
		if strings.HasSuffix(got, "ie") || strings.HasSuffix(got, "sse") {
			t.Errorf("singularKind(%q) = %q, a mangled irregular plural", k, got)
		}
	}
}
