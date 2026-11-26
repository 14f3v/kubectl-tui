package clusters

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

// MaxPasteBytes caps what Inspect accepts; a real kubeconfig is a few KB.
const MaxPasteBytes = 1 << 20

var (
	ErrNotKubeconfig = errors.New("this doesn't look like a kubeconfig")
	ErrNoContexts    = errors.New("this kubeconfig has no contexts")
	ErrTooLarge      = errors.New("too large for a kubeconfig")
)

const maskText = "••••"

// RelativePathError reports a file reference that would resolve against
// kubetui's clusters/ folder once saved, and so break.
type RelativePathError struct{ Field, Owner, Path string }

func (e *RelativePathError) Error() string {
	return fmt.Sprintf("%s of %q is a relative path (%s); export a self-contained copy with: kubectl config view --raw --flatten --minify --context <name>",
		e.Field, Sanitize(e.Owner), Sanitize(e.Path))
}

// ContextCheck is one context in a pasted kubeconfig. Name is the context as
// the kubeconfig spells it: the identifier kubetui connects to and keys
// state.yaml by, never drawn. Display is Name as it is drawn, through Sanitize.
// Problem is what ConfirmUsable said, "" when the context is usable.
type ContextCheck struct{ Name, Display, Server, Problem string }

// ExecCheck is an exec login command kubetui would run on every connect.
type ExecCheck struct {
	User, Command   string
	Args            []string // secret values masked
	EnvNames        []string // values are never kept
	InteractiveMode string
}

type AuthProviderCheck struct{ User, Name, IssuerURL string }

// Report is what a paste contains, for the review box. Every string in it is
// for the screen and has been through Sanitize, except the context names that
// identify a context: CurrentContext and each ContextCheck's Name.
type Report struct {
	Data []byte // normalised bytes to save
	// CurrentContext is the paste's current-context as it spells it, which
	// only picks the context the box starts on; it is never drawn.
	CurrentContext string
	Contexts       []ContextCheck // sorted by name
	Exec           []ExecCheck
	AuthProviders  []AuthProviderCheck
	LocalFiles     []string // absolute paths the config reads
	Insecure       []string // cluster names with insecure-skip-tls-verify
}

// NeedsReview is true when the paste would run something, read a local file,
// contact an issuer or skip TLS verification.
func (r *Report) NeedsReview() bool {
	return len(r.Exec) > 0 || len(r.AuthProviders) > 0 || len(r.LocalFiles) > 0 || len(r.Insecure) > 0
}

// ExecCommands returns the distinct exec commands, sorted, as the review shows
// them. A command holding a control character is listed without it, so it never
// matches the real one in the exec allowlist and is refused: the user approved
// the command they saw, not one with hidden characters.
func (r *Report) ExecCommands() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range r.Exec {
		if !seen[e.Command] {
			seen[e.Command] = true
			out = append(out, e.Command)
		}
	}
	sort.Strings(out)
	return out
}

// Normalize drops a UTF-8 BOM and turns CRLF and lone CR into LF.
func Normalize(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(data, []byte("\r"), []byte("\n"))
}

