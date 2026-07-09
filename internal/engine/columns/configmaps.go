package columns

import (
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(configMapsProjector{}) }

type configMapsProjector struct{}

func (configMapsProjector) Kind() string { return "configmaps" }

func (configMapsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "DATA", MinWidth: 5, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (configMapsProjector) Project(obj any, now time.Time) (Row, bool) {
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return Row{}, false
	}
	dataCount := len(cm.Data) + len(cm.BinaryData)

	ageTxt, ageKey := age(cm.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: cm.Name, Role: RoleName},
		{Text: strconv.Itoa(dataCount), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(cm.Name),
		NumKey(float64(dataCount)),
		ageKey,
	}
	return Row{
		UID: string(cm.UID), Namespace: cm.Namespace, Name: cm.Name,
		Version: cm.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
