package columns

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(pvcsProjector{}) }

// shortAccessModes maps PersistentVolume access modes to their short kubectl
// abbreviations (RWO/ROX/RWX/RWOP), joined with ",". It returns "" for none.
func shortAccessModes(modes []corev1.PersistentVolumeAccessMode) string {
	if len(modes) == 0 {
		return ""
	}
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		switch m {
		case corev1.ReadWriteOnce:
			out = append(out, "RWO")
		case corev1.ReadOnlyMany:
			out = append(out, "ROX")
		case corev1.ReadWriteMany:
			out = append(out, "RWX")
		case corev1.ReadWriteOncePod:
			out = append(out, "RWOP")
		default:
			out = append(out, string(m))
		}
	}
	return strings.Join(out, ",")
}

type pvcsProjector struct{}

func (pvcsProjector) Kind() string { return "persistentvolumeclaims" }

func (pvcsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "STATUS", MinWidth: 10, Align: AlignLeft},
		{Title: "VOLUME", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "CAPACITY", MinWidth: 9, Align: AlignRight},
		{Title: "ACCESS-MODES", MinWidth: 13, Align: AlignLeft},
		{Title: "STORAGECLASS", MinWidth: 14, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (pvcsProjector) Project(obj any, now time.Time) (Row, bool) {
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		return Row{}, false
	}
	phase := pvc.Status.Phase
	health := StatusNeutral
	switch phase {
	case corev1.ClaimBound:
		health = StatusOK
	case corev1.ClaimPending:
		health = StatusWarn
	case corev1.ClaimLost:
		health = StatusError
	}

	capTxt := ""
	if q, present := pvc.Status.Capacity[corev1.ResourceStorage]; present {
		capTxt = q.String()
	}

	storageClass := ""
	if pvc.Spec.StorageClassName != nil {
		storageClass = *pvc.Spec.StorageClassName
	}

	ageTxt, ageKey := age(pvc.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: pvc.Name, Role: RoleName},
		{Text: string(phase), Role: RoleStatus, Status: health},
		{Text: dash(pvc.Spec.VolumeName), Status: StatusMuted},
		{Text: dash(capTxt), Status: StatusMuted},
		{Text: shortAccessModes(pvc.Status.AccessModes), Status: StatusMuted},
		{Text: dash(storageClass), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(pvc.Name),
		StrKey(string(phase)),
		StrKey(pvc.Spec.VolumeName),
		StrKey(capTxt),
		StrKey(shortAccessModes(pvc.Status.AccessModes)),
		StrKey(storageClass),
		ageKey,
	}
	return Row{
		UID: string(pvc.UID), Namespace: pvc.Namespace, Name: pvc.Name,
		Version: pvc.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
