# docs/demo.tape — reproducible kubetui screenshots + demo GIF via VHS.
#
#   brew install vhs
#   vhs docs/demo.tape        # run from the repo root, with KUBECONFIG pointed
#                             # at a demo/dev cluster that has real resources
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

# Pods.
Sleep 1s
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
