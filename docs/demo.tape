# docs/demo.tape — reproducible kubetui screenshots + demo GIF via VHS.
#
#   brew install vhs
#   vhs docs/demo.tape        # point kubetui at a demo/dev cluster with real data
#
# Produces:
#   docs/screenshots/pods.png
#   docs/screenshots/deployments.png
#   docs/screenshots/filter.png
#   docs/screenshots/demo.gif   (the whole session)
#
# Tweak the terminal size/theme and the keystrokes below to taste. Use a demo or
# dev cluster so no real resource names end up in the images.

Require kubetui

Output docs/screenshots/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1280
Set Height 720
Set Padding 24
Set Theme "Catppuccin Mocha"

# Launch kubetui and let it connect + paint (hide the typing of the command).
Hide
Type "kubetui"
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

Sleep 1s
