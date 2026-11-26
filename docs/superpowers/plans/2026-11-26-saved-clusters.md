# Saved Clusters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Launching `kubetui` without `--kubeconfig` opens a picker of every known cluster (pasted, remembered files, default kubeconfig); a kubeconfig can be pasted in and is saved; `:ctx` switches across all of them; each cluster keeps its last namespace and view.

**Architecture:** A new pure-ish `internal/clusters` package owns storage (`clusters/` folder + `state.yaml`), kubeconfig checks, and building picker rows. `internal/k8s` gains a `ClusterRef` that names one context in one source, and `NewSession` takes it. The root model in `internal/app` gains a session-free launch state (picker + paste modal), token-guarded bootstraps that keep the current session until the new one is ready, and debounced state writes.

**Tech Stack:** Go 1.26, charm.land/bubbletea/v2 v2.0.8, charm.land/lipgloss/v2, client-go v0.36.2 (`tools/clientcmd`, `rest`), sigs.k8s.io/yaml.

**Spec:** `docs/superpowers/specs/2026-11-26-saved-clusters-design.md` (read it alongside this plan; this plan argues from it).

## Global Constraints

- Prepend every Go command with `export PATH="/opt/homebrew/bin:$PATH"; export GOTOOLCHAIN=local`.
- Work on branch `feat/saved-clusters`. Never commit to `main`. Every commit must build on its own.
- Before each commit: `gofmt -l internal cmd hack` prints nothing; `go build ./... && go vet ./... && go test ./...` pass; `GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows go vet ./...` pass.
- Commit messages: imperative subject in the repo's style (e.g. "Show a Secret's values as sizes in the :apply preview"), body explaining why, then a blank line and the trailers:
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_013qSBxAPhBKi6a6gCxKQF2J`. Run commit scripts with `bash`.
- No IO inside `Update` (`internal/app/app.go:1-6`); IO runs in `tea.Cmd`s.
- `View` never touches a Session (pages are smoke-tested with none).
- Width math uses `lipgloss.Width` / `ansi.StringWidth`, never `len()`.
- kubetui never writes `~/.kube/config`, `$KUBECONFIG` files, or a `--kubeconfig` file.
- The paste box never renders any of the pasted text.
- Files under `clusters/`: directory chmod 0700, files 0600 on Unix; never set the read-only attribute on Windows (no 0400/0444).
- `clusters/.gitignore` contains exactly `*\n`.
- Placeholder copy is fixed by the spec: rows show `(file missing)`, `needs review`, `connecting…`; the switch confirm says `Switching closes N port-forwards.`; the relative-path error hint is `kubectl config view --raw --flatten --minify --context <name>`.
- Remove is `ctrl+d` (`internal/view/keys.go:25` `keyDelete`), never `d`.
- Masking: env values always; an exec argument's value when the flag name contains `secret`, `token`, `password` or `key` (`--flag=value` and `--flag value`).

## Review Focus

1. **An oversized or non-kubeconfig paste** (a 5 MB log, a binary blob): the box must stay responsive and say "this doesn't look like a kubeconfig"; anything over 1 MiB is refused without parsing. Test: Task 10 `TestPasteBoxRefusesOversizedPaste`.
2. **The same kubeconfig pasted twice**: a second identical paste must reuse the existing saved file and entry, not create `name-2.yaml` with a duplicate row. Test: Task 4 `TestSavePasteReusesIdenticalContent`.
3. **A relative `--kubeconfig` path** (`kubetui --kubeconfig ./dev.yaml`): it must be remembered as an absolute path, or the entry breaks when launched from another directory. Test: Task 12 `TestRememberFileStoresAbsolutePath`.
4. **Removing the cluster you are connected to** from `:ctx`: kubetui must refuse with a toast rather than delete the file under a live session (oidc refresh would also recreate it). Test: Task 9 `TestRemoveConnectedEntryIsRefused`.
5. **A `state.yaml` written by a newer kubetui** (`version: 2`, or an entry with an unknown `source`): it must load what it understands, skip the rest in the picker, and keep the unknown entries when it writes. Test: Task 5 `TestStateKeepsUnknownEntries`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/k8s/ref.go` (new) | `Source`, `ClusterRef`, `Options`, `ContextInfo`, `ListContexts` |
| `internal/k8s/session.go` | `NewSession(parent, ref, opts)`, identity from the requested context, nil-safe `Dispose`, `RefreshServerVersion(ctx)`, exec allowlist |
| `internal/config/config.go` | `Dir()` |
| `internal/clusters/store.go` (new) | `Store`, pasted files, atomic writes, permissions |
| `internal/clusters/state.go` (new) | `State`, `Entry`, `Key`, load/update/remove of `state.yaml` |
| `internal/clusters/inspect.go` (new) | `Normalize`, `Inspect`, `Report`, masking, sanitising |
| `internal/clusters/rows.go` (new) | `Row`, `Build`, `Collect`, `Match` |
| `internal/view/clusterpicker.go` (new, replaces `contextpicker.go`) | picker page |
| `internal/view/textview.go` | `SwitchContextRequest{Ref}` and new request messages |
| `internal/app/app.go` | bootstrap tokens, switching, launch branch, request handling, state recording |
| `internal/app/pastebox.go` (new) | paste modal state, input, review, save flow |
| `internal/app/render.go` | session-free rendering, nil-safe header, picker/paste rendering |
| `internal/app/palette.go` | `:ctx` description |
| `cmd/kubetui/main.go` | explicit-flag detection, launch mode, final state flush |
| `README.md`, `docs/ARCHITECTURE.md` | updated per task |

---

### Task 1: Identity follows the requested context; `Dispose` is nil-safe

Spec: "Existing bugs" 1 and 7.

**Files:**
- Modify: `internal/k8s/session.go:95-152` (`NewSession` call to `deriveIdentity`), `:183-190` (`Dispose`), `:320-340` (`deriveIdentity`)
- Test: `internal/k8s/session_test.go` (new)

**Interfaces:**
- Produces: `deriveIdentity(cc clientcmd.ClientConfig, restCfg *rest.Config) Identity` now reads `cc.MergedRawConfig()`; `(*Session).Dispose()` is safe on a zero `Session` and on one with nil `Forwards`/`Engine`/`cancel`.

- [ ] **Step 1: Write the failing tests**

`writeKubeconfig(t, yaml string) string` helper writes to `t.TempDir()`. Fixture: contexts `a` (cluster `ca`, server `https://a.example`, namespace `nsa`) and `b` (cluster `cb`, server `https://b.example`, namespace `nsb`), `current-context: a`, token users.

