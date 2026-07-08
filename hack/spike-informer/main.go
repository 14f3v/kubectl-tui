// Command spike-informer de-risks the watch engine's error handling. It starts a
// pods informer with a SetWatchErrorHandler and prints each add/update/delete and
// every watch error with its classification. Point it at a namespace you cannot
// access (or a resource you lack RBAC for) to confirm a 403 is classified
// terminal and stops the informer, rather than retrying forever.
//
//	go run ./hack/spike-informer -context <ctx> -n <namespace>
//	go run ./hack/spike-informer -context <ctx> -n <forbidden-namespace>
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {
	ctxName := flag.String("context", "", "kubeconfig context (default: current-context)")
	ns := flag.String("n", "", "namespace to watch (default: all)")
	flag.Parse()

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if *ctxName != "" {
		overrides.CurrentContext = *ctxName
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kubeconfig:", err)
		os.Exit(1)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clientset:", err)
		os.Exit(1)
	}

	lw := cache.NewListWatchFromClient(cs.CoreV1().RESTClient(), "pods", *ns, fields.Everything())
	inf := cache.NewSharedIndexInformer(lw, &corev1.Pod{}, 0, cache.Indexers{})

	stop := make(chan struct{})
	_ = inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		class := "transient"
		terminal := false
		switch {
		case apierrors.IsForbidden(err):
			class, terminal = "forbidden (terminal)", true
		case apierrors.IsUnauthorized(err):
			class, terminal = "unauthorized (terminal)", true
		}
		fmt.Printf("[watch-error] %s: %v\n", class, err)
		if terminal {
			fmt.Println("→ terminal error: stopping informer (would surface in the UI, no retry)")
			select {
			case <-stop:
			default:
				close(stop)
			}
		}
	})
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(o any) {
			if p, ok := o.(*corev1.Pod); ok {
				fmt.Printf("[add]    %s/%s\n", p.Namespace, p.Name)
			}
		},
		UpdateFunc: func(_, o any) {
			if p, ok := o.(*corev1.Pod); ok {
				fmt.Printf("[update] %s/%s\n", p.Namespace, p.Name)
			}
		},
		DeleteFunc: func(o any) {
			if p, ok := o.(*corev1.Pod); ok {
				fmt.Printf("[delete] %s/%s\n", p.Namespace, p.Name)
			}
		},
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; close(stop) }()

	nsLabel := *ns
	if nsLabel == "" {
		nsLabel = "all namespaces"
	}
	fmt.Printf("watching pods in %s (ctrl-c to stop)…\n", nsLabel)
	go inf.Run(stop)

	if cache.WaitForCacheSync(stop, inf.HasSynced) {
		fmt.Printf("synced: %d pods in cache\n", len(inf.GetStore().List()))
	} else {
		fmt.Println("cache did not sync (informer stopped — see error above)")
	}

	<-stop
}
