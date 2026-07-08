// Command kubetui is a full-screen terminal UI for managing and monitoring
// Kubernetes clusters.
package main

import (
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
	kindFlag := flag.String("kind", "overview", "initial view (overview, pods, deploy, …)")
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

	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "kubetui:", err)
		os.Exit(1)
	}
}