```go
func TestIdentityFollowsContextOverride(t *testing.T) {
	path := writeKubeconfig(t, twoContexts)
	s, err := NewSession(t.Context(), path, "b", false, func(tea.Msg) {})
	// (Task 2 changes this call; keep the old signature here.)
	if err != nil { t.Fatal(err) }
	defer s.Dispose()
	want := Identity{Context: "b", Cluster: "cb", User: "ub", Server: "https://b.example", Namespace: "nsb"}
	if s.Identity != want { t.Errorf("identity = %+v, want %+v", s.Identity, want) }
	if _, cur := s.Contexts(); cur != "b" { t.Errorf("current = %q, want b", cur) }
}

func TestDisposeIsNilSafe(t *testing.T) {
	(&Session{}).Dispose() // must not panic
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/k8s/ -run 'TestIdentityFollowsContextOverride|TestDisposeIsNilSafe' -v`
Expected: FAIL: identity has `Context:"a"`; `Dispose` panics on nil `Forwards`.

- [ ] **Step 3: Implement**

`deriveIdentity`: use `cc.MergedRawConfig()` (client-go v0.36.2 `client_config.go:137`, applies overrides) instead of `RawConfig()`. `Dispose`: guard each of `s.Forwards`, `s.Engine`, `s.cancel` for nil.

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/k8s/ -v`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Show the context kubetui is on, not the kubeconfig's current one". Body: the header, start namespace, whoami and the `:ctx` "current" marker described the file's `current-context` after `--context` or `:ctx`.

---

### Task 2: `ClusterRef` names what a session connects to

Spec: "Cluster entries" (the `ClusterRef` and pinned context).

**Files:**
- Create: `internal/k8s/ref.go`
- Modify: `internal/k8s/session.go` (`NewSession`, new `Session.Ref` field, `RefreshServerVersion`), `internal/app/app.go` (`Config`, `Init`, `bootstrapCmd`, `switchContext`, `reconnect`, `runCommand` `ctx` case), `internal/view/textview.go:46-48`, `internal/view/contextpicker.go` (emit a `Ref`), `cmd/kubetui/main.go:45-51`, `internal/k8s/session_test.go`
- Test: `internal/k8s/ref_test.go` (new), `internal/k8s/session_test.go`

**Interfaces:**
- Produces:
  ```go
  type Source string
  const (
  	SourcePasted     Source = "pasted"
  	SourceFile       Source = "file"
  	SourceKubeconfig Source = "kubeconfig"
  	SourceInCluster  Source = "in-cluster"
  )
  // ClusterRef names one context in one kubeconfig source. Comparable with ==.
  type ClusterRef struct {
  	Source  Source
  	Path    string // pasted/file: absolute file path; kubeconfig/in-cluster: ""
  	Context string // always set, except in-cluster
  }
  type Options struct {
  	ReadOnly     bool
  	Sink         engine.Sink
  	ApprovedExec []string // pasted only; enforced in Task 11
  }
  type ContextInfo struct{ Name, Cluster, Server, Namespace string }
  func ListContexts(path string) (contexts []ContextInfo, current string, err error) // path "" = default loading rules
  func NewSession(parent context.Context, ref ClusterRef, opts Options) (*Session, error)
  func (s *Session) RefreshServerVersion(ctx context.Context) error // bounded by 8s
  // Session gains: Ref ClusterRef
  ```
  `view.SwitchContextRequest{Ref k8s.ClusterRef}` (replaces `{Name string}`). `app.Config` replaces `KubeconfigPath`/`ContextOverride` with `Start k8s.ClusterRef`; `Model` gains `ref k8s.ClusterRef` (the active one).

- [ ] **Step 1: Write the failing tests**

```go
func TestListContexts(t *testing.T) {
	path := writeKubeconfig(t, twoContexts)
	got, cur, err := ListContexts(path)
	// want two ContextInfo sorted by name: {a ca https://a.example nsa}, {b cb https://b.example nsb}; cur == "a"
}
func TestListContextsDefaultRulesUsesKUBECONFIG(t *testing.T) {
	t.Setenv("KUBECONFIG", writeKubeconfig(t, twoContexts))
	got, _, err := ListContexts("") // same two contexts
}
func TestNewSessionPinsRefContext(t *testing.T) {
	path := writeKubeconfig(t, twoContexts)
	s, err := NewSession(t.Context(), ClusterRef{Source: SourceFile, Path: path, Context: "b"}, Options{Sink: func(tea.Msg) {}})
	// s.Ref == the ref; s.RestCfg.Host == "https://b.example"
}
```
Update `TestIdentityFollowsContextOverride` to the new signature.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/k8s/ -v`. Expected: compile failure (`ClusterRef` undefined).

- [ ] **Step 3: Implement `ref.go` and the new `NewSession`**

`NewSession`: `SourceInCluster` uses `rest.InClusterConfig()`; otherwise default loading rules with `ExplicitPath = ref.Path` when non-empty and `overrides.CurrentContext = ref.Context`. `ListContexts` uses `loadingRules.Load()` (no client config), sorted by name, server from the context's cluster. `RefreshServerVersion(ctx)`: `context.WithTimeout(ctx, 8*time.Second)`.

- [ ] **Step 4: Rewire the app with no behaviour change**

`main.go` builds `Start`: `--kubeconfig` → `{SourceFile, abs path, --context}`; otherwise `{SourceKubeconfig, "", --context}`. If `--context` is empty, resolve it with `ListContexts` (current) in `main` before `app.New`, so the context is always pinned. `bootstrapCmd(sink engine.Sink, ref k8s.ClusterRef, opts k8s.Options) tea.Cmd`. `switchContext(ref k8s.ClusterRef)`. `reconnect` uses `m.ref`. The `contextpicker` emits `SwitchContextRequest{Ref: k8s.ClusterRef{Source: sess.Ref.Source, Path: sess.Ref.Path, Context: name}}` (pass `sess.Ref` into `NewContextPicker`). `:ctx <name>` builds the same ref from `m.ref`. Fix the existing app tests that construct `Config` or `SwitchContextRequest`.

- [ ] **Step 5: Run everything**

Run: `go build ./... && go vet ./... && go test ./...`. Expected: PASS.

- [ ] **Step 6: Commit**

Subject: "Name the kubeconfig and context a session connects to". Body: a switch can now target another kubeconfig file, and the context is pinned so a `use-context` elsewhere no longer moves a reconnect to another cluster.

