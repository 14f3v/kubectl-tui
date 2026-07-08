package engine

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrClass is the sealed taxonomy of failures the engine distinguishes. Terminal
// classes stop retrying; transient classes mark the view stale and keep retrying.
type ErrClass int

const (
	// ClassNone is no error.
	ClassNone ErrClass = iota
	// ClassAuth is 401 Unauthorized — credentials missing or expired.
	ClassAuth
	// ClassForbidden is 403 — RBAC denies this verb on this resource.
	ClassForbidden
	// ClassTLS is a certificate/handshake failure.
	ClassTLS
	// ClassNotFound is 404 — the resource kind or object is absent.
	ClassNotFound
	// ClassConflict is 409 — optimistic-concurrency conflict on a write.
	ClassConflict
	// ClassTransient is a network error or 5xx; retrying may recover.
	ClassTransient
)

// EngineErr is a classified error with the operation that produced it and a
// human-readable detail.
type EngineErr struct {
	Class  ErrClass
	Op     string
	Detail string
}

// Error implements error.
func (e *EngineErr) Error() string {
	if e == nil {
		return ""
	}
	if e.Op != "" {
		return e.Op + ": " + e.Detail
	}
	return e.Detail
}

// Terminal reports whether this error should stop retrying and surface to the
// user rather than degrade to a stale, self-recovering state.
func (e *EngineErr) Terminal() bool {
	if e == nil {
		return false
	}
	switch e.Class {
	case ClassAuth, ClassForbidden, ClassTLS:
		return true
	default:
		return false
	}
}

// Classify maps a Kubernetes/transport error onto the taxonomy. TLS and network
// errors are unwrapped from the error chain since client-go wraps them.
func Classify(op string, err error) *EngineErr {
	if err == nil {
		return nil
	}
	detail := err.Error()
	switch {
	case apierrors.IsUnauthorized(err):
		return &EngineErr{Class: ClassAuth, Op: op, Detail: detail}
	case apierrors.IsForbidden(err):
		return &EngineErr{Class: ClassForbidden, Op: op, Detail: detail}
	case apierrors.IsNotFound(err):
		return &EngineErr{Class: ClassNotFound, Op: op, Detail: detail}
	case apierrors.IsConflict(err):
		return &EngineErr{Class: ClassConflict, Op: op, Detail: detail}
	case isTLSError(err):
		return &EngineErr{Class: ClassTLS, Op: op, Detail: detail}
	case apierrors.IsServerTimeout(err), apierrors.IsTimeout(err), apierrors.IsTooManyRequests(err), apierrors.IsInternalError(err), apierrors.IsServiceUnavailable(err):
		return &EngineErr{Class: ClassTransient, Op: op, Detail: detail}
	default:
		// Bare network errors (dial/reset) are transient.
		var netErr net.Error
		if errors.As(err, &netErr) {
			return &EngineErr{Class: ClassTransient, Op: op, Detail: detail}
		}
		return &EngineErr{Class: ClassTransient, Op: op, Detail: detail}
	}
}

// isTLSError reports whether the error chain contains a certificate or TLS
// handshake failure, which is terminal — retrying a bad cert never succeeds.
func isTLSError(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var certInvalid x509.CertificateInvalidError
	var hostErr x509.HostnameError
	var recordErr tls.RecordHeaderError
	var certVerify *tls.CertificateVerificationError
	return errors.As(err, &unknownAuthority) ||
		errors.As(err, &certInvalid) ||
		errors.As(err, &hostErr) ||
		errors.As(err, &recordErr) ||
		errors.As(err, &certVerify)
}
