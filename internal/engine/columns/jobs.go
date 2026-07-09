package columns

import (
	"fmt"
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func init() { Register(jobsProjector{}) }

type jobsProjector struct{}

func (jobsProjector) Kind() string { return "jobs" }

func (jobsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "COMPLETIONS", MinWidth: 12, Align: AlignLeft},
		{Title: "DURATION", MinWidth: 9, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (jobsProjector) Project(obj any, now time.Time) (Row, bool) {
	j, ok := obj.(*batchv1.Job)
	if !ok {
		return Row{}, false
	}

	completionsWant := "1"
	if j.Spec.Completions != nil {
		completionsWant = strconv.Itoa(int(*j.Spec.Completions))
	}
	completions := fmt.Sprintf("%d/%s", j.Status.Succeeded, completionsWant)

	health := StatusMuted
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			health = StatusOK
			break
		}
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			health = StatusError
			break
		}
	}
	if health == StatusMuted && j.Status.Active > 0 {
		health = StatusWarn
	}

	duration := dash("")
	if j.Status.StartTime != nil {
		end := now
		if j.Status.CompletionTime != nil {
			end = j.Status.CompletionTime.Time
		}
		duration = humanAge(end.Sub(j.Status.StartTime.Time))
	}

	ageTxt, ageKey := age(j.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: j.Name, Role: RoleName},
		{Text: completions, Status: health},
		{Text: duration, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(j.Name),
		NumKey(float64(j.Status.Succeeded)),
		StrKey(duration),
		ageKey,
	}
	return Row{
		UID: string(j.UID), Namespace: j.Namespace, Name: j.Name,
		Version: j.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
