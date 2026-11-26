package k8s

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

var twoContextInfos = []ContextInfo{
	{Name: "a", Cluster: "ca", Server: "https://a.example", Namespace: "nsa"},
	{Name: "b", Cluster: "cb", Server: "https://b.example", Namespace: "nsb"},
}

func TestListContexts(t *testing.T) {
	path := writeKubeconfig(t, twoContexts)
	got, cur, err := ListContexts(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, twoContextInfos) {
		t.Errorf("contexts = %+v, want %+v", got, twoContextInfos)
	}
	if cur != "a" {
		t.Errorf("current = %q, want a", cur)
	}
}

func TestListContextsDefaultRulesUsesKUBECONFIG(t *testing.T) {
	t.Setenv("KUBECONFIG", writeKubeconfig(t, twoContexts))
	got, _, err := ListContexts("")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, twoContextInfos) {
		t.Errorf("contexts = %+v, want %+v", got, twoContextInfos)
	}
}

// client-go's default loading rules carry a migration rule that copies
// ~/.kube/.kubeconfig to ~/.kube/config, when the latter is missing, on every
// Load, an explicit path or not. kubetui never writes the user's kubeconfig, so
// it loads without the rule. The rule's paths are fixed when client-go starts,
// so the loads run in a child process with its own HOME.
func TestLoadingNeverWritesKubeconfig(t *testing.T) {
	if os.Getenv("KUBETUI_MIGRATE_CHILD") != "" {
		kc := os.Getenv("KUBECONFIG")
		_, _, _ = ListContexts("")
		_, _, _ = ListContexts(kc)
		for _, ref := range []ClusterRef{{Source: SourceFile, Path: kc, Context: "a"}, {Source: SourceKubeconfig, Context: "a"}} {
			if s, err := NewSession(context.Background(), ref, Options{}); err == nil {
				s.Dispose()
			}
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("the rule's paths come from USERPROFILE and HOME there")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".kube"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kube", ".kubeconfig"), []byte(twoContexts), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLoadingNeverWritesKubeconfig$")
	cmd.Env = append(os.Environ(), "HOME="+home, "KUBETUI_MIGRATE_CHILD=1", "KUBECONFIG="+writeKubeconfig(t, twoContexts))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".kube", "config")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("loading a kubeconfig wrote ~/.kube/config (stat: %v)", err)
	}
}
