package k8s

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrReadOnly is returned instead of sending a request that would change the
// cluster while read-only mode is on.
var ErrReadOnly = errors.New("read-only mode: this operation is disabled")

// readOnlyRoundTripper refuses every mutating request before it leaves the
// process.
//
// It sits on rest.Config, so it covers every client built from that config —
// typed, dynamic, discovery, metrics — and, because both
// transport/spdy.RoundTripperFor and transport/websocket.RoundTripperFor route
// through transport.HTTPWrappersForConfig, it covers exec, attach and
// port-forward too. That reach is the point: gating in the UI means a page that
// forgets the check can still mutate, and three already do (shell, cp and
// port-forward are ungated today).
type readOnlyRoundTripper struct{ base http.RoundTripper }

func (rt readOnlyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if isMutatingRequest(req) {
		return nil, fmt.Errorf("%w (%s %s)", ErrReadOnly, req.Method, req.URL.Path)
	}
	return rt.base.RoundTrip(req)
}

// selfReviewResources are the only mutating-verb endpoints read-only mode
// permits. They are POSTs by API shape but read-only in effect — they answer
// "who am I" and "can I" — and :whoami plus the can-i grid are exactly what a
// read-only operator reaches for first.
var selfReviewResources = []string{
	"selfsubjectaccessreviews",
	"selfsubjectrulesreviews",
	"selfsubjectreviews",
}

// isMutatingRequest reports whether a request would change cluster state.
//
// The rule is deliberately the RBAC one rather than a hand-listed set of
// actions: create, update, patch and delete are refused, everything else is
// allowed. That makes read-only mode behave the way a genuinely read-only
// ServiceAccount would — including refusing exec, attach and port-forward, which
// are create verbs on pod subresources even though they look like reads.
func isMutatingRequest(req *http.Request) bool {
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	// Match a whole trailing path segment, never a substring: a CRD or namespace
	// whose name merely contains "selfsubjectreviews" must not slip through.
	path := strings.TrimSuffix(req.URL.Path, "/")
	last := path[strings.LastIndexByte(path, '/')+1:]
	for _, r := range selfReviewResources {
		if last == r {
			return false
		}
	}
	return true
}
