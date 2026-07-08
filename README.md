# kubetui

A full-screen terminal UI for managing and monitoring Kubernetes clusters, in the
spirit of [k9s](https://k9scli.io/). Built with Go and [Bubble Tea](https://github.com/charmbracelet/bubbletea).

`kubetui` browses pods, deployments, services, nodes, namespaces, and events through
a `:`-command line, streams live watch updates, and runs the usual pod actions
(describe, logs, exec shell, edit, delete, port-forward). It also renders a
[Capsule](https://projectcapsule.dev/) multi-tenancy dashboard when the operator is installed.

> Status: **v1 feature-complete** — read/inspect/mutate across the core kinds, metrics, and
> Capsule tenants are implemented and unit-tested. The cluster-dependent action paths
> (exec, logs, port-forward, edit) are exercised by the `hack/` risk spikes and should be
> validated against your own cluster. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

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

## Selecting a cluster

`kubetui` resolves its kubeconfig the same way `kubectl` does, in precedence order:

```sh
./kubetui -kubeconfig /path/to/config      # explicit file (highest precedence)
KUBECONFIG=/path/to/config ./kubetui       # env var (colon-separated files are merged)
KUBECONFIG=/a/config:/b/config ./kubetui   # merge multiple files
./kubetui                                  # default: ~/.kube/config
```

Pick a context within whatever config is loaded (defaults to its current-context),
and combine with `-kubeconfig`:

```sh
./kubetui -context prod-us-east-1
./kubetui -kubeconfig ~/work.kubeconfig -context staging
```

You can also switch contexts live from inside the TUI with `:ctx <name>`.

## Usage

`kubetui` opens on the **overview dashboard** — cluster KPIs (nodes ready, pods
running, tenants, alerts), capacity bars, a node summary, pod-phase breakdown,
workload readiness, top CPU consumers, and recent events. From there, `p`/`t`/`n`/`e`
jump to pods/tenants/nodes/events, or use the command line. (`--kind pods` opens
straight on a resource list instead.)

| Key | Action |
|-----|--------|
| `:` | command line — `:overview` `:pods` `:deploy` `:svc` `:nodes` `:ns` `:events` `:tenants` `:pf`; `:ctx <name>` switch context; `:q` quit |
| `/` | filter rows (`!term` to invert) |
| `j`/`k`, `↑`/`↓`, `g`/`G` | move the cursor / top / bottom |
| `enter` | drill in (pod → containers → logs/shell) |
| `d` `y` | describe · yaml (scrollable, `/` to search) |
| `l` `s` | logs (follow, `f` to pause) · shell (interactive) |
| `e` | edit in `$EDITOR`, applied with server-side apply |
| `ctrl-d` / `ctrl-k` | delete · kill (force); both confirm |
| `p` | port-forward the pod's container ports (see `:pf`) |
| `0`–`9` | jump to a namespace (`0` = all, `1`–`9` = favorites) |
| `?` | help · `esc` back · `q` quit |

The header shows cluster CPU/MEM gauges and per-pod CPU/MEM columns when
`metrics-server` is present, and hides them otherwise. `:tenants` renders a
[Capsule](https://projectcapsule.dev/) dashboard (tier, quota bars, owner, status)
when the operator is installed.

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
