package csr

import (
	"context"
	"testing"

	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func makeCSR(name string, conds ...certv1.CertificateSigningRequestCondition) *certv1.CertificateSigningRequest {
	return &certv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: certv1.CertificateSigningRequestSpec{
			SignerName: "kubernetes.io/kube-apiserver-client",
			Username:   "alice",
		},
		Status: certv1.CertificateSigningRequestStatus{Conditions: conds},
	}
}

func TestCondition(t *testing.T) {
	cases := []struct {
		name  string
		conds []certv1.CertificateSigningRequestCondition
		want  string
	}{
		{"pending", nil, "Pending"},
		{
			"approved",
			[]certv1.CertificateSigningRequestCondition{{Type: certv1.CertificateApproved, Status: corev1.ConditionTrue}},
			"Approved",
		},
		{
			"denied",
			[]certv1.CertificateSigningRequestCondition{{Type: certv1.CertificateDenied, Status: corev1.ConditionTrue}},
			"Denied",
		},
		{
			"failed",
			[]certv1.CertificateSigningRequestCondition{{Type: certv1.CertificateFailed, Status: corev1.ConditionTrue}},
			"Failed",
		},
		{
			// A condition present but not True must not count as decided.
			"approved-but-false",
			[]certv1.CertificateSigningRequestCondition{{Type: certv1.CertificateApproved, Status: corev1.ConditionFalse}},
			"Pending",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Condition(makeCSR("c", tc.conds...)); got != tc.want {
				t.Fatalf("Condition = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApprove(t *testing.T) {
	cs := fake.NewClientset(makeCSR("req-1"))
	ctx := context.Background()

	if err := Approve(ctx, cs, "req-1", "looks good"); err != nil {
		t.Fatalf("Approve returned error: %v", err)
	}

	got, err := cs.CertificatesV1().CertificateSigningRequests().Get(ctx, "req-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after Approve: %v", err)
	}
	if state := Condition(got); state != "Approved" {
		t.Fatalf("after Approve, Condition = %q, want Approved (conditions=%+v)", state, got.Status.Conditions)
	}
	// Verify the reason/message we stamped survived the round-trip.
	var found bool
	for _, c := range got.Status.Conditions {
		if c.Type == certv1.CertificateApproved && c.Status == corev1.ConditionTrue {
			found = true
			if c.Reason != "KubetuiApprove" {
				t.Fatalf("Reason = %q, want KubetuiApprove", c.Reason)
			}
			if c.Message != "looks good" {
				t.Fatalf("Message = %q, want %q", c.Message, "looks good")
			}
		}
	}
	if !found {
		t.Fatalf("no Approved=True condition present: %+v", got.Status.Conditions)
	}
}

func TestDeny(t *testing.T) {
	cs := fake.NewClientset(makeCSR("req-2"))
	ctx := context.Background()

	if err := Deny(ctx, cs, "req-2", "not allowed"); err != nil {
		t.Fatalf("Deny returned error: %v", err)
	}

	got, err := cs.CertificatesV1().CertificateSigningRequests().Get(ctx, "req-2", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after Deny: %v", err)
	}
	if state := Condition(got); state != "Denied" {
		t.Fatalf("after Deny, Condition = %q, want Denied (conditions=%+v)", state, got.Status.Conditions)
	}
}

func TestApproveAlreadyApproved(t *testing.T) {
	cs := fake.NewClientset(makeCSR("req-3",
		certv1.CertificateSigningRequestCondition{Type: certv1.CertificateApproved, Status: corev1.ConditionTrue},
	))
	ctx := context.Background()

	err := Approve(ctx, cs, "req-3", "again")
	if err == nil {
		t.Fatal("Approve on an already-approved CSR should error")
	}
	if want := "CSR req-3 is already Approved"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestApproveMissing(t *testing.T) {
	cs := fake.NewClientset()
	ctx := context.Background()

	if err := Approve(ctx, cs, "nope", "x"); err == nil {
		t.Fatal("Approve on a missing CSR should error")
	}
}
