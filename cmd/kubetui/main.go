// Command kubetui is a full-screen terminal UI for managing and monitoring
// Kubernetes clusters.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/app"
	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/config"
	"github.com/14f3v/kubectl-tui/internal/k8s"
)

// version is stamped at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	cfg, err := launchConfig(os.Args[1:], os.Getwd)
	switch {
	case errors.Is(err, errVersion):
		fmt.Println("kubetui", version)
		return
	case errors.Is(err, flag.ErrHelp):
		return // -h: the usage is printed
	case err != nil:
		os.Exit(2) // the flag set has printed the error and the usage
	}

	app.Version = version
	cfg.Config = config.Load()
	// Without a usable config folder the picker still lists the kubeconfig's
	// contexts; paste, load-file and the saved state are off, and say why.
	cfg.Store, err = clusters.Open(config.Dir())
	if err != nil {
		cfg.StoreErr = err.Error()
	}

	// The engine's snapshot sink is tea.Program.Send, but the program is built
	// from the model, which needs the sink first. Break the cycle with a closure
	// over a program variable that is assigned before Run starts delivering.
	var program *tea.Program
	cfg.Sink = func(m tea.Msg) {
		if program != nil {
			program.Send(m)
		}
	}

	program = tea.NewProgram(app.New(cfg))

	_, err = program.Run()
	code, report := exitStatus(err)
	if report {
		fmt.Fprintln(os.Stderr, "kubetui:", err)
	}
	if code != 0 {
		os.Exit(code)
	}
}

// errVersion is launchConfig's answer to -version, which asks for the version
// instead of a launch, as flag.ErrHelp is its answer to -h.
var errVersion = errors.New("version requested")

// launchConfig maps the command line to how kubetui starts. A flag that names a
// cluster connects to it directly: -kubeconfig a file (remembered, so it is made
// absolute against getwd), -context a context of it or of the default
// kubeconfig. With neither, kubetui opens the cluster picker, even with
// $KUBECONFIG set: it names files, often several clusters' worth, and decides
// only which rows the picker lists.
//
// The string flags default to "", so an explicit -kind pods is told apart from
// no -kind, and an empty value names nothing. Errors come from the flag set,
// which has printed them with the usage; errVersion and flag.ErrHelp are
// requests, not failures.
func launchConfig(args []string, getwd func() (string, error)) (app.Config, error) {
	fs := flag.NewFlagSet("kubetui", flag.ContinueOnError)
	var kubeconfig string
	// Resolving the path in the flag's own Set makes a failed getwd a usage
	// error like any other bad value.
	fs.Func("kubeconfig", "connect to the kubeconfig file at `path` (overrides $KUBECONFIG and ~/.kube/config); the cluster picker remembers it",
		func(path string) (err error) {
			kubeconfig, err = absPath(path, getwd)
			return err
		})
	contextName := fs.String("context", "", "connect to this context (default: the -kubeconfig file's current-context)")
	kind := fs.String("kind", "", "initial resource view (default pods)")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return app.Config{}, err
	}
	if *showVersion {
		return app.Config{}, errVersion
	}

	cfg := app.Config{Launch: app.LaunchPicker, StartKind: *kind, StartKindExplicit: *kind != ""}
	switch {
	case kubeconfig != "":
		cfg.Launch, cfg.Start = app.LaunchDirect, fileRef(kubeconfig, *contextName)
	case *contextName != "":
		cfg.Launch, cfg.Start = app.LaunchDirect, k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: *contextName}
	}
	return cfg, nil
}

// absPath is filepath.Abs against the working directory getwd reports; "" stays
// "". On Windows, \dir is on the current drive and D:dir is relative to D:'s own
// working directory, which only the OS keeps, so both are left to filepath.Abs.
func absPath(path string, getwd func() (string, error)) (string, error) {
	switch {
	case path == "":
		return "", nil
	case filepath.IsAbs(path):
		return filepath.Clean(path), nil
	case filepath.VolumeName(path) != "" || os.IsPathSeparator(path[0]):
		return filepath.Abs(path)
	}
	wd, err := getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, path), nil
}

// fileRef names the cluster to connect to in the kubeconfig file at path, which
// is absolute. Without a context it pins the file's current-context now, so a
// `kubectl config use-context` run later cannot move a reconnect to another
// cluster. When there is no current context to pin (a missing or unreadable
// file), the context stays empty and the connect reports the problem.
func fileRef(path, contextName string) k8s.ClusterRef {
	ref := k8s.ClusterRef{Source: k8s.SourceFile, Path: path, Context: contextName}
	if ref.Context == "" {
		if _, current, err := k8s.ListContexts(path); err == nil {
			ref.Context = current
		}
	}
	return ref
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
