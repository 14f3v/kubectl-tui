package view

import (
	"strings"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

// FilterCompleter is implemented by pages whose live "/" filter offers
// suggestions and Tab/arrow completion of the last term against the page's rows.
type FilterCompleter interface {
	// FilterMatches parses buf's last term and returns the fixed prefix (the head
	// plus any "!"/"col:" affixes and any earlier "a|" alternatives) to re-prepend,
	// the typed value, and the distinct candidate values the value is a
	// case-insensitive prefix of. matches is nil for an empty last token or a regex
	// ("~") value; a trailing "|" yields an empty value and therefore every candidate.
	FilterMatches(buf string) (prefix, value string, matches []string)
}

// filterMatches parses buf's last term and gathers the completion candidates for
// it. The prefix is everything before the completable value (head + "!"/"col:"),
// so prefix+candidate rebuilds the buffer with a chosen suggestion.
func filterMatches(buf string, rows []columns.Row, colTitles []string) (prefix, value string, matches []string) {
	head, last := splitLastToken(buf)
	if last == "" {
		return "", "", nil
	}
	rebuild, rest := "", last
	if strings.HasPrefix(rest, "!") {
		rebuild, rest = "!", rest[1:]
	}
	scope, cellIdx := scopeAny, -1
	if col, val, ok := strings.Cut(rest, ":"); ok {
		if s, idx, matched := resolveScope(col, colTitles); matched {
			scope, cellIdx = s, idx
			rebuild += col + ":"
			rest = val
		}
	}
	if strings.HasPrefix(rest, "~") {
		return "", "", nil // a regex value isn't meaningfully completable
	}
	// Complete the LAST alternative of an "a|b" term, folding the earlier ones
	// into the prefix so a chosen suggestion rebuilds the whole term. Without
	// this the dropdown goes dark exactly while the user types the second value.
	if i := strings.LastIndexByte(rest, '|'); i >= 0 {
		rebuild, rest = rebuild+rest[:i+1], rest[i+1:]
		// The parser tolerates a repeated "col:" inside an alternative, so completion
		// has to as well — otherwise typing "ns:a|ns:de" offers nothing, which is the
		// point at which a user concludes the feature is broken. Keep what they typed
		// in the prefix and complete the value after it.
		if col, val, ok := strings.Cut(rest, ":"); ok {
			if s, idx, matched := resolveScope(col, colTitles); matched && s == scope && idx == cellIdx {
				rebuild, rest = rebuild+col+":", val
			}
		}
	}
	value = rest
	matches = prefixMatches(completionValues(rows, scope, cellIdx), value)
	return head + rebuild, value, matches
}

// splitLastToken splits a buffer into everything up to and including the last
// space (head) and the trailing token to complete (last).
func splitLastToken(s string) (head, last string) {
	if i := strings.LastIndexByte(s, ' '); i >= 0 {
		return s[:i+1], s[i+1:]
	}
	return "", s
}

// completionValues collects the candidate strings for a term's scope: row names
// for scopeAny/scopeName, namespaces for scopeNamespace, and the cell text for a
// scoped column.
func completionValues(rows []columns.Row, scope filterScope, cellIdx int) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		switch scope {
		case scopeNamespace:
			out = append(out, r.Namespace)
		case scopeCell:
			if cellIdx >= 0 && cellIdx < len(r.Cells) {
				out = append(out, r.Cells[cellIdx].Text)
			}
		default: // scopeAny, scopeName
			out = append(out, r.Name)
		}
	}
	return out
}

// prefixMatches returns the distinct candidates that have value as a
// case-insensitive prefix, preserving first-seen order.
func prefixMatches(cands []string, value string) []string {
	lower := strings.ToLower(value)
	var out []string
	seen := map[string]bool{}
	for _, c := range cands {
		if c == "" || seen[c] {
			continue
		}
		if strings.HasPrefix(strings.ToLower(c), lower) {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
