# kubetui

A full-screen terminal UI for managing and monitoring Kubernetes clusters, in the
spirit of [k9s](https://k9scli.io/). Built with Go and [Bubble Tea](https://github.com/charmbracelet/bubbletea).

`kubetui` browses pods, deployments, services, nodes, namespaces, and events through
a `:`-command line, streams live watch updates, and runs the usual pod actions
(describe, logs, exec shell, edit, delete, port-forward). It also renders a
[Capsule](https://projectcapsule.dev/) multi-tenancy dashboard when the operator is installed.

> Status: **early development.** See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the design and phase plan.

## Highlights

- **Live views** driven by client-go informers — one per resource kind, each independently
  restartable so an RBAC `403` on one kind degrades only that view.
- **Resilient reads** — the rendered table is never cleared on a network blip; it is marked
  stale and keeps rendering while the watch reconnects. Terminal errors (`401`/`403`/TLS)
  stop retrying and surface an actionable message.
- **Fire-and-observe writes** — a mutation never edits the local cache; the subsequent watch
  event does. A failed write shows a toast and leaves the read state untouched.
- **Exec-plugin auth** — standard kubeconfig loading, so Teleport (`tsh kube login`),
  `aws eks get-token`, and `gke-gcloud-auth-plugin` contexts work transparently.
- **Optional metrics** — CPU/MEM columns and header gauges appear when `metrics-server`
  is present and vanish gracefully when it is not.

## Install

Requires Go 1.26+.

```sh
go build -o kubetui ./cmd/kubetui
./kubetui            # uses your current kubeconfig context
```

## Usage

`kubetui` opens on the pods view for your current context.

| Key | Action |
|-----|--------|
| `:` | command line (`:pods`, `:deploy`, `:svc`, `:nodes`, `:ns`, `:events`, `:tenants`, `:ctx`, `:pf`, `:q`) |
| `/` | filter rows (`/regex…` for regex, `!term` to invert) |
| `j`/`k`, `↑`/`↓` | move the cursor |
| `enter` | drill in (pod → containers) |
| `d` `l` `s` `y` `e` | describe · logs · shell · yaml · edit |
| `ctrl-d` / `ctrl-k` | delete · kill (force) |
| `p` | port-forward |
| `0`–`9` | jump to a namespace |
| `?` | help · `esc` back · `q` quit |

## Configuration

Optional `~/.config/kubetui/config.yaml`:

```yaml
accent: "#6366F1"   # or a preset name: indigo | green | teal | pink
density: comfortable # comfortable | compact
readOnly: false      # true disables all mutating actions
tierLabel: tier      # tenant label key used for the TIER column
favorites: [default, kube-system]
```

## License

TBD.
