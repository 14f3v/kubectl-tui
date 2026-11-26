// Command kubetui is a full-screen terminal UI for managing and monitoring
// Kubernetes clusters.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/app"
	"github.com/14f3v/kubectl-tui/internal/config"
)

// version is stamped at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	kubeconfigFlag := flag.String("kubeconfig", "", "path to a kubeconfig file (overrides $KUBECONFIG and ~/.kube/config)")
	ctxFlag := flag.String("context", "", "kubeconfig context to use (default: current-context)")
	kindFlag := flag.String("kind", "pods", "initial resource view")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("kubetui", version)
		return
	}

	app.Version = version
	cfg := config.Load()

	// The engine's snapshot sink is tea.Program.Send, but the program is built
	// from the model, which needs the sink first. Break the cycle with a closure
	// over a program variable that is assigned before Run starts delivering.
	var program *tea.Program
	sink := func(m tea.Msg) {
		if program != nil {
			program.Send(m)
		}
	}

	model := app.New(app.Config{
		Sink:            sink,
		Config:          cfg,
		KubeconfigPath:  *kubeconfigFlag,
		ContextOverride: *ctxFlag,
		StartKind:       *kindFlag,
	})
	program = tea.NewProgram(model)

	_, err := program.Run()
	code, report := exitStatus(err)
	if report {
		fmt.Fprintln(os.Stderr, "kubetui:", err)
	}
	if code != 0 {
		os.Exit(code)
	}
}

// exitStatus maps the error from Program.Run to the process exit code and
// whether the error is worth printing.
//
// An interrupt is a request to stop, not a failure. Bubble Tea ends Run with
// tea.ErrInterrupted when the process receives os.Interrupt: a kill -INT on Unix,
// or Ctrl+Break on Windows, which the Go runtime delivers as SIGINT (Ctrl+C is
// read as a key there, as it is in raw mode on Unix). The terminal is already
// restored by then, so exit quietly with 130, the shell convention for SIGINT,
// which still tells a script that this was not a normal quit. Run wraps the
// error (ErrProgramKilled: ErrInterrupted), so it is matched with errors.Is.
func exitStatus(err error) (code int, report bool) {
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, tea.ErrInterrupted):
		return 130, false
	default:
		return 1, true
	}
}
