# Saved clusters: paste a kubeconfig, pick a cluster at launch, switch across all of them

Status: design, awaiting approval · 2026-11-26

## Why

Today kubetui only knows the kubeconfig it was launched with. To use a cluster
whose kubeconfig you were handed (a cloud console's "copy kubeconfig", a
colleague's file), you have to save it somewhere and pass `--kubeconfig <path>`
every time. Someone who works across many clusters (the user's example is 10)
has to remember which file holds which, and `:ctx` can only switch between the
contexts of the one file kubetui started with.

The user wants:

- to launch `kubetui` with no flags and paste a kubeconfig straight in;
- kubetui to keep it, so the next launch offers it again;
- every launch without `--kubeconfig` to start on a list of the clusters they
  have worked with, and pick one;
- to switch between all of those clusters from inside kubetui;
- each cluster to come back where they left off (namespace and view).

## Goals

1. Pasting a kubeconfig into kubetui saves it and connects to it.
2. Launching without `--kubeconfig` opens a cluster picker listing every cluster
   kubetui knows, most recently used first. Enter on the top row returns to the
   last cluster.
3. `:ctx` opens the same picker inside kubetui and switches across all of them,
   not only the contexts of the launch kubeconfig.
4. Each cluster remembers its last namespace and base view, restored on pick or
   switch.
5. Works the same on macOS, Linux and Windows (including WSL), within the
   Windows support shipped in v1.2.0.

## Non-goals (this version)

- Encrypting saved kubeconfigs or using the OS keychain. Possible later without
  changing the picker (see Approaches).
- Editing or renaming a saved kubeconfig after it is saved.
- Restoring drill-ins (logs, yaml, describe, menus), CRD/dynamic pages, filters
  or sort order. Only the base kind and namespace come back.
- Per-cluster settings such as read-only or favorites (favorites stay global).
- Importing many kubeconfig files at once.
- Fixing exec-plugin stdin/stderr handling (an existing issue: client-go hands a
  plugin `os.Stdin` while Bubble Tea is reading it, and the plugin's stderr draws
  over the TUI). Noted because pasted configs often use exec plugins.

## Anti-goals (would make this a failure even if it works)

- kubetui writing to `~/.kube/config`, to `$KUBECONFIG` files, or to a file passed
  with `--kubeconfig`. Those stay the user's; kubetui only reads them. (client-go's
  oidc auth provider already writes refreshed tokens back to whichever file the
  user is on; that is client-go's existing behaviour and stays as is.)
- A pasted kubeconfig's credentials appearing on screen. The paste box never
  echoes the pasted text, and the review masks env values and secret-looking
  arguments (see Checks before saving).
- Running a command named in a pasted kubeconfig without showing it first.
- Making launch slower for someone with one cluster by more than one Enter.
- Changing what `--kubeconfig` and `--context` do today.

## Approaches considered (storage of pasted kubeconfigs)

1. **A folder of files kubetui owns. Chosen by the user.** Each paste becomes
   its own file under kubetui's config directory, readable only by the user, plus
   a small state file. Same model as kubectl's `~/.kube/config`, and how Lens and
   Headlamp store pasted configs. Works identically on every OS, including
   headless Linux and WSL. Cost: credentials are on disk unencrypted, so the
   README's "kubeconfigs are never written to disk" promise changes.
2. **Merge into `~/.kube/config`.** Pasted clusters would show up in kubectl and
   k9s too, but kubetui would be rewriting a file other tools depend on; pasted
   configs reuse names like `default`/`admin`/`kubernetes-admin` and would
   collide; removing a cluster cleanly is fiddly. Rejected.
3. **Option 1, encrypted, key in the OS keychain.** Keeps secrets off disk in the
   clear, but the whole kubeconfig can't live in the keychain (Windows Credential
   Manager caps an entry at about 2.5 KB; configs with embedded certificates are
   usually 5–10 KB), Linux servers and WSL often have no keychain service, macOS
   may prompt for access, and exec-plugin configs (aws, tsh) hold no secret to
   protect. Deferred; it can be layered onto option 1 later.

## What exists today (relevant facts)

- `cmd/kubetui/main.go` passes `--kubeconfig` / `--context` / `--kind` into
  `app.Config`. `app.Model.Init` always bootstraps a session at once
  (`app.go:131`); there is no pre-connect screen.
- `k8s.NewSession(parent, kubeconfigPath, contextName, readOnly, sink)` uses
  client-go's default loading rules, with `ExplicitPath` when a path is given
  (which turns off `$KUBECONFIG` merging) and `overrides.CurrentContext` for the
  context. It does no network IO; `SessionError` only means the kubeconfig could
  not be loaded or the context does not exist. An unreachable cluster still gives
  `SessionReady` and shows up later as a page banner.
- `:ctx` pushes `view/contextpicker.go`, which lists `sess.Contexts()` (contexts of
  the one loaded kubeconfig) and emits `SwitchContextRequest{Name}`.
  `switchContext` disposes the session and bootstraps again with the same
  `m.cfg.KubeconfigPath`.
- A failed bootstrap sets `m.fatal`: "could not connect to the cluster … check your
  kubeconfig / context, then restart". Only `q` works there.
- The namespace belongs to each page; "all namespaces" is `""`. Every namespace or
  kind change goes through `Model.navigate(kind, ns)`. The first page is
  `navigate(StartKind, sess.Identity.Namespace)` in `onSessionReady`.
- Pasting works since v1.2.0: a `tea.PasteMsg` reaches the active page intact
  when no prompt or input is open. Bracketed paste is on by default. Windows
  Terminal sends bracketed paste; the classic Windows console most likely does not,
  so a paste there arrives as key presses (CR as `KeyEnter`, LF as ctrl+j, tab as
  `KeyTab`). The existing single-line prompt flattens newlines and cannot hold YAML.
- `config.Path()` is `$XDG_CONFIG_HOME/kubetui/config.yaml`, else
  `~/.config/kubetui/config.yaml` (also on Windows: `%USERPROFILE%\.config\…`).
  `config.yaml` is only ever read.
- No `kubectl` subprocess exists; every cluster feature uses the Session's clients,
  so switching between kubeconfig files mid-run is safe once the old session is
  disposed.

### Existing bugs this feature depends on (fix first)

These were found while mapping the code and must be fixed as part of this work,
because the feature leans on each of them:

1. **Identity ignores the context override.** `deriveIdentity` reads the raw
   config's `current-context` (`session.go:327`), and `RawConfig()` ignores
   overrides. After `--context b` or `:ctx b`, kubetui talks to b but the header,
   the start namespace, whoami and the picker's "current" marker all describe the
   file's current-context. Derive identity from the requested context (e.g.
   `MergedRawConfig()` / `cc.Namespace()`). Per-cluster state must never be keyed on
   the buggy value.