---

### Task 3: A switch keeps the current cluster until the new one is ready

Spec: "Existing bugs" 2 and 5, and "Switching".

**Files:**
- Modify: `internal/msg/msg.go:41-50`, `internal/app/app.go` (`Model` fields, `Init`, `bootstrapCmd`, `Update` `SessionReady`/`SessionError` cases, `onSessionReady`, `switchContext`, `reconnect`)
- Test: `internal/app/switch_test.go` (new)

**Interfaces:**
- Consumes: Task 2 `NewSession`, `ClusterRef`, `Options`, `RefreshServerVersion(ctx)`.
- Produces:
  ```go
  // msg
  type SessionReady struct{ Session any; Token int }
  type SessionError struct{ Err error; Token int; Ref any } // Ref is the k8s.ClusterRef that failed
  // app.Model gains:
  bootToken  int                // increments per bootstrap
  bootCancel context.CancelFunc // cancels the in-flight version probe
  connecting *k8s.ClusterRef    // non-nil while a bootstrap is in flight
  // app
  func bootstrapCmd(ctx context.Context, token int, ref k8s.ClusterRef, opts k8s.Options) tea.Cmd
  func (m *Model) switchContext(ref k8s.ClusterRef) (tea.Model, tea.Cmd) // starts a bootstrap; does not dispose
  func (m *Model) cancelConnect()                                       // cancels and clears connecting
  ```
  The `Session` passed to `NewSession` uses `context.Background()` as parent; only the version probe uses the cancellable ctx.

- [ ] **Step 1: Write the failing tests**

Use real sessions from a temp kubeconfig (Task 1's helper, copied into the app package's test file) so `Dispose` is real.

