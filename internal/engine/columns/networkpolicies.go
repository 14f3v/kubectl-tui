package columns

import (
	"sort"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
)

func init() { Register(networkPoliciesProjector{}) }

type networkPoliciesProjector struct{}

func (networkPoliciesProjector) Kind() string { return "networkpolicies" }

func (networkPoliciesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 22, Grow: 2, Align: AlignLeft},
		{Title: "POD-SELECTOR", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (networkPoliciesProjector) Project(obj any, now time.Time) (Row, bool) {
	np, ok := obj.(*networkingv1.NetworkPolicy)
	if !ok {
		return Row{}, false
	}

	selector := netpolSelector(np.Spec.PodSelector.MatchLabels)

	ageTxt, ageKey := age(np.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: np.Name, Role: RoleName},
		{Text: selector, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(np.Name),
		StrKey(selector),
		ageKey,
	}
	return Row{
		UID: string(np.UID), Namespace: np.Namespace, Name: np.Name,
		Version: np.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}

// netpolSelector formats a MatchLabels map as sorted "k=v" pairs joined by ",".
// An empty map selects all pods, rendered as "<none>".
func netpolSelector(labels map[string]string) string {
	if len(labels) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, ",")
}
