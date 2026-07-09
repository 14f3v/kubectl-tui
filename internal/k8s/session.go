// Package k8s owns everything that touches a cluster: kubeconfig loading,
// context enumeration and switching, the typed/dynamic/discovery/metrics clients,
// and the engine factory registration that wires each resource kind to a
// list/watch. A Session is per-context and disposable: cancelling its context
// tears down every informer, log stream, and port-forward at once.
package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
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

	"github.com/14f3v/kubectl-tui/internal/action/portfwd"
	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
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
	Forwards *portfwd.Manager
	Identity Identity

	contexts []string // available kubeconfig context names, sorted

	ctx    context.Context
	cancel context.CancelFunc
}

// Contexts returns the available kubeconfig context names (sorted) and the
// currently active one, for the :ctx picker.
func (s *Session) Contexts() (names []string, current string) {
	return s.contexts, s.Identity.Context
}

// NewSession loads the kubeconfig and builds the clients, then registers the
// engine factories. It does not start any informer; pages start kinds lazily.
//
// Config resolution, in precedence order: an explicit kubeconfigPath (empty to
// skip) wins; otherwise the KUBECONFIG environment variable (colon-separated
// files are merged); otherwise ~/.kube/config. contextName overrides the
// current-context when non-empty.
func NewSession(parent context.Context, kubeconfigPath, contextName string, sink engine.Sink) (*Session, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}
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
	s.Forwards = portfwd.NewManager(restCfg, cs, sink)
	s.Identity = deriveIdentity(cc, restCfg)
	s.contexts = contextNames(cc)
	s.registerFactories()
	return s, nil
}

// contextNames returns the sorted kubeconfig context names, or nil if the raw
// config cannot be read.
func contextNames(cc clientcmd.ClientConfig) []string {
	raw, err := cc.RawConfig()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(raw.Contexts))
	for name := range raw.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Context returns the Session's context; it is cancelled on Dispose.
func (s *Session) Context() context.Context { return s.ctx }

// Dispose cancels the context and stops every engine store and port-forward.
// After Dispose the Session must not be reused.
func (s *Session) Dispose() {
	s.Forwards.StopAll()
	s.Engine.StopAll()
	s.cancel()
}

// RefreshServerVersion queries /version for the cluster's Kubernetes version and
// updates Identity. It is bounded by an 8s timeout so an unreachable cluster
// cannot stall startup indefinitely (client-go's ServerVersion() takes no
// context). Best-effort; safe to ignore the error. Must be called before the
// Session is handed to the UI so the Identity write is not raced by rendering.
func (s *Session) RefreshServerVersion() error {
	ctx, cancel := context.WithTimeout(s.ctx, 8*time.Second)
	defer cancel()
	body, err := s.Disco.RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return err
	}
	var info struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return err
	}
	s.Identity.K8sVersion = info.GitVersion
	return nil
}

// registerFactories wires each supported kind to a list/watch-backed ViewStore.
// Core kinds are warm (kept running after their view is left); events is
// screen-scoped (highest churn). Cluster-scoped kinds ignore the namespace.
func (s *Session) registerFactories() {
	core := s.CS.CoreV1().RESTClient()
	apps := s.CS.AppsV1().RESTClient()
	batch := s.CS.BatchV1().RESTClient()
	net := s.CS.NetworkingV1().RESTClient()
	disc := s.CS.DiscoveryV1().RESTClient()
	rbac := s.CS.RbacV1().RESTClient()
	storage := s.CS.StorageV1().RESTClient()
	autoscaling := s.CS.AutoscalingV2().RESTClient()
	policy := s.CS.PolicyV1().RESTClient()
	certificates := s.CS.CertificatesV1().RESTClient()

	reg := func(kind, resource string, warm bool, getter cache.Getter, example runtime.Object, clusterScoped bool) {
		s.Engine.Register(kind, warm, func(sink engine.Sink, ns string) *engine.ViewStore {
			scope := ns
			if clusterScoped {
				scope = ""
			}
			lw := cache.NewListWatchFromClient(getter, resource, nsOrAll(scope), fields.Everything())
			return engine.NewViewStore(kind, lw, example, columns.For(kind), sink)
		})
	}

	reg("pods", "pods", true, core, &corev1.Pod{}, false)
	reg("deployments", "deployments", true, apps, &appsv1.Deployment{}, false)
	reg("services", "services", true, core, &corev1.Service{}, false)
	reg("nodes", "nodes", true, core, &corev1.Node{}, true)
	reg("namespaces", "namespaces", true, core, &corev1.Namespace{}, true)
	reg("events", "events", false, core, &corev1.Event{}, false)

	// Workloads (#3).
	reg("statefulsets", "statefulsets", true, apps, &appsv1.StatefulSet{}, false)
	reg("daemonsets", "daemonsets", true, apps, &appsv1.DaemonSet{}, false)
	reg("replicasets", "replicasets", false, apps, &appsv1.ReplicaSet{}, false)
	reg("jobs", "jobs", true, batch, &batchv1.Job{}, false)
	reg("cronjobs", "cronjobs", true, batch, &batchv1.CronJob{}, false)

	// Config & storage (#4) and secrets (#2).
	reg("configmaps", "configmaps", true, core, &corev1.ConfigMap{}, false)
	reg("secrets", "secrets", true, core, &corev1.Secret{}, false)
	reg("persistentvolumeclaims", "persistentvolumeclaims", true, core, &corev1.PersistentVolumeClaim{}, false)
	reg("persistentvolumes", "persistentvolumes", true, core, &corev1.PersistentVolume{}, true)
	reg("storageclasses", "storageclasses", true, storage, &storagev1.StorageClass{}, true)

	// Networking (#5).
	reg("ingresses", "ingresses", true, net, &networkingv1.Ingress{}, false)
	reg("networkpolicies", "networkpolicies", true, net, &networkingv1.NetworkPolicy{}, false)
	reg("endpointslices", "endpointslices", true, disc, &discoveryv1.EndpointSlice{}, false)

	// RBAC (#6).
	reg("serviceaccounts", "serviceaccounts", true, core, &corev1.ServiceAccount{}, false)
	reg("roles", "roles", true, rbac, &rbacv1.Role{}, false)
	reg("rolebindings", "rolebindings", true, rbac, &rbacv1.RoleBinding{}, false)
	reg("clusterroles", "clusterroles", true, rbac, &rbacv1.ClusterRole{}, true)
	reg("clusterrolebindings", "clusterrolebindings", true, rbac, &rbacv1.ClusterRoleBinding{}, true)

	// Autoscaling & policy (#7).
	reg("horizontalpodautoscalers", "horizontalpodautoscalers", true, autoscaling, &autoscalingv2.HorizontalPodAutoscaler{}, false)
	reg("poddisruptionbudgets", "poddisruptionbudgets", true, policy, &policyv1.PodDisruptionBudget{}, false)
	reg("resourcequotas", "resourcequotas", true, core, &corev1.ResourceQuota{}, false)
	reg("limitranges", "limitranges", true, core, &corev1.LimitRange{}, false)

	// Certificate signing requests (#25) — cluster-scoped.
	reg("certificatesigningrequests", "certificatesigningrequests", true, certificates, &certv1.CertificateSigningRequest{}, true)
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
