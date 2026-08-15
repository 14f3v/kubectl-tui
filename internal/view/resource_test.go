package view

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/metrics"
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

func TestFilterAlternation(t *testing.T) {
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

	// A term may list alternatives with "|"; the term matches if ANY of them do.
	if got := filterRows(rows, "ns:prod|staging", titles); len(got) != 3 {
		t.Errorf("ns:prod|staging: got %q, want 3", names(got))
	}
	// Inversion applies to the whole term, so "!" means NOT(prod OR staging).
	if got := filterRows(rows, "!ns:prod|staging", titles); len(got) != 1 || got[0].Namespace != "kube-system" {
		t.Errorf("!ns:prod|staging: got %q, want kube-system/coredns", names(got))
	}
	// Terms are still AND-ed, so two separate ns: terms remain unsatisfiable —
	// this is the behavior alternation exists to give users an alternative to.
	if got := filterRows(rows, "ns:prod ns:staging", titles); len(got) != 0 {
		t.Errorf("ns:prod ns:staging: got %q, want 0 (terms AND)", names(got))
	}
	// An empty alternative must be dropped, not treated as the empty substring
	// (which matches everything). Otherwise the table resets while typing.
	if got := filterRows(rows, "ns:prod|", titles); len(got) != 2 {
		t.Errorf("ns:prod|: got %q, want 2", names(got))
	}
	if got := filterRows(rows, "ns:|", titles); len(got) != 4 {
		t.Errorf("ns:| (no alternatives left): got %q, want all 4", names(got))
	}
	// A "~" value is a regex and must NOT be split: splitting "^(prod|staging)"
	// would yield two uncompilable fragments, both dropped, matching everything.
	if got := filterRows(rows, "ns:~^(prod|staging)$", titles); len(got) != 3 {
		t.Errorf("ns:~^(prod|staging)$: got %q, want 3", names(got))
	}
	// "~" is only special at the start of a value, so "a|~b" is two literals.
	if got := filterRows(rows, "ns:prod|~zzz", titles); len(got) != 2 {
		t.Errorf("ns:prod|~zzz: got %q, want 2 (literal ~zzz matches nothing)", names(got))
	}
	// Comma is ordinary text: nine projectors join cell values with it.
	if got := filterRows(rows, "image:nginx:1.25,app", titles); len(got) != 0 {
		t.Errorf("comma stays literal: got %q, want 0", names(got))
	}
	// Alternation composes with unscoped terms and with AND across terms.
	if got := filterRows(rows, "checkout ns:prod|staging", titles); len(got) != 2 {
		t.Errorf("checkout ns:prod|staging: got %q, want 2", names(got))
	}
}

func TestFilterMatches(t *testing.T) {
	titles := []string{"NAME", "STATUS", "IMAGE"}
	rows := []columns.Row{
		nsRow("prod", "alpha", "Running", "img:1"),
		nsRow("prod", "alpine", "Running", "img:2"),
		nsRow("kube", "bravo", "Pending", "img:3"),
	}
	eq := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		in         string
		wantPrefix string
		wantValue  string
		wantMatch  []string
	}{
		{"al", "", "al", []string{"alpha", "alpine"}},           // names by prefix
		{"prod al", "prod ", "al", []string{"alpha", "alpine"}}, // only the last term
		{"!al", "!", "al", []string{"alpha", "alpine"}},         // negation kept in prefix
		{"ns:", "ns:", "", []string{"prod", "kube"}},            // distinct namespaces
		{"name:al", "name:", "al", []string{"alpha", "alpine"}}, // name scope
		{"status:Run", "status:", "Run", []string{"Running"}},   // column scope, deduped
		{"zzz", "", "zzz", nil},                                 // no candidates
		{"~al", "", "", nil},                                    // regex not suggested
		{"", "", "", nil},                                       // empty
		{"prod ", "", "", nil},                                  // trailing space => empty term
		// Completion follows the last alternative, so the dropdown keeps working
		// while the user types the second value of a "a|b" term.
		{"ns:prod|ku", "ns:prod|", "ku", []string{"kube"}},
		{"ns:prod|", "ns:prod|", "", []string{"prod", "kube"}},
		{"al|alp", "al|", "alp", []string{"alpha", "alpine"}},
		// A regex value is still not completable, even after a pipe.
		{"ns:~prod|ku", "", "", nil},
	}
	for _, c := range cases {
		prefix, value, matches := filterMatches(c.in, rows, titles)
		if prefix != c.wantPrefix || value != c.wantValue || !eq(matches, c.wantMatch) {
			t.Errorf("filterMatches(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, prefix, value, matches, c.wantPrefix, c.wantValue, c.wantMatch)
		}
	}
}

