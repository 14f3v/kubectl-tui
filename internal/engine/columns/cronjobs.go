package columns

import (
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
)

func init() { Register(cronJobsProjector{}) }

type cronJobsProjector struct{}

func (cronJobsProjector) Kind() string { return "cronjobs" }

func (cronJobsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "SCHEDULE", MinWidth: 12, Align: AlignLeft},
		{Title: "SUSPEND", MinWidth: 8, Align: AlignLeft},
		{Title: "ACTIVE", MinWidth: 7, Align: AlignRight},
		{Title: "LAST-SCHEDULE", MinWidth: 14, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (cronJobsProjector) Project(obj any, now time.Time) (Row, bool) {
	c, ok := obj.(*batchv1.CronJob)
	if !ok {
		return Row{}, false
	}

	suspended := c.Spec.Suspend != nil && *c.Spec.Suspend
	suspendTxt := "False"
	if suspended {
		suspendTxt = "True"
	}

	health := StatusNeutral
	if suspended {
		health = StatusMuted
	}

	active := len(c.Status.Active)

	lastSchedule := dash("")
	if c.Status.LastScheduleTime != nil {
		lastSchedule = humanAge(now.Sub(c.Status.LastScheduleTime.Time))
	}

	ageTxt, ageKey := age(c.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: c.Name, Role: RoleName},
		{Text: c.Spec.Schedule, Status: StatusNeutral},
		{Text: suspendTxt, Status: StatusMuted},
		{Text: strconv.Itoa(active), Status: StatusMuted},
		{Text: lastSchedule, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(c.Name),
		StrKey(c.Spec.Schedule),
		StrKey(suspendTxt),
		NumKey(float64(active)),
		StrKey(lastSchedule),
		ageKey,
	}
	return Row{
		UID: string(c.UID), Namespace: c.Namespace, Name: c.Name,
		Version: c.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
