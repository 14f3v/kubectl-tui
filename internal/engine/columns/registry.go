package columns

import (
	"fmt"
	"sort"
	"time"
)

// registry maps a kind key to its projector. Kinds register themselves in init()
// so adding a resource type is a single new file plus a registry entry.
var registry = map[string]Projector{}

// Register adds a projector for a kind. It panics on a duplicate kind, which can
// only happen from a programming error at init time.
func Register(p Projector) {
	if _, dup := registry[p.Kind()]; dup {
		panic(fmt.Sprintf("columns: duplicate projector for kind %q", p.Kind()))
	}
	registry[p.Kind()] = p
}

// For returns the projector for a kind, or nil if none is registered.
func For(kind string) Projector { return registry[kind] }

// Kinds returns all registered kind keys, sorted, for diagnostics and tests.
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// age renders a creation time as a humanized age plus a numeric sort key (the
// creation unix seconds, so ascending sort is oldest-first).
func age(created, now time.Time) (string, SortKey) {
	if created.IsZero() {
		return "—", SortKey{}
	}
	return humanAge(now.Sub(created)), NumKey(float64(created.Unix()))
}

// humanAge renders a duration the way kubectl does: the two most significant
// units, collapsing to one once the value is large (e.g. "38m", "4d2h", "11d").
func humanAge(since time.Duration) string {
	if since < 0 {
		since = 0
	}
	sec := int64(since.Seconds())
	switch {
	case sec < 60:
		return fmt.Sprintf("%ds", sec)
	case sec < 3600:
		m := sec / 60
		s := sec % 60
		if m < 10 && s > 0 {
			return fmt.Sprintf("%dm%ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	case sec < 86400:
		h := sec / 3600
		m := (sec % 3600) / 60
		if h < 10 && m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	case sec < 86400*7:
		d := sec / 86400
		h := (sec % 86400) / 3600
		if d < 10 && h > 0 {
			return fmt.Sprintf("%dd%dh", d, h)
		}
		return fmt.Sprintf("%dd", d)
	default:
		d := sec / 86400
		if d < 365 {
			return fmt.Sprintf("%dd", d)
		}
		y := d / 365
		rd := d % 365
		if y < 8 && rd > 0 {
			return fmt.Sprintf("%dy%dd", y, rd)
		}
		return fmt.Sprintf("%dy", y)
	}
}
