// This file extends the apply package with a read-only "what would apply
// change?" preview. Diff mirrors the resolution machinery of Apply — split the
// stream, parse each doc, map its GVK to a REST resource + namespace scope — but
// instead of persisting anything it fetches the LIVE object and runs the same
// server-side apply as a *dry run* (metav1.DryRunAll) to obtain the PROPOSED
// object the server would produce. It then renders a unified diff of live vs
// proposed so the UI can show the operator exactly what a real Apply would do
// before asking for confirmation.
//
// Because the apply is a dry run, Diff never mutates the cluster and is safe to
// run without a confirmation prompt; the confirmation belongs to the subsequent
// real Apply. As with Apply, each document is processed independently and its
// outcome recorded, so one bad doc never aborts the batch.

package apply

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// DiffResult records the outcome of diffing a single YAML document against the
// cluster. GVK, Namespace and Name identify the object as best we could resolve
// it — filled in progressively, exactly like Result — so the UI can point at the
// document a failure belongs to. Diff holds the unified-diff text: for an
// existing object it is the live→proposed delta (empty when a server-side dry-run
// apply would change nothing); for a not-yet-created object it is the whole
// proposed object rendered as additions. An empty Diff with a nil Err therefore
// means "no change". Err is non-nil only when we could not compute a diff at all
// (parse, mapping, fetch, or dry-run failure).
type DiffResult struct {
	GVK       string
	Namespace string
	Name      string
	Diff      string
	Err       error
}

// Diff computes, for every document in data, the change a server-side Apply would
// make — without making it. For each doc it resolves the GVK, maps it to a REST
// resource + scope, and defaults a namespaced object's namespace to
// defaultNamespace when the doc omits one (all identical to Apply). It then
// fetches the LIVE object; a NotFound live object is treated as "new" (the diff
// shows the whole proposed object added). Finally it issues the same server-side
// apply as Apply but with DryRun: [All], so the server returns the PROPOSED
// object without persisting it, and renders a unified diff of the two normalized
// YAML forms. Force is set so the dry run reflects the force-conflicts behavior a
// real Apply would use, keeping the preview faithful. A per-doc error is recorded
// and processing continues, mirroring Apply's resilience.
func Diff(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, data []byte, fieldManager, defaultNamespace string) []DiffResult {
	var results []DiffResult

	for _, doc := range SplitDocuments(data) {
		var res DiffResult

		obj, gvk, err := parseDoc(doc)
		res.GVK = gvk.String()
		if err != nil {
			res.Err = err
			results = append(results, res)
			continue
		}
		res.Name = obj.GetName()

		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			res.Err = fmt.Errorf("no REST mapping for %s: %w", gvk.String(), err)
			results = append(results, res)
			continue
		}

		ns := obj.GetNamespace()
		namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
		if namespaced && ns == "" {
			ns = defaultNamespace
		}
		res.Namespace = ns

		// The resource client is namespaced or cluster-scoped depending on the
		// mapping; capture it once so the fetch and dry-run apply below share it.
		var ri dynamic.ResourceInterface
		if namespaced {
			ri = dyn.Resource(mapping.Resource).Namespace(ns)
		} else {
			ri = dyn.Resource(mapping.Resource)
		}

		// Fetch the live object. A genuine NotFound means the object does not yet
		// exist, so the diff is "everything is new"; any other Get error is a real
		// failure we cannot recover from for this doc.
		live, err := ri.Get(ctx, obj.GetName(), metav1.GetOptions{})
		newObject := false
		if err != nil {
			if apierrors.IsNotFound(err) {
				newObject = true
				live = nil
			} else {
				res.Err = fmt.Errorf("fetch live object: %w", err)
				results = append(results, res)
				continue
			}
		}

		// Server-side apply as a dry run: the server computes the object it *would*
		// produce and returns it without writing anything to storage. Force matches
		// what a real Apply would do so the preview does not understate the change.
		jsonBytes, err := obj.MarshalJSON()
		if err != nil {
			res.Err = fmt.Errorf("encode object: %w", err)
			results = append(results, res)
			continue
		}
		force := true
		opts := metav1.PatchOptions{
			FieldManager: fieldManager,
			Force:        &force,
			DryRun:       []string{metav1.DryRunAll},
		}
		proposed, err := ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, jsonBytes, opts)
		if err != nil {
			res.Err = fmt.Errorf("dry-run apply: %w", err)
			results = append(results, res)
			continue
		}

		res.Diff, res.Err = renderDiff(live, proposed, newObject, diffLabel(gvk.String(), ns, obj.GetName()))
		results = append(results, res)
	}

	return results
}

// diffLabel builds a short, human-readable header for a diff hunk that names the
// object being compared (e.g. "apps/v1, Kind=Deployment demo/web"). The namespace
// is omitted for cluster-scoped objects so the label does not carry a stray "/".
func diffLabel(gvk, namespace, name string) string {
	if namespace == "" {
		return fmt.Sprintf("%s %s", gvk, name)
	}
	return fmt.Sprintf("%s %s/%s", gvk, namespace, name)
}

