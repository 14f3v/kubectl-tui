package app

import (
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/view"
)

func TestPaletteWindow(t *testing.T) {
	// The window must always keep the selection visible and stay the right size.
	for _, tc := range []struct{ n, size int }{{10, 5}, {10, 10}, {12, 7}, {3, 5}, {1, 1}} {
		size := tc.size
		if size > tc.n {
			size = tc.n
		}
		for sel := 0; sel < tc.n; sel++ {
			start, end := paletteWindow(sel, tc.n, tc.size)
			if start < 0 || end > tc.n || start > end {
				t.Fatalf("n=%d size=%d sel=%d: bad window [%d,%d)", tc.n, tc.size, sel, start, end)
			}
			if sel < start || sel >= end {
				t.Fatalf("n=%d size=%d sel=%d: selection outside window [%d,%d)", tc.n, tc.size, sel, start, end)
			}
			if got := end - start; got != size {
				t.Fatalf("n=%d size=%d sel=%d: window has %d rows, want %d", tc.n, tc.size, sel, got, size)
			}
		}
	}
	// Spot-check the scroll boundary: n=10, size=5.
	if s, e := paletteWindow(4, 10, 5); s != 0 || e != 5 {
		t.Fatalf("sel=4: got [%d,%d), want [0,5)", s, e)
	}
	if s, e := paletteWindow(5, 10, 5); s != 1 || e != 6 {
		t.Fatalf("sel=5: got [%d,%d), want [1,6)", s, e)
	}
	if s, e := paletteWindow(9, 10, 5); s != 5 || e != 10 {
		t.Fatalf("sel=9: got [%d,%d), want [5,10)", s, e)
	}
}

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

func TestSelectedCommand(t *testing.T) {
	// Regression: typing a command then Enter must resolve to that command, not
	// the first catalog entry. (The bug: the buffer was cleared before filtering.)
	if name, ok := (&Model{inputBuf: "pods", cmdSel: 0}).selectedCommand(); !ok || name != "pods" {
		t.Fatalf("typed 'pods' = %q/%v, want pods/true", name, ok)
	}
	if name, ok := (&Model{inputBuf: "de", cmdSel: 0}).selectedCommand(); !ok || name != "deployments" {
		t.Fatalf("typed 'de' = %q/%v, want deployments/true", name, ok)
	}
	// Argument form (has a space) runs the raw buffer, not a palette pick.
	if _, ok := (&Model{inputBuf: "pods kube-system", cmdSel: 0}).selectedCommand(); ok {
		t.Fatal("argument form should not resolve to a palette command")
	}
	// Unknown token: no match; caller parses the raw buffer.
	if _, ok := (&Model{inputBuf: "zzzz", cmdSel: 0}).selectedCommand(); ok {
		t.Fatal("unknown token should not resolve to a palette command")
	}
	// Cursor path: empty buffer, selection indexes the full catalog.
	cat := commandCatalog()
	idx := -1
	for i, c := range cat {
		if c.Name == "pods" {
			idx = i
		}
	}
	if name, ok := (&Model{inputBuf: "", cmdSel: idx}).selectedCommand(); !ok || name != "pods" {
		t.Fatalf("cursor-selected pods = %q/%v, want pods/true", name, ok)
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
