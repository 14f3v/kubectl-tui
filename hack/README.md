# Risk spikes

Throwaway diagnostic programs that de-risk the three failure-prone subsystems
before the real code is built on top of them (Phase 1 of the plan). Each is a
standalone `main` you run against a real cluster — ideally a Teleport-proxied one
and a local `kind` cluster.

| Spike | De-risks | Run |
|-------|----------|-----|
| `spike-auth` | kubeconfig loading + exec credential plugins (tsh/aws/gke), identity, RBAC probe | `go run ./hack/spike-auth -context <ctx>` |
| `spike-informer` | watch engine error handling; 403 → terminal, no retry | `go run ./hack/spike-informer -context <ctx> -n <ns>` |
| `spike-tty` | interactive exec: WebSocket→SPDY fallback, raw-terminal handoff, SIGWINCH resize | `go run ./hack/spike-tty -context <ctx> -n <ns> -pod <pod>` |

## What to verify

- **spike-auth** against a Teleport context: identity prints, `auth: exec plugin`
  is reported, `/version` and the nodes probe succeed. Let the `tsh` cert expire,
  re-run: the exec plugin should re-mint transparently, or the error should be
  clean and actionable (never a hang or a prompt swallowed by the UI).
- **spike-informer** against a namespace you *can* access: adds stream in, cache
  syncs. Against a *forbidden* namespace: a single `forbidden (terminal)` line
  prints and the informer stops — it does not retry forever.
- **spike-tty** against a `kind` pod and a Teleport pod: the shell takes over the
  terminal, `resize` reflows the child, and on exit the terminal is fully
  restored (test in iTerm2, tmux, and Terminal.app).

These are not part of the shipped binary; they exist to validate assumptions.