// renderDiff turns a live/proposed object pair into unified-diff text. For a new
// object (no live counterpart) it renders the proposed object entirely as
// additions via renderAddDiff. Otherwise it normalizes both objects to YAML and
// feeds them to unifiedDiff. Marshaling failures are returned as errors; identical
// objects yield an empty string, which callers read as "no change".
func renderDiff(live, proposed *unstructured.Unstructured, newObject bool, label string) (string, error) {
	proposedYAML, err := normalizeYAML(proposed)
	if err != nil {
		return "", fmt.Errorf("encode proposed object: %w", err)
	}

	if newObject || live == nil {
		return renderAddDiff(proposedYAML, label), nil
	}

	liveYAML, err := normalizeYAML(live)
	if err != nil {
		return "", fmt.Errorf("encode live object: %w", err)
	}
	return unifiedDiff(liveYAML, proposedYAML, label), nil
}

// normalizeYAML marshals an unstructured object to canonical YAML with map keys
// sorted (sigs.k8s.io/yaml routes through JSON, which orders keys deterministically),
// so two logically-equal objects always produce byte-identical text and the diff
// reflects real changes rather than key-ordering noise. A nil object marshals to
// the empty string.
func normalizeYAML(obj *unstructured.Unstructured) (string, error) {
	if obj == nil {
		return "", nil
	}
	b, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// renderAddDiff renders proposedYAML as a unified diff in which every line is an
// addition — the representation for an object that does not yet exist in the
// cluster. It is a pure function of its inputs (no cluster access) so the
// new-object formatting can be unit-tested directly. An empty proposedYAML yields
// an empty string.
func renderAddDiff(proposedYAML, label string) string {
	if proposedYAML == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(diffHeader(label))
	for _, line := range splitLines(proposedYAML) {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// unifiedDiff produces a readable, line-based unified diff of oldText vs newText.
// It is intentionally simple: rather than computing a minimal LCS-based edit
// script, it walks both line sequences and, wherever they diverge, emits the run
// of removed old lines ("-") followed by the run of added new lines ("+"),
// resynchronizing on the next common line. Identical inputs produce an empty
// string (callers treat that as "no change"). The output is meant to be
// human-readable, not byte-identical to git's diff. This function is pure, so it
// is unit-tested on hand-built strings.
func unifiedDiff(oldText, newText, label string) string {
	if oldText == newText {
		return ""
	}

	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	var b strings.Builder
	b.WriteString(diffHeader(label))

	i, j := 0, 0
	for i < len(oldLines) || j < len(newLines) {
		switch {
		case i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j]:
			// Lines match: emit as unchanged context and advance both cursors.
			b.WriteString(" ")
			b.WriteString(oldLines[i])
			b.WriteString("\n")
			i++
			j++
		default:
			// The lines diverge here. Find the next point where the two sequences
			// realign (the earliest old/new line pair that is equal at or after the
			// current cursors) and treat everything before it as a removed run
			// followed by an added run. resyncPoint bounds the search so a total
			// rewrite still terminates.
			nextI, nextJ := resyncPoint(oldLines, newLines, i, j)
			for ; i < nextI; i++ {
				b.WriteString("-")
				b.WriteString(oldLines[i])
				b.WriteString("\n")
			}
			for ; j < nextJ; j++ {
				b.WriteString("+")
				b.WriteString(newLines[j])
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

// resyncPoint finds the nearest (nextI, nextJ) at or after (i, j) where
// oldLines[nextI] == newLines[nextJ], i.e. the point at which the two sequences
// realign after a divergence. It searches by increasing total distance so it
// prefers the closest realignment, which keeps removed/added runs short and the
// diff readable. If no common line remains, it returns the ends of both slices,
// so the trailing old lines are all removals and the trailing new lines all
// additions.
func resyncPoint(oldLines, newLines []string, i, j int) (int, int) {
	maxD := (len(oldLines) - i) + (len(newLines) - j)
	for d := 1; d <= maxD; d++ {
		for di := 0; di <= d; di++ {
			dj := d - di
			ni, nj := i+di, j+dj
			if ni < len(oldLines) && nj < len(newLines) && oldLines[ni] == newLines[nj] {
				return ni, nj
			}
		}
	}
	return len(oldLines), len(newLines)
}

// diffHeader returns the one-line "--- a/label / +++ b/label" style banner that
// precedes a hunk. It is kept trivial and separate so both the change diff and
// the add-only diff share an identical header format.
func diffHeader(label string) string {
	return fmt.Sprintf("--- live: %s\n+++ proposed: %s\n", label, label)
}

// splitLines splits text into lines without the trailing newline producing a
// spurious empty final element. A trailing newline (the normal case for YAML)
// would otherwise add an empty "" line that shows up as a bogus diff entry.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	// yaml.Marshal ends with a newline; drop the empty element it produces.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}