2. **Bootstrap races.** Bootstraps carry no generation; `onSessionReady` replaces
   `m.sess` without disposing the previous one, and nothing stops `:ctx` while a
   connect is in flight. A picker makes "pick A, it's slow, pick B" common. Tag each
   bootstrap with a token, ignore and dispose stale results, and let a new pick
   cancel the connect in flight (bootstrap currently uses `context.Background()`).
3. **Dead-end error screen.** A `SessionError` (startup or switch) must return to
   the picker with the error shown, instead of the fatal screen.
4. **Rendering without a session.** `render.go:62` hides every page while
   `m.sess == nil`, and `renderHeader` dereferences `m.sess.Identity` (nil panic).
   Confirm and prompt dialogs render inside the content area, so they are hidden
   too. The startup picker needs a render path that works with no session.
5. **Switch leftovers.** `switchContext` does not reset the header's metrics gauges
   and does not warn about open port-forwards (only `reconnect` asks). Use the same
   confirm for any switch with forwards open.
6. **`--kind` default.** `--kind` defaults to `"pods"`, so an explicit
   `--kind pods` is indistinguishable from no flag. Detect explicit flags with
   `flag.Visit` (or default to `""`).
7. **`Dispose` on test sessions panics** (nil `cancel` / `Forwards`). Make it
   nil-safe so the switch path can be unit-tested.

