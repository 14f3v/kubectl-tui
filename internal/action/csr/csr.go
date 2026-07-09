// Package csr approves or denies CertificateSigningRequests. Approval and denial
// are recorded on the CSR's approval subresource (not spec), matching how
// `kubectl certificate approve/deny` works, so the certificate controller sees a
// well-formed decision. Both are terminal: a CSR can only be decided once, so we
// refuse to act on one that is already approved, denied, or failed.
package csr

import (
	"context"
	"fmt"

	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Condition collapses a CSR's condition list into the single word the UI shows:
// "Approved", "Denied", "Failed", or "Pending". Approval, denial, and failure are
// mutually exclusive terminal states enforced by the API server, so the first
// matching true condition wins; a CSR with none is still Pending.
func Condition(csr *certv1.CertificateSigningRequest) string {
	for _, c := range csr.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case certv1.CertificateApproved:
			return "Approved"
		case certv1.CertificateDenied:
			return "Denied"
		case certv1.CertificateFailed:
			return "Failed"
		}
	}
	return "Pending"
}

// Approve records an approval decision on a pending CSR. It first fetches the
// current object so the update carries the right resourceVersion, refuses to
// touch a CSR that has already been decided (approval is one-shot), then appends
// an Approved condition and writes it through the approval subresource. reason is
// surfaced to operators inspecting the CSR later.
func Approve(ctx context.Context, cs kubernetes.Interface, name, reason string) error {
	return decide(ctx, cs, name, certv1.CertificateApproved, "KubetuiApprove", reason)
}

// Deny records a denial decision on a pending CSR. It mirrors Approve, writing a
// Denied condition instead so the signer never issues a certificate.
func Deny(ctx context.Context, cs kubernetes.Interface, name, reason string) error {
	return decide(ctx, cs, name, certv1.CertificateDenied, "KubetuiDeny", reason)
}

// decide is the shared approve/deny body: the two actions differ only in the
// condition type and reason they stamp onto the CSR.
func decide(ctx context.Context, cs kubernetes.Interface, name string, condType certv1.RequestConditionType, condReason, message string) error {
	client := cs.CertificatesV1().CertificateSigningRequests()
	csr, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if state := Condition(csr); state != "Pending" {
		return fmt.Errorf("CSR %s is already %s", name, state)
	}
	csr.Status.Conditions = append(csr.Status.Conditions, certv1.CertificateSigningRequestCondition{
		Type:    condType,
		Status:  corev1.ConditionTrue,
		Reason:  condReason,
		Message: message,
	})
	_, err = client.UpdateApproval(ctx, name, csr, metav1.UpdateOptions{})
	return err
}
