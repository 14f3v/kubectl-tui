package columns

import (
	"time"

	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
)

func init() { Register(certificateSigningRequestsProjector{}) }

type certificateSigningRequestsProjector struct{}

func (certificateSigningRequestsProjector) Kind() string {
	return "certificatesigningrequests"
}

func (certificateSigningRequestsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "SIGNERNAME", MinWidth: 24, Grow: 1, Align: AlignLeft},
		{Title: "REQUESTOR", MinWidth: 18, Grow: 1, Align: AlignLeft},
		{Title: "REQUESTEDDURATION", MinWidth: 17, Align: AlignRight},
		{Title: "CONDITION", MinWidth: 12, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (certificateSigningRequestsProjector) Project(obj any, now time.Time) (Row, bool) {
	csr, ok := obj.(*certv1.CertificateSigningRequest)
	if !ok {
		return Row{}, false
	}

	cond := csrCondition(csr)

	// The CSR's health drives both the row's overall status and the CONDITION
	// cell color: an approved request is done and healthy, denied or failed is a
	// hard stop, and a still-pending request is the actionable (warn) state.
	health := StatusWarn
	switch cond {
	case "Approved":
		health = StatusOK
	case "Denied", "Failed":
		health = StatusError
	}

	// A requested duration is optional; when unset we show a dash rather than a
	// misleading zero so operators can tell "no duration requested" apart from
	// "zero seconds".
	duration := dash("")
	if csr.Spec.ExpirationSeconds != nil {
		duration = humanAge(time.Duration(*csr.Spec.ExpirationSeconds) * time.Second)
	}

	ageTxt, ageKey := age(csr.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: csr.Name, Role: RoleName},
		{Text: dash(csr.Spec.SignerName), Status: StatusMuted},
		{Text: dash(csr.Spec.Username), Status: StatusMuted},
		{Text: duration, Status: StatusMuted},
		{Text: cond, Status: health, Role: RoleStatus},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(csr.Name),
		StrKey(csr.Spec.SignerName),
		StrKey(csr.Spec.Username),
		NumKey(expirationSeconds(csr)),
		StrKey(cond),
		ageKey,
	}
	return Row{
		UID:      string(csr.UID),
		Name:     csr.Name,
		Version:  csr.ResourceVersion,
		Health:   health,
		Cells:    cells,
		SortKeys: sortKeys,
	}, true
}

// expirationSeconds is the numeric sort value for the REQUESTED DURATION column;
// an unset duration sorts as zero so it groups with the smallest durations.
func expirationSeconds(csr *certv1.CertificateSigningRequest) float64 {
	if csr.Spec.ExpirationSeconds == nil {
		return 0
	}
	return float64(*csr.Spec.ExpirationSeconds)
}

// csrCondition collapses a CSR's condition list into the single word kubectl
// shows in its CONDITION column. Approval/denial/failure are mutually exclusive
// terminal states enforced by the API server, so the first matching true
// condition wins; a CSR with none is still Pending.
func csrCondition(csr *certv1.CertificateSigningRequest) string {
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
