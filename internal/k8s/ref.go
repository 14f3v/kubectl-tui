package k8s

import (
	"sort"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/14f3v/kubectl-tui/internal/engine"
)

// Source says where a ClusterRef's kubeconfig comes from.
type Source string

const (
	SourcePasted     Source = "pasted"     // a file kubetui saved from a paste
	SourceFile       Source = "file"       // a kubeconfig file the user named, read in place
	SourceKubeconfig Source = "kubeconfig" // client-go's default rules: $KUBECONFIG, else ~/.kube/config
	SourceInCluster  Source = "in-cluster" // the pod's service account
)

// ClusterRef names one context in one kubeconfig source. Comparable with ==.
//
// The context is always named rather than left to the file's current-context,
// so a `kubectl config use-context` run elsewhere never moves a reconnect to
// another cluster.
type ClusterRef struct {
	Source  Source
	Path    string // pasted/file: absolute file path; kubeconfig/in-cluster: ""
	Context string // always set, except in-cluster
}

// Options are the per-Session settings that are not part of what it connects to.
type Options struct {
	// ReadOnly wraps every client's transport so mutating requests are refused
	// before they leave the process; see readonly.go.
	ReadOnly bool
	// Sink receives engine snapshots and port-forward events.
	Sink engine.Sink
	// ApprovedExec lists the exec credential commands the user approved for a
	// pasted kubeconfig. Pasted refs only; NewSession allows no other command.
	ApprovedExec []string
}

// ContextInfo describes one kubeconfig context for a picker row.
type ContextInfo struct{ Name, Cluster, Server, Namespace string }

// ListContexts reads a kubeconfig without building a client and returns its
// contexts sorted by name, with the file's current-context. An empty path uses
// client-go's default loading rules ($KUBECONFIG, merged, else ~/.kube/config),
// the same rules NewSession uses for a kubeconfig ref.
func ListContexts(path string) (contexts []ContextInfo, current string, err error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path != "" {
		loadingRules.ExplicitPath = path
	}
	raw, err := loadingRules.Load()
	if err != nil {
		return nil, "", err
	}
	contexts = make([]ContextInfo, 0, len(raw.Contexts))
	for name, c := range raw.Contexts {
		info := ContextInfo{Name: name}
		if c != nil {
			info.Cluster = c.Cluster
			info.Namespace = c.Namespace
			if cl, ok := raw.Clusters[c.Cluster]; ok && cl != nil {
				info.Server = cl.Server
			}
		}
		contexts = append(contexts, info)
	}
	sort.Slice(contexts, func(i, j int) bool { return contexts[i].Name < contexts[j].Name })
	return contexts, raw.CurrentContext, nil
}
