package view

import "charm.land/bubbles/v2/key"

// Navigation bindings shared by every table page.
var (
	keyUp       = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	keyDown     = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	keyPageUp   = key.NewBinding(key.WithKeys("pgup", "ctrl+b"), key.WithHelp("pgup", "page up"))
	keyPageDown = key.NewBinding(key.WithKeys("pgdown", "ctrl+f"), key.WithHelp("pgdn", "page down"))
	keyHome     = key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g", "top"))
	keyEnd      = key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G", "bottom"))
)

// Resource action bindings shown in the header grid and footer. Their handlers
// are wired incrementally (inspect actions in phase 4, mutations in phase 5); the
// bindings exist now so the chrome renders the full action set.
var (
	keyEnter    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view"))
	keyDescribe = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe"))
	keyLogs     = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs"))
	keyShell    = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "shell"))
	keyYAML     = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yaml"))
	keyEdit     = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit"))
	keyDelete   = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl-d", "delete"))
	keyKill     = key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl-k", "kill"))
	keyPortFwd  = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "port-fwd"))
	keyScale    = key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "scale"))
	keyRollout  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rollout"))
	keySortNext = key.NewBinding(key.WithKeys(">"), key.WithHelp(">", "sort col"))
	keySortDir  = key.NewBinding(key.WithKeys("<"), key.WithHelp("<", "sort dir"))
)
