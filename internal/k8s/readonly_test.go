package k8s

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// recordingRT stands in for the real transport so tests can tell "refused before
// it left the process" from "sent to the API server".
type recordingRT struct{ called bool }

func (rt *recordingRT) RoundTrip(*http.Request) (*http.Response, error) {
	rt.called = true
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func do(t *testing.T, method, path string) (*recordingRT, error) {
	t.Helper()
	base := &recordingRT{}
	rt := readOnlyRoundTripper{base: base}
	req := httptest.NewRequest(method, "https://cluster.example"+path, nil)
	_, err := rt.RoundTrip(req)
	return base, err
}

func TestReadOnlyAllowsReads(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/namespaces/default/pods"},
		{http.MethodGet, "/api/v1/namespaces/default/pods/web/log"},
		{http.MethodGet, "/apis/apps/v1/deployments"},
		{http.MethodHead, "/version"},
	} {
		base, err := do(t, tc.method, tc.path)
		if err != nil {
			t.Errorf("%s %s: %v, want allowed", tc.method, tc.path, err)
		}
		if !base.called {
			t.Errorf("%s %s: request never reached the transport", tc.method, tc.path)
		}
	}
}

func TestReadOnlyRefusesMutations(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/namespaces/default/pods"},
		{http.MethodPut, "/apis/apps/v1/namespaces/default/deployments/web"},
		{http.MethodPatch, "/apis/apps/v1/namespaces/default/deployments/web"},
		{http.MethodDelete, "/api/v1/namespaces/default/pods/web"},
		{http.MethodPost, "/apis/apps/v1/namespaces/default/deployments/web/scale"},
	} {
		base, err := do(t, tc.method, tc.path)
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s %s: err = %v, want ErrReadOnly", tc.method, tc.path, err)
		}
		if base.called {
			t.Errorf("%s %s: mutation reached the transport", tc.method, tc.path)
		}
	}
}

func TestReadOnlyRefusesExecAndPortForward(t *testing.T) {
	// These are the holes the UI-level guard misses today: `s` opens a shell and
	// `C` copies files into a container, both of which mutate freely from inside;
	// port-forward needs create on pods/portforward. RBAC treats all three as
	// create verbs, so a genuinely read-only account cannot do them either.
	for _, path := range []string{
		"/api/v1/namespaces/default/pods/web/exec",
		"/api/v1/namespaces/default/pods/web/attach",
		"/api/v1/namespaces/default/pods/web/portforward",
	} {
		base, err := do(t, http.MethodPost, path)
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("POST %s: err = %v, want ErrReadOnly", path, err)
		}
		if base.called {
			t.Errorf("POST %s: reached the transport", path)
		}
	}
}

func TestReadOnlyAllowsSelfReviews(t *testing.T) {
	// These are POSTs that only read: they answer "who am I" and "can I", and
	// blocking them would break :whoami and the can-i grid for exactly the users
	// most likely to be running in read-only mode.
	for _, path := range []string{
		"/apis/authorization.k8s.io/v1/selfsubjectaccessreviews",
		"/apis/authorization.k8s.io/v1/selfsubjectrulesreviews",
		"/apis/authentication.k8s.io/v1/selfsubjectreviews",
	} {
		base, err := do(t, http.MethodPost, path)
		if err != nil {
			t.Errorf("POST %s: %v, want allowed", path, err)
		}
		if !base.called {
			t.Errorf("POST %s: never reached the transport", path)
		}
	}
}

func TestReadOnlyDoesNotAllowLookalikePaths(t *testing.T) {
	// The allowlist is a path-segment match, not a substring one: a CRD or
	// namespace whose name merely contains a review resource must not slip past.
	for _, path := range []string{
		"/apis/example.com/v1/selfsubjectreviewsextra",
		"/api/v1/namespaces/selfsubjectreviews/pods",
		"/apis/example.com/v1/namespaces/x/myselfsubjectreviews",
	} {
		base, err := do(t, http.MethodPost, path)
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("POST %s: err = %v, want ErrReadOnly", path, err)
		}
		if base.called {
			t.Errorf("POST %s: reached the transport", path)
		}
	}
}
