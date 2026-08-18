package view

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/metrics"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// allPages builds one of every page with no live Session. View must not touch the
// Session — only OnEnter does — so a nil one is the point: it proves rendering is
// independent of cluster state, and it lets every page be exercised at once.
func allPages(th style.Theme) map[string]Page {
	tbl := func(cols []columns.Column) *component.Table {
		t := component.NewTable(th)
		t.SetColumns(cols)
		return t
	}
	return map[string]Page{
		"metamenu":      newMetaMenuPage(nil, th, "pods", "demo", "web"),
		"setmenu":       newSetMenuPage(nil, th, "deployments", "demo", "web"),
		"cpmenu":        newCpMenuPage(nil, th, "demo", "web", "app"),
		"cronjobmenu":   newCronJobMenuPage(nil, th, "demo", "nightly", false),
		"nodeops":       newNodeOpsPage(nil, th, "node-1", false),
		"rollout":       newRolloutPage(nil, th, "deployments", "demo", "web", false),
		"contextpicker": &contextPickerPage{theme: th, contexts: []string{"kind-a", "kind-b"}, current: "kind-a"},
		"portforwards":  &pfPage{theme: th},
		"secretreveal":  &secretRevealPage{theme: th, namespace: "demo", name: "creds", stype: "Opaque"},
		"whoami":        &whoamiPage{theme: th, namespace: "demo"},
		"nodetop":       &nodeTopPage{theme: th, loaded: true, available: true, rows: []metrics.NodeUsage{{Name: "node-1"}}},
		"nodetop-none":  &nodeTopPage{theme: th, loaded: true, available: false, reason: "not installed"},
		"apiresources":  &apiResourcesPage{theme: th, loaded: true},
		"apiversions":   &apiVersionsPage{theme: th, loaded: true},
		"rollouthist":   &rolloutHistoryPage{theme: th, kind: "deployments", namespace: "demo", name: "web", loaded: true},
		"crdlist":       &crdListPage{theme: th, table: tbl(crdListCols), loaded: true},
		"crdbrowse":     &crdBrowsePage{theme: th, table: component.NewTable(th), loaded: true, title: "Widget (demo.io)"},
		"tenants":       &tenantsPage{theme: th},
		"containers":    &containersPage{theme: th, namespace: "demo", pod: "web"},
		"textview":      NewTextView("yaml", "line one\nline two\n", th),
		"resource":      newBarePage("pods"),
	}
}

// TestPagesRenderAtAnySize is the coverage this layer was missing entirely: 17 of
// 20 pages had no test touching View. It does not assert appearance — it asserts
// the invariants a renderer must not break at sizes real terminals produce,
// including the very small ones where "width - 4" style arithmetic goes negative.
func TestPagesRenderAtAnySize(t *testing.T) {
	th := style.Default()
	sizes := []struct{ w, h int }{
		{1, 1}, {2, 2}, {5, 3}, {20, 4}, {40, 10}, {80, 24}, {200, 60}, {300, 2},
	}
	for name, p := range allPages(th) {
		for _, s := range sizes {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s.View(%d,%d) panicked: %v", name, s.w, s.h, r)
					}
				}()
				out := p.View(s.w, s.h)
				if !utf8.ValidString(out) {
					t.Errorf("%s.View(%d,%d) emitted invalid UTF-8 (a truncation split a rune)", name, s.w, s.h)
				}
			}()
		}
	}
}

// Every page must answer the interface the chrome calls on each frame. A nil map
// or a panic here takes down the whole UI, not just the page.
func TestPagesAnswerChromeQueriesWithoutASession(t *testing.T) {
	th := style.Default()
	for name, p := range allPages(th) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked answering chrome queries: %v", name, r)
				}
			}()
			if p.Title() == "" {
				t.Errorf("%s has an empty Title, so the breadcrumb renders blank", name)
			}
			_ = p.Kind()
			_ = p.Namespace()
			_ = p.Filter()
			_ = p.Summary()
			_ = p.Keys()
		}()
	}
}

// A page that advertises a key must not panic when it receives it. This is the
// gap that let three actions ship with no read-only guard: nothing ever pressed a
// key against a page in a test.
func TestPagesSurviveTheirOwnAdvertisedKeys(t *testing.T) {
	th := style.Default()
	for name, p := range allPages(th) {
		for _, b := range p.Keys() {
			for _, k := range b.Keys() {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("%s panicked on its own key %q: %v", name, k, r)
						}
					}()
					_, _ = p.Update(keyPress(k))
				}()
			}
		}
	}
}

// keyPress builds the KeyPressMsg a binding's key string denotes, mirroring how
// bubbles/key matches: it compares against KeyPressMsg.String().
func keyPress(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	}
	if after, ok := strings.CutPrefix(k, "ctrl+"); ok && len(after) == 1 {
		return tea.KeyPressMsg{Code: rune(after[0]), Mod: tea.ModCtrl}
	}
	if r := []rune(k); len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: k}
	}
	return tea.KeyPressMsg{Text: k}
}

func TestTextViewRendersItsContent(t *testing.T) {
	tv := NewTextView("t", strings.Repeat("hello\n", 50), style.Default())
	if out := tv.View(40, 10); !strings.Contains(out, "hello") {
		t.Errorf("TextView did not render its body:\n%s", out)
	}
}
