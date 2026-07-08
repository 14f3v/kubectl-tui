package app

import (
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/view"
)

func names(cmds []view.Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Name
	}
	return out
}

func TestMatchCommands(t *testing.T) {
	cat := []view.Command{
		{Name: "pods", Aliases: []string{"po", "pod"}, Desc: "pods"},
		{Name: "deployments", Aliases: []string{"deploy", "dp"}, Desc: "deployments"},
		{Name: "portforwards", Aliases: []string{"pf"}, Desc: "port-forwards"},
	}

	if got := matchCommands(cat, ""); len(got) != 3 {
		t.Fatalf("empty token = %v, want all 3", names(got))
	}
	// "po" is a prefix of both "pods" (name/alias) and "portforwards" (name);
	// both are prefix matches, returned in catalog order.
	if got := names(matchCommands(cat, "po")); len(got) != 2 || got[0] != "pods" || got[1] != "portforwards" {
		t.Fatalf("token 'po' = %v, want [pods portforwards]", got)
	}
	if got := names(matchCommands(cat, "pf")); len(got) != 1 || got[0] != "portforwards" {
		t.Fatalf("token 'pf' = %v, want [portforwards]", got)
	}
	// substring-only match (not a prefix of any name/alias)
	if got := names(matchCommands(cat, "loy")); len(got) != 1 || got[0] != "deployments" {
		t.Fatalf("token 'loy' = %v, want [deployments]", got)
	}
	if got := matchCommands(cat, "zzz"); len(got) != 0 {
		t.Fatalf("token 'zzz' = %v, want none", names(got))
	}
}

func TestPrefixMatchesRankAboveSubstring(t *testing.T) {
	cat := []view.Command{
		{Name: "alpha-x", Desc: "substring"}, // contains "x" mid-string
		{Name: "xray", Desc: "prefix"},       // starts with "x"
	}
	// Both contain "x"; the prefix match (xray) ranks ahead of the substring
	// match (alpha-x) regardless of catalog order.
	got := names(matchCommands(cat, "x"))
	if len(got) != 2 || got[0] != "xray" || got[1] != "alpha-x" {
		t.Fatalf("token 'x' = %v, want [xray alpha-x]", got)
	}
}

func TestCatalogHasAppVerbs(t *testing.T) {
	cat := commandCatalog()
	found := map[string]bool{}
	for _, c := range cat {
		found[c.Name] = true
		if c.Desc == "" {
			t.Fatalf("command %q has no description", c.Name)
		}
	}
	for _, want := range []string{"pods", "tenants", "portforwards", "ctx", "q"} {
		if !found[want] {
			t.Fatalf("catalog missing %q; have %v", want, names(cat))
		}
	}
}

func TestRenderPalette(t *testing.T) {
	m := &Model{theme: style.Default(), mode: modeCommand, inputBuf: "po", cmdSel: 0}
	out := strings.Join(m.renderPalette(80), "\n")
	for _, want := range []string{"COMMANDS", "select", "pods"} {
		if !strings.Contains(out, want) {
			t.Fatalf("palette missing %q:\n%s", want, out)
		}
	}

	// Filtering narrows the list: tenants shows, pods does not.
	m.inputBuf = "tenants"
	out = strings.Join(m.renderPalette(80), "\n")
	if !strings.Contains(out, "tenants") || strings.Contains(out, "pods ") {
		t.Fatalf("filtered palette wrong:\n%s", out)
	}
}
