package columns

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
)

func init() { Register(hpaProjector{}) }

type hpaProjector struct{}

func (hpaProjector) Kind() string { return "horizontalpodautoscalers" }

func (hpaProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "REFERENCE", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "TARGETS", MinWidth: 16, Grow: 1, Align: AlignLeft},
		{Title: "MINPODS", MinWidth: 8, Align: AlignRight},
		{Title: "MAXPODS", MinWidth: 8, Align: AlignRight},
		{Title: "REPLICAS", MinWidth: 9, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (hpaProjector) Project(obj any, now time.Time) (Row, bool) {
	h, ok := obj.(*autoscalingv2.HorizontalPodAutoscaler)
	if !ok {
		return Row{}, false
	}

	reference := h.Spec.ScaleTargetRef.Kind + "/" + h.Spec.ScaleTargetRef.Name
	targets := hpaTargets(h)

	minPods := int32(1)
	if h.Spec.MinReplicas != nil {
		minPods = *h.Spec.MinReplicas
	}
	maxPods := h.Spec.MaxReplicas
	replicas := h.Status.CurrentReplicas

	health := StatusOK
	if replicas > 0 && replicas == maxPods {
		health = StatusWarn
	}

	ageTxt, ageKey := age(h.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: h.Name, Role: RoleName},
		{Text: reference, Status: StatusMuted},
		{Text: targets, Status: StatusMuted},
		{Text: strconv.Itoa(int(minPods)), Status: StatusMuted},
		{Text: strconv.Itoa(int(maxPods)), Status: StatusMuted},
		{Text: strconv.Itoa(int(replicas)), Status: health},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(h.Name),
		StrKey(reference),
		StrKey(targets),
		NumKey(float64(minPods)),
		NumKey(float64(maxPods)),
		NumKey(float64(replicas)),
		ageKey,
	}
	return Row{
		UID: string(h.UID), Namespace: h.Namespace, Name: h.Name,
		Version: h.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}

func hpaTargets(h *autoscalingv2.HorizontalPodAutoscaler) string {
	var parts []string
	for _, m := range h.Spec.Metrics {
		if m.Type != autoscalingv2.ResourceMetricSourceType || m.Resource == nil {
			continue
		}
		name := string(m.Resource.Name)
		if m.Resource.Target.AverageUtilization != nil {
			parts = append(parts, fmt.Sprintf("%s:%d%%", name, *m.Resource.Target.AverageUtilization))
		} else if m.Resource.Target.AverageValue != nil {
			parts = append(parts, fmt.Sprintf("%s:%s", name, m.Resource.Target.AverageValue.String()))
		}
	}
	if len(parts) == 0 {
		return "<none>"
	}
	return strings.Join(parts, ",")
}
