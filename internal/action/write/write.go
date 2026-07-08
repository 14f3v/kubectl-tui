// Package write implements the mutating actions that change cluster state:
// delete and force-delete (kill). Writes are fire-and-observe — they never touch
// the read caches; the resulting watch event removes the row. A SelfSubjectAccess
// review lets the UI pre-announce a forbidden action before attempting it.
package write

import (
	"context"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Delete removes an object. When force is true it uses grace period 0 with
// background propagation (the "kill" action). Cluster-scoped kinds pass an empty
// namespace and namespaced=false.
func Delete(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespaced bool, namespace, name string, force bool) error {
	opts := metav1.DeleteOptions{}
	if force {
		zero := int64(0)
		policy := metav1.DeletePropagationBackground
		opts.GracePeriodSeconds = &zero
		opts.PropagationPolicy = &policy
	}
	if namespaced {
		return dyn.Resource(gvr).Namespace(namespace).Delete(ctx, name, opts)
	}
	return dyn.Resource(gvr).Delete(ctx, name, opts)
}

// CanI runs a SelfSubjectAccessReview for a verb on a resource. It is best-effort:
// on error the caller should proceed and let the real request report any denial.
func CanI(ctx context.Context, cs kubernetes.Interface, verb string, gvr schema.GroupVersionResource, namespace string) (bool, error) {
	review := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     gvr.Group,
				Resource:  gvr.Resource,
			},
		},
	}
	res, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, err
	}
	return res.Status.Allowed, nil
}
