package columns

import (
	"time"

	storagev1 "k8s.io/api/storage/v1"
)

func init() { Register(storageClassesProjector{}) }

type storageClassesProjector struct{}

func (storageClassesProjector) Kind() string { return "storageclasses" }

func (storageClassesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "PROVISIONER", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "RECLAIMPOLICY", MinWidth: 14, Align: AlignLeft},
		{Title: "VOLUMEBINDINGMODE", MinWidth: 18, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (storageClassesProjector) Project(obj any, now time.Time) (Row, bool) {
	sc, ok := obj.(*storagev1.StorageClass)
	if !ok {
		return Row{}, false
	}
	name := sc.Name
	if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
		name += " (default)"
	}

	reclaim := ""
	if sc.ReclaimPolicy != nil {
		reclaim = string(*sc.ReclaimPolicy)
	}

	bindingMode := ""
	if sc.VolumeBindingMode != nil {
		bindingMode = string(*sc.VolumeBindingMode)
	}

	ageTxt, ageKey := age(sc.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: name, Role: RoleName},
		{Text: sc.Provisioner, Status: StatusMuted},
		{Text: dash(reclaim), Status: StatusMuted},
		{Text: dash(bindingMode), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(name),
		StrKey(sc.Provisioner),
		StrKey(reclaim),
		StrKey(bindingMode),
		ageKey,
	}
	return Row{
		UID: string(sc.UID), Namespace: sc.Namespace, Name: sc.Name,
		Version: sc.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
