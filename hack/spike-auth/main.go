// Command spike-auth de-risks kubeconfig loading and exec credential plugins
// (Teleport tsh, aws eks get-token, gke-gcloud-auth-plugin). It loads the
// current (or -context) context, prints the resolved identity, reports whether
// an exec credential plugin is in play, and runs the validation ladder:
// /version, then a nodes LIST as an RBAC probe. Run it against a real Teleport
// context — including after letting the tsh cert expire — to confirm the exec
// plugin re-mints transparently and that expiry surfaces as a clean error.
//
//	go run ./hack/spike-auth -context <tsh-kube-context>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {
	ctxName := flag.String("context", "", "kubeconfig context (default: current-context)")
	flag.Parse()

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if *ctxName != "" {
		overrides.CurrentContext = *ctxName
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	raw, err := cc.RawConfig()
	if err != nil {
		die("read kubeconfig", err)
	}
	current := raw.CurrentContext
	if *ctxName != "" {
		current = *ctxName
	}
	fmt.Printf("context:   %s\n", current)
	if kc, ok := raw.Contexts[current]; ok {
		fmt.Printf("cluster:   %s\n", kc.Cluster)
		fmt.Printf("user:      %s\n", kc.AuthInfo)
		if ai, ok := raw.AuthInfos[kc.AuthInfo]; ok {
			switch {
			case ai.Exec != nil:
				fmt.Printf("auth:      exec plugin %q (args=%v)\n", ai.Exec.Command, ai.Exec.Args)
			case ai.Token != "" || ai.TokenFile != "":
				fmt.Println("auth:      bearer token")
			case ai.ClientCertificate != "" || len(ai.ClientCertificateData) > 0:
				fmt.Println("auth:      client certificate")
			default:
				fmt.Println("auth:      (other / provider)")
			}
		}
	}

	restCfg, err := cc.ClientConfig()
	if err != nil {
		die("build rest config", err)
	}
	fmt.Printf("server:    %s\n", restCfg.Host)

	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		die("build clientset", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Rung 1: /version (exercises the exec plugin end to end).
	ver, err := cs.Discovery().ServerVersion()
	if err != nil {
		die("GET /version (exec plugin / connectivity)", err)
	}
	fmt.Printf("k8s:       %s\n", ver.GitVersion)

	// Rung 2: nodes LIST as an RBAC probe.
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 5})
	if err != nil {
		fmt.Printf("nodes:     RBAC probe failed (this may be expected): %v\n", err)
	} else {
		fmt.Printf("nodes:     listed %d (probe ok)\n", len(nodes.Items))
	}

	fmt.Println("\n✓ auth spike passed")
}

func die(op string, err error) {
	fmt.Fprintf(os.Stderr, "✗ %s: %v\n", op, err)
	os.Exit(1)
}
