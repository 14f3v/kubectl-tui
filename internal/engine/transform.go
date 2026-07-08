package engine

import "k8s.io/apimachinery/pkg/api/meta"

// stripManagedFields is the informer transform applied to every cached object.
// managedFields and the last-applied-configuration annotation are large, never
// displayed, and dominate the memory footprint on big clusters, so we drop them
// before the object enters the store. This trims 30–50% of cache memory.
func stripManagedFields(obj any) (any, error) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		// Not a metav1.Object (e.g. a cache.DeletedFinalStateUnknown tombstone);
		// leave it untouched.
		return obj, nil
	}
	accessor.SetManagedFields(nil)
	if ann := accessor.GetAnnotations(); ann != nil {
		if _, ok := ann["kubectl.kubernetes.io/last-applied-configuration"]; ok {
			delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
			accessor.SetAnnotations(ann)
		}
	}
	return obj, nil
}
