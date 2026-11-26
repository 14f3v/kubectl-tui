package editor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

// object is what the edit action writes: sigs.k8s.io/yaml output, LF only, with
// block scalars so a line-ending rewrite reaches inside values too.
const object = `apiVersion: v1
data:
  script: |
    echo one
    echo two
  folded: >
    a
    b
kind: ConfigMap
metadata:
  name: web
  namespace: demo
`

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

func TestUnchanged(t *testing.T) {
	sum := digest([]byte(object))
	cases := []struct {
		name string
		data string
		want bool
	}{
		{name: "as written", data: object, want: true},
		{name: "saved with CRLF", data: crlf(object), want: true},
		{name: "some lines CRLF", data: strings.Replace(object, "\n", "\r\n", 3), want: true},
		{name: "saved with a BOM", data: "\xef\xbb\xbf" + object, want: true},
		{name: "saved with a BOM and CRLF", data: "\xef\xbb\xbf" + crlf(object), want: true},
		{name: "edited and saved with a BOM", data: "\xef\xbb\xbf" + strings.Replace(object, "echo two", "echo three", 1), want: false},
		{name: "edited", data: strings.Replace(object, "echo two", "echo three", 1), want: false},
		{name: "edited and saved with CRLF", data: crlf(strings.Replace(object, "name: web", "name: api", 1)), want: false},
		{name: "final newline dropped", data: strings.TrimSuffix(object, "\n"), want: false},
		{name: "emptied", data: "", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Unchanged([]byte(c.data), sum); got != c.want {
				t.Errorf("Unchanged(%q) = %v, want %v", c.data, got, c.want)
			}
		})
	}
}

// Folding CRLF into LF for the no-op check is only sound because the YAML parser
// already treats them alike — block scalars included — so a CRLF save that does
// carry a change is applied with exactly the values an LF save would send.
func TestCRLFParsesLikeLF(t *testing.T) {
	lf, err := yaml.YAMLToJSON([]byte(object))
	if err != nil {
		t.Fatalf("YAMLToJSON(LF): %v", err)
	}
	cr, err := yaml.YAMLToJSON([]byte(crlf(object)))
	if err != nil {
		t.Fatalf("YAMLToJSON(CRLF): %v", err)
	}
	if !bytes.Equal(lf, cr) {
		t.Errorf("CRLF parses differently:\n LF   %s\n CRLF %s", lf, cr)
	}
	if bytes.Contains(cr, []byte(`\r`)) {
		t.Errorf("a carriage return survived into the applied JSON: %s", cr)
	}
}

var configmaps = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

