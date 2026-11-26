package inspect

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubectl/pkg/describe"
)

// kubectl's describer prints the token of a service-account-token Secret in
// clear text. Describe must show it as a size, like every other value, and
// leave the rest of the page as kubectl renders it.
func TestSecretDescriberSizesTheToken(t *testing.T) {
	const token = "eyJhbGciOiJSUzI1NiJ9.LEAKED-JWT"
	saToken := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ci-token", Namespace: "demo", Labels: map[string]string{"app": "ci"}},
		Type:       corev1.SecretTypeServiceAccountToken,
		Data: map[string][]byte{
			corev1.ServiceAccountTokenKey: []byte(token),
			"ca.crt":                      []byte("-----BEGIN CERTIFICATE-----"),
			"namespace":                   []byte("demo"),
		},
	}
	// An Opaque Secret's token key is printed as a size by kubectl already.
	opaque := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "demo"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{corev1.ServiceAccountTokenKey: []byte(token)},
	}
	cs := fake.NewClientset(saToken, opaque)

	out, err := secretDescriber(cs).Describe("demo", "ci-token", describe.DescriberSettings{ShowEvents: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, token) || strings.Contains(out, "LEAKED") {
		t.Fatalf("the token is on the describe page:\n%s", out)
	}
	for _, want := range []string{"ci-token", "app=ci", "kubernetes.io/service-account-token", "ca.crt:     27 bytes", "namespace:  4 bytes", "token:      31 bytes"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe page lacks %q:\n%s", want, out)
		}
	}

	out, err = secretDescriber(cs).Describe("demo", "app", describe.DescriberSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, token) || !strings.Contains(out, "token:  31 bytes") {
		t.Errorf("Opaque Secret's describe page:\n%s", out)
	}

	// The stored Secret, which the cache may share, keeps its token.
	got, err := cs.CoreV1().Secrets("demo").Get(t.Context(), "ci-token", metav1.GetOptions{})
	if err != nil || string(got.Data[corev1.ServiceAccountTokenKey]) != token {
		t.Errorf("the stored token changed: %q, %v", got.Data[corev1.ServiceAccountTokenKey], err)
	}

	if _, err := secretDescriber(cs).Describe("demo", "missing", describe.DescriberSettings{}); err == nil {
		t.Error("describing a missing Secret did not fail")
	}
}
