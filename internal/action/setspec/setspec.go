// Package setspec builds strategic-merge patches for kubectl-set-style edits
// (image, env, resources, service account) and applies an arbitrary patch through
// the dynamic client. We emit strategic-merge patches so that a change to one
// container merges into the existing pod spec instead of replacing the whole
// containers list: the containers array is a strategic-merge list keyed by "name",
// so patching a single container by name touches only that container and leaves
// its siblings and unspecified fields untouched.
//
// The same edit applies to two shapes of object. For a bare Pod the container
// lives at spec.containers; for a workload (Deployment, StatefulSet, DaemonSet,
// ...) it lives one level deeper at spec.template.spec.containers. The inTemplate
// bool selects which wrapping to use so callers do not have to know the object
// kind's layout — they only need to know "is this a pod template or a bare pod".
package setspec

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// wrapSpec centralizes the spec-vs-template wrapping. When inTemplate is true the
// given spec fragment is nested under spec.template.spec (a workload's pod
// template); otherwise it sits directly under spec (a bare pod). Keeping this in
// one place means every builder produces identically-shaped nesting and a caller
// flipping inTemplate cannot accidentally target the wrong level.
func wrapSpec(inTemplate bool, spec map[string]any) map[string]any {
	if inTemplate {
		return map[string]any{
			"spec": map[string]any{
				"template": map[string]any{
					"spec": spec,
				},
			},
		}
	}
	return map[string]any{
		"spec": spec,
	}
}

// containerFrag builds the {"containers":[{"name":..., ...}]} strategic-merge
// fragment for a single named container. The list is keyed on "name", so a
// one-element list patches exactly the matching container and merges by name.
func containerFrag(name string, fields map[string]any) map[string]any {
	c := map[string]any{"name": name}
	for k, v := range fields {
		c[k] = v
	}
	return map[string]any{
		"containers": []any{c},
	}
}

// marshal serializes a patch body. The maps we build here contain only plain
// strings, maps, and slices, so a marshal error would be a programming bug; we
// return a nil body rather than panicking, and json.Valid in tests guards the
// happy path.
func marshal(v map[string]any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// SetImage patches a single container's image. This is the workhorse behind
// "set image", equivalent to `kubectl set image`: only the named container's
// image field changes.
func SetImage(inTemplate bool, container, image string) (types.PatchType, []byte) {
	spec := containerFrag(container, map[string]any{"image": image})
	return types.StrategicMergePatchType, marshal(wrapSpec(inTemplate, spec))
}

// SetEnv replaces the named container's env with the given key/value pairs. The
// entries are sorted by key so the produced patch is deterministic (stable output
// makes tests reliable and diffs readable). Because env is itself a strategic-merge
// list keyed by "name", listing a variable here upserts it.
func SetEnv(inTemplate bool, container string, env map[string]string) (types.PatchType, []byte) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	list := make([]any, 0, len(keys))
	for _, k := range keys {
		list = append(list, map[string]any{"name": k, "value": env[k]})
	}
	spec := containerFrag(container, map[string]any{"env": list})
	return types.StrategicMergePatchType, marshal(wrapSpec(inTemplate, spec))
}

// SetResources patches a container's resource requests and limits. Values are
// parsed with resource.ParseQuantity (never MustParse) so a single bad value from
// user input cannot panic the process; invalid quantities are silently skipped.
// The canonical quantity string is stored, which the API server accepts directly.
func SetResources(inTemplate bool, container string, requests, limits map[string]string) (types.PatchType, []byte) {
	resources := map[string]any{}
	if r := quantityMap(requests); len(r) > 0 {
		resources["requests"] = r
	}
	if l := quantityMap(limits); len(l) > 0 {
		resources["limits"] = l
	}
	spec := containerFrag(container, map[string]any{"resources": resources})
	return types.StrategicMergePatchType, marshal(wrapSpec(inTemplate, spec))
}

// SetServiceAccount sets serviceAccountName at the pod-spec level (template.spec
// for a workload, spec for a bare pod). We use the corev1.PodSpec JSON tag as the
// canonical key so the patch matches exactly what the API server expects.
func SetServiceAccount(inTemplate bool, name string) (types.PatchType, []byte) {
	spec := map[string]any{serviceAccountNameKey: name}
	return types.StrategicMergePatchType, marshal(wrapSpec(inTemplate, spec))
}

// Patch applies a prepared patch to a single object via the dynamic client,
// mirroring write.Delete's namespaced/cluster-scoped branch so cluster-scoped
// kinds (namespaced=false) skip the .Namespace() call. The body and patch type
// come from the builders above (or any caller-supplied strategic-merge patch).
func Patch(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespaced bool, namespace, name string, pt types.PatchType, body []byte) error {
	if namespaced {
		_, err := dyn.Resource(gvr).Namespace(namespace).Patch(ctx, name, pt, body, metav1.PatchOptions{})
		return err
	}
	_, err := dyn.Resource(gvr).Patch(ctx, name, pt, body, metav1.PatchOptions{})
	return err
}

// quantityMap parses a map of resource strings into a map of canonical quantity
// strings, skipping any value that fails to parse. Storing the canonical string
// (rather than a resource.Quantity) keeps the resulting JSON simple and avoids
// leaking the internal quantity representation into the patch body.
func quantityMap(in map[string]string) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		if strings.TrimSpace(v) == "" {
			continue
		}
		q, err := resource.ParseQuantity(v)
		if err != nil {
			continue
		}
		out[k] = q.String()
	}
	return out
}

// serviceAccountNameKey is the JSON field name for PodSpec.ServiceAccountName. We
// derive it from the corev1 type rather than hard-coding a bare string so the key
// stays anchored to the real API shape; referencing corev1 here also documents
// that these patches are pod-spec edits.
const serviceAccountNameKey = "serviceAccountName"

// _ keeps corev1 referenced as the canonical source of the resource-name keys
// (e.g. corev1.ResourceCPU / corev1.ResourceMemory) that callers pass into
// SetResources' requests and limits maps.
var _ = corev1.ResourceCPU
