package view

import (
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// A successful fetch that returns nothing rendered a bare column header over blank
// space — identical to a page that failed to load. On a cluster with no CRDs (a
// fresh kind cluster, say) ":crds" therefore looked broken rather than empty.
func TestCRDListSaysWhenThereAreNoCRDs(t *testing.T) {
	th := style.Default()
	p := &crdListPage{theme: th, table: component.NewTable(th), loaded: true}
	p.table.SetColumns(crdListCols)

	out := p.View(100, 8)
	if !strings.Contains(out, "no CustomResourceDefinitions") {
		t.Errorf("empty CRD list gave no explanation:\n%s", out)
	}
}

func TestCRDBrowseSaysWhenAKindHasNoInstances(t *testing.T) {
	th := style.Default()
	p := &crdBrowsePage{theme: th, table: component.NewTable(th), loaded: true,
		title: "Widget (demo.io)", namespaced: true, namespace: "demo"}

	out := p.View(100, 8)
	if !strings.Contains(out, "no Widget (demo.io)") {
		t.Errorf("empty instance table gave no explanation:\n%s", out)
	}
	if !strings.Contains(out, "demo") {
		t.Errorf("namespaced empty state should name the namespace scope:\n%s", out)
	}

	// All-namespaces scope must not claim a namespace it does not have.
	p.namespace = ""
	if out := p.View(100, 8); strings.Contains(out, "in namespace") {
		t.Errorf("all-namespaces empty state invented a namespace:\n%s", out)
	}

	// Still loading is a different state and must keep its own message.
	p.loaded = false
	if out := p.View(100, 8); !strings.Contains(out, "loading") {
		t.Errorf("loading state was replaced by the empty state:\n%s", out)
	}
}
