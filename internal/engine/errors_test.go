package engine

import (
	"crypto/x509"
	"errors"
	"net"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestAllClassesIsComplete(t *testing.T) {
	// allClasses lets callers enumerate the taxonomy — a bare int cannot be walked
	// otherwise. If a class is added without being listed here, recovery logic that
	// switches over the terminal classes silently gains a hole.
	seen := map[ErrClass]bool{}
	for _, c := range allClasses {
		if seen[c] {
			t.Errorf("duplicate class %v in allClasses", c)
		}
		seen[c] = true
	}
	// ErrClass is a dense iota starting at ClassNone; every value up to the
	// highest listed one must be present.
	var max ErrClass
	for _, c := range allClasses {
		if c > max {
			max = c
		}
	}
	for c := ClassNone; c <= max; c++ {
		if !seen[c] {
			t.Errorf("ErrClass(%d) is missing from allClasses", c)
		}
	}
}

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
		// An exec credential plugin (Teleport tsh, aws eks get-token,
		// gke-gcloud-auth-plugin) that fails to mint credentials is an auth
		// failure, not a blip: retrying runs the same broken command again. These
		// are the message shapes client-go actually produces — it formats them with
		// %v, so the error chain is severed and only the text survives.
		{
			"exec plugin exit",
			errors.New(`Get "https://cluster:3026/api/v1/pods?watch=true": getting credentials: exec: executable tsh failed with exit code 1`),
			ClassAuth, true,
		},
		{
			"exec plugin missing",
			errors.New(`getting credentials: exec: executable aws not found`),
			ClassAuth, true,
		},
		// A genuine network failure must stay transient even though it mentions the
		// same host — the discriminator is the plugin, not the words around it.
		{
			"network not exec",
			errors.New(`Get "https://cluster:3026/api/v1/pods?watch=true": dial tcp: connection refused`),
			ClassTransient, false,
		},
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