```go
func TestSwitchKeepsSessionUntilReady(t *testing.T) {
	// m has sess A (token 1). switchContext(refB) → m.sess is still A, m.connecting == &refB.
	// Deliver SessionReady{Session: B, Token: m.bootToken} → m.sess == B, A's Context() is Done, m.connecting == nil.
}
func TestStaleSessionReadyIsDisposed(t *testing.T) {
	// switchContext(refB) then switchContext(refC); deliver SessionReady{B, oldToken} → m.sess unchanged, B.Context() Done.
}
func TestFailedSwitchLeavesCurrentSession(t *testing.T) {
	// m has sess A; switchContext(ref with missing context); deliver SessionError{Token: current} → m.sess == A, m.fatal == nil, toast set.
}
func TestNewPickCancelsConnectInFlight(t *testing.T) {
	// switchContext(refB) records bootCancel; switchContext(refC) → the ctx handed to B's bootstrap is Done.
}
func TestSwitchResetsMetricsGauges(t *testing.T) {
	// metricsOK/clusterCPU/clusterMem set; after SessionReady for a switch they are false/0/0.
}
func TestSwitchWithForwardsAsksFirst(t *testing.T) {
	// A session whose Forwards.Count() > 0: SwitchContextRequest returns a ConfirmRequest whose Prompt is "Switching closes 1 port-forwards."
}
```
(If `portfwd.Manager` has no test seam to fake a count, add an unexported test hook in `internal/action/portfwd` with a one-line comment.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ -run 'Switch|Stale|Failed' -v`. Expected: FAIL.

- [ ] **Step 3: Implement**

`Update`: a `SessionReady`/`SessionError` whose `Token != m.bootToken` is stale; dispose a stale ready session and return. On a current `SessionReady`: dispose the previous `m.sess`, reset gauges, set `m.sess`, `m.ref`, `m.connecting = nil`, then the existing poller start and `navigate`. On a current `SessionError` with `m.sess != nil`: toast `"connect <context>: <err>"` at `LevelError`, clear `connecting`. With `m.sess == nil`, keep today's `m.fatal` (Task 12 replaces it with the picker). `switchContext` with forwards open returns the confirm; its `Action` returns a `switchConfirmed{ref}` message that starts the bootstrap. `reconnect` keeps its own wording. A new bootstrap calls `cancelConnect()` first, so a probe in flight is cancelled. Engine view IDs are process-global (`internal/engine/engine.go:30-36`); confirm pages drop snapshots whose view ID is not theirs, so late snapshots from the disposed engine change nothing, and if one does not, drop them in the root.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/app/ -v && go test ./...`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Keep the current cluster until a switch has connected". Body: a failed or slow switch used to leave no session, and two quick picks leaked a session.

---

### Task 4: Store pasted kubeconfigs in kubetui's own folder

Spec: "Storage" (folder, permissions, atomic writes, `.gitignore`, file names, no usable directory).

**Files:**
- Modify: `internal/config/config.go` (add `Dir`)
- Create: `internal/clusters/store.go`, `internal/clusters/store_test.go`

**Interfaces:**
- Produces:
  ```go
  // config
  func Dir() string // filepath.Dir(Path()), or "" when Path() is ""
  // clusters
  var ErrNoConfigDir = errors.New("no usable config directory")
  type Store struct{ /* dir string */ }
  func Open(dir string) (*Store, error)               // ErrNoConfigDir when dir == "" or !filepath.IsAbs(dir)
  func (s *Store) Dir() string
  func (s *Store) ClustersDir() string                // <dir>/clusters
  func (s *Store) SavePaste(name string, data []byte) (file string, err error)
  func (s *Store) PastedFiles() ([]string, error)     // base names of *.yaml, sorted; skips .gitignore, *.lock, temp files
  func (s *Store) PastePath(file string) string
  func (s *Store) DeletePaste(file string) error
  func writeAtomic(path string, data []byte) error    // temp in same dir, fsync, close, rename; 5 rename retries 50ms apart on Windows
  ```
  `SavePaste` creates `clusters/` (MkdirAll 0700, then Chmod 0700), writes `clusters/.gitignore` = `"*\n"` if missing, picks `slug(name)+".yaml"` using `cp.WindowsFileName` for safety on every OS (lowercased, spaces→`-`), adds `-2`, `-3`… on collision, and returns the existing file instead when an existing file has byte-identical content.

- [ ] **Step 1: Write the failing tests**

```go
func TestOpenRejectsUnusableDir(t *testing.T)       // Open("") and Open("rel/dir") → ErrNoConfigDir
func TestSavePasteWritesFileAndGitignore(t *testing.T) {
	// SavePaste("dev tokyo", data) → file "dev-tokyo.yaml"; content == data; clusters/.gitignore == "*\n"
	// on Unix (runtime.GOOS != "windows"): dir mode 0700, file mode 0600
}
func TestSavePasteNameCollision(t *testing.T)       // two different contents named "default" → "default.yaml", "default-2.yaml"
func TestSavePasteReusesIdenticalContent(t *testing.T) // same bytes twice → same file, one *.yaml in the dir
func TestSavePasteTightensExistingDir(t *testing.T) // Unix: pre-create clusters/ as 0755 → 0700 after SavePaste
func TestPastedFilesSkipsLocksAndTemps(t *testing.T) // a.yaml, a.yaml.lock, .tmp-123, .gitignore → ["a.yaml"]
func TestDeletePaste(t *testing.T)
func TestConfigDir(t *testing.T)                    // XDG_CONFIG_HOME=/x → "/x/kubetui"
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/clusters/ ./internal/config/ -v`. Expected: compile failure.

- [ ] **Step 3: Implement `store.go` and `config.Dir`**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/clusters/ ./internal/config/ -v` and `GOOS=windows go test -c -o /dev/null ./internal/clusters/`. Expected: PASS / compiles.

- [ ] **Step 5: Commit**

Subject: "Save pasted kubeconfigs in a folder only you can read". Update the README "Configuration" section with the `clusters/` folder.

---

### Task 5: Record clusters and their state in `state.yaml`

Spec: "`state.yaml`" (schema, set-aside, read-modify-write, no credentials) and Review Focus 5.

**Files:**
- Create: `internal/clusters/state.go`, `internal/clusters/state_test.go`

**Interfaces:**
- Consumes: Task 2 `k8s.Source`; Task 4 `Store`, `writeAtomic`.
- Produces:
  ```go
  const StateVersion = 1
  type Entry struct {
  	Source       k8s.Source `json:"source"`
  	File         string     `json:"file,omitempty"`   // pasted
  	Path         string     `json:"path,omitempty"`   // file
  	Name         string     `json:"name,omitempty"`   // pasted
  	Context      string     `json:"context,omitempty"`
  	Server       string     `json:"server,omitempty"` // kubeconfig: part of the key
  	LastUsed     time.Time  `json:"lastUsed,omitempty"`
  	Namespace    string     `json:"namespace"`
  	Kind         string     `json:"kind,omitempty"`
  	ApprovedExec []string   `json:"approvedExec,omitempty"`
  }
  type Key struct{ Source k8s.Source; File, Path, Context, Server string }
  func (e Entry) Key() Key // Server only for kubeconfig, File only for pasted, Path only for file
  func KeyFor(ref k8s.ClusterRef, server string) Key // pasted: File = filepath.Base(ref.Path); file: Path; kubeconfig: Server
  type State struct {
  	Version  int     `json:"version"`
  	Clusters []Entry `json:"clusters"`
  	// unknown entries are kept verbatim for rewriting
  }
  func (s *Store) LoadState() (State, error)                  // missing → empty; malformed → renamed to state.yaml.bad, empty
  func (s *Store) Update(k Key, fn func(*Entry)) error        // re-reads, applies fn to the entry (created if absent), writes atomically
  func (s *Store) Remove(k Key) error
  func (s *Store) Entries() ([]Entry, error)                  // known sources only
  ```
  Unknown entries (an unrecognised `source`, or extra fields from a newer version) survive `Update`/`Remove`: decode each entry as `json.RawMessage` first, decode only known sources into `Entry`, write the rest back unchanged. A `version` greater than `StateVersion` is read the same way and written back with its own version number.

- [ ] **Step 1: Write the failing tests**

```go
func TestLoadStateMissingIsEmpty(t *testing.T)
func TestLoadStateMalformedIsSetAside(t *testing.T)   // "{{{" → empty state, state.yaml.bad holds the bytes
func TestUpdateCreatesAndModifies(t *testing.T)       // two Updates on one key → one entry with both changes
func TestUpdateKeepsOtherWritersEntries(t *testing.T) // two Stores on the same dir update different keys → both present
func TestKubeconfigKeyIncludesServer(t *testing.T)    // "default"@https://a and "default"@https://b are two entries
func TestKeyFor(t *testing.T)                         // one ref per source → the Key Entry.Key() gives for the matching entry
func TestRemove(t *testing.T)
func TestStateKeepsUnknownEntries(t *testing.T) {
	// state.yaml: version 2, entries [{source: future, x: 1}, {source: file, path: /a, context: c}]
	// Entries() → only the file entry; after Update(file key, set Kind "pods") the file still contains source: future and x: 1, and version: 2
}
func TestStateHoldsNoCredentials(t *testing.T) // after Updates, state.yaml contains none of the fixture's token/cert strings
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/clusters/ -run 'State|Update|Remove|Key' -v`. Expected: FAIL.

- [ ] **Step 3: Implement `state.go`**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/clusters/ -v`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Remember clusters and where you were in each in state.yaml".

---

### Task 6: Check a kubeconfig before saving it

Spec: "Checks before saving" 1–4 and the masking rule.

**Files:**
- Create: `internal/clusters/inspect.go`, `internal/clusters/inspect_test.go`, `internal/clusters/testdata/` (fixtures)

**Interfaces:**
- Produces:
  ```go
  const MaxPasteBytes = 1 << 20
  var ErrNotKubeconfig = errors.New("this doesn't look like a kubeconfig")
  var ErrNoContexts    = errors.New("this kubeconfig has no contexts")
  var ErrTooLarge      = errors.New("too large for a kubeconfig")
  type RelativePathError struct{ Field, Owner, Path string } // Error() ends with the hint copy
  func Normalize(data []byte) []byte // drop UTF-8 BOM; CRLF and lone CR → LF
  type ContextCheck struct{ Name, Server, Problem string } // Problem from ConfirmUsable, "" when usable
  type ExecCheck struct {
  	User, Command string
  	Args          []string // masked
  	EnvNames      []string
  	InteractiveMode string
  }
  type AuthProviderCheck struct{ User, Name, IssuerURL string }
  type Report struct {
  	Data           []byte // normalised bytes to save
  	CurrentContext string
  	Contexts       []ContextCheck // sorted by name
  	Exec           []ExecCheck
  	AuthProviders  []AuthProviderCheck
  	LocalFiles     []string // absolute paths the config reads
  	Insecure       []string // cluster names with insecure-skip-tls-verify
  }
  func Inspect(data []byte) (*Report, error)
  func (r *Report) NeedsReview() bool     // any Exec, AuthProviders, LocalFiles or Insecure
  func (r *Report) ExecCommands() []string // distinct exec Command values, sorted
  func maskArgs(args []string) []string
  func sanitize(s string) string          // drops C0/C1 control characters and ESC
  ```
  Order inside `Inspect`: size > `MaxPasteBytes` → `ErrTooLarge`; `Normalize`; `clientcmd.Load` error → `fmt.Errorf("%w: %v", ErrNotKubeconfig, err)`; `api.IsConfigEmpty` → `ErrNotKubeconfig`; no contexts → `ErrNoContexts`; any relative `certificate-authority`, `client-certificate`, `client-key`, `tokenFile`, or an `exec.command` containing `/` or `\` that is not absolute → `*RelativePathError`; then collect the review items and run `ConfirmUsable` per context. Masked arguments become `--flag=••••` or `--flag ••••`. Every displayed string goes through `sanitize`.

- [ ] **Step 1: Write the failing tests**

Table-driven `TestInspect` over fixtures, asserting the error (with `errors.Is` / `errors.As`) or the report fields:

| case | expect |
|---|---|
| Secret manifest | `ErrNotKubeconfig` |
| YAML without `kind` | `ErrNotKubeconfig` |
| `contexts: []` | `ErrNoContexts` |
| tab indentation | `ErrNotKubeconfig` |
| CRLF + BOM copy of the two-context fixture | 2 contexts, `Data` has no `\r` and no BOM |
| multi-document (`---` then a second config) | only the first document's contexts |
| `certificate-authority: ca.crt` | `*RelativePathError{Field: "certificate-authority"}`, message contains `kubectl config view --raw --flatten --minify --context` |
| exec `aws` with `--region eu-west-1`, env `AWS_PROFILE=prod` | `Exec[0]` Command `aws`, Args unchanged, EnvNames `[AWS_PROFILE]`, `NeedsReview()` true |
| exec kubelogin `--oidc-client-secret=s3cr3t` and `--token abc` | Args `--oidc-client-secret=••••`, `--token`, `••••`; `fmt.Sprint(report)` contains neither `s3cr3t` nor `abc` |
| exec arg with `"\x1b[2Jfake"` | sanitised, no ESC |
| auth-provider oidc with `idp-issuer-url` | `AuthProviders[0]` |
| `tokenFile: /etc/token` | `LocalFiles` contains it |
| `insecure-skip-tls-verify: true` | `Insecure` contains the cluster |
| plain token config | `NeedsReview()` false |
| 1 MiB + 1 bytes | `ErrTooLarge` |

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/clusters/ -run TestInspect -v`. Expected: FAIL.

- [ ] **Step 3: Implement `inspect.go`**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/clusters/ -v`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Check a pasted kubeconfig and list what it would run".

---

### Task 7: Build the picker's rows from every source

Spec: "Cluster entries", "The cluster picker" (rows, sort, labels, missing files, unreviewed files, dropped records), `:ctx <name>`, `in-cluster`.

**Files:**
- Create: `internal/clusters/rows.go`, `internal/clusters/rows_test.go`

**Interfaces:**
- Consumes: Tasks 2, 4, 5.
- Produces:
  ```go
  type Row struct {
  	Ref         k8s.ClusterRef
  	Key         Key
  	Label       string // pasted: Name, or "Name / context" when its file has >1 context; else the context name
  	Source      k8s.Source
  	Server      string
  	LastUsed    time.Time // zero = never
  	Missing     bool      // file source whose file is gone
  	NeedsReview bool      // pasted file with no state record
  	Problem     string    // why it cannot be listed fully (unreadable file)
  }
  type Inputs struct {
  	Entries     []Entry
  	Pasted      map[string][]k8s.ContextInfo // file → contexts (nil value = unreadable)
  	Files       map[string][]k8s.ContextInfo // path → contexts (nil value = missing)
  	Kubeconfig  []k8s.ContextInfo
  	InCluster   bool
  	PastedDir   string
  }
  func Build(in Inputs) []Row        // pure: sorted LastUsed desc, then never-used by Label
  func Collect(s *Store) ([]Row, error) // IO: reads state, clusters/, remembered files, default kubeconfig, in-cluster availability; s may be nil (kubeconfig rows only)
  func Match(rows []Row, name string) []Row // exact match on Label or Ref.Context
  ```
  `InCluster` rows only when `Kubeconfig` is empty and `rest.InClusterConfig()` succeeds. A pasted state entry whose file is not in `Pasted` is dropped from the rows (and `Collect` removes it from the state). A pasted file with no entry → one `NeedsReview` row per context, labelled by its file name without `.yaml`.

- [ ] **Step 1: Write the failing tests**

```go
func TestBuildSortsByLastUsed(t *testing.T)
func TestBuildLabelsMultiContextPaste(t *testing.T)  // "dev-tokyo / admin", "dev-tokyo / viewer"
func TestBuildMarksMissingFile(t *testing.T)
func TestBuildMarksUnreviewedPaste(t *testing.T)
func TestBuildDropsRecordWithoutFile(t *testing.T)
func TestBuildKubeconfigStateByContextAndServer(t *testing.T) // two "default" contexts on different servers get their own LastUsed
func TestBuildInClusterOnlyWithoutKubeconfig(t *testing.T)
func TestMatch(t *testing.T)                          // one, several, none
func TestCollectWithTempDirs(t *testing.T)            // KUBECONFIG via t.Setenv, a pasted file, a remembered file
func TestCollectWithoutStore(t *testing.T)            // Collect(nil) → kubeconfig rows only
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/clusters/ -run 'Build|Match|Collect' -v`. Expected: FAIL.

- [ ] **Step 3: Implement `rows.go`**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/clusters/ -v`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "List every cluster kubetui knows, most recently used first".

---

### Task 8: The cluster picker page

Spec: "The cluster picker" (sketch, keys, remove rules, connecting row, errors).

**Files:**
- Create: `internal/view/clusterpicker.go`, `internal/view/clusterpicker_test.go`
- Delete: `internal/view/contextpicker.go`
- Modify: `internal/view/textview.go` (request messages), `internal/view/render_smoke_test.go:32` (replace `contextpicker` with `clusterpicker` in `allPages`)

**Interfaces:**
- Consumes: Task 7 `clusters.Row`.
- Produces (in `view`):
  ```go
  type ClustersLoaded struct{ Rows []clusters.Row; Err error; NoStore string } // NoStore: why paste/f/state are off, "" when on
  type OpenPasteBox struct{}
  type AddKubeconfigFile struct{ Path string }
  type ForgetCluster struct{ Row clusters.Row }
  type ReviewCluster struct{ Row clusters.Row } // enter on a NeedsReview row
  type ClusterConnectFailed struct{ Ref k8s.ClusterRef; Err string }
  func NewClusterPicker(theme style.Theme, current *k8s.ClusterRef, atLaunch bool) *clusterPickerPage
  // (*clusterPickerPage).SetConnecting(ref *k8s.ClusterRef) // nil clears
  ```
  Behaviour: rows arrive by `ClustersLoaded`. Cursor starts on the row whose `Ref` equals `current`, else row 0. Enter: `Missing` → toast "<label>: file missing"; `NeedsReview` → `ReviewCluster`; the connected row → toast "already on <label>"; else `SwitchContextRequest{Ref}`. `p` → `OpenPasteBox` (toast with `NoStore` when off). `f` → `PromptRequest{Title: "Add kubeconfig file", Label: "Path", Action: func(s) tea.Msg { return AddKubeconfigFile{Path: unquotePath(s)} }}`. `ctrl+d` → `ConfirmRequest{Danger: true, ...}` whose action returns `ForgetCluster`, with prompt text per source (pasted: "Delete <file> and its contexts: a, b?"; file: "Forget <path>? The file is not touched."); a kubeconfig/in-cluster row gives the toast "comes from your kubeconfig; kubetui doesn't edit it". `ClusterConnectFailed` shows the error under that row. Columns NAME, SOURCE, SERVER, LAST USED; LAST USED as "2h ago", "yesterday", "5 days ago", "—"; `●` before the connected row; dim + "(file missing)" for missing; "needs review" in SERVER for unreviewed; "connecting…" in LAST USED for the connecting row. `Keys()` returns bindings for enter (connect), p (paste), f (load file), ctrl+d (remove) and esc (back), which feed the help overlay. The bottom hint line is the sketch's footer (`enter connect · p paste kubeconfig · f load file · ctrl+d remove · q quit`, with `esc back` instead of `q quit` when not at launch).

- [ ] **Step 1: Write the failing tests**

```go
func TestPickerEnterEmitsRef(t *testing.T)
func TestPickerEnterOnConnectedRowToasts(t *testing.T)
func TestPickerEnterOnMissingRowToasts(t *testing.T)
func TestPickerEnterOnUnreviewedRowAsksReview(t *testing.T)
func TestPickerRemoveConfirmPerSource(t *testing.T)     // pasted, file, kubeconfig
func TestPickerViewFitsWidth(t *testing.T)              // 40 and 120 columns; a 60-rune CJK label; every line's lipgloss.Width <= width
func TestPickerShowsConnectingAndErrors(t *testing.T)
func TestPickerPasteOffWithoutStore(t *testing.T)
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/view/ -run Picker -v`. Expected: FAIL.

- [ ] **Step 3: Implement `clusterpicker.go`**

Keep the `app` package building: `:ctx` pushes the new picker, with rows loaded by a `tea.Cmd` that calls `clusters.Collect(nil)`, until Task 9 wires the store.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/view/ -v && go build ./... && go test ./...`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Pick a cluster from every source, not just one kubeconfig".

---

### Task 9: Wire the picker into the app for `:ctx`

Spec: "The cluster picker", "Switching", `:ctx <name>`, and Review Focus 4.

**Files:**
- Modify: `internal/app/app.go` (`Model` gains `store *clusters.Store`, `picker` handling, new request cases, `runCommand` `ctx`), `internal/app/command.go` (`:ctx` argument keeps the rest of the line: `strings.Join(fields[1:], " ")`), `internal/app/palette.go:13`, `cmd/kubetui/main.go` (open the store: `clusters.Open(config.Dir())`, pass it in `app.Config.Store`)
- Test: `internal/app/picker_test.go` (new)

**Interfaces:**
- Consumes: Tasks 3, 5, 7, 8.
- Produces: `app.Config.Store *clusters.Store` (nil when `Open` failed; `app.Config.StoreErr string` carries the reason); `func loadClustersCmd(s *clusters.Store) tea.Cmd` returning `view.ClustersLoaded`; handlers for `AddKubeconfigFile` (Cmd: `k8s.ListContexts(path)` must succeed with ≥1 context; then `store.Update` for each context's `file` key with an absolute path; reload rows), `ForgetCluster` (Cmd: pasted → `DeletePaste` + `Remove` for every key of that file; file → `Remove`; reload rows), `ClusterConnectFailed` routed to the picker on a `SessionError` while it is open.

- [ ] **Step 1: Write the failing tests**

```go
func TestCtxOpensPickerWithRows(t *testing.T)          // ":ctx" pushes the picker and returns a Cmd yielding ClustersLoaded
func TestCtxNameOneMatchSwitches(t *testing.T)
func TestCtxNameSeveralMatchesOpensPicker(t *testing.T)
func TestCtxNameNoMatchToasts(t *testing.T)
func TestAddKubeconfigFileRemembersAbsolutePath(t *testing.T)
func TestAddKubeconfigFileRejectsNonKubeconfig(t *testing.T)
func TestForgetPastedDeletesFile(t *testing.T)
func TestForgetFileLeavesFile(t *testing.T)
func TestRemoveConnectedEntryIsRefused(t *testing.T)   // ForgetCluster for m.ref's row → toast "disconnect first: switch to another cluster", file still exists
func TestSwitchFailureShowsOnPickerRow(t *testing.T)
func TestEscOnPickerCancelsConnect(t *testing.T)       // pick B, esc → picker popped, m.connecting == nil, bootstrap ctx Done, m.sess unchanged
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ -run 'Ctx|AddKubeconfig|Forget|Remove|SwitchFailure|EscOnPicker' -v`. Expected: FAIL.

- [ ] **Step 3: Implement the app wiring**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/app/ -v && go test ./...`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Switch across every saved cluster with :ctx". Update the README "Namespaces & contexts" section, the command table row for `:ctx`, and the help text.

---

### Task 10: The paste box

Spec: "The paste box" and "Checks before saving" (review screen), Review Focus 1.

**Files:**
- Create: `internal/app/pastebox.go`, `internal/app/pastebox_test.go`
- Modify: `internal/app/app.go` (`handleKey`/`handlePaste` check `m.paste` first, after the panic screen; `Update` handles `view.OpenPasteBox`, `view.ReviewCluster`), `internal/app/render.go` (render the box in the content area like `renderPrompt`)

**Interfaces:**
- Consumes: Task 4 `SavePaste`, Task 5 `Update`, Task 6 `Inspect`/`Report`, Task 3 `switchContext`.
- Produces:
  ```go
  type pasteBox struct {
  	buf      strings.Builder
  	lines    int
  	report   *clusters.Report
  	err      error
  	cursor   int    // selected context in report.Contexts
  	review   bool   // the review step is showing
  	existing string // ReviewCluster: the pasted file being reviewed, "" for a new paste
  }
  func (m *Model) openPasteBox(existing string) (tea.Model, tea.Cmd)
  func (m *Model) handlePasteBoxKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd)
  func (m *Model) handlePasteBoxPaste(p tea.PasteMsg) (tea.Model, tea.Cmd)
  func (m *Model) renderPasteBox(h int) string
  type pasteSaved struct{ ref k8s.ClusterRef; err error } // result of the save Cmd
  ```
  Input: `PasteMsg.Content` appended whole (several pastes concatenate); `enter` and `ctrl+j` append `\n`; `tab` appends two spaces; other keys append `k.Text`; `backspace` removes the last rune; `ctrl+u` clears. The buffer is parsed with `clusters.Inspect` after each `PasteMsg` and on `ctrl+s`, never per key. A buffer over `clusters.MaxPasteBytes` stops accepting input and shows `ErrTooLarge`. `j`/`k` are text, so the context list uses `up`/`down`. `ctrl+s`: with errors, nothing; when `NeedsReview()` and the review is not showing, show it (footer "ctrl+s I've read this, save and connect · esc cancel"); otherwise open the name prompt (`PromptRequest{Title: "Name this cluster", Label: "Name", Initial: <selected context name>}`) whose action returns a message that runs the save Cmd: `SavePaste`, then `store.Update` for every context in the file (`Name`, `ApprovedExec = report.ExecCommands()`), then `pasteSaved{ref}` → `switchContext(ref)`. `esc` closes; with content received, it asks first via `ConfirmRequest`. `ReviewCluster` opens the box with `existing` set and the report of that file: it skips the name prompt and records state with the file's base name as `Name`. Rendering shows only: the instruction line, `received N lines, X KB`, the contexts with servers and problems, the review items, errors, and the footer.

- [ ] **Step 1: Write the failing tests**

```go
func TestPasteBoxTakesPasteWhole(t *testing.T)          // two-context fixture via PasteMsg → report has 2 contexts
func TestPasteBoxConcatenatesPastes(t *testing.T)       // fixture split across two PasteMsgs
func TestPasteBoxKeystrokesBuildSameText(t *testing.T)  // fixture typed as KeyPressMsgs with enter/tab → same report after ctrl+s
func TestPasteBoxCapturesGlobalKeys(t *testing.T)       // q, :, /, digits, esc-less keys don't quit or open anything
func TestPasteBoxNeverRendersPastedText(t *testing.T)   // View contains no token, cert data or server-unrelated line from the fixture
func TestPasteBoxReviewBeforeSave(t *testing.T)         // exec fixture: first ctrl+s shows review, second opens the name prompt
func TestPasteBoxEscAsksWhenNotEmpty(t *testing.T)
func TestPasteBoxRefusesOversizedPaste(t *testing.T)    // MaxPasteBytes+1 bytes → ErrTooLarge shown, no Inspect panic, buffer frozen
func TestPasteBoxSaveConnects(t *testing.T)             // with a temp Store: after name prompt → file saved, state has Name and ApprovedExec, switch bootstraps the ref
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ -run PasteBox -v`. Expected: FAIL.

- [ ] **Step 3: Implement `pastebox.go` and its wiring**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/app/ -v && go test ./...`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Paste a kubeconfig into kubetui". Update the README with the paste box and the classic Windows console note (use `f`, or Windows Terminal).

---

### Task 11: Allow only the approved login command for a pasted cluster

Spec: "Checks before saving" 5.

**Files:**
- Modify: `internal/k8s/session.go` (`NewSession`), `internal/app/app.go` (pass `ApprovedExec` from the entry when bootstrapping a pasted ref)
- Test: `internal/k8s/session_test.go`

**Interfaces:**
- Consumes: Task 2 `Options.ApprovedExec`.
- Produces: for `SourcePasted` with `restCfg.ExecProvider != nil`, `restCfg.ExecProvider.PluginPolicy = clientcmdapi.PluginPolicy{PolicyType: clientcmdapi.PluginPolicyAllowlist, Allowlist: <one AllowlistEntry{Command: c} per approved command>}`; with no approved commands, `PolicyType: clientcmdapi.PluginPolicyDenyAll`. Other sources are untouched.

- [ ] **Step 1: Write the failing tests**

```go
func TestPastedExecAllowlist(t *testing.T)   // exec fixture, ApprovedExec ["aws"] → policy Allowlist with Command "aws"
func TestPastedExecDeniedWhenUnapproved(t *testing.T) // ApprovedExec nil → DenyAll
func TestFileSourceExecUntouched(t *testing.T)        // SourceFile → PolicyType ""
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/k8s/ -run Exec -v`. Expected: FAIL.

- [ ] **Step 3: Implement the policy in `NewSession`**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/k8s/ ./internal/app/ -v`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Run only the login command you approved for a pasted cluster".

---

### Task 12: Launch on the picker

Spec: "Launch behaviour", "Existing bugs" 3, 4 and 6, Review Focus 3.

**Files:**
- Modify: `cmd/kubetui/main.go` (explicit flags via `flag.Visit`; `--kind` default `""`), `internal/app/app.go` (`Init` branch; `SessionError` with no session → picker with the error on the row; picker reachable when `m.sess == nil`; booting keys), `internal/app/render.go:56-66` (session-free branch), `:335-337` (nil-safe header), `:626-632` (fatal text)
- Test: `internal/app/launch_test.go` (new), `internal/app/command_test.go:88` (extend the size sweep)

**Interfaces:**
- Consumes: Tasks 3, 8, 9, 10.
- Produces: `app.Config.Launch` enum `LaunchDirect | LaunchPicker`; `app.Config.StartKindExplicit bool`. `main`: `--kubeconfig` given → `LaunchDirect` with `{SourceFile, abs(path), ctx}`; `--context` alone → `LaunchDirect` with `{SourceKubeconfig, "", ctx}`; neither → `LaunchPicker`. `Init`: `LaunchPicker` pushes the picker (`atLaunch=true`) and loads rows; when the loaded rows are empty and the store is usable, it opens the paste box. While `m.sess == nil`, `render` draws a one-line title (`kubetui · pick a cluster`), the content area (picker / paste box / prompt / confirm / help) and the toast line, with no header or breadcrumb; only picker keys, `?` and `q` (and the paste box's capture) work. A `SessionError` at launch (no session) clears `connecting`, keeps the picker, and sends `ClusterConnectFailed`. `m.fatal` is no longer set by `SessionError`.

- [ ] **Step 1: Write the failing tests**

```go
func TestLaunchDirectWithKubeconfig(t *testing.T)   // Init returns a bootstrap; no picker
func TestLaunchPickerWithoutFlags(t *testing.T)     // Init pushes the picker; m.sess == nil; render shows "pick a cluster"
func TestLaunchEmptyOpensPasteBox(t *testing.T)     // ClustersLoaded{Rows: nil} with a store → paste box open
func TestLaunchErrorReturnsToPicker(t *testing.T)   // SessionError at launch → picker still active, m.fatal == nil
func TestLaunchKeysBeforeSession(t *testing.T)      // ":" and "/" and "1" do nothing; "q" quits
func TestRenderSurvivesEverySizeWhileBooting(t *testing.T) // extended: picker at launch, paste box at launch
func TestHeaderWithoutSession(t *testing.T)         // renderHeader with m.sess == nil does not panic
// cmd/kubetui:
func TestLaunchModeFromFlags(t *testing.T)          // table: none, --kubeconfig, --context, both, --kind only, $KUBECONFIG set with no flags (→ picker)
func TestRememberFileStoresAbsolutePath(t *testing.T) // "--kubeconfig ./dev.yaml" from a temp cwd → Start.Path is absolute
```
(Factor the flag → `app.Config` mapping in `main.go` into `func launchConfig(args []string, getwd func() (string, error)) (app.Config, error)` so it is testable.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ ./cmd/kubetui/ -run 'Launch|Render|Header|Remember' -v`. Expected: FAIL.

- [ ] **Step 3: Implement the launch branch and session-free rendering**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/app/ ./cmd/kubetui/ -v && go test ./...`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Start on the cluster picker when no kubeconfig is named". Update the README "Usage", the flags table (`-kubeconfig` connects directly and is remembered; `-context` connects directly), and the resolution-order bullet.

---

### Task 13: Remember and restore each cluster's namespace and view

Spec: "Per-cluster namespace and view".

**Files:**
- Modify: `internal/app/app.go` (`navigate` records; `onSessionReady` restores and records last-used, and for `SourceFile` remembers the path; debounce), `cmd/kubetui/main.go` (final flush after `program.Run`, before `os.Exit`)
- Test: `internal/app/state_test.go` (new)

**Interfaces:**
- Consumes: Task 5 `Store.Update`, `Entry`, `Key`; Task 12 `StartKindExplicit`.
- Produces:
  ```go
  type stateSaveMsg struct{ token int }        // debounce tick, 500ms
  func (m *Model) recordPosition(kind, ns string) tea.Cmd // sets pending, returns the debounce tick
  func (m *Model) FlushState() error           // writes pending synchronously; called from main after Run
  func (m *Model) startPosition(e *clusters.Entry) (kind, ns string)
  ```
  `navigate(kind, ns)` calls `recordPosition` with the `kind` argument (canonical, via `view.ResolveKind`) when it builds a page. `stateSaveMsg` with the current token returns a Cmd that calls `store.Update(key, …)` for the active entry. `onSessionReady`: `store.Update` sets `LastUsed = time.Now()` (and for kubeconfig the `Server`); then `startPosition`: explicit `--kind` wins for the first ready of the run only; else the entry's `Kind` if `view.ResolveKind` accepts it; else `"pods"`; namespace: entry's `Namespace` if the entry has been used before, else `sess.Identity.Namespace` (Task 1), else `""`. The auth retry (`reconnect`) and every switch go through this path.

- [ ] **Step 1: Write the failing tests**

```go
func TestNavigateRecordsPositionDebounced(t *testing.T) // two navigates → one write with the last kind/ns
func TestRestoreRememberedPosition(t *testing.T)
func TestExplicitKindWinsOnFirstReadyOnly(t *testing.T)
func TestUnknownRememberedKindFallsBackToPods(t *testing.T)
func TestReconnectReturnsToPosition(t *testing.T)
func TestFlushStateWritesPending(t *testing.T)
func TestNoStoreNoWrites(t *testing.T)                  // Store nil → no Cmds, no panics
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ -run 'Position|Restore|Explicit|Unknown|Reconnect|Flush|NoStore' -v`. Expected: FAIL.

- [ ] **Step 3: Implement recording, restoring and flushing**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/app/ -v && go test ./...`. Expected: PASS.

- [ ] **Step 5: Commit**

Subject: "Return to the namespace and view you left in each cluster".

---

### Task 14: Say what kubetui now writes to disk

Spec: "Text to update" (the parts not already done in earlier tasks).

**Files:**
- Modify: `README.md` (the "Notes" promise at `:439`; Windows notes `:394-395` folder location and the ACL note for `XDG_CONFIG_HOME` outside the profile), `docs/ARCHITECTURE.md` (session and config sections), `internal/app/render.go` (any text still mentioning "restart" for connect errors)

- [ ] **Step 1: Replace the README promise**

New text (copy fixed by the spec): pasted kubeconfigs are saved under the `clusters/` folder, readable only by you, like `~/.kube/config`; kubetui never writes your own kubeconfig files (client-go's oidc provider refreshing tokens excepted); Secret values are never written to disk.

- [ ] **Step 2: Check every task's docs landed**

Run: `grep -n 'never written to disk\|-kubeconfig\|:ctx' README.md` and read each hit against the spec's "Text to update" list. Expected: no statement contradicts the new behaviour.

- [ ] **Step 3: Full verification**

Run the Global Constraints checks, plus `GOOS=linux go build ./...`, `go test -race -timeout 180s ./...`, and for each package `GOOS=windows go test -c -o /dev/null <pkg>`. Then the per-commit build check: `for c in $(git rev-list --reverse main..HEAD); do git archive $c | (d=$(mktemp -d) && tar -x -C $d && cd $d && go build ./... && go vet ./...) || echo FAIL $c; done`. Expected: no FAIL.

- [ ] **Step 4: Commit**

Subject: "Say where kubetui keeps pasted kubeconfigs".
