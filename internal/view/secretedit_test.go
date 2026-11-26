package view

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientfeatures "k8s.io/client-go/features"
	clientfeaturestesting "k8s.io/client-go/features/testing"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/msg"
)

// secretEdit is the edit action on a cached Secret, run up to the point where
// the editor would start: the hand-off it returned, the temp file it wrote, and
// the patch the apply after the editor sends, nil until one is sent.
type secretEdit struct {
	req   ExecRequest
	path  string
	patch *[]byte
}

// startSecretEdit caches the Secret demo/web holding password=hunter2 at
// resourceVersion 41, selects it, and runs the edit action.
func startSecretEdit(t *testing.T) secretEdit {
	t.Helper()
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	p := actionPage(t, "secrets", false)
	sess := p.sess.Session

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "demo", ResourceVersion: "41"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"password": []byte("hunter2")},
	}
	lw := &cache.ListWatch{
		ListFunc: func(metav1.ListOptions) (runtime.Object, error) {
			return &corev1.SecretList{Items: []corev1.Secret{secret}}, nil
		},
		WatchFunc: func(metav1.ListOptions) (watch.Interface, error) { return watch.NewFake(), nil },
	}
	sess.Engine.Register("secrets", true, func(sink engine.Sink, _ string) *engine.ViewStore {
		return engine.NewViewStore("secrets", lw, &corev1.Secret{}, columns.For("secrets"), sink)
	})
	vs, err := sess.Engine.Ensure("secrets", "")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, ok := vs.Get("demo", "web"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the Secret never reached the cache")
		}
	}

	var patch []byte
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dyn.PrependReactor("patch", "secrets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		patch = a.(k8stesting.PatchAction).GetPatch()
		return true, nil, nil
	})
	sess.Dyn = dyn

	cmd := p.editAction()
	if cmd == nil {
		t.Fatal("edit returned no command")
	}
	req, ok := cmd().(ExecRequest)
	if !ok {
		t.Fatalf("edit did not hand off to the editor: %#v", cmd())
	}
	path := req.Process.Args[len(req.Process.Args)-1]
	t.Cleanup(func() { os.Remove(path) })
	return secretEdit{req: req, path: path, patch: &patch}
}

// save stands in for the editor: it rewrites the temp file with edit applied.
func (e secretEdit) save(t *testing.T, edit func(string) string) {
	t.Helper()
	b, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.path, []byte(edit(string(b))), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEditSecretKeepsValuesOffDiskAndRestoresThem(t *testing.T) {
	e := startSecretEdit(t)
	onDisk, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "hunter2") || strings.Contains(string(onDisk), base64.StdEncoding.EncodeToString([]byte("hunter2"))) {
		t.Fatalf("the Secret's value was written to the editor's temp file:\n%s", onDisk)
	}
	if !strings.Contains(string(onDisk), "password: '<redacted: 7 bytes>'") {
		t.Fatalf("temp file lacks the readable placeholder:\n%s", onDisk)
	}

	// The user adds a label and leaves the value alone.
	e.save(t, func(s string) string {
		return strings.Replace(s, "  name: web\n", "  labels:\n    tier: db\n  name: web\n", 1)
	})
	got, _ := e.req.After(nil).(msg.Toast)
	if got.Text != "web applied" {
		t.Fatalf("after the editor: toast %q, want the edit applied", got.Text)
	}
	for _, want := range []string{`"password":"aHVudGVyMg=="`, `"resourceVersion":"41"`, `"tier":"db"`} {
		if !strings.Contains(string(*e.patch), want) {
			t.Errorf("applied patch lacks %s: %s", want, *e.patch)
		}
	}
	if _, err := os.Stat(e.path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("temp file left behind after the edit: %v", err)
	}
}

func TestEditSecretRefusesAPlaceholderItCannotRestore(t *testing.T) {
	e := startSecretEdit(t)
	e.save(t, func(s string) string { return strings.Replace(s, "  password:", "  passwd:", 1) })
	got, _ := e.req.After(nil).(msg.Toast)
	if got.Level != msg.LevelError || !strings.Contains(got.Text, "data.passwd is still redacted") {
		t.Errorf("toast = %+v, want an error naming data.passwd", got)
	}
	if *e.patch != nil {
		t.Errorf("a refused edit was applied: %s", *e.patch)
	}
}

// Quitting without an edit is still a no-op, with nothing sent.
func TestEditSecretUnchangedIsANoOp(t *testing.T) {
	e := startSecretEdit(t)
	got, _ := e.req.After(nil).(msg.Toast)
	if *e.patch != nil || got.Level == msg.LevelError || got.Level == msg.LevelSuccess {
		t.Errorf("unchanged edit: toast %+v, patch %s; want a no-op", got, *e.patch)
	}
}
