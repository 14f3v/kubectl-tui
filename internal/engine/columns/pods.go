package columns

import (
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(podsProjector{}) }

type podsProjector struct{}

func (podsProjector) Kind() string { return "pods" }

func (podsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "READY", MinWidth: 6, Align: AlignLeft},
		{Title: "STATUS", MinWidth: 18, Grow: 1, Align: AlignLeft},
		{Title: "RESTARTS", MinWidth: 8, Align: AlignRight},
		{Title: "IP", MinWidth: 14, Align: AlignLeft},
		{Title: "NODE", MinWidth: 16, Grow: 1, Align: AlignLeft},
		{Title: "CPU", MinWidth: 6, Align: AlignRight},
		{Title: "MEM", MinWidth: 6, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (podsProjector) Project(obj any, now time.Time) (Row, bool) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return Row{}, false
	}

	status := podStatus(pod)
	ready, total := readyCounts(pod)
	restarts := restartCount(pod)

	ip := dash(pod.Status.PodIP)
	node := dash(pod.Spec.NodeName)

	age := "—"
	var ageKey SortKey
	if t := pod.CreationTimestamp.Time; !t.IsZero() {
		age = humanAge(now.Sub(t))
		ageKey = NumKey(float64(t.Unix()))
	}

	cells := []Cell{
		{Text: pod.Name, Role: RoleName},
		{Text: fmt.Sprintf("%d/%d", ready, total), Status: readyClass(ready, total)},
		{Text: status, Status: statusClass(status), Role: RoleStatus},
		{Text: strconv.Itoa(restarts), Status: restartClass(restarts)},
		{Text: ip, Status: StatusMuted},
		{Text: node, Status: StatusMuted},
		{Text: "—", Status: StatusMuted}, // CPU — filled by the metrics join (phase 6)
		{Text: "—", Status: StatusMuted}, // MEM — filled by the metrics join (phase 6)
		{Text: age, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(pod.Name),
		NumKey(float64(ready)),
		StrKey(status),
		NumKey(float64(restarts)),
		StrKey(ip),
		StrKey(node),
		NumKey(0),
		NumKey(0),
		ageKey,
	}

	return Row{
		UID:       string(pod.UID),
		Namespace: pod.Namespace,
		Name:      pod.Name,
		Version:   pod.ResourceVersion,
		Health:    statusClass(status),
		Cells:     cells,
		SortKeys:  sortKeys,
	}, true
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// readyCounts returns the number of ready regular containers and the total.
func readyCounts(pod *corev1.Pod) (ready, total int) {
	total = len(pod.Spec.Containers)
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return ready, total
}

// restartCount sums restarts across init and regular containers, matching what
// kubectl reports in the RESTARTS column.
func restartCount(pod *corev1.Pod) int {
	n := 0
	for _, cs := range pod.Status.InitContainerStatuses {
		n += int(cs.RestartCount)
	}
	for _, cs := range pod.Status.ContainerStatuses {
		n += int(cs.RestartCount)
	}
	return n
}

// podStatus reproduces kubectl's STATUS column derivation: it accounts for the
// pod's deletion state, init-container progress, and per-container waiting or
// terminated reasons, falling back to the pod phase.
func podStatus(pod *corev1.Pod) string {
	reason := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}

	// Init containers gate the pod; surface their state first.
	initializing := false
	for i, cs := range pod.Status.InitContainerStatuses {
		switch {
		case cs.State.Terminated != nil && cs.State.Terminated.ExitCode == 0:
			continue
		case cs.State.Terminated != nil:
			if cs.State.Terminated.Reason != "" {
				reason = "Init:" + cs.State.Terminated.Reason
			} else if cs.State.Terminated.Signal != 0 {
				reason = fmt.Sprintf("Init:Signal:%d", cs.State.Terminated.Signal)
			} else {
				reason = fmt.Sprintf("Init:ExitCode:%d", cs.State.Terminated.ExitCode)
			}
			initializing = true
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + cs.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
			initializing = true
		}
		break
	}

	if !initializing {
		hasRunning := false
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			cs := pod.Status.ContainerStatuses[i]
			switch {
			case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
				reason = cs.State.Waiting.Reason
			case cs.State.Terminated != nil && cs.State.Terminated.Reason != "":
				reason = cs.State.Terminated.Reason
			case cs.State.Terminated != nil:
				if cs.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Signal:%d", cs.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("ExitCode:%d", cs.State.Terminated.ExitCode)
				}
			case cs.Ready && cs.State.Running != nil:
				hasRunning = true
			}
		}
		if reason == "Completed" && hasRunning {
			reason = "Running"
		}
	}

	if pod.DeletionTimestamp != nil {
		if pod.Status.Reason == "NodeLost" {
			return "Unknown"
		}
		return "Terminating"
	}
	return reason
}

// statusClass maps a pod STATUS string to a semantic color bucket, matching the
// design's status palette.
func statusClass(status string) StatusClass {
	switch status {
	case "Running":
		return StatusOK
	case "Completed", "Succeeded":
		return StatusInfo
	case "Pending", "ContainerCreating", "PodInitializing", "Init":
		return StatusWarn
	case "Terminating", "Unknown":
		return StatusMuted
	case "CrashLoopBackOff", "Error", "OOMKilled", "Evicted", "Failed", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError":
		return StatusError
	}
	// Prefix-based buckets for the derived Init:* / *BackOff strings.
	switch {
	case len(status) >= 5 && status[:5] == "Init:":
		return StatusWarn
	case hasSuffix(status, "BackOff"), hasSuffix(status, "Error"):
		return StatusError
	}
	return StatusNeutral
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

func readyClass(ready, total int) StatusClass {
	switch {
	case total == 0:
		return StatusMuted
	case ready == total:
		return StatusOK
	case ready == 0:
		return StatusError
	default:
		return StatusWarn
	}
}

func restartClass(n int) StatusClass {
	switch {
	case n == 0:
		return StatusMuted
	case n > 4:
		return StatusError
	default:
		return StatusWarn
	}
}
