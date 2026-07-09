package columns

import (
	"testing"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIngressesProject(t *testing.T) {
	proj := For("ingresses")
	class := "nginx"
	ing := &networkingv1.Ingress{
		ObjectMeta: meta("web"),
		Spec: networkingv1.IngressSpec{
			IngressClassName: &class,
			Rules: []networkingv1.IngressRule{
				{Host: "a.example.com"},
				{Host: "b.example.com"},
				{Host: ""},
			},
			TLS: []networkingv1.IngressTLS{{}},
		},
		Status: networkingv1.IngressStatus{
			LoadBalancer: networkingv1.IngressLoadBalancerStatus{
				Ingress: []networkingv1.IngressLoadBalancerIngress{
					{IP: "10.0.0.5"},
					{Hostname: "lb.example.com"},
				},
			},
		},
	}
	row, ok := proj.Project(ing, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "CLASS"); got.Text != "nginx" {
		t.Fatalf("CLASS = %q", got.Text)
	}
	if got := find(t, proj, row, "HOSTS"); got.Text != "a.example.com,b.example.com" {
		t.Fatalf("HOSTS = %q", got.Text)
	}
	if got := find(t, proj, row, "ADDRESS"); got.Text != "10.0.0.5,lb.example.com" {
		t.Fatalf("ADDRESS = %q", got.Text)
	}
	if got := find(t, proj, row, "PORTS"); got.Text != "80, 443" {
		t.Fatalf("PORTS = %q", got.Text)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestIngressesProject_Defaults(t *testing.T) {
	proj := For("ingresses")
	ing := &networkingv1.Ingress{ObjectMeta: meta("bare")}
	row, ok := proj.Project(ing, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "CLASS"); got.Text != "—" {
		t.Fatalf("CLASS = %q, want dash", got.Text)
	}
	if got := find(t, proj, row, "HOSTS"); got.Text != "—" {
		t.Fatalf("HOSTS = %q, want dash", got.Text)
	}
	if got := find(t, proj, row, "ADDRESS"); got.Text != "—" {
		t.Fatalf("ADDRESS = %q, want dash", got.Text)
	}
	if got := find(t, proj, row, "PORTS"); got.Text != "80" {
		t.Fatalf("PORTS = %q, want 80", got.Text)
	}
}

func TestNetworkPoliciesProject(t *testing.T) {
	proj := For("networkpolicies")
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: meta("allow-web"),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"tier": "web", "app": "api"},
			},
		},
	}
	row, ok := proj.Project(np, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "POD-SELECTOR"); got.Text != "app=api,tier=web" {
		t.Fatalf("POD-SELECTOR = %q", got.Text)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestNetworkPoliciesProject_SelectAll(t *testing.T) {
	proj := For("networkpolicies")
	np := &networkingv1.NetworkPolicy{ObjectMeta: meta("default-deny")}
	row, ok := proj.Project(np, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "POD-SELECTOR"); got.Text != "<none>" {
		t.Fatalf("POD-SELECTOR = %q, want <none>", got.Text)
	}
}

func TestEndpointSlicesProject(t *testing.T) {
	proj := For("endpointslices")
	p1 := int32(80)
	p2 := int32(443)
	es := &discoveryv1.EndpointSlice{
		ObjectMeta:  meta("web-abc"),
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports: []discoveryv1.EndpointPort{
			{Port: &p1},
			{Port: nil},
			{Port: &p2},
		},
		Endpoints: []discoveryv1.Endpoint{
			{Addresses: []string{"10.0.0.1"}},
			{Addresses: []string{"10.0.0.2"}},
		},
	}
	row, ok := proj.Project(es, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "ADDRESSTYPE"); got.Text != "IPv4" {
		t.Fatalf("ADDRESSTYPE = %q", got.Text)
	}
	if got := find(t, proj, row, "PORTS"); got.Text != "80,443" {
		t.Fatalf("PORTS = %q", got.Text)
	}
	if got := find(t, proj, row, "ENDPOINTS"); got.Text != "2" {
		t.Fatalf("ENDPOINTS = %q", got.Text)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestEndpointSlicesProject_NoPorts(t *testing.T) {
	proj := For("endpointslices")
	es := &discoveryv1.EndpointSlice{
		ObjectMeta:  meta("empty"),
		AddressType: discoveryv1.AddressTypeIPv6,
	}
	row, ok := proj.Project(es, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "PORTS"); got.Text != "—" {
		t.Fatalf("PORTS = %q, want dash", got.Text)
	}
	if got := find(t, proj, row, "ENDPOINTS"); got.Text != "0" {
		t.Fatalf("ENDPOINTS = %q, want 0", got.Text)
	}
}
