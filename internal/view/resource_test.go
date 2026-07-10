package view

import (
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func row(name, status string, class columns.StatusClass) columns.Row {
	return columns.Row{
		Name:   name,
		UID:    "uid-" + name,
		Health: class,
		Cells: []columns.Cell{
			{Text: name, Role: columns.RoleName},
			{Text: status, Role: columns.RoleStatus, Status: class},
		},
	}
}

func TestFilterRows(t *testing.T) {
	rows := []columns.Row{
		row("checkout-api", "Running", columns.StatusOK),
		row("payments-worker", "Running", columns.StatusOK),
		row("notifications", "CrashLoopBackOff", columns.StatusError),
	}

	if got := filterRows(rows, "", nil); len(got) != 3 {
		t.Fatalf("empty filter: got %d, want 3", len(got))
	}
	if got := filterRows(rows, "pay", nil); len(got) != 1 || got[0].Name != "payments-worker" {
		t.Fatalf("substring filter: got %v", got)
	}
	// Match on a cell value, not just the name.
	if got := filterRows(rows, "crashloop", nil); len(got) != 1 || got[0].Name != "notifications" {
		t.Fatalf("cell-value filter: got %v", got)
	}
	// Inverse filter.
	if got := filterRows(rows, "!running", nil); len(got) != 1 || got[0].Name != "notifications" {
		t.Fatalf("inverse filter: got %v", got)
	}
	// Regex filter (leading "~"), case-insensitive, anchored alternation.
	if got := filterRows(rows, "~^(checkout|payments)", nil); len(got) != 2 {
		t.Fatalf("regex filter: got %d, want 2", len(got))
	}
	// Inverted regex composes with "!".
	if got := filterRows(rows, "!~worker$", nil); len(got) != 2 {
		t.Fatalf("inverse regex filter: got %d, want 2", len(got))
	}
	// An unparseable regex filters nothing (all rows pass) until it's valid.
	if got := filterRows(rows, "~[unclosed", nil); len(got) != 3 {
		t.Fatalf("invalid regex should pass all: got %d, want 3", len(got))
	}
}

// nsRow builds a row with a namespace and NAME/STATUS/IMAGE cells for the
// multi-term / column-scoped filter tests.
func nsRow(ns, name, status, image string) columns.Row {
	return columns.Row{
		Namespace: ns, Name: name, UID: "uid-" + ns + "-" + name,
		Cells: []columns.Cell{
			{Text: name, Role: columns.RoleName},
			{Text: status, Role: columns.RoleStatus},
			{Text: image},
		},
	}
}

func TestFilterRowsMulti(t *testing.T) {
	titles := []string{"NAME", "STATUS", "IMAGE"}
	rows := []columns.Row{
		nsRow("prod", "checkout-api", "Running", "nginx:1.25"),
		nsRow("prod", "payments-worker", "Running", "app:2.0"),
		nsRow("staging", "checkout-api", "Error", "nginx:1.25"),
		nsRow("kube-system", "coredns", "Running", "coredns:1.11"),
	}
	names := func(got []columns.Row) string {
		var b strings.Builder
		for _, r := range got {
			b.WriteString(r.Namespace + "/" + r.Name + " ")
		}
		return strings.TrimSpace(b.String())
	}

	// AND terms: both must match (across any column).
	if got := filterRows(rows, "checkout running", titles); len(got) != 1 || got[0].Namespace != "prod" {
		t.Fatalf("AND terms: got %q, want prod/checkout-api", names(got))
	}
	// ns: scopes to the namespace field.
	if got := filterRows(rows, "ns:prod", titles); len(got) != 2 {
		t.Fatalf("ns scope: got %q, want 2 prod rows", names(got))
	}
	// name: scopes to the name; two checkout-api across namespaces.
	if got := filterRows(rows, "name:checkout", titles); len(got) != 2 {
		t.Fatalf("name scope: got %q, want 2", names(got))
	}
	// Column title scope (case-insensitive) + a second scoped term.
	if got := filterRows(rows, "status:running ns:prod", titles); len(got) != 2 {
		t.Fatalf("status+ns: got %q, want 2 prod running", names(got))
	}
	// Combined: namespace + name + negation.
	if got := filterRows(rows, "ns:prod name:checkout !error", titles); len(got) != 1 || got[0].Name != "checkout-api" {
		t.Fatalf("combined: got %q, want prod/checkout-api", names(got))
	}
	// Per-term negation on a scoped column.
	if got := filterRows(rows, "checkout !ns:staging", titles); len(got) != 1 || got[0].Namespace != "prod" {
		t.Fatalf("scoped negation: got %q, want prod/checkout-api", names(got))
	}
	// An unknown "col:" is treated as literal text (matches the image tag).
	if got := filterRows(rows, "nginx:1.25", titles); len(got) != 2 {
		t.Fatalf("unknown col as literal: got %q, want 2 nginx rows", names(got))
	}
	// Per-term regex composes with AND.
	if got := filterRows(rows, "~^checkout ns:prod", titles); len(got) != 1 {
		t.Fatalf("regex term + ns: got %q, want 1", names(got))
	}
	// A scoped term with no value is ignored (forgiving typing), so only "prod" applies.
	if got := filterRows(rows, "ns: prod", titles); len(got) != 2 {
		t.Fatalf("empty scoped value ignored: got %q, want 2", names(got))
	}
	// A regex whose text contains a colon: "col" (~nginx) isn't a column, so the
	// whole token is a regex — it must not be mistaken for column scoping.
	if got := filterRows(rows, "~nginx:1", titles); len(got) != 2 {
		t.Fatalf("regex with colon: got %q, want 2 nginx rows", names(got))
	}
	// Runs of whitespace between terms collapse (strings.Fields), no empty terms.
	if got := filterRows(rows, "checkout    running", titles); len(got) != 1 {
		t.Fatalf("collapsed whitespace: got %q, want 1", names(got))
	}
}

func TestCompleteFilter(t *testing.T) {
	titles := []string{"NAME", "STATUS", "IMAGE"}
	rows := []columns.Row{
		nsRow("prod", "microservice-api", "Running", "img:1"),
		nsRow("prod", "microservice-worker", "Running", "img:2"),
		nsRow("kube-system", "coredns", "Running", "img:3"),
		nsRow("prod", "payments", "Pending", "img:4"),
	}
	cases := []struct {
		in          string
		want        string
		wantChanged bool
	}{
		{"micro", "microservice-", true},           // extend to the shared prefix of two names
		{"pay", "payments", true},                  // single match completes fully
		{"zzz", "zzz", false},                       // no candidate
		{"", "", false},                             // nothing to complete
		{"prod micro", "prod microservice-", true},  // only the last term is completed
		{"ns:kube", "ns:kube-system", true},         // namespace scope, single match
		{"name:micro", "name:microservice-", true},  // name scope
		{"status:Run", "status:Running", true},      // column scope, case-insensitive
		{"microservice-", "microservice-", false},   // already at the shared prefix
		{"~micro", "~micro", false},                 // regex is not completed
		{"!micro", "!microservice-", true},          // negation is preserved
	}
	for _, c := range cases {
		got, changed := completeFilter(c.in, rows, titles)
		if got != c.want || changed != c.wantChanged {
			t.Errorf("completeFilter(%q) = (%q, %v), want (%q, %v)", c.in, got, changed, c.want, c.wantChanged)
		}
	}
}

// newBarePage constructs a resourcePage without a Session, for rendering tests
// (View does not touch the Session — only OnEnter does).
func newBarePage(kind string) *resourcePage {
	th := style.Default()
	tbl := component.NewTable(th)
	if proj := columns.For(kind); proj != nil {
		tbl.SetColumns(proj.Columns())
	}
	return &resourcePage{kind: kind, title: kind, theme: th, table: tbl}
}

func TestResourcePageView(t *testing.T) {
	p := newBarePage("pods")
	p.apply(engine.Remote[columns.Row]{
		Phase: engine.PhaseReady,
		Rows: []columns.Row{
			row("checkout-api", "Running", columns.StatusOK),
			row("notifications", "CrashLoopBackOff", columns.StatusError),
		},
	})
	out := p.View(120, 12)
	lines := strings.Split(out, "\n")
	if len(lines) != 12 {
		t.Fatalf("view line count = %d, want 12", len(lines))
	}
	if !strings.Contains(out, "checkout-api") || !strings.Contains(out, "NAME") {
		t.Fatalf("view missing expected content:\n%s", out)
	}
	// Summary must reflect the rows.
	s := p.Summary()
	if s.Total != 2 || s.OK != 1 || s.Err != 1 {
		t.Fatalf("summary = %+v, want total 2 ok 1 err 1", s)
	}
}

func TestStatusCounts(t *testing.T) {
	rows := []columns.Row{
		row("a", "Running", columns.StatusOK),
		row("b", "Running", columns.StatusOK),
		row("c", "Pending", columns.StatusWarn),
		row("d", "CrashLoopBackOff", columns.StatusError),
	}
	total, ok, warn, errc := statusCounts(rows)
	if total != 4 || ok != 2 || warn != 1 || errc != 1 {
		t.Fatalf("counts = %d/%d/%d/%d, want 4/2/1/1", total, ok, warn, errc)
	}
}
