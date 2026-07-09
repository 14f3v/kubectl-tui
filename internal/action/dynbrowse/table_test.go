package dynbrowse

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	columns "github.com/14f3v/kubectl-tui/internal/engine/columns"
)

// TestResourcePath checks the REST path builder for the four combinations that
// matter: core vs grouped resource, each namespaced-to-a-namespace vs
// cluster-wide. The exact strings must match what the API server expects.
func TestResourcePath(t *testing.T) {
	pods := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	certs := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}

	cases := []struct {
		name       string
		gvr        schema.GroupVersionResource
		namespaced bool
		namespace  string
		want       string
	}{
		{"core namespaced", pods, true, "demo", "/api/v1/namespaces/demo/pods"},
		{"core cluster-wide", pods, true, "", "/api/v1/pods"},
		{"grouped namespaced", certs, true, "demo", "/apis/cert-manager.io/v1/namespaces/demo/certificates"},
		{"grouped cluster-wide", certs, true, "", "/apis/cert-manager.io/v1/certificates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resourcePath(tc.gvr, tc.namespaced, tc.namespace)
			if got != tc.want {
				t.Errorf("resourcePath = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestToColumns hand-builds a Table with three column definitions — a string
// Name, an integer Replicas, and a Priority>0 Extra kubectl would hide — plus two
// rows carrying object metadata. It asserts the hidden column is dropped, the
// integer column is right-aligned, the first cell is tagged RoleName, the numeric
// cell renders as a plain integer (not "3.0"), and identity is lifted out of the
// row's PartialObjectMetadata.
func TestToColumns(t *testing.T) {
	meta1 := partialMeta(t, "demo", "web", "uid-web", "101")
	meta2 := partialMeta(t, "demo", "api", "uid-api", "102")

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string", Priority: 0},
			{Name: "Replicas", Type: "integer", Priority: 0},
			{Name: "Extra", Type: "string", Priority: 1},
		},
		Rows: []metav1.TableRow{
			{
				Cells:  []interface{}{"web", int64(3), "hide"},
				Object: runtime.RawExtension{Raw: meta1},
			},
			{
				Cells:  []interface{}{"api", int64(0), "x"},
				Object: runtime.RawExtension{Raw: meta2},
			},
		},
	}

	cols, rows := ToColumns(table)

	// The Priority>0 column must be dropped.
	if len(cols) != 2 {
		t.Fatalf("kept %d columns, want 2 (Extra hidden): %+v", len(cols), cols)
	}
	if cols[0].Title != "NAME" || cols[1].Title != "REPLICAS" {
		t.Errorf("titles = %q,%q, want NAME,REPLICAS", cols[0].Title, cols[1].Title)
	}
	if cols[0].Align != columns.AlignLeft {
		t.Errorf("NAME align = %v, want AlignLeft", cols[0].Align)
	}
	if cols[1].Align != columns.AlignRight {
		t.Errorf("REPLICAS align = %v, want AlignRight (integer)", cols[1].Align)
	}
	if cols[0].Grow != 3 {
		t.Errorf("first column Grow = %d, want 3", cols[0].Grow)
	}

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	r0 := rows[0]
	if len(r0.Cells) != 2 {
		t.Fatalf("row0 has %d cells, want 2", len(r0.Cells))
	}
	if r0.Cells[0].Role != columns.RoleName {
		t.Errorf("row0 name cell role = %v, want RoleName", r0.Cells[0].Role)
	}
	if r0.Cells[1].Role != columns.RolePlain {
		t.Errorf("row0 replicas cell role = %v, want RolePlain", r0.Cells[1].Role)
	}
	if r0.Cells[0].Text != "web" {
		t.Errorf("row0 name text = %q, want web", r0.Cells[0].Text)
	}
	// int64(3) must stringify to "3" — a plain integer, never "3.0".
	if r0.Cells[1].Text != "3" {
		t.Errorf("row0 replicas text = %q, want 3", r0.Cells[1].Text)
	}

	// Identity is extracted from the row's PartialObjectMetadata.
	if r0.Name != "web" || r0.Namespace != "demo" || r0.UID != "uid-web" {
		t.Errorf("row0 identity = {ns:%q name:%q uid:%q}, want demo/web/uid-web", r0.Namespace, r0.Name, r0.UID)
	}
	if r0.Version != "101" {
		t.Errorf("row0 version = %q, want 101", r0.Version)
	}
	if rows[1].Cells[1].Text != "0" {
		t.Errorf("row1 replicas text = %q, want 0", rows[1].Cells[1].Text)
	}
	if rows[1].Name != "api" || rows[1].UID != "uid-api" {
		t.Errorf("row1 identity = name:%q uid:%q, want api/uid-api", rows[1].Name, rows[1].UID)
	}
}

// TestToColumnsFloatFromJSON confirms the float64-handling path: when a Table is
// round-tripped through JSON (as the real API response is), numeric cells decode
// as float64, and a whole number must still render without a fractional part.
func TestToColumnsFloatFromJSON(t *testing.T) {
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string", Priority: 0},
			{Name: "Count", Type: "integer", Priority: 0},
		},
		Rows: []metav1.TableRow{
			{Cells: []interface{}{"a", int64(5)}},
		},
	}
	raw, err := json.Marshal(table)
	if err != nil {
		t.Fatalf("marshal table: %v", err)
	}
	var decoded metav1.Table
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal table: %v", err)
	}

	_, rows := ToColumns(&decoded)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if got := rows[0].Cells[1].Text; got != "5" {
		t.Errorf("count text = %q, want 5 (float64 whole number)", got)
	}
	// The numeric column must carry a numeric sort key so ordering is numeric.
	if !rows[0].SortKeys[1].IsNum || rows[0].SortKeys[1].Num != 5 {
		t.Errorf("count sort key = %+v, want numeric 5", rows[0].SortKeys[1])
	}
}

// TestStringifyCell exercises stringifyCell's type switch directly.
func TestStringifyCell(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"hello", "hello"},
		{true, "true"},
		{false, "false"},
		{float64(3), "3"},
		{float64(3.5), "3.5"},
		{json.Number("42"), "42"},
		{int64(7), "7"},
	}
	for _, tc := range cases {
		if got := stringifyCell(tc.in); got != tc.want {
			t.Errorf("stringifyCell(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// partialMeta marshals a PartialObjectMetadata to JSON for use as a TableRow's
// Object.Raw, the way the API server embeds object identity in a Table.
func partialMeta(t *testing.T, namespace, name, uid, resourceVersion string) []byte {
	t.Helper()
	pom := metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       namespace,
			Name:            name,
			UID:             types.UID(uid),
			ResourceVersion: resourceVersion,
		},
	}
	raw, err := json.Marshal(pom)
	if err != nil {
		t.Fatalf("marshal PartialObjectMetadata: %v", err)
	}
	return raw
}
