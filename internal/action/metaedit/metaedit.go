// Package metaedit adds or removes a single label or annotation on any resource
// through a JSON merge patch. A merge patch is the right tool here because its
// null-value semantics let us delete a map key without reading the object first:
// setting a key to null in the merge document removes it, while setting it to a
// string upserts it, leaving every sibling label/annotation untouched. That maps
// cleanly onto `kubectl label`/`kubectl annotate` (set) and their `key-` remove
// form, and it works uniformly across kinds via the dynamic client — no per-kind
// typed client needed.
package metaedit

import (
	"context"
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// metaPatch builds a {"metadata":{field:{key: value}}} JSON merge patch, where
// field is "labels" or "annotations". When remove is true the key maps to a nil
// value so json.Marshal emits JSON null — the merge-patch signal to delete that
// key. Using a map[string]any (rather than fmt) keeps the value correctly quoted
// and escaped for any user-supplied key or value. A marshal error is impossible
// for these plain string/nil maps, so we drop it rather than panicking.
func metaPatch(field, key, value string, remove bool) []byte {
	var entry any
	if remove {
		entry = nil
	} else {
		entry = value
	}
	body := map[string]any{
		"metadata": map[string]any{
			field: map[string]any{
				key: entry,
			},
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil
	}
	return b
}

// LabelPatch builds the merge patch that sets (remove=false) or deletes
// (remove=true) a single label. Set emits {"metadata":{"labels":{key:value}}};
// remove emits {"metadata":{"labels":{key:null}}}.
func LabelPatch(key, value string, remove bool) []byte {
	return metaPatch("labels", key, value, remove)
}

// AnnotationPatch builds the merge patch that sets or deletes a single
// annotation, with the same null-removes-key semantics as LabelPatch.
func AnnotationPatch(key, value string, remove bool) []byte {
	return metaPatch("annotations", key, value, remove)
}

// Apply sends a prepared merge patch to a single object via the dynamic client,
// mirroring write.Delete's namespaced/cluster-scoped branch so cluster-scoped
// kinds (namespaced=false) skip the .Namespace() call. The body comes from the
// LabelPatch/AnnotationPatch builders (or any caller-supplied merge patch).
func Apply(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespaced bool, namespace, name string, body []byte) error {
	if namespaced {
		_, err := dyn.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
		return err
	}
	_, err := dyn.Resource(gvr).Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
	return err
}
