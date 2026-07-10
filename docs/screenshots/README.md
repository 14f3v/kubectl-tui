# Screenshots

These images are generated reproducibly with [VHS](https://github.com/charmbracelet/vhs)
from [`../demo.tape`](../demo.tape) — don't hand-capture them:

```bash
brew install vhs
vhs docs/demo.tape
```

That writes `pods.png`, `deployments.png`, `filter.png`, and `demo.gif` here.
Point kubetui at a demo/dev cluster with representative resources first so no real
names appear, then commit the generated files. Re-run the tape whenever the UI
changes to keep the README images current.
