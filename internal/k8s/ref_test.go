package k8s

import (
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