// Inspect checks a pasted kubeconfig and lists what it would run or read.
func Inspect(data []byte) (*Report, error) {
	if len(data) > MaxPasteBytes {
		return nil, ErrTooLarge
	}
	data = Normalize(data)
	cfg, err := clientcmd.Load(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotKubeconfig, SafeLoadError(err))
	}
	if api.IsConfigEmpty(cfg) {
		return nil, ErrNotKubeconfig
	}
	if len(cfg.Contexts) == 0 {
		return nil, ErrNoContexts
	}

	clusterNames := sortedKeys(cfg.Clusters)
	userNames := sortedKeys(cfg.AuthInfos)

	for _, n := range clusterNames {
		if err := relativeCheck(n, "certificate-authority", cfg.Clusters[n].CertificateAuthority); err != nil {
			return nil, err
		}
	}
	for _, n := range userNames {
		u := cfg.AuthInfos[n]
		for _, f := range [][2]string{{"client-certificate", u.ClientCertificate}, {"client-key", u.ClientKey}, {"tokenFile", u.TokenFile}} {
			if err := relativeCheck(n, f[0], f[1]); err != nil {
				return nil, err
			}
		}
		if u.Exec != nil && strings.ContainsAny(u.Exec.Command, `/\`) && !filepath.IsAbs(u.Exec.Command) {
			return nil, &RelativePathError{Field: "exec.command", Owner: n, Path: u.Exec.Command}
		}
	}

	r := &Report{Data: data, CurrentContext: cfg.CurrentContext}
	for _, n := range clusterNames {
		c := cfg.Clusters[n]
		if c.CertificateAuthority != "" {
			r.LocalFiles = append(r.LocalFiles, Sanitize(c.CertificateAuthority))
		}
		if c.InsecureSkipTLSVerify {
			r.Insecure = append(r.Insecure, Sanitize(n))
		}
	}
	for _, n := range userNames {
		u := cfg.AuthInfos[n]
		for _, f := range []string{u.ClientCertificate, u.ClientKey, u.TokenFile} {
			if f != "" {
				r.LocalFiles = append(r.LocalFiles, Sanitize(f))
			}
		}
		if e := u.Exec; e != nil {
			ec := ExecCheck{User: Sanitize(n), Command: Sanitize(e.Command), Args: maskArgs(e.Args), InteractiveMode: Sanitize(string(e.InteractiveMode))}
			for _, env := range e.Env {
				ec.EnvNames = append(ec.EnvNames, Sanitize(env.Name))
			}
			r.Exec = append(r.Exec, ec)
		}
		if p := u.AuthProvider; p != nil {
			r.AuthProviders = append(r.AuthProviders, AuthProviderCheck{User: Sanitize(n), Name: Sanitize(p.Name), IssuerURL: Sanitize(p.Config["idp-issuer-url"])})
		}
	}

	for _, n := range sortedKeys(cfg.Contexts) {
		cc := ContextCheck{Name: n, Display: Sanitize(n)}
		if cl := cfg.Clusters[cfg.Contexts[n].Cluster]; cl != nil {
			cc.Server = Sanitize(cl.Server)
		}
		if err := clientcmd.ConfirmUsable(*cfg, n); err != nil {
			cc.Problem = Sanitize(err.Error())
		}
		r.Contexts = append(r.Contexts, cc)
	}
	return r, nil
}

func relativeCheck(owner, field, path string) error {
	if path == "" || filepath.IsAbs(path) {
		return nil
	}
	return &RelativePathError{Field: field, Owner: owner, Path: path}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// maskArgs hides the value of any flag whose name contains secret, token,
// password or key, in both --flag=value and --flag value forms. Everything
// else is shown in full so the user sees what will run.
func maskArgs(args []string) []string {
	out := make([]string, 0, len(args))
	maskNext := false
	for _, a := range args {
		a = Sanitize(a)
		if maskNext {
			maskNext = false
			if !strings.HasPrefix(a, "-") {
				out = append(out, maskText)
				continue
			}
		}
		if strings.HasPrefix(a, "-") {
			name, _, hasValue := strings.Cut(a, "=")
			if secretFlag(name) {
				if hasValue {
					a = name + "=" + maskText
				} else {
					maskNext = true
				}
			}
		}
		out = append(out, a)
	}
	return out
}

func secretFlag(name string) bool {
	name = strings.ToLower(name)
	for _, w := range []string{"secret", "token", "password", "key"} {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

// SafeLoadError makes a clientcmd.Load error fit to show. client-go prints the
// whole duplicate list with %v, token and password included, so the text is cut
// at " in list:". Control characters are dropped and the length is capped.
func SafeLoadError(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, " in list:"); i >= 0 {
		msg = msg[:i]
	}
	msg = Sanitize(msg)
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:200]) + "…"
	}
	return msg
}

// Sanitize drops C0 and C1 control characters, ESC and DEL among them, so a
// string from a kubeconfig cannot move the cursor, fake a line of the screen or
// the review box, or end the window title and reach the terminal raw. Every
// kubeconfig-derived string kubetui draws goes through it; the raw string stays
// the identifier (a context to connect to, a key in state.yaml).
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
