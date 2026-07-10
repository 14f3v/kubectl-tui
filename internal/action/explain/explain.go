// Package explain renders field documentation for a resource from the cluster's
// OpenAPI v3 schema, mirroring `kubectl explain`. Given an expression like
// "pod.spec.containers" it resolves the kind via discovery, fetches the
// group-version's OpenAPI v3 document, walks the field path through the schema
// (following $ref, array items, and additionalProperties), and returns readable
// plain text. It is pure Go + client-go with no TUI dependencies; the cluster
// touching lives only in Explain, while every navigation and rendering step is a
// pure helper that operates on already-parsed data so it can be unit-tested
// without a live cluster.
package explain

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// Explain resolves expr ("<kind>[.field.path]", e.g. "pods",
// "pod.spec.containers", "deployments.spec.replicas") to a GVK via discovery,
// fetches that group-version's OpenAPI v3 document, navigates the field path
// through the schema, and returns kubectl-explain-style text. The first
// dot-separated token is the kind/resource token; the remainder is the field
// path. Discovery is tolerated partially: a non-empty result proceeds even when
// the error is non-nil, since some clusters fail a subset of groups. An empty
// expression, an unresolved token, a missing OpenAPI document, or an unknown
// field are all clear errors.
func Explain(ctx context.Context, disco discovery.DiscoveryInterface, expr string) (string, error) {
	token, path := splitExpr(expr)
	if token == "" {
		return "", fmt.Errorf("explain: empty resource expression")
	}

	gvk, err := resolveKind(disco, token)
	if err != nil {
		return "", err
	}

	b, err := fetchSchema(disco, gvk)
	if err != nil {
		return "", err
	}

	d, err := parseDoc(b)
	if err != nil {
		return "", err
	}

	root, ok := d.schemaForGVK(gvk)
	if !ok {
		return "", fmt.Errorf("explain: no OpenAPI schema for %s", gvk)
	}

	node, err := d.navigate(root, path)
	if err != nil {
		return "", err
	}

	gv := gvk.GroupVersion().String()
	fieldPath := token
	if len(path) > 0 {
		fieldPath = token + "." + strings.Join(path, ".")
	}
	return renderSchema(gvk.Kind, gv, fieldPath, node), nil
}

// splitExpr splits an explain expression into its resource token (before the
// first dot) and the field path (the remaining dot-separated segments). Empty
// path segments are dropped so trailing or doubled dots do not produce blank
// steps.
func splitExpr(expr string) (token string, path []string) {
	parts := strings.Split(strings.TrimSpace(expr), ".")
	if len(parts) == 0 {
		return "", nil
	}
	token = parts[0]
	for _, p := range parts[1:] {
		if p != "" {
			path = append(path, p)
		}
	}
	return token, path
}

// resolveKind matches token (case-insensitively) against the served resources'
// plural Name, SingularName, ShortNames, or Kind, returning the GVK of the first
// match. It tolerates partial discovery failure: a non-empty result proceeds
// even when the error is non-nil. An unmatched token is a clear error.
func resolveKind(disco discovery.DiscoveryInterface, token string) (schema.GroupVersionKind, error) {
	lists, err := disco.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return schema.GroupVersionKind{}, err
	}
	gvk, ok := findKind(lists, token)
	if !ok {
		return schema.GroupVersionKind{}, fmt.Errorf("explain: %q matches no known resource", token)
	}
	return gvk, nil
}

// findKind scans discovery results for the resource whose plural Name,
// SingularName, one of its ShortNames, or Kind equals token case-insensitively,
// returning its GVK. Subresources (names containing "/") are skipped so we match
// only top-level kinds. It is pure so it can be unit-tested against a hand-built
// resource list.
func findKind(lists []*metav1.APIResourceList, token string) (schema.GroupVersionKind, bool) {
	want := strings.ToLower(token)
	for _, list := range lists {
		if list == nil {
			continue
		}
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue // subresource, e.g. pods/status
			}
			if matchesResource(r, want) {
				return gv.WithKind(r.Kind), true
			}
		}
	}
	return schema.GroupVersionKind{}, false
}

// matchesResource reports whether want (already lowercased) equals the resource's
// plural Name, SingularName, Kind, or any ShortName, compared case-insensitively.
func matchesResource(r metav1.APIResource, want string) bool {
	if strings.ToLower(r.Name) == want ||
		strings.ToLower(r.SingularName) == want ||
		strings.ToLower(r.Kind) == want {
		return true
	}
	for _, s := range r.ShortNames {
		if strings.ToLower(s) == want {
			return true
		}
	}
	return false
}