## Design

### Cluster entries

A **cluster entry** is what the picker lists and what a switch targets:

```
ClusterRef{ Source, Path, Context }
```

| Source | Path | Where its contexts come from | Stored in state |
|---|---|---|---|
| `pasted` | a file in kubetui's `clusters/` folder | that file | yes, with its display name |
| `file` | an absolute path passed earlier with `--kubeconfig`, or loaded with `f` in the picker | that file, read live | yes (path only; never copied) |
| `kubeconfig` | `""` (client-go default loading rules: `$KUBECONFIG`, else `~/.kube/config`) | read live each time the picker opens | only once used (for last-used, namespace, view), keyed by context name **and** server URL |
| `in-cluster` | `""` | shown only when the default kubeconfig is empty and in-cluster config is available (kubetui running in a pod) | only once used |

- A kubeconfig with several contexts gives one entry per context. Entries are keyed
  by (source, path, context); context names may repeat across sources. `kubeconfig`
  entries add the context's server URL to the key, because `$KUBECONFIG` can point
  at different files over time and many installers (k3s and others) name their
  context `default`; two such clusters must not share namespace, view or last-used
  state.
- `NewSession` takes a `ClusterRef` instead of `(path, context)`. The context is
  always pinned explicitly, so a `kubectl config use-context` run elsewhere never
  moves a running kubetui to another cluster on reconnect.
- `SwitchContextRequest` carries a `ClusterRef`. `reconnect` (the auth-banner retry)
  rebuilds the active `ClusterRef`.

### Launch behaviour

