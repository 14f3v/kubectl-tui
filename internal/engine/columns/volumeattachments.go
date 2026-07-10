package columns

import (
	"strconv"
	"time"

	storagev1 "k8s.io/api/storage/v1"
)

func init() { Register(volumeAttachmentsProjector{}) }

// volumeAttachmentsProjector projects VolumeAttachment objects into table rows.
// It is cluster-scoped.
type volumeAttachmentsProjector struct{}

// Kind returns the resource kind key for VolumeAttachment objects.
func (volumeAttachmentsProjector) Kind() string { return "volumeattachments" }

// Columns describes the column layout for VolumeAttachment rows.
func (volumeAttachmentsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "ATTACHER", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "PV", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "NODE", MinWidth: 16, Grow: 1, Align: AlignLeft},
		{Title: "ATTACHED", MinWidth: 8, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *VolumeAttachment into a Row, returning ok=false for a
// wrong-typed object. The PV column shows a dash when the source has no
// persistent volume name.
func (volumeAttachmentsProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*storagev1.VolumeAttachment)
	if !ok {
		return Row{}, false
	}

	attacher := o.Spec.Attacher

	pv := ""
	if o.Spec.Source.PersistentVolumeName != nil {
		pv = *o.Spec.Source.PersistentVolumeName
	}

	node := o.Spec.NodeName
	attached := strconv.FormatBool(o.Status.Attached)

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: dash(attacher), Status: StatusMuted},
		{Text: dash(pv), Status: StatusMuted},
		{Text: dash(node), Status: StatusMuted},
		{Text: attached, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		StrKey(attacher),
		StrKey(pv),
		StrKey(node),
		StrKey(attached),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
