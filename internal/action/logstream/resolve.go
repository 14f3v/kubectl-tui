package logstream

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodRef identifies one container to tail as part of a merged workload stream.
// Tag is the human-facing prefix prepended to each of that container's lines so a
// merged stream stays attributable to its origin.
type PodRef struct {
	Pod       string
	Container string
	Tag       string
}

// PodRefsForSelector expands a workload's label selector into the flat list of
// containers to tail. It lists pods once (no informer, so no watch-list gate) and,
// per pod, emits one PodRef per container. Single-container pods tag by pod name so
// the common case reads cleanly; multi-container pods disambiguate with pod/container.
//
// A nil selector is treated as an error rather than "match everything": a workload
// with no pod selector has no well-defined set of pods to follow, and silently
// tailing the whole namespace would be surprising and expensive.
func PodRefsForSelector(ctx context.Context, cs kubernetes.Interface, namespace string, sel *metav1.LabelSelector) ([]PodRef, error) {
	if sel == nil {
		return nil, errors.New("workload has no pod selector")
	}
	list, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(sel),
	})
	if err != nil {
		return nil, err
	}

	var refs []PodRef
	for i := range list.Items {
		pod := &list.Items[i]
		single := len(pod.Spec.Containers) == 1
		for _, c := range pod.Spec.Containers {
			tag := pod.Name
			if !single {
				tag = pod.Name + "/" + c.Name
			}
			refs = append(refs, PodRef{Pod: pod.Name, Container: c.Name, Tag: tag})
		}
	}
	return refs, nil
}

// taggedLine prefixes a raw log line with its source tag so lines from many pods
// remain attributable once merged into one stream.
func taggedLine(tag, line string) string { return "[" + tag + "] " + line }
