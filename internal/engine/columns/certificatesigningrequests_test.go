package columns

import (
	"testing"
	"time"

	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func makeCSR(name string, mut func(*certv1.CertificateSigningRequest)) *certv1.CertificateSigningRequest {
	csr := &certv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			UID:               types.UID("uid-" + name),
			ResourceVersion:   "1",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
		},
		Spec: certv1.CertificateSigningRequestSpec{
			SignerName: "kubernetes.io/kube-apiserver-client",
			Username:   "alice",
		},
	}
	if mut != nil {
		mut(csr)
	}
	return csr
}

func TestCSRProject_Pending(t *testing.T) {
	proj := For("certificatesigningrequests")
	if proj == nil {
		t.Fatal("certificatesigningrequests projector not registered")
	}
	csr := makeCSR("req-pending", nil)

	row, ok := proj.Project(csr, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.UID != "uid-req-pending" || row.Name != "req-pending" {
		t.Fatalf("identity mismatch: %+v", row)
	}
	// CSRs are cluster-scoped, so Namespace must stay empty.
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if got := cellByTitle(t, proj, row, "CONDITION"); got.Text != "Pending" || got.Status != StatusWarn || got.Role != RoleStatus {
		t.Fatalf("CONDITION = %q/%v/%v, want Pending/Warn/RoleStatus", got.Text, got.Status, got.Role)
	}
	if row.Health != StatusWarn {
		t.Fatalf("Health = %v, want StatusWarn", row.Health)
	}
	if got := cellByTitle(t, proj, row, "SIGNERNAME"); got.Text != "kubernetes.io/kube-apiserver-client" {
		t.Fatalf("SIGNERNAME = %q", got.Text)
	}
	if got := cellByTitle(t, proj, row, "REQUESTOR"); got.Text != "alice" {
		t.Fatalf("REQUESTOR = %q, want alice", got.Text)
	}
	// No ExpirationSeconds requested -> dash.
	if got := cellByTitle(t, proj, row, "REQUESTEDDURATION"); got.Text != "—" {
		t.Fatalf("REQUESTEDDURATION = %q, want dash", got.Text)
	}
}

func TestCSRProject_Approved(t *testing.T) {
	proj := For("certificatesigningrequests")
	if proj == nil {
		t.Fatal("certificatesigningrequests projector not registered")
	}
	exp := int32(3600)
	csr := makeCSR("req-approved", func(c *certv1.CertificateSigningRequest) {
		c.Spec.ExpirationSeconds = &exp
		c.Status.Conditions = []certv1.CertificateSigningRequestCondition{
			{Type: certv1.CertificateApproved, Status: corev1.ConditionTrue},
		}
	})

	row, ok := proj.Project(csr, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := cellByTitle(t, proj, row, "CONDITION"); got.Text != "Approved" || got.Status != StatusOK {
		t.Fatalf("CONDITION = %q/%v, want Approved/OK", got.Text, got.Status)
	}
	if row.Health != StatusOK {
		t.Fatalf("Health = %v, want StatusOK", row.Health)
	}
	// 3600s -> humanAge should render "1h".
	if got := cellByTitle(t, proj, row, "REQUESTEDDURATION"); got.Text != "1h" {
		t.Fatalf("REQUESTEDDURATION = %q, want 1h", got.Text)
	}
}

func TestCSRProject_Denied(t *testing.T) {
	proj := For("certificatesigningrequests")
	csr := makeCSR("req-denied", func(c *certv1.CertificateSigningRequest) {
		c.Status.Conditions = []certv1.CertificateSigningRequestCondition{
			{Type: certv1.CertificateDenied, Status: corev1.ConditionTrue},
		}
	})

	row, ok := proj.Project(csr, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := cellByTitle(t, proj, row, "CONDITION"); got.Text != "Denied" || got.Status != StatusError {
		t.Fatalf("CONDITION = %q/%v, want Denied/Error", got.Text, got.Status)
	}
	if row.Health != StatusError {
		t.Fatalf("Health = %v, want StatusError", row.Health)
	}
}
