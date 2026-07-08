// Package msg holds cross-cutting Bubble Tea message types that flow between the
// root model, pages, and action subsystems. The engine's SnapshotMsg lives in the
// engine package (its output); everything else that multiple packages must speak
// lives here. This package may import engine, but engine never imports it.
package msg

// Level classifies a toast's severity, which selects its color.
type Level int

const (
	// LevelInfo is neutral information.
	LevelInfo Level = iota
	// LevelSuccess is a completed action.
	LevelSuccess
	// LevelWarn is a recoverable problem.
	LevelWarn
	// LevelError is a failed action.
	LevelError
)

// Toast asks the root model to show a transient, auto-dismissing message. Actions
// emit these on completion; they never mutate view state.
type Toast struct {
	Text  string
	Level Level
}

// Navigate asks the root model to switch the main view to a resource kind,
// optionally scoped to a namespace. Emitted by the command line.
type Navigate struct {
	Kind      string
	Namespace string
}

// SwitchContext asks the root model to dispose the current Session and build one
// for the named kubeconfig context.
type SwitchContext struct {
	Context string
}

// SessionReady is delivered when a Session finishes bootstrapping.
type SessionReady struct {
	// Session is typed as any to avoid importing the k8s package here (which would
	// create an import cycle); the root model type-asserts it.
	Session any
}

// SessionError is delivered when a Session fails to bootstrap.
type SessionError struct {
	Err error
}
