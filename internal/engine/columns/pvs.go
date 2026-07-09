package columns

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(pvsProjector{}) }

type pvsProjector struct{}

func (pvsProjector) Kind() string { return "persistentvolumes" }

func (pvsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "CAPACITY", MinWidth: 9, Align: AlignRight},
		{Title: "ACCESS-MODES", MinWidth: 13, Align: AlignLeft},
		{Title: "RECLAIM", MinWidth: 10, Align: AlignLeft},
		{Title: "STATUS", MinWidth: 10, Align: AlignLeft},
		{Title: "CLAIM", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "STORAGECLASS", MinWidth: 14, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (pvsProjector) Project(obj any, now time.Time) (Row, bool) {
	pv, ok := obj.(*corev1.PersistentVolume)
	if !ok {
		return Row{}, false
	}
	phase := pv.Status.Phase
	health := StatusNeutral
	switch phase {
	case corev1.VolumeBound, corev1.VolumeAvailable:
		health = StatusOK
	case corev1.VolumeReleased:
		health = StatusWarn
	case corev1.VolumeFailed:
		health = StatusError
	case corev1.VolumePending:
		health = StatusWarn
	}

	capTxt := ""
	if q, present := pv.Spec.Capacity[corev1.ResourceStorage]; present {
		capTxt = q.String()
	}

	claim := ""
	if pv.Spec.ClaimRef != nil {
		claim = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
	}

	ageTxt, ageKey := age(pv.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: pv.Name, Role: RoleName},
		{Text: dash(capTxt), Status: StatusMuted},
		{Text: shortAccessModes(pv.Spec.AccessModes), Status: StatusMuted},
		{Text: string(pv.Spec.PersistentVolumeReclaimPolicy), Status: StatusMuted},
		{Text: string(phase), Role: RoleStatus, Status: health},
		{Text: dash(claim), Status: StatusMuted},
		{Text: dash(pv.Spec.StorageClassName), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(pv.Name),
		StrKey(capTxt),
		StrKey(shortAccessModes(pv.Spec.AccessModes)),
		StrKey(string(pv.Spec.PersistentVolumeReclaimPolicy)),
		StrKey(string(phase)),
		StrKey(claim),
		StrKey(pv.Spec.StorageClassName),
		ageKey,
	}
	return Row{
		UID: string(pv.UID), Namespace: pv.Namespace, Name: pv.Name,
		Version: pv.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
