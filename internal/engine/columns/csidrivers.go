package columns

import (
	"strconv"
	"strings"
	"time"

	storagev1 "k8s.io/api/storage/v1"
)

func init() { Register(csiDriversProjector{}) }

// csiDriversProjector projects CSIDriver objects into table rows. It is
// cluster-scoped.
type csiDriversProjector struct{}

// Kind returns the resource kind key for CSIDriver objects.
func (csiDriversProjector) Kind() string { return "csidrivers" }

// Columns describes the column layout for CSIDriver rows.
func (csiDriversProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "ATTACHREQUIRED", MinWidth: 14, Align: AlignLeft},
		{Title: "PODINFOONMOUNT", MinWidth: 14, Align: AlignLeft},
		{Title: "MODES", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *CSIDriver into a Row, returning ok=false for a wrong-typed
// object. The boolean columns show a dash when the underlying pointer is nil.
func (csiDriversProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*storagev1.CSIDriver)
	if !ok {
		return Row{}, false
	}

	attachRequired := ""
	if o.Spec.AttachRequired != nil {
		attachRequired = strconv.FormatBool(*o.Spec.AttachRequired)
	}

	podInfoOnMount := ""
	if o.Spec.PodInfoOnMount != nil {
		podInfoOnMount = strconv.FormatBool(*o.Spec.PodInfoOnMount)
	}

	modeStrs := make([]string, 0, len(o.Spec.VolumeLifecycleModes))
	for _, m := range o.Spec.VolumeLifecycleModes {
		modeStrs = append(modeStrs, string(m))
	}
	modes := strings.Join(modeStrs, ",")

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: dash(attachRequired), Status: StatusMuted},
		{Text: dash(podInfoOnMount), Status: StatusMuted},
		{Text: dash(modes), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		StrKey(attachRequired),
		StrKey(podInfoOnMount),
		StrKey(modes),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
