package main

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestExitStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		code       int
		wantReport bool
	}{
		{"clean quit", nil, 0, false},
		// The exact shape Program.Run returns after os.Interrupt (Ctrl+Break on
		// Windows): ErrInterrupted wrapped under ErrProgramKilled with two %w verbs.
		{"interrupt as Run wraps it", fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrInterrupted), 130, false},
		{"bare interrupt", tea.ErrInterrupted, 130, false},
		{"panic", fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrProgramPanic), 1, true},
		{"killed", tea.ErrProgramKilled, 1, true},
		{"other error", errors.New("open /dev/tty: no such device"), 1, true},
	}
	for _, c := range cases {
		code, report := exitStatus(c.err)
		if code != c.code || report != c.wantReport {
			t.Errorf("%s: exitStatus(%v) = %d, %v; want %d, %v", c.name, c.err, code, report, c.code, c.wantReport)
		}
	}
}
