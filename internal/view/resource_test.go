package view

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

func TestUnsatisfiableHint(t *testing.T) {
	titles := []string{"NAME", "STATUS", "IMAGE"}
	cases := []struct {
		filter string
		want   string // "" means no hint
	}{
		// The reported case: two ns: terms can never both match one namespace.
		{"ns:demo ns:kube-system", "ns: has 2 AND-ed terms — try ns:demo|kube-system"},
		{"name:web name:api", "name: has 2 AND-ed terms — try name:web|api"},
		{"status:Running status:Error", "status: has 2 AND-ed terms — try status:Running|Error"},
		// Already using alternation: fold the extra term into it.
		{"ns:a|b ns:c", "ns: has 2 AND-ed terms — try ns:a|b|c"},
		// Three terms.
		{"ns:a ns:b ns:c", "ns: has 3 AND-ed terms — try ns:a|b|c"},
		// A single scoped term is fine.
		{"ns:demo", ""},
		// Different columns AND legitimately.
		{"ns:demo status:Running", ""},
		// Unscoped terms can each match a different cell, so AND is meaningful.
		{"web running", ""},
		// Negated terms legitimately AND: "!ns:a !ns:b" means neither.
		{"!ns:a !ns:b", ""},
		// A regex value is not a plain value to join with "|".
		{"ns:~a ns:~b", ""},
		// An unknown col: is literal text, not a scope.
		{"nginx:1.25 nginx:1.26", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := unsatisfiableHint(c.filter, titles); got != c.want {
			t.Errorf("unsatisfiableHint(%q) = %q, want %q", c.filter, got, c.want)
		}
	}
}

func TestViewExplainsEmptyFilter(t *testing.T) {
	p := newBarePage("pods")
	p.apply(engine.Remote[columns.Row]{Phase: engine.PhaseReady, Rows: []columns.Row{
		nsRow("demo", "web", "Running", "nginx"),
		nsRow("kube-system", "coredns", "Running", "coredns"),
	}})

	// A filter that can never match must say why, not just show an empty table.
	p.SetFilter("ns:demo ns:kube-system")
	out := p.View(120, 10)
	if !strings.Contains(out, "AND-ed terms") || !strings.Contains(out, "ns:demo|kube-system") {
		t.Errorf("empty unsatisfiable filter gave no explanation:\n%s", out)
	}

	// An ordinary empty result says so, but must not invent an alternation hint.
	p.SetFilter("ns:nowhere")
	out = p.View(120, 10)
	if strings.Contains(out, "AND-ed terms") {
		t.Errorf("ordinary empty result should not claim AND-ed terms:\n%s", out)
	}
	if !strings.Contains(out, "no rows match") {
		t.Errorf("empty result gave no feedback at all:\n%s", out)
	}

	// A filter that matches must render the table, not a message.
	p.SetFilter("ns:demo")
	out = p.View(120, 10)
	if strings.Contains(out, "no rows match") {
		t.Errorf("matching filter rendered the empty-state message:\n%s", out)
	}
	if !strings.Contains(out, "web") {
		t.Errorf("matching filter did not render its row:\n%s", out)
	}
}

func TestAlternationToleratesRepeatedScope(t *testing.T) {
	titles := []string{"NAME", "STATUS", "IMAGE"}
	rows := []columns.Row{
		nsRow("demo", "web", "Running", "nginx:1.25"),
		nsRow("kube-system", "coredns", "Running", "coredns:1.11"),
		nsRow("prod", "api", "Running", "redis:7"),
	}

	// Repeating the scope inside an alternative is redundant but is what people
	// reach for: "either ns:a or ns:b". Accept it rather than silently matching the
	// literal text "ns:b", which no namespace can contain.
	if got := filterRows(rows, "ns:kube-system|ns:demo", titles); len(got) != 2 {
		t.Errorf("ns:kube-system|ns:demo matched %d rows, want 2", len(got))
	}
	// Same thing on a column scope, where values legitimately contain colons.
	if got := filterRows(rows, "image:nginx|image:redis", titles); len(got) != 2 {
		t.Errorf("image:nginx|image:redis matched %d rows, want 2", len(got))
	}
	if got := filterRows(rows, "image:nginx:1.25|image:redis:7", titles); len(got) != 2 {
		t.Errorf("colon-bearing values matched %d rows, want 2", len(got))
	}
	// The canonical form is unaffected.
	if got := filterRows(rows, "ns:kube-system|demo", titles); len(got) != 2 {
		t.Errorf("canonical form matched %d rows, want 2", len(got))
	}
	// A DIFFERENT column inside an alternative is not a scope change — a term has
	// one scope — so it stays literal text and matches nothing here.
	if got := filterRows(rows, "ns:demo|name:api", titles); len(got) != 1 {
		t.Errorf("cross-scope alternative matched %d rows, want 1 (demo only)", len(got))
	}
	// An unscoped token whose value contains a colon is still literal.
	if got := filterRows(rows, "nginx:1.25", titles); len(got) != 1 {
		t.Errorf("unknown col as literal matched %d rows, want 1", len(got))
	}
}

func TestFilterMatchesCompletesAfterRepeatedScope(t *testing.T) {
	titles := []string{"NAME", "STATUS", "IMAGE"}
	rows := []columns.Row{
		nsRow("demo", "web", "Running", "nginx"),
		nsRow("kube-system", "coredns", "Running", "coredns"),
	}
	prefix, value, matches := filterMatches("ns:kube-system|ns:de", rows, titles)
	if prefix != "ns:kube-system|ns:" || value != "de" {
		t.Errorf("prefix/value = %q/%q, want %q/%q", prefix, value, "ns:kube-system|ns:", "de")
	}
	if len(matches) != 1 || matches[0] != "demo" {
		t.Errorf("matches = %v, want [demo]", matches)
	}
}
