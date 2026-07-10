# docs/demo.tape — reproducible kubetui screenshots + demo GIF via VHS.
#
#   brew install vhs
#   vhs docs/demo.tape        # run from the repo root
#
# CLUSTER: kubetui connects to your CURRENT kube-context, and VHS runs in its own
# shell — so it needs a REACHABLE cluster with data. Easiest for a public README is
# a throwaway local cluster seeded with generic workloads (see docs/demo-fixtures.yaml):
#
#   brew install kind
#   kind create cluster --name kubetui-demo
#   kubectl apply -f docs/demo-fixtures.yaml
#   kubectl -n demo rollout status deploy/web
#   vhs docs/demo.tape
#   kind delete cluster --name kubetui-demo
#
# To record against another cluster instead, export its kubeconfig when invoking vhs:
#   KUBECONFIG=/path/to/kubeconfig vhs docs/demo.tape
# or add flags to the launch line below (kept out of git):
#   Type "./kubetui -kubeconfig /path/to/kubeconfig -context my-cluster"
#
# The tape builds kubetui from source (hidden) and runs the local ./kubetui, so
# it does NOT need to be installed on your PATH. If you'd rather use an installed
# binary (e.g. `brew install 14f3v/tap/kubetui`), replace the two build lines
# below with a single:  Type "kubetui"
#
# Produces docs/screenshots/{pods,deployments,filter}.png and demo.gif.

Require go

Output docs/screenshots/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1280
Set Height 720
Set Padding 24
Set Theme "Catppuccin Mocha"

# Build from source and launch — hidden, so the GIF starts on the running UI.
# Incremental builds take a few seconds; bump the Sleep if your first build is cold.
Hide
Type "go build -o kubetui ./cmd/kubetui"
Enter
Sleep 20s
Type "clear"
Enter
Sleep 500ms
Type "./kubetui"
Enter
Sleep 4s
Show

# Pods across all namespaces (press 0) so the table has content.
Type "0"
Sleep 1500ms
Screenshot docs/screenshots/pods.png

# Deployments.
Type ":deploy"
Enter
Sleep 2s
Screenshot docs/screenshots/deployments.png

# Multi-term filter with the live suggestion dropdown.
Type "/"
Sleep 500ms
Type "web"
Sleep 1500ms
Screenshot docs/screenshots/filter.png
Escape

# Quit cleanly.
Sleep 1s
Type "q"
