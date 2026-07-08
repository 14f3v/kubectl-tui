// Package k8s owns everything that touches a cluster: kubeconfig loading,
// context enumeration and switching, the typed/dynamic/discovery/metrics clients,
// and the engine factory registration that wires each resource kind to a
// list/watch. A Session is per-context and disposable: cancelling its context
// tears down every informer, log stream, and port-forward at once.
package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	// Blank import registers the legacy auth providers (gcp/azure/oidc). Exec
	// credential plugins (Teleport tsh, aws eks get-token, gke-gcloud-auth-plugin)
	// work through rest.Config's exec provider without any import.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine/columns"
)

// Identity is the human-facing description of the active context, shown in the
// header. K8sVersion is filled best-effort and may be empty.
type Identity struct {
	Context    string
	Cluster    string
	User       string
	Server     string
	Namespace  string
	K8sVersion string
}

// Session bundles the clients and engine for one kubeconfig context.
type Session struct {
	RestCfg  *rest.Config
	CS       kubernetes.Interface
	Dyn      dynamic.Interface
	Disco    discovery.DiscoveryInterface
	Metrics  metricsclient.Interface // nil if the metrics client cannot be built
	Engine   *engine.Engine
	Identity Identity

	ctx    context.Context
	cancel context.CancelFunc
}

// NewSession loads the kubeconfig (honoring KUBECONFIG merging and the current
// context, or contextName when non-empty), builds the clients, and registers the
// engine factories. It does not start any informer; pages start kinds lazily.
func NewSession(parent context.Context, contextName string, sink engine.Sink) (*Session, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	restCfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	// Bound client-side timeouts keep the UI responsive; watches set their own.
	restCfg.QPS = 50
	restCfg.Burst = 100

	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	var metrics metricsclient.Interface
	if mc, err := metricsclient.NewForConfig(restCfg); err == nil {
		metrics = mc
	}

	ctx, cancel := context.WithCancel(parent)
	s := &Session{
		RestCfg: restCfg,
		CS:      cs,
		Dyn:     dyn,
		Disco:   cs.Discovery(),
		Metrics: metrics,
		Engine:  engine.NewEngine(ctx, sink),
		ctx:     ctx,
		cancel:  cancel,
	}
	s.Identity = deriveIdentity(cc, restCfg)
	s.registerFactories()
	return s, nil
}

// Context returns the Session's context; it is cancelled on Dispose.
func (s *Session) Context() context.Context { return s.ctx }

// Dispose cancels the context and stops every engine store. After Dispose the
// Session must not be reused.
func (s *Session) Dispose() {
	s.Engine.StopAll()
	s.cancel()
}

// RefreshServerVersion queries discovery for the cluster's Kubernetes version and
// updates Identity. Best-effort; safe to ignore the error.
func (s *Session) RefreshServerVersion() error {
	v, err := s.Disco.ServerVersion()
	if err != nil {
		return err
	}
	s.Identity.K8sVersion = v.GitVersion
	return nil
}

// registerFactories wires each supported kind to a list/watch-backed ViewStore.
// Core kinds are warm (kept running after their view is left); events and tenants
// are screen-scoped and registered by their own packages later.
func (s *Session) registerFactories() {
	core := s.CS.CoreV1().RESTClient()

	s.Engine.Register("pods", true, func(sink engine.Sink, ns string) *engine.ViewStore {
		lw := cache.NewListWatchFromClient(core, "pods", nsOrAll(ns), fields.Everything())
		return engine.NewViewStore("pods", lw, &corev1.Pod{}, columns.For("pods"), sink)
	})
}

// nsOrAll maps an empty namespace to the all-namespaces sentinel.
func nsOrAll(ns string) string {
	if ns == "" {
		return "" // metav1.NamespaceAll
	}
	return ns
}

// deriveIdentity extracts the header identity from the raw kubeconfig and the
// resolved rest.Config.
func deriveIdentity(cc clientcmd.ClientConfig, restCfg *rest.Config) Identity {
	id := Identity{Server: restCfg.Host}
	raw, err := cc.RawConfig()
	if err != nil {
		return id
	}
	id.Context = raw.CurrentContext
	if ctx, ok := raw.Contexts[raw.CurrentContext]; ok && ctx != nil {
		id.Cluster = ctx.Cluster
		id.User = ctx.AuthInfo
		id.Namespace = ctx.Namespace
		if cl, ok := raw.Clusters[ctx.Cluster]; ok && cl != nil && cl.Server != "" {
			id.Server = cl.Server
		}
	}
	if id.Namespace == "" {
		id.Namespace = "" // empty means all-namespaces in our UI
	}
	return id
}
