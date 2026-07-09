// Package apply implements the apply action: take arbitrary (possibly
// multi-document) YAML — as a user might paste or load from a manifest file —
// and reconcile every document into the cluster with server-side apply. It
// mirrors `kubectl apply --server-side`: each doc is treated as the desired
// state and PATCHed with types.ApplyPatchType under the "kubetui" field manager,
// so we own only the fields we send and never clobber fields owned by other
// managers.
//
// Unlike the editor action (which already knows the GVR of the single object it
// dumped), apply receives free-form YAML of unknown kinds. It therefore needs a
// RESTMapper to turn each document's GroupVersionKind into the REST resource
// (GVR) and namespace scope before it can address the dynamic client. Each
// document is applied independently and its outcome recorded, so one bad doc
// never aborts the rest of the batch — the UI can show a per-document report.
package apply

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/yaml"
)

// NewMapper builds a RESTMapper backed by a cached copy of the server's
// discovery data. The deferred mapper only hits discovery on the first lookup
// (and the memory cache keeps repeat lookups off the wire), which matters
// because a multi-doc apply may resolve many kinds in a row. It is a discrete
// helper so callers can build the mapper once and reuse it across Apply calls.
func NewMapper(disco discovery.DiscoveryInterface) meta.RESTMapper {
	return restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disco))
}

// Result records the outcome of applying a single YAML document. GVK, Namespace
// and Name identify the object as best we could resolve it, so the UI can point
// at the exact document that failed; Err is nil on success. Fields are filled in
// progressively — a doc that fails to parse still yields a Result (with whatever
// we learned before the failure) rather than being silently dropped.
type Result struct {
	GVK       string
	Namespace string
	Name      string
	Err       error
}

// SplitDocuments splits a multi-document YAML stream into its individual raw
// documents. Documents are separated by a line that is exactly "---" (the YAML
// document separator), which is why we compare trimmed lines rather than using a
// naive strings.Split on "---" — that would also cut on "---" appearing inside a
// value or block scalar. Documents that are empty or contain only whitespace and
// comments carry no object to apply, so they are dropped; the surviving raw docs
// are returned unmodified (trailing newline trimmed).
func SplitDocuments(data []byte) [][]byte {
	var docs [][]byte
	var cur [][]byte

	flush := func() {
		joined := bytes.Join(cur, []byte("\n"))
		cur = nil
		if hasContent(joined) {
			docs = append(docs, bytes.TrimRight(joined, "\n"))
		}
	}

	for _, line := range bytes.Split(data, []byte("\n")) {
		if strings.TrimSpace(string(line)) == "---" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return docs
}

// hasContent reports whether a raw YAML document carries anything to apply, i.e.
// at least one line that is not blank and not a comment. A document that is only
// whitespace and "#" comments unmarshals to nothing, so we skip it upstream.
func hasContent(doc []byte) bool {
	for _, line := range bytes.Split(doc, []byte("\n")) {
		t := strings.TrimSpace(string(line))
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		return true
	}
	return false
}

// parseDoc unmarshals one raw YAML document into an *unstructured.Unstructured
// and returns its GroupVersionKind. YAML is converted via yaml.Unmarshal into a
// generic map so we can apply objects of any kind without compiled-in types. The
// GVK is read from apiVersion/kind, which SplitDocuments guarantees are the only
// content-bearing docs we hand in.
func parseDoc(doc []byte) (*unstructured.Unstructured, schema.GroupVersionKind, error) {
	var m map[string]interface{}
	if err := yaml.Unmarshal(doc, &m); err != nil {
		return nil, schema.GroupVersionKind{}, fmt.Errorf("parse YAML: %w", err)
	}
	obj := &unstructured.Unstructured{Object: m}
	apiVersion := obj.GetAPIVersion()
	kind := obj.GetKind()
	if apiVersion == "" || kind == "" {
		return obj, schema.GroupVersionKind{}, fmt.Errorf("document is missing apiVersion or kind")
	}
	return obj, schema.FromAPIVersionAndKind(apiVersion, kind), nil
}

// Apply reconciles every document in data into the cluster with server-side
// apply and returns one Result per document, in order. For each doc it resolves
// the GVK, maps it to a REST resource + scope, defaults a namespaced object's
// namespace to defaultNamespace when the doc omits one, and PATCHes the object
// as the desired state. Force is always true so we take ownership of conflicting
// fields (matching `kubectl apply --server-side --force-conflicts`); without it
// a field co-owned by another manager would fail the apply. A per-doc error is
// recorded and processing continues, so a single malformed or un-mappable
// document cannot sink the rest of the batch.
func Apply(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, data []byte, fieldManager, defaultNamespace string) []Result {
	var results []Result

	for _, doc := range SplitDocuments(data) {
		var res Result

		obj, gvk, err := parseDoc(doc)
		res.GVK = gvk.String()
		if err != nil {
			res.Err = err
			results = append(results, res)
			continue
		}
		res.Name = obj.GetName()

		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			res.Err = fmt.Errorf("no REST mapping for %s: %w", gvk.String(), err)
			results = append(results, res)
			continue
		}

		ns := obj.GetNamespace()
		namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
		if namespaced && ns == "" {
			ns = defaultNamespace
		}
		res.Namespace = ns

		jsonBytes, err := obj.MarshalJSON()
		if err != nil {
			res.Err = fmt.Errorf("encode object: %w", err)
			results = append(results, res)
			continue
		}

		force := true
		opts := metav1.PatchOptions{FieldManager: fieldManager, Force: &force}
		if namespaced {
			_, err = dyn.Resource(mapping.Resource).Namespace(ns).Patch(ctx, obj.GetName(), types.ApplyPatchType, jsonBytes, opts)
		} else {
			_, err = dyn.Resource(mapping.Resource).Patch(ctx, obj.GetName(), types.ApplyPatchType, jsonBytes, opts)
		}
		res.Err = err
		results = append(results, res)
	}

	return results
}
