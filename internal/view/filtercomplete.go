package view

import (
	"strings"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

// FilterCompleter is implemented by pages whose live "/" filter supports
// Tab-completion of the last term against the page's current rows. The app calls
// CompleteFilter with the current filter buffer and, when it returns changed,
// replaces the buffer and re-applies the filter.
type FilterCompleter interface {
	CompleteFilter(buf string) (string, bool)
}

// completeFilter Tab-completes the last whitespace-separated term of a filter
// buffer against the values in rows. It peels the term's "!", "col:", and "~"
// affixes, gathers candidate values for the term's scope (row names, namespaces,
// or a specific column's cells), keeps those the typed value is a case-insensitive
// prefix of, and extends the value to their longest common prefix. It returns the
// new buffer and whether anything changed. A regex value ("~") is left alone.
func completeFilter(buf string, rows []columns.Row, colTitles []string) (string, bool) {
	head, last := splitLastToken(buf)
	if last == "" {
		return buf, false
	}

	// Peel the affixes, remembering them so we can rebuild the token.
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
		return buf, false // a regex value isn't meaningfully completable
	}
	value := rest

	matches := prefixMatches(completionValues(rows, scope, cellIdx), value)
	if len(matches) == 0 {
		return buf, false
	}
	completion := matches[0]
	if len(matches) > 1 {
		completion = longestCommonPrefix(matches)
	}
	if len(completion) <= len(value) {
		return buf, false // already at the shared prefix; nothing to add
	}
	return head + rebuild + completion, true
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

// longestCommonPrefix returns the longest byte-prefix shared by every string.
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		n := min(len(prefix), len(s))
		i := 0
		for i < n && prefix[i] == s[i] {
			i++
		}
		prefix = prefix[:i]
		if prefix == "" {
			break
		}
	}
	return prefix
}