| Launch | Behaviour |
|---|---|
| `kubetui --kubeconfig X [--context C]` | Connect directly, as today. On `SessionReady`, remember X (source `file`, absolute path) and update its last-used time. |
| `kubetui --context C` | Connect directly to C in the default kubeconfig, as today (an explicit choice; no picker). |
| `kubetui` | Open the cluster picker. |
| `kubetui`, and the picker would be empty | Open the paste box directly (replaces today's "could not connect" screen for a machine with no kubeconfig). |

The picker always shows when no flag names a cluster, even with one entry; the
cursor starts on the most recently used entry, so returning to the last cluster is
one Enter (my call, to keep launch predictable).

A set `$KUBECONFIG` does not skip the picker (my call): it names files, which often
hold several clusters, and many people set it once in their shell profile to merge
files. It decides which `kubeconfig` rows the picker lists. Someone who sets it per
shell to choose a cluster sees those rows labelled with their server, and
`--context` still connects directly.

### The cluster picker

One page, used at launch (no session yet) and by `:ctx` (pushed on the stack, esc
returns). It replaces `view/contextpicker.go`.

```
 kubetui · pick a cluster

   NAME               SOURCE       SERVER                        LAST USED
 › prod-eu            kubeconfig   https://10.0.4.1:6443         2h ago
   staging            pasted       https://staging.acme.io       yesterday
   dev-tokyo / admin  pasted       https://34.84.10.2            2 weeks ago
   minikube           kubeconfig   https://127.0.0.1:52011       —
   ci                 file         (file missing)                5 days ago

 enter connect · p paste kubeconfig · f load file · ctrl+d remove · q quit
```

- **Rows:** all entries, sorted by last-used (newest first), then never-used ones
  alphabetically. A pasted entry shows its saved name, plus `/ <context>` when its
  file holds more than one context. `●` marks the connected entry when opened from
  `:ctx`. A `file` entry whose file no longer exists is shown dimmed as
  "(file missing)"; enter on it shows a toast.
- **Keys:** enter connects; `p` opens the paste box; `f` asks for a file path
  (existing single-line prompt, quote-aware like the cp prompts) and adds that file
  as a `file` entry, exactly as if it had been passed with `--kubeconfig`: the path
  is remembered, the file is not copied, so later edits to it are picked up (my
  call: people who keep one kubeconfig file per cluster, as the user does in
  `~/.kube/`, would otherwise get stale copies). `f` checks the file loads as a
  kubeconfig with at least one context and lists its contexts as rows. `ctrl+d`
  removes (it is kubetui's delete key everywhere else; `d` means describe); `q`
  quits at launch; esc closes the picker when opened from `:ctx`.
- **Unreviewed pasted files:** a file in `clusters/` with no record in `state.yaml`
  (after a crash between the two writes, a reset state file, or a file copied in by
  hand) is listed under its file name, marked "needs review". Enter on it shows the
  same review as a fresh paste (Checks before saving, step 3) before anything
  connects; approving records it. Nothing in such a file runs unreviewed. A record
  whose pasted file is gone is dropped.
- **Remove (`ctrl+d`, with a danger confirm):** a `pasted` entry deletes its saved
  file, which removes every context from that paste (the confirm lists them);
  a `file` entry forgets the remembered path and leaves the file alone; a
  `kubeconfig` entry cannot be removed (toast: it comes from your kubeconfig, which
  kubetui does not edit).
- **Switching** to the connected entry is a no-op toast. See Switching below for
  what happens to the current cluster while the new one connects.
- **Connect failure** (`SessionError`) leaves the picker open with the error shown
  on that row or in a banner; the entry stays.
- **`:ctx <name>`** stays (the README documents it). The name is matched exactly
  against each entry's display name (a pasted entry's saved name, `name / context`,
  or a context name). Exactly one match switches to it; several matches open the
  picker with the cursor on the first; none gives a toast (my call).
- Reading the default kubeconfig and remembered files happens in a `tea.Cmd` when
  the picker opens, never in `Update`.
- At launch, before any session exists, only picker keys, `?` and `q` work; `:`,
  `/` and digit jumps need a cluster.

### Switching

My call, so a failed or slow switch never leaves the user without a cluster:

- **The current session stays up until the new one is ready.** On a pick, kubetui
  builds the new Session in a `tea.Cmd` while the current one keeps running. This is
  cheap: `NewSession` does no network IO and the new engine starts no informers
  until a page asks, so the old engine is the only one sending snapshots. The only
  slow step is the version probe (up to 8 s).
- **On `SessionReady`:** dispose the old session (closing its port-forwards), clear
  the page stack and the header's metrics gauges, swap in the new session, and
  restore that cluster's namespace and view. Snapshots that arrive late from the
  disposed engine are ignored.
- **On `SessionError`:** the current cluster is untouched; the picker stays open with
  the error on that row.
- **Port-forwards:** picking a cluster while forwards are open asks first: "Switching
  closes N port-forwards." They close only when the switch succeeds.
- **While connecting,** the picker stays usable: the row shows "connecting…", and
  picking another entry cancels the connect in flight (each bootstrap carries a
  token and a cancellable context; a result with a stale token is disposed). esc
  closes the picker and cancels the connect, staying on the current cluster. At
  launch there is no current cluster: the picker shows the same "connecting…" row,
  and `q` quits.

### The paste box

A root-level modal (like `m.prompt`, checked first in `handleKey` and
`handlePaste`), not a page: pages only see keys after the global ones (`q`, `:`,
`/`, digits, esc), and base64 almost always contains a `q`.

```
 kubetui · paste a kubeconfig

   Paste it now (ctrl+shift+v or right-click in most terminals).

   received 142 lines, 5.8 KB
   contexts:  › dev-tokyo   https://34.84.10.2
                staging     https://staging.acme.io

 ctrl+s save · esc cancel
```

- **Input:** a `tea.PasteMsg` is taken whole. Key presses are accepted too, for
  terminals that type a paste in: enter and ctrl+j insert a newline, tab inserts two
  spaces, printable text is appended; built with a `strings.Builder` (a 10 KB paste
  is ~10 K key messages). Never pass the text through `singleLine`. CR and CRLF are
  normalised to LF and a UTF-8 BOM is dropped before parsing.
- **Never echo the text.** The box shows line and byte counts, the contexts found
  with their server URLs, and any problem. It parses after each paste and on
  ctrl+s, not on every key.
- **ctrl+s** saves (it never appears in pasted text); **esc** cancels, with a
  confirm if something was received. Choosing a context in the list picks which
  one to connect to after saving (default: the paste's current-context, else the
  first).
- **Name:** on ctrl+s, the existing prompt asks for a name, prefilled with the
  chosen context's name. This is the label in the picker; it also tells apart the
  many pastes that cloud consoles name `default` or `kubernetes-admin`.
- **Where paste arrives as keystrokes and is unreliable** (the classic Windows
  console), the user can save the text to a file and use `f` in the picker.

### Checks before saving

In order; the first two are errors, the rest are shown in the box:

1. `clientcmd.Load` succeeds and the result is not empty (`api.IsConfigEmpty`), and
   it has at least one context. Otherwise: "this doesn't look like a kubeconfig"
   with the parse error. (A pasted Secret manifest fails here; a YAML document
   without `kind` loads as empty.)
2. **No relative file references.** `certificate-authority`, `client-certificate`,
   `client-key`, `tokenFile`, and an `exec.command` containing a path separator
   would resolve against kubetui's `clusters/` folder once saved and break. Reject
   with a hint to export a self-contained copy:
   `kubectl config view --raw --flatten --minify --context <name>`.
3. **Review, shown before saving when present** (control characters stripped from
   everything displayed, so a paste cannot fake the dialog):
   - each `exec` login command with its arguments and env var names, and
     `interactiveMode`. kubetui runs this command every time it connects to that
     cluster. Env values are always masked. An argument's value is masked when
     its flag name contains `secret`, `token`, `password` or `key`, in both
     `--flag=value` and `--flag value` forms (e.g. kubelogin's
     `--oidc-client-secret=…`); the command and every other argument are shown in
     full, so the user still sees what will run (my call);
   - each `auth-provider` (in client-go v0.36 only `oidc` works; it contacts the
     issuer URL and writes refreshed tokens back into the saved file);
   - each absolute local file path the config reads (its contents are sent to the
     pasted server: a `tokenFile` is sent as a bearer token);
   - `insecure-skip-tls-verify: true`.

   With any of these, ctrl+s means "I've read this" and the footer says so; with
   none, ctrl+s saves directly.
4. `ConfirmUsable` per context; failures are listed next to the context, not
   blocking (a paste may hold one good context and one stale one).
5. **Exec allowlist:** the approved exec commands are stored with the entry, and
   when connecting to a `pasted` entry kubetui sets the client-go exec plugin policy
   (`ExecConfig.PluginPolicy`, not settable from a kubeconfig) to allow only those
   commands. If the saved file were later edited to name another command, it would
   be refused. `file` and `kubeconfig` entries are the user's own files and are not
   reviewed, as today.

### Storage

All under `config.Dir()` = the directory of `config.Path()`:

```
~/.config/kubetui/            (%USERPROFILE%\.config\kubetui\ on Windows)
  config.yaml                 user's settings, unchanged, still only read
  state.yaml                  cluster list and per-cluster state
  clusters/
    .gitignore                "*" — many people keep ~/.config in a dotfiles repo
    dev-tokyo.yaml            one file per paste, saved whole
```

- **Saved whole, one file per paste, never merged.** Each file is loaded alone
  (`ExplicitPath`), so names repeated across pastes never clash. Save the validated
  bytes (normalised line endings), not a re-serialisation: `clientcmd.Write` drops
  comments and unknown fields and adds defaults. Do not use `FlattenConfig`.
- **File name:** derived from the entry name, made safe for every OS (reuse the
  Windows-safe naming from `internal/action/cp/winname.go`), with a suffix on
  collision. The listing ignores temp files and client-go's `*.lock` files (oidc
  refresh writes go through client-go's lock).
- **Permissions:** `clusters/` is created and explicitly chmod-ed to 0700, files
  0600 (on Unix a pre-existing directory or file is not tightened by `MkdirAll` /
  `WriteFile`, so chmod explicitly). On Windows the permission bits only set the
  read-only attribute, which must never be set (it breaks rename and delete);
  protection comes from the user profile's default ACL. If `XDG_CONFIG_HOME` points
  outside the profile on Windows, that protection may not hold; document it.
- **Atomic writes:** temp file in the same directory → write → fsync → close →
  rename. On Windows a rename fails while another process (a second kubetui,
  antivirus, the indexer) holds the target open; retry a few times with a short
  backoff.
- **No usable config directory** (no home directory, or a relative
  `XDG_CONFIG_HOME`): the picker still lists `kubeconfig` contexts; paste, load-file
  and state are off, with a one-line explanation in the picker.

#### `state.yaml`

```yaml
version: 1
clusters:
  - source: pasted            # pasted | file | kubeconfig | in-cluster
    file: dev-tokyo.yaml      # pasted: name inside clusters/
    name: dev-tokyo           # pasted: label shown in the picker
    context: admin
    lastUsed: 2026-11-26T10:31:00Z
    namespace: payments       # "" = all namespaces
    kind: deployments         # canonical first-class kind name
    approvedExec: [aws]       # pasted: exec commands approved at save time
  - source: file
    path: /Users/me/work/ci.kubeconfig
    context: ci
    lastUsed: 2026-11-21T08:02:00Z
    namespace: ""
    kind: pods
```

- No credentials in `state.yaml`; it holds paths, names, positions and exec
  approvals.
- `state.yaml` is the record of which pasted files were reviewed. Saving a paste
  writes the cluster file first, then the state; a crash between the two leaves an
  unreviewed file, which the picker marks "needs review" (see The cluster picker).
- A missing or unreadable `state.yaml` is not an error (like `config.yaml`); a
  malformed one is renamed aside (`state.yaml.bad`) and a fresh one started, so a
  bad file never blocks launch. Pasted files then need review once more, which is
  the safe direction: nothing is approved by losing the state.
- **Two kubetui instances:** each save re-reads the file and updates only its own
  entry before writing (read-modify-write per entry; last writer wins per entry).

### Per-cluster namespace and view

- **Record** in `navigate(kind, ns)` (every namespace and base-kind change passes
  through it): the `kind` argument (canonical name; several base pages return an
  empty `Kind()`), and the namespace. Also record on `SessionReady` (last-used).
- **Write** from a debounced `tea.Cmd` (no IO in `Update`; debounce with a token
  like `toastToken`), plus one final flush in `main` after `program.Run` returns and
  before `os.Exit`. That covers `q`, `:q`, ctrl+c, SIGINT/Ctrl+Break and SIGTERM
  (including Windows console close). It does not cover SIGHUP (closing the terminal
  window on macOS/Linux) or exits during a pod shell; the debounced writes have
  already saved the state by then.
- **Restore** in `onSessionReady`:
  - kind: an explicit `--kind` wins for the first page of the run; otherwise the
    remembered kind if it is still a known first-class kind (`ResolveKind`); else
    pods.
  - namespace: remembered, else the context's namespace (correctly derived, bug 1),
    else all namespaces.
  - A remembered namespace that was deleted shows an empty table; one the user can
    no longer list shows the existing "forbidden" banner, from which digits and `:`
    still work. Both are acceptable; no special handling.
- The auth-banner retry (`r`) and every switch go through `onSessionReady`, so they
  now return to the remembered position instead of `StartKind` (today they reset).
- Recorded as-is: `:deploy` without a namespace means all namespaces today, so the
  remembered namespace becomes "all". Unchanged behaviour.

## Text to update

- README:
  - "Usage": plain `kubetui` opens the picker; `--kubeconfig` connects directly and
    is remembered.
  - Flags table: `-kubeconfig`, `-context`.
  - "Namespaces & contexts" and the command table: `:ctx` lists all saved clusters.
  - Configuration: the `clusters/` folder and `state.yaml`.
  - Resolution order bullet.
  - Windows notes: the kubetui folder location, ACL note, paste in the classic
    console (use `f`, or Windows Terminal).
  - Notes: replace "kubeconfigs and secrets are never written to disk by kubetui"
    with what is true: pasted kubeconfigs are saved under `clusters/`, readable only
    by you, like `~/.kube/config`; kubetui never writes your own kubeconfig files
    (client-go's oidc provider refreshing tokens excepted); Secret values are never
    written to disk.
- `render.go` dead-end screen text (now unreachable for connect errors).
- `palette.go` `:ctx` description; help overlay entries for the picker keys.
- `docs/ARCHITECTURE.md` session and config sections.

## Testing

- **Store** (new package, temp dirs): save/list/remove, name collisions, ignoring
  `.lock`/temp files, permissions on Unix, read-modify-write with two writers,
  malformed `state.yaml` set aside, no config dir.
- **Checks:** table-driven over the cases found while mapping: a Secret manifest,
  YAML without `kind`, multi-document, tab indentation, CRLF, BOM, relative paths,
  exec with and without `interactiveMode`, auth-provider, `tokenFile`,
  `insecure-skip-tls-verify`, control characters in exec args, and a secret in exec
  args (`--oidc-client-secret=…` and `--token …`) that must not appear in the
  review's rendered text.
- **Unreviewed files:** a `clusters/` file with no state record is listed as needing
  review and its exec command is not allowed until approved.
- **Paste box:** a `PasteMsg` arrives intact; keystroke input (enter, ctrl+j, tab)
  builds the same text; the rendered box never contains any of the pasted text
  (assert no token/key substring appears in `View()`); ctrl+s and esc paths.
- **Picker:** render smoke test with no session (add to `allPages` and the booting
  size sweep); sort order; missing file; remove confirm per source.
- **App:** launch branch per flag combination; switch with a real `NewSession` on a
  temp kubeconfig (works offline); the old session stays up until the new one is
  ready, and a failed switch leaves it untouched; stale bootstrap results are
  disposed; a pick during a connect cancels it; `:ctx <name>` with one, several and
  no matches; restore precedence (`--kind`, remembered, fallback).
- **Bug fixes:** identity after a context override; `Dispose` nil-safety.
- Windows: CI already runs the suite on windows-latest; the classic-console
  keystroke path needs a manual check.

## Calls made without the user

The user chose option 1 for storage and confirmed the overall behaviour (paste,
save, picker at launch, switching across all clusters, per-cluster namespace and
view). These were decided while writing the design and are open to correction:

- The picker shows at every launch without `--kubeconfig`/`--context`, even with one
  entry; a set `$KUBECONFIG` does not skip it.
- `ctrl+d` removes an entry (kubetui's delete key), not `d` (describe).
- `f` remembers a file's path like `--kubeconfig`; it does not copy the file.
- A paste is named at save time, prefilled with its context name.
- A switch keeps the current cluster until the new one is ready.
- `:ctx <name>` is kept with exact-match resolution.
- Secret-looking exec arguments and all env values are masked in the review.
- Files in `clusters/` without a state record need review before they connect.
- An `in-cluster` entry appears when kubetui runs in a pod with no kubeconfig.

## Left to the builder

- Package layout (e.g. `internal/clusters` for entries, store and checks).
- Exact column widths, truncation and the picker's empty and error rendering.
- Whether the paste box uses `charm.land/bubbles/v2` textarea or a small
  hand-rolled buffer. If textarea: it turns each `\r` and `\n` into a newline
  (CRLF doubles lines), blocks enter after 99 lines unless the height limits are
  set, binds ctrl+d and ctrl+v, and pulls in a new clipboard dependency. A
  hand-rolled buffer is likely simpler since the box mostly receives one paste and
  never displays it.
- How the commits are split, as long as each builds.
