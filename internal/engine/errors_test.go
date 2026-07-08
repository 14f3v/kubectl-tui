package engine

import (
	"crypto/x509"
	"errors"
	"net"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestClassify(t *testing.T) {
	gr := schema.GroupResource{Group: "", Resource: "pods"}
	cases := []struct {
		name     string
		err      error
		want     ErrClass
		terminal bool
	}{
		{"nil", nil, ClassNone, false},
		{"forbidden", apierrors.NewForbidden(gr, "web", errors.New("nope")), ClassForbidden, true},
		{"unauthorized", apierrors.NewUnauthorized("bad token"), ClassAuth, true},
		{"notfound", apierrors.NewNotFound(gr, "web"), ClassNotFound, false},
		{"conflict", apierrors.NewConflict(gr, "web", errors.New("rv")), ClassConflict, false},
		{"tls", x509.UnknownAuthorityError{}, ClassTLS, true},
		{"network", &net.OpError{Op: "dial", Err: errors.New("refused")}, ClassTransient, false},
		{"servertimeout", apierrors.NewServerTimeout(gr, "list", 1), ClassTransient, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ee := Classify("op", tc.err)
			if tc.err == nil {
				if ee != nil {
					t.Fatalf("Classify(nil) = %+v, want nil", ee)
				}
				return
			}
			if ee == nil {
				t.Fatalf("Classify returned nil for %v", tc.err)
			}
			if ee.Class != tc.want {
				t.Fatalf("class = %v, want %v", ee.Class, tc.want)
			}
			if ee.Terminal() != tc.terminal {
				t.Fatalf("terminal = %v, want %v", ee.Terminal(), tc.terminal)
			}
		})
	}
}

func TestClassify_WrappedTLS(t *testing.T) {
	// TLS errors are commonly wrapped by the transport; Classify must unwrap them.
	inner := x509.CertificateInvalidError{Reason: x509.Expired}
	wrapped := errors.Join(errors.New("get https://api: "), inner)
	ee := Classify("watch", wrapped)
	if ee.Class != ClassTLS || !ee.Terminal() {
		t.Fatalf("wrapped TLS classified as %v (terminal=%v), want TLS/terminal", ee.Class, ee.Terminal())
	}
}