// fetchSchema retrieves the OpenAPI v3 document JSON for the GVK's group-version.
// The path key is "api/v1" for the core group and "apis/<group>/<version>"
// otherwise. A missing key or a Schema fetch error is surfaced with context.
func fetchSchema(disco discovery.DiscoveryInterface, gvk schema.GroupVersionKind) ([]byte, error) {
	paths, err := disco.OpenAPIV3().Paths()
	if err != nil {
		return nil, err
	}
	key := openAPIPathKey(gvk.Group, gvk.Version)
	gvDoc, ok := paths[key]
	if !ok {
		return nil, fmt.Errorf("explain: no OpenAPI v3 document for %q", key)
	}
	b, err := gvDoc.Schema("application/json")
	if err != nil {
		return nil, fmt.Errorf("explain: fetch OpenAPI schema for %q: %w", key, err)
	}
	return b, nil
}

// openAPIPathKey builds the Paths() map key for a group/version: "api/v1" for the
// core group (empty group) and "apis/<group>/<version>" for grouped resources.
func openAPIPathKey(group, version string) string {
	if group == "" {
		return "api/" + version
	}
	return "apis/" + group + "/" + version
}

// doc is a parsed OpenAPI v3 document reduced to its components.schemas map
// (schemaName -> schema object). Every navigation helper hangs off it so field
// walking needs no cluster access.
type doc struct {
	schemas map[string]any
}

// parseDoc parses OpenAPI v3 document JSON into a doc, extracting
// components.schemas. Malformed JSON or a missing schemas map is an error, since
// without it no field can be resolved.
func parseDoc(b []byte) (doc, error) {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return doc{}, fmt.Errorf("explain: parse OpenAPI document: %w", err)
	}
	components, _ := raw["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if schemas == nil {
		return doc{}, fmt.Errorf("explain: OpenAPI document has no components.schemas")
	}
	return doc{schemas: schemas}, nil
}

// schemaForGVK finds the schema object describing a GVK. It prefers an exact
// match on the schema's "x-kubernetes-group-version-kind" extension (an array of
// {group,version,kind}); failing that it falls back to a name heuristic, matching
// a schema key that ends in ".<Kind>" for the right version. The boolean reports
// whether a schema was found.
func (d doc) schemaForGVK(gvk schema.GroupVersionKind) (map[string]any, bool) {
	// Exact match via the group-version-kind extension.
	for _, v := range d.schemas {
		obj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if schemaHasGVK(obj, gvk) {
			return obj, true
		}
	}
	// Name heuristic: a schema key like "io.k8s.api.core.v1.Pod".
	suffix := "." + gvk.Kind
	verSeg := "." + gvk.Version + "."
	for name, v := range d.schemas {
		obj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if strings.HasSuffix(name, suffix) && strings.Contains(name, verSeg) {
			return obj, true
		}
	}
	return nil, false
}

// schemaHasGVK reports whether a schema object carries an
// "x-kubernetes-group-version-kind" entry matching the given GVK.
func schemaHasGVK(obj map[string]any, gvk schema.GroupVersionKind) bool {
	raw, ok := obj["x-kubernetes-group-version-kind"].([]any)
	if !ok {
		return false
	}
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		g, _ := m["group"].(string)
		v, _ := m["version"].(string)
		k, _ := m["kind"].(string)
		if g == gvk.Group && v == gvk.Version && k == gvk.Kind {
			return true
		}
	}
	return false
}

// resolveRef follows a single "$ref" ("#/components/schemas/NAME") on node,
// returning the referenced schema. A node without a "$ref", or one whose target
// is absent, is returned unchanged so callers can treat resolution as idempotent.
func (d doc) resolveRef(node map[string]any) map[string]any {
	if ref, ok := node["$ref"].(string); ok {
		const prefix = "#/components/schemas/"
		name := strings.TrimPrefix(ref, prefix)
		if name == ref { // not a local components ref we can resolve
			return node
		}
		if target, ok := d.schemas[name].(map[string]any); ok {
			return target
		}
		return node
	}
	// Kubernetes wraps an object-typed field as `allOf: [{$ref: ...}]` (usually with
	// a sibling default/description), rather than a bare $ref. Resolve the first
	// $ref inside allOf so navigation can descend into the referenced type's
	// properties — without this, `explain pod.spec.containers` stops at `spec`.
	if allOf, ok := node["allOf"].([]any); ok {
		for _, e := range allOf {
			if em, ok := e.(map[string]any); ok {
				if _, hasRef := em["$ref"]; hasRef {
					return d.resolveRef(em)
				}
			}
		}
	}
	return node
}

