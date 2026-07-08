package app

import "strings"

// command is a parsed ":" command-line entry.
type command struct {
	verb      string // "nav" | "quit" | "noop"
	kind      string // resource alias for nav
	namespace string // optional namespace for nav
}

// parseCommand parses a command-line string into a command. The grammar is
// "kind [namespace]", with quit aliases handled specially.
func parseCommand(s string) command {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return command{verb: "noop"}
	}
	head := strings.ToLower(fields[0])
	switch head {
	case "q", "quit", "q!", "exit":
		return command{verb: "quit"}
	default:
		c := command{verb: "nav", kind: head}
		if len(fields) > 1 {
			c.namespace = fields[1]
		}
		return c
	}
}
