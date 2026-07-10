package columns

import (
	"time"

	storagev1 "k8s.io/api/storage/v1"
)

func init() { Register(csiStorageCapacitiesProjector{}) }

// csiStorageCapacitiesProjector projects CSIStorageCapacity objects into table
// rows. It is namespaced.
type csiStorageCapacitiesProjector struct{}

// Kind returns the resource kind key for CSIStorageCapacity objects.
func (csiStorageCapacitiesProjector) Kind() string { return "csistoragecapacities" }

// Columns describes the column layout for CSIStorageCapacity rows.
func (csiStorageCapacitiesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "STORAGECLASS", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "CAPACITY", MinWidth: 12, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *CSIStorageCapacity into a Row, returning ok=false for a
// wrong-typed object. The capacity column shows a dash when no capacity is
// reported.
func (csiStorageCapacitiesProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*storagev1.CSIStorageCapacity)
	if !ok {
		return Row{}, false
	}

	storageClass := o.StorageClassName

	capacity := ""
	if o.Capacity != nil {
		capacity = o.Capacity.String()
	}

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: dash(storageClass), Status: StatusMuted},
		{Text: dash(capacity), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		StrKey(storageClass),
		StrKey(capacity),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: o.Namespace, Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
