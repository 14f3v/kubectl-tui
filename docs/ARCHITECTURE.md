# kubetui — Architecture

A full-screen terminal UI for managing and monitoring Kubernetes clusters, in the
spirit of k9s, built with Go + Bubble Tea v2. This document is the design of record;
it was selected from a judged panel of three independent architecture proposals
("informer-first" won), with grafts from the runners-up (per-kind informers, the
TerminalGate TTY arbiter, the log-stream backpressure ring, the port-forward state
machine, and time-driven quota refresh).

## Layers

```
cmd/kubetui            flags, Session bootstrap, tea.NewProgram, engine→p.Send wiring
   │
internal/k8s.Session ◄─── sink func(tea.Msg) = p.Send ──► internal/app  (root tea.Model)
   │  rest.Config, clients, PF manager, engine              page stack, dialog stack,
   │                                                        ":" routing, chrome, recover()
   ├─ internal/engine        per-kind informers → ViewStore[T] → coalescer
   ├─ internal/action        TerminalGate, execshell, editor, logstream, portfwd, write, inspect
   │
   └────────────────────────────────► internal/view (pages) · internal/component · internal/style
```

**Data flow, watch event → rendered cell.** API watch event → informer Reflector →
indexer upsert → event handler sets the ViewStore's dirty flag → a per-store coalescer
(≤1 flush / 150 ms) projects the indexer through the kind's column registry into
`[]Row{Cells, SortKeys, StatusClass, UID}` → emits **one** `SnapshotMsg{Kind, ViewID, Remote[Row]}`
via `p.Send` → the root `Update` drops it if the `ViewID` is stale, otherwise routes it to
the active page → the page stores rows and applies sort/filter → a windowed table renders
only the visible rows (per-row render cache) → the root composes chrome + table into the view.

**Writes never touch this path.** An action `tea.Cmd` performs the API call; on failure it
emits a toast; on success it does nothing — the subsequent watch event updates the row.
This is the "fire-and-observe" rule borrowed from the sibling Flutter app.

## Key decisions

1. **Informers are the single data plane, one per kind.** The client-go `Reflector` already
   implements 410-relist, watch bookmarks, jittered backoff, and cache persistence across
   reconnects. We use an individual `cache.NewSharedIndexInformer` per kind, each with its own
   `stopCh` — not a `SharedInformerFactory`, which cannot stop a single informer. A `403` on
   pods closes exactly the pods informer; a namespace change restarts only that kind.
2. **Lazy-start, keep-warm core kinds.** Pods/deployments/services/nodes/namespaces informers
   start on first view and stay warm for the session (instant view switches; a laptop has no
   battery constraint). Events and Tenants are strictly screen-scoped (events capped at 1000
   items newest-first; tenants is a dynamic informer).
3. **Coalesced snapshots via `p.Send`.** Informer handlers never talk to Bubble Tea; they set a
   dirty flag; a 150 ms per-store flusher emits one pre-projected snapshot per burst. The UI
   never sees per-object events — this is the render-thrash answer.
4. **Column registry over server-side Table protocol.** A typed object → typed cells + sort keys
   + status class gives correct numeric/age sort, status colors, and action targets. The
   server-side Table protocol is the documented future path for a generic CRD browser, not v1.
5. **Hand-rolled windowed table.** `bubbles/table` cannot do per-cell styling or typed sort. We
   render only the visible window with a per-row cache keyed by (UID, data version, width,
   selected). All width math uses `x/ansi`, never `len()`.
6. **`Remote[T]` envelope + sealed error taxonomy.** Rows are never cleared on failure; `Stale`
   keeps rendering; `Terminal` (401/403/TLS/exec-plugin failure) stops retrying and surfaces
   actionably.
7. **Fire-and-observe writes.** Write commands never mutate read caches; the watch event does.
   Failures emit an `ActionResultMsg` toast. `readOnly` config disables the write package.
8. **TerminalGate arbitrates all TTY handoffs.** One state machine
   (`TUIOwned → HandingOff → ChildOwned → Restoring`) wraps every `tea.Exec`/`tea.ExecProcess`;
   concurrent attempts are rejected with a toast; the engine coalescer pauses while a child owns
   the terminal and flushes exactly one fresh snapshot per active view on restore.
9. **Session-per-context, dispose-don't-mutate.** A context switch cancels the old Session's root
   context (all informers, log streams, and forwards die) and builds a fresh one. There is no
   discovery disk cache keyed by server URL (which poisons Teleport).
10. **Capsule via dynamic client + hand-decoded struct.** We never import the Capsule Go module
    (it drags controller-runtime); we decode ~10 unstructured fields, which tolerates version
    skew. Quota aggregation is time-driven, not tenant-event-driven, because `ResourceQuota`
    `status.used` changes emit no `Tenant` events.
11. **Theme built once.** All lipgloss styles are constructed on `tea.BackgroundColorMsg`, never
    in a render loop.
12. **Engine tests are the spec.** The sibling's numbered watch-engine T-cases run against the
    ViewStore layer with a fake clientset; pages get golden tests across state × density.

## Package map

| Package | Responsibility |
|---|---|
| `cmd/kubetui` | flag parsing, Session bootstrap, `tea.NewProgram`, wiring the engine sink to `p.Send` |
| `internal/app` | root Bubble Tea model: page stack, dialog overlay stack, `:` routing, chrome, global keys, `recover()` |
| `internal/view` | one file per page + the `Page` interface and alias registry |
| `internal/component` | hand-rolled windowed table, command line, filter input, confirm dialog, gauges, help grid, toast |
| `internal/style` | the `Theme` struct, built once from palette tokens; per-status styles; density |
| `internal/engine` | per-kind informer engine: `ViewStore[T]`, coalescer, error taxonomy, `Remote[T]` |
| `internal/engine/columns` | per-kind column registries (typed object → `Row`) |
| `internal/k8s` | kubeconfig loading, context enumeration/switch, client factories, `Session` |
| `internal/metrics` | metrics.k8s.io availability prober + 15 s poller |
| `internal/tenant` | `TenantProvider` interface + Capsule implementation |
| `internal/action` | `TerminalGate` + `execshell`, `editor`, `portfwd`, `logstream`, `write`, `inspect` |
| `internal/msg` | shared `tea.Msg` types |
| `internal/config` | `~/.config/kubetui/config.yaml` |

**Rule:** pages are the only `tea.Msg` speakers; components are plain structs with imperative
methods. No IO in `Update`; all IO is a `tea.Cmd` or lives in an engine/action goroutine.

## Implementation phases

1. **Scaffold & risk spikes** — repo, root model, Theme, panic recovery, Session; throwaway
   spikes for exec-plugin auth, TTY handoff, and informer-on-403.
2. **Pods read-only** — engine, pods columns, windowed table, chrome, filter/sort, namespace scope.
3. **Command line & sibling kinds** — `:` grammar, deployments/services/nodes/namespaces/events,
   containers drill-in, `:ctx` swap, help overlay.
4. **Inspect actions** — describe/yaml textview, logstream with backpressure.
5. **Mutating actions** — TerminalGate, confirm dialogs, delete/kill, edit, exec shell, port-forward.
6. **Metrics** — prober, poller, render-time join, graceful absence.
7. **Tenants** — Capsule `TenantProvider`, quota aggregation, tenants columns, degradation ladder.
8. **Polish & release** — config, golden matrix, teatest flows, goreleaser.

See the full plan (with verification steps and the technical-facts appendix) in the design spec.
