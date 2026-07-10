package app

import "strings"

// command is a parsed ":" command-line entry.
type command struct {
	verb      string // "nav" | "ctx" | "quit" | "noop"
	kind      string // resource alias for nav
	namespace string // optional namespace for nav
	arg       string // argument for ctx (context name)
}

// parseCommand parses a command-line string into a command. The grammar is
// "kind [namespace]", with quit and context aliases handled specially.
func parseCommand(s string) command {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return command{verb: "noop"}
	}
	head := strings.ToLower(fields[0])
	switch head {
	case "q", "quit", "q!", "exit":
		return command{verb: "quit"}
	case "ctx", "context":
		c := command{verb: "ctx"}
		if len(fields) > 1 {
			c.arg = fields[1] // preserve case for the context name
		}
		return c
	case "apply", "create":
		return command{verb: "apply"}
	case "explain", "exp":
		c := command{verb: "explain"}
		if len(fields) > 1 {
			c.arg = fields[1] // preserve case: field paths are camelCase (e.g. securityContext)
		}
		return c
	case "crds", "crd":
		return command{verb: "crds"}
	default:
		// A dotted token with no space is a fully-qualified resource: <plural>.<group>
		// (e.g. certificates.cert-manager.io) — open it via the Table-protocol browser.
		if strings.Contains(head, ".") {
			return command{verb: "crdopen", arg: head}
		}
		c := command{verb: "nav", kind: head}
		if len(fields) > 1 {
			c.namespace = fields[1]
		}
		return c
	}
}