// navigate walks path from root through the schema, selecting properties[field]
// at each step. Before descending it resolves any "$ref", and it unwraps
// container schemas: an "array" node steps into its "items" and a map node
// (object with additionalProperties) steps into that value schema, so a path can
// traverse list and map elements transparently. An unknown field is a clear
// error naming the offending segment.
func (d doc) navigate(root map[string]any, path []string) (map[string]any, error) {
	node := d.resolveRef(root)
	for _, field := range path {
		node = d.unwrapContainers(node)
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("explain: field %q not found (node has no fields)", field)
		}
		next, ok := props[field].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("explain: field %q not found", field)
		}
		node = d.resolveRef(next)
	}
	return d.unwrapContainers(node), nil
}

// unwrapContainers descends through container wrappers so property lookups land
// on the element schema: it steps into an array's "items" and into an object's
// "additionalProperties" (a map value schema), resolving a "$ref" at each layer.
// A plain object or scalar is returned unchanged. It loops so an array-of-map or
// similar nesting is fully unwrapped.
func (d doc) unwrapContainers(node map[string]any) map[string]any {
	for {
		if typeString(node) == "array" {
			items, ok := node["items"].(map[string]any)
			if !ok {
				return node
			}
			node = d.resolveRef(items)
			continue
		}
		if ap, ok := node["additionalProperties"].(map[string]any); ok {
			// A map value schema; descend only when it is a structured value
			// (has its own properties or a ref) rather than a bare "true".
			if _, hasRef := ap["$ref"]; hasRef {
				node = d.resolveRef(ap)
				continue
			}
			if _, hasProps := ap["properties"]; hasProps {
				node = ap
				continue
			}
		}
		return node
	}
}

// typeString returns a schema node's "type" as a string, or "" when absent or not
// a string. OpenAPI v3 also permits type to be a list; we only read the scalar
// form here, which is what the Kubernetes-published schemas use.
func typeString(node map[string]any) string {
	t, _ := node["type"].(string)
	return t
}

// renderSchema formats a resolved schema node as kubectl-explain-style plain
// text: a KIND/VERSION header, the FIELD line with its path and type, a
// DESCRIPTION block, and (for objects) a FIELDS list of immediate fields as
// "  <name>\t<type>\t<first line of description>" sorted by name. kind and gv
// come from the resolved GVK; fieldPath is the full dotted expression.
func renderSchema(kind, gv, fieldPath string, node map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "KIND:     %s\n", kind)
	fmt.Fprintf(&b, "VERSION:  %s\n\n", gv)

	fmt.Fprintf(&b, "FIELD: %s <%s>\n\n", fieldPath, fieldType(node))

	b.WriteString("DESCRIPTION:\n")
	desc := stringOr(node["description"], "<no description>")
	for _, line := range strings.Split(desc, "\n") {
		fmt.Fprintf(&b, "    %s\n", line)
	}

	fields := immediateFields(node)
	if len(fields) > 0 {
		b.WriteString("\nFIELDS:\n")
		for _, f := range fields {
			fmt.Fprintf(&b, "  %s\t%s\t%s\n", f.name, f.typ, f.desc)
		}
	}
	return b.String()
}

// fieldType returns a human label for a schema node's type. Arrays are rendered
// as "[]<element>" and objects with additionalProperties as "map[string]<value>";
// a "$ref" is reported as "Object". A bare scalar returns its type; an untyped
// node returns "Object".
func fieldType(node map[string]any) string {
	if _, ok := node["$ref"].(string); ok {
		return "Object"
	}
	switch typeString(node) {
	case "array":
		if items, ok := node["items"].(map[string]any); ok {
			return "[]" + fieldType(items)
		}
		return "[]Object"
	case "object", "":
		if ap, ok := node["additionalProperties"].(map[string]any); ok {
			return "map[string]" + fieldType(ap)
		}
		return "Object"
	default:
		return typeString(node)
	}
}

// field is one immediate property of an object schema, prepared for the FIELDS
// list: its name, human type label, and the first line of its description.
type field struct {
	name string
	typ  string
	desc string
}

// immediateFields lists an object schema's direct properties as field rows,
// sorted by name for stable output. A node without a properties map (scalar,
// array, or unstructured) yields no rows.
func immediateFields(node map[string]any) []field {
	props, ok := node["properties"].(map[string]any)
	if !ok {
		return nil
	}
	fields := make([]field, 0, len(props))
	for name, v := range props {
		child, ok := v.(map[string]any)
		if !ok {
			continue
		}
		fields = append(fields, field{
			name: name,
			typ:  fieldType(child),
			desc: firstLine(stringOr(child["description"], "")),
		})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].name < fields[j].name })
	return fields
}

// firstLine returns the first line of s (up to the first newline), trimmed of
// trailing space, so a field's summary stays to one line in the FIELDS list.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// stringOr returns v as a string when it is one and non-empty, otherwise def.
func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}
