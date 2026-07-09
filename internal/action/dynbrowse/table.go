// Package dynbrowse lets the UI browse any Kubernetes resource — including custom
// resources it was never compiled to know about — without a per-kind column
// projector. It does this by asking the API server for a server-side Table (the
// same representation kubectl renders from), so the column set and cell text are
// identical to `kubectl get`, and by discovering the CustomResourceDefinitions
// installed on the cluster so the UI can offer them as browsable kinds.
//
// table.go covers the Table half: fetch the Table for a GVR over the raw REST
// client (mirroring session.RefreshServerVersion's AbsPath/Do/Raw pattern) and
// translate it into the engine's kind-agnostic columns.Column/columns.Row model.
package dynbrowse

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	columns "github.com/14f3v/kubectl-tui/internal/engine/columns"
)

// tableAccept is the content negotiation header that asks the API server to
// return a meta.k8s.io/v1 Table instead of the object list. This is exactly what
// kubectl sends for its human-readable output, so the columns and cell strings we
// get back are the ones kubectl would print.
const tableAccept = "application/json;as=Table;v=v1;g=meta.k8s.io"

// resourcePath builds the REST path for a resource collection. Core-group
// resources live under /api/<version>; every other group lives under
// /apis/<group>/<version>. The "/namespaces/<ns>" segment is inserted only for a
// namespaced list scoped to a specific namespace — an empty namespace (or a
// cluster-scoped resource) lists across the whole cluster.
func resourcePath(gvr schema.GroupVersionResource, namespaced bool, namespace string) string {
	var b strings.Builder
	if gvr.Group == "" {
		b.WriteString("/api/")
		b.WriteString(gvr.Version)
	} else {
		b.WriteString("/apis/")
		b.WriteString(gvr.Group)
		b.WriteString("/")
		b.WriteString(gvr.Version)
	}
	if namespaced && namespace != "" {
		b.WriteString("/namespaces/")
		b.WriteString(namespace)
	}
	b.WriteString("/")
	b.WriteString(gvr.Resource)
	return b.String()
}

// FetchTable requests a server-side Table for the given resource and namespace
// scope. It talks to the raw REST client directly — setting the Table Accept
// header and reading the response bytes — because client-go's typed and dynamic
// clients do not negotiate the Table representation. The raw JSON is unmarshalled
// into a metav1.Table, which carries the column definitions and one row per
// object.
func FetchTable(ctx context.Context, rc rest.Interface, gvr schema.GroupVersionResource, namespaced bool, namespace string) (*metav1.Table, error) {
	raw, err := rc.Get().
		AbsPath(resourcePath(gvr, namespaced, namespace)).
		SetHeader("Accept", tableAccept).
		Do(ctx).
		Raw()
	if err != nil {
		return nil, err
	}
	var t metav1.Table
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ToColumns converts a server-side Table into the engine's column layout and
// rows. It keeps only the columns kubectl shows by default: definitions with
// Priority == 0. (Higher priorities are the "-o wide" extras kubectl hides.) The
// first surviving column is treated as the primary name column — it flexes to
// absorb surplus width and its cells are tagged RoleName so the renderer
// emphasizes them on the selected row. Numeric columns are right-aligned and get
// numeric sort keys so ordering is correct rather than lexicographic.
func ToColumns(t *metav1.Table) ([]columns.Column, []columns.Row) {
	if t == nil {
		return nil, nil
	}

	// keptIdx maps each output column back to its index in the Table's cell
	// arrays, so we can pull the right cell for a row even when hidden columns
	// sit between kept ones.
	var keptIdx []int
	cols := make([]columns.Column, 0, len(t.ColumnDefinitions))
	for i, def := range t.ColumnDefinitions {
		if def.Priority != 0 {
			continue
		}
		title := strings.ToUpper(def.Name)
		col := columns.Column{
			Title: title,
			Align: alignFor(def.Type),
		}
		if len(cols) == 0 {
			// The first kept column is the primary identifier; give it room and
			// let it absorb surplus terminal width.
			col.Grow = 3
			col.MinWidth = 24
		} else {
			col.MinWidth = clamp(len(title)+2, 6, 40)
		}
		cols = append(cols, col)
		keptIdx = append(keptIdx, i)
	}

	rows := make([]columns.Row, 0, len(t.Rows))
	for _, tr := range t.Rows {
		cells := make([]columns.Cell, 0, len(keptIdx))
		sortKeys := make([]columns.SortKey, 0, len(keptIdx))
		for out, src := range keptIdx {
			var text string
			if src < len(tr.Cells) {
				text = stringifyCell(tr.Cells[src])
			}
			role := columns.RolePlain
			if out == 0 {
				role = columns.RoleName
			}
			cells = append(cells, columns.Cell{
				Text:   text,
				Role:   role,
				Status: columns.StatusNeutral,
			})
			sortKeys = append(sortKeys, sortKeyFor(t.ColumnDefinitions[src].Type, text))
		}

		row := columns.Row{
			Cells:    cells,
			SortKeys: sortKeys,
			Health:   columns.StatusNeutral,
		}
		if tr.Object.Raw != nil {
			var meta metav1.PartialObjectMetadata
			if err := json.Unmarshal(tr.Object.Raw, &meta); err == nil {
				row.Namespace = meta.Namespace
				row.Name = meta.Name
				row.UID = string(meta.UID)
				row.Version = meta.ResourceVersion
			}
		}
		rows = append(rows, row)
	}
	return cols, rows
}

// alignFor right-aligns numeric columns (kubectl's Table types "integer" and
// "number") and left-aligns everything else.
func alignFor(colType string) columns.Align {
	if colType == "integer" || colType == "number" {
		return columns.AlignRight
	}
	return columns.AlignLeft
}

// sortKeyFor builds the typed sort key for a cell: a numeric key for numeric
// columns whose text actually parses as a float, and a string key otherwise so a
// blank or non-numeric value still sorts sensibly.
func sortKeyFor(colType, text string) columns.SortKey {
	if colType == "integer" || colType == "number" {
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			return columns.NumKey(f)
		}
	}
	return columns.StrKey(text)
}

// stringifyCell renders one Table cell value as display text. Table JSON decodes
// numbers as float64 (or json.Number when configured), so whole numbers are
// printed without a trailing ".0"; other types fall back to their natural string
// form.
func stringifyCell(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'g', -1, 64)
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}

// clamp constrains n to the inclusive range [lo, hi].
func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
