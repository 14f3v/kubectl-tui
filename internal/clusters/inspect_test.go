package clusters

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInspectErrors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"secret", fixture(t, "secret.yaml"), ErrNotKubeconfig},
		{"no kind", fixture(t, "nokind.yaml"), ErrNotKubeconfig},
		{"no contexts", fixture(t, "nocontexts.yaml"), ErrNoContexts},
		{"tabs", fixture(t, "tabs.yaml"), ErrNotKubeconfig},
		{"too large", bytes.Repeat([]byte("a"), MaxPasteBytes+1), ErrTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Inspect(c.data)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestInspectCRLFAndBOM(t *testing.T) {
	data := append([]byte("\xef\xbb\xbf"), bytes.ReplaceAll(fixture(t, "two.yaml"), []byte("\n"), []byte("\r\n"))...)
	r, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Contexts) != 2 || r.Contexts[0].Name != "dev" || r.Contexts[1].Name != "prod" {
		t.Fatalf("contexts = %+v", r.Contexts)
	}
	if r.Contexts[0].Server != "https://dev.example:6443" || r.Contexts[0].Problem != "" {
		t.Fatalf("ctx0 = %+v", r.Contexts[0])
	}
	if r.CurrentContext != "dev" {
		t.Fatalf("current = %q", r.CurrentContext)
	}
	if bytes.ContainsRune(r.Data, '\r') || bytes.HasPrefix(r.Data, []byte("\xef\xbb\xbf")) {
		t.Fatal("Data not normalised")
	}
	if r.NeedsReview() {
		t.Fatal("plain token config needs no review")
	}
}

func TestNormalizeLoneCR(t *testing.T) {
	if got := string(Normalize([]byte("a\rb\r\nc"))); got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestInspectMultiDocument(t *testing.T) {
	r, err := Inspect(fixture(t, "multidoc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Contexts) != 2 || len(r.Exec) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestInspectRelativePath(t *testing.T) {
	_, err := Inspect(fixture(t, "relca.yaml"))
	var rp *RelativePathError
	if !errors.As(err, &rp) {
		t.Fatalf("err = %v", err)
	}
	if rp.Field != "certificate-authority" || rp.Owner != "dev" || rp.Path != "ca.crt" {
		t.Fatalf("rp = %+v", rp)
	}
	if !strings.Contains(err.Error(), "kubectl config view --raw --flatten --minify --context") {
		t.Fatalf("message lacks hint: %v", err)
	}
}

func TestInspectRelativeExecCommand(t *testing.T) {
	y := strings.Replace(string(fixture(t, "exec.yaml")), "command: aws", "command: ./bin/aws", 1)
	_, err := Inspect([]byte(y))
	var rp *RelativePathError
	if !errors.As(err, &rp) || rp.Field != "exec.command" {
		t.Fatalf("err = %v", err)
	}
	absCmd := filepath.Join(t.TempDir(), "aws")
	y = strings.Replace(string(fixture(t, "exec.yaml")), "command: aws", "command: "+strconv.Quote(absCmd), 1)
	if _, err := Inspect([]byte(y)); err != nil {
		t.Fatalf("absolute command: %v", err)
	}
}

func TestInspectExec(t *testing.T) {
	r, err := Inspect(fixture(t, "exec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Exec) != 1 {
		t.Fatalf("exec = %+v", r.Exec)
	}
	e := r.Exec[0]
	if e.Command != "aws" || e.User != "u" || fmt.Sprint(e.Args) != "[eks get-token --region eu-west-1]" ||
		fmt.Sprint(e.EnvNames) != "[AWS_PROFILE]" || e.InteractiveMode != "Never" {
		t.Fatalf("exec = %+v", e)
	}
	if !r.NeedsReview() || fmt.Sprint(r.ExecCommands()) != "[aws]" {
		t.Fatal("review expected")
	}
}

func TestInspectMasksSecrets(t *testing.T) {
	r, err := Inspect(fixture(t, "kubelogin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "[get-token --oidc-client-secret=•••• --token •••• --other x]"
	if got := fmt.Sprint(r.Exec[0].Args); got != want {
		t.Fatalf("args = %s, want %s", got, want)
	}
	s := fmt.Sprint(r.Exec, r.Contexts, r.LocalFiles)
	if strings.Contains(s, "s3cr3t") || strings.Contains(s, "abc") {
		t.Fatalf("leak: %s", s)
	}
}

func TestMaskArgs(t *testing.T) {
	got := maskArgs([]string{"--API-Key=k", "--password", "p", "--region", "r", "--client-id=i", "--token", "--next", "-k"})
	want := "[--API-Key=•••• --password •••• --region r --client-id=i --token --next -k]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got %v", got)
	}
}

func TestInspectSanitizes(t *testing.T) {
	r, err := Inspect(fixture(t, "escarg.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if a := r.Exec[0].Args[0]; strings.ContainsRune(a, 0x1b) || a != "[2Jfake" {
		t.Fatalf("arg = %q", a)
	}
	if got := Sanitize("a\x00b\u0085c\x7fd\te\nf"); got != "abcdef" {
		t.Fatalf("Sanitize = %q", got)
	}
}

func TestInspectAuthProvider(t *testing.T) {
	r, err := Inspect(fixture(t, "oidc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.AuthProviders) != 1 || r.AuthProviders[0] != (AuthProviderCheck{User: "u", Name: "oidc", IssuerURL: "https://issuer.example"}) {
		t.Fatalf("providers = %+v", r.AuthProviders)
	}
	if !r.NeedsReview() {
		t.Fatal("review expected")
	}
}

func TestInspectLocalFilesAndInsecure(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	r, err := Inspect([]byte(strings.Replace(string(fixture(t, "tokenfile.yaml")), "@@TOKENFILE@@", strconv.Quote(tok), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(r.LocalFiles) != "["+tok+"]" || !r.NeedsReview() {
		t.Fatalf("files = %v", r.LocalFiles)
	}
	r, err = Inspect(fixture(t, "insecure.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(r.Insecure) != "[dev]" || !r.NeedsReview() {
		t.Fatalf("insecure = %v", r.Insecure)
	}
}

func TestInspectUnusableContext(t *testing.T) {
	y := strings.Replace(string(fixture(t, "two.yaml")), "{cluster: prod, user: u}", "{cluster: nope, user: u}", 1)
	r, err := Inspect([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if r.Contexts[0].Problem != "" || r.Contexts[1].Problem == "" {
		t.Fatalf("contexts = %+v", r.Contexts)
	}
}

func TestInspectLoadErrorsLeakNothing(t *testing.T) {
	dup := `apiVersion: v1
kind: Config
users:
- name: u
  user: {token: SUPERSECRETTOKEN}
- name: u
  user: {username: x, password: hunter2}
`
	_, err := Inspect([]byte(dup))
	if !errors.Is(err, ErrNotKubeconfig) {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{"SUPERSECRETTOKEN", "hunter2"} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("error leaks %q: %v", bad, err)
		}
	}
	_, err = Inspect([]byte("apiVersion: v1\nkind: \"X\\e[2J\"\n"))
	if err == nil || strings.ContainsRune(err.Error(), 0x1b) {
		t.Fatalf("err = %q", err)
	}
}