// editRoundTrip writes object with WriteTemp, replaces the file with saved as
// though an editor had written it, and runs Apply against a fake client. It
// returns what Apply reported and the patch it sent, nil if none.
func editRoundTrip(t *testing.T, saved string) (changed bool, patch []byte) {
	t.Helper()
	changed, patch, err := applyEdit(t, saved, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return changed, patch
}

// applyEdit is editRoundTrip with a prepare func, returning Apply's error too.
func applyEdit(t *testing.T, saved string, prepare func([]byte) ([]byte, error)) (changed bool, patch []byte, err error) {
	t.Helper()
	path, sum, err := WriteTemp(object)
	if err != nil {
		t.Fatalf("WriteTemp: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })
	if got, err := os.ReadFile(path); err != nil || string(got) != object {
		t.Fatalf("temp file holds %q (err %v), want the object as given", got, err)
	}
	if err := os.WriteFile(path, []byte(saved), 0o600); err != nil {
		t.Fatalf("rewrite temp file: %v", err)
	}

	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dyn.PrependReactor("patch", "configmaps", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pa := a.(k8stesting.PatchAction)
		if pa.GetPatchType() != types.ApplyPatchType {
			t.Errorf("patch type = %s, want server-side apply", pa.GetPatchType())
		}
		patch = pa.GetPatch()
		return true, nil, nil
	})
	changed, err = Apply(context.Background(), dyn, configmaps, true, "demo", "web", path, sum, prepare)
	return changed, patch, err
}

// An editor that only rewrote the line endings changed nothing, so nothing may
// reach the server.
func TestApplySkipsALineEndingRewrite(t *testing.T) {
	changed, patch := editRoundTrip(t, crlf(object))
	if changed || patch != nil {
		t.Errorf("CRLF-only save: changed=%v patch=%s, want a no-op", changed, patch)
	}
}

// A real edit saved with CRLF is applied, and the server sees clean values.
func TestApplySendsACRLFEdit(t *testing.T) {
	changed, patch := editRoundTrip(t, crlf(strings.Replace(object, "echo two", "echo three", 1)))
	if !changed || patch == nil {
		t.Fatalf("CRLF edit: changed=%v patch=%s, want it applied", changed, patch)
	}
	if !bytes.Contains(patch, []byte(`"script":"echo one\necho three\n"`)) || bytes.Contains(patch, []byte(`\r`)) {
		t.Errorf("applied patch = %s, want the edited script with LF line breaks", patch)
	}
}

// prepare gets the edited object as JSON, and what it returns is what the server
// gets.
func TestApplySendsWhatPrepareReturns(t *testing.T) {
	var got []byte
	prepare := func(edited []byte) ([]byte, error) {
		got = edited
		return []byte(`{"prepared":true}`), nil
	}
	changed, patch, err := applyEdit(t, strings.Replace(object, "echo two", "echo three", 1), prepare)
	if err != nil || !changed {
		t.Fatalf("Apply: changed=%v err=%v, want the edit applied", changed, err)
	}
	if !bytes.Contains(got, []byte(`"script":"echo one\necho three\n"`)) {
		t.Errorf("prepare got %s, want the edited object as JSON", got)
	}
	if string(patch) != `{"prepared":true}` {
		t.Errorf("applied patch = %s, want what prepare returned", patch)
	}
}

// An error from prepare stops the apply: it is reported and nothing reaches the
// server.
func TestApplyStopsOnAPrepareError(t *testing.T) {
	refuse := func([]byte) ([]byte, error) { return nil, errors.New("data.password is still redacted") }
	changed, patch, err := applyEdit(t, strings.Replace(object, "echo two", "echo three", 1), refuse)
	if err == nil || err.Error() != "data.password is still redacted" {
		t.Errorf("Apply error = %v, want prepare's", err)
	}
	if !changed || patch != nil {
		t.Errorf("changed=%v patch=%s, want the edit seen but nothing sent", changed, patch)
	}
}

// An unchanged file is a no-op before prepare is asked anything.
func TestApplySkipsPrepareWhenUnchanged(t *testing.T) {
	called := false
	prepare := func(b []byte) ([]byte, error) { called = true; return b, nil }
	changed, patch, err := applyEdit(t, crlf(object), prepare)
	if err != nil || changed || patch != nil || called {
		t.Errorf("unchanged save: changed=%v patch=%s err=%v prepare called=%v, want a no-op", changed, patch, err, called)
	}
}

func TestQuickExitHint(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		hint    bool
	}{
		{elapsed: 0, hint: true},
		{elapsed: 80 * time.Millisecond, hint: true}, // code without --wait
		{elapsed: quickExit - time.Millisecond, hint: true},
		{elapsed: quickExit, hint: false},
		{elapsed: 30 * time.Second, hint: false}, // a real look, no edit
	}
	for _, c := range cases {
		for goos, example := range map[string]string{
			"linux":   `KUBE_EDITOR="code --wait"`,
			"darwin":  `KUBE_EDITOR="code --wait"`,
			"windows": `$env:KUBE_EDITOR='code --wait'`, // PowerShell; cmd's set would keep the quotes
		} {
			got := quickExitHint(c.elapsed, goos)
			if (got != "") != c.hint {
				t.Errorf("quickExitHint(%v, %s) = %q, want a hint: %v", c.elapsed, goos, got, c.hint)
			}
			// KUBE_EDITOR, because advice to set EDITOR does nothing while a
			// KUBE_EDITOR holds the editor that did not wait.
			if c.hint && !strings.HasSuffix(got, example) {
				t.Errorf("quickExitHint(%v, %s) = %q, want it to end with %s", c.elapsed, goos, got, example)
			}
		}
	}
}

// The Windows example must survive its own round trip: what PowerShell stores
// for $env:KUBE_EDITOR='code --wait' has to split back into code plus its flag.
func TestQuickExitHintExampleSplits(t *testing.T) {
	env := func(k string) string {
		if k == "KUBE_EDITOR" {
			return "code --wait"
		}
		return ""
	}
	got := command(env, "windows", func(string) bool { return false })
	if len(got) != 2 || got[0] != "code" || got[1] != "--wait" {
		t.Errorf("command for KUBE_EDITOR=code --wait = %q, want [code --wait]", got)
	}
}