func TestFilterPodRows(t *testing.T) {
	// A deployment's selector; its pods carry app=web (and other labels).
	sel, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}})
	if err != nil {
		t.Fatal(err)
	}
	pods := map[string]*corev1.Pod{
		"prod/web-a":   {ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "1"}}},
		"prod/web-b":   {ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "2"}}},
		"prod/other-c": {ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}}},
		"prod/nolabel": {ObjectMeta: metav1.ObjectMeta{}},
	}
	lookup := func(ns, name string) (*corev1.Pod, bool) {
		p, ok := pods[ns+"/"+name]
		return p, ok
	}
	rows := []columns.Row{
		{Namespace: "prod", Name: "web-a"},
		{Namespace: "prod", Name: "web-b"},
		{Namespace: "prod", Name: "other-c"},
		{Namespace: "prod", Name: "nolabel"},
		{Namespace: "prod", Name: "evicted"}, // not in cache
	}
	got := filterPodRows(rows, sel, lookup)
	if len(got) != 2 || got[0].Name != "web-a" || got[1].Name != "web-b" {
		names := make([]string, len(got))
		for i, r := range got {
			names[i] = r.Name
		}
		t.Fatalf("filterPodRows = %v, want [web-a web-b]", names)
	}
}

// newBarePage constructs a resourcePage without a Session, for rendering tests
// (View does not touch the Session — only OnEnter does).
func newBarePage(kind string) *resourcePage {
	th := style.Default()
	tbl := component.NewTable(th)
	p := &resourcePage{kind: kind, title: kind, theme: th, table: tbl, cpuCol: -1, memCol: -1}
	if proj := columns.For(kind); proj != nil {
		cols := proj.Columns()
		tbl.SetColumns(cols)
		p.colTitles = make([]string, len(cols))
		for i, c := range cols {
			p.colTitles[i] = c.Title
			switch c.Title {
			case "CPU":
				p.cpuCol = i
			case "MEM":
				p.memCol = i
			}
		}
	}
	return p
}

// podRow builds a row shaped like the pods projector's output: nine cells, with
// CPU and MEM carrying the "—" placeholder the metrics join is meant to replace.
func podRow(ns, name string) columns.Row {
	cells := []columns.Cell{
		{Text: name, Role: columns.RoleName},
		{Text: "1/1"},
		{Text: "Running", Role: columns.RoleStatus, Status: columns.StatusOK},
		{Text: "0"},
		{Text: "10.0.0.1"},
		{Text: "node-1"},
		{Text: "—", Status: columns.StatusMuted}, // CPU
		{Text: "—", Status: columns.StatusMuted}, // MEM
		{Text: "5m"},
	}
	keys := make([]columns.SortKey, len(cells))
	for i := range keys {
		keys[i] = columns.StrKey(cells[i].Text)
	}
	keys[6], keys[7] = columns.NumKey(0), columns.NumKey(0)
	return columns.Row{
		UID: ns + "/" + name, Namespace: ns, Name: name, Version: "1",
		Health: columns.StatusOK, Cells: cells, SortKeys: keys,
	}
}

func TestMetricsOverlayIsFilterable(t *testing.T) {
	p := newBarePage("pods")
	p.apply(engine.Remote[columns.Row]{Phase: engine.PhaseReady, Rows: []columns.Row{
		podRow("default", "web"),
		podRow("default", "api"),
	}})
	// Set a filter FIRST. With a filter active, filterRows returns a fresh slice,
	// so the overlay never writes back into allRows — which is the only reason a
	// cpu: term appeared to work at all before this fix.
	p.SetFilter("web")
	p.Update(metrics.Snapshot{Available: true, Pods: map[string]metrics.PodUsage{
		"default/web": {CPUMillis: 12, MemBytes: 48 * 1024 * 1024},
	}})

	// A cpu: term must match what the table displays. Filtering ran before the
	// overlay, so it used to compare against the "—" placeholder instead.
	p.SetFilter("cpu:12m")
	if got := p.table.RowCount(); got != 1 {
		t.Errorf("cpu:12m matched %d rows, want 1 (the overlaid value)", got)
	}
	p.SetFilter("mem:48Mi")
	if got := p.table.RowCount(); got != 1 {
		t.Errorf("mem:48Mi matched %d rows, want 1", got)
	}
}

func TestMetricsOverlaySetsSortKeys(t *testing.T) {
	p := newBarePage("pods")
	// Insert the busy pod first, so a working sort has to reorder them.
	p.apply(engine.Remote[columns.Row]{Phase: engine.PhaseReady, Rows: []columns.Row{
		podRow("default", "aaa-busy"),
		podRow("default", "zzz-idle"),
	}})
	p.Update(metrics.Snapshot{Available: true, Pods: map[string]metrics.PodUsage{
		"default/aaa-busy": {CPUMillis: 500},
		"default/zzz-idle": {CPUMillis: 5},
	}})

	// Sorting by the CPU column must order by actual usage. The projector emits a
	// constant NumKey(0) for CPU, so without the overlay filling SortKeys the sort
	// is a no-op and the rows keep their incoming order.
	p.table.SetSortState(p.cpuCol, false)
	first, ok := p.table.Selected()
	if !ok {
		t.Fatal("no rows after sort")
	}
	if first.Name != "zzz-idle" {
		t.Errorf("ascending CPU sort put %q first, want zzz-idle (5m < 500m)", first.Name)
	}
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
