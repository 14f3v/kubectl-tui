package clusters

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	"github.com/14f3v/kubectl-tui/internal/k8s"
)

// Row is one line of the cluster picker.
type Row struct {
	Ref         k8s.ClusterRef
	Key         Key
	Label       string // pasted: Name, or "Name / context" when its file has >1 context; else the context name. Drawn through Sanitize; Ref and Key are the identifiers
	Source      k8s.Source
	Server      string
	LastUsed    time.Time // zero = never
	Missing     bool      // file source whose file is gone
	NeedsReview bool      // pasted file with no state record
	Problem     string    // why it cannot be listed fully (unreadable file)
}

// Inputs is everything Build needs, already read from disk.
type Inputs struct {
	Entries    []Entry
	Pasted     map[string][]k8s.ContextInfo // file → contexts (nil value = unreadable)
	Files      map[string][]k8s.ContextInfo // path → contexts (nil value = missing)
	Kubeconfig []k8s.ContextInfo
	InCluster  bool
	PastedDir  string
}

const inClusterLabel = "in-cluster"

// Build turns the inputs into picker rows, most recently used first and the
// never-used ones by label. It does no IO.
func Build(in Inputs) []Row {
	byKey := make(map[Key]Entry, len(in.Entries))
	for _, e := range in.Entries {
		byKey[e.Key()] = e
	}
	var rows []Row

	// Pasted files: an entry whose file is gone is dropped.
	for file, ctxs := range in.Pasted {
		path := filepath.Join(in.PastedDir, file)
		stem := strings.TrimSuffix(file, pasteExt)
		if ctxs == nil {
			rows = append(rows, pastedUnreadable(in.Entries, file, path, stem)...)
			continue
		}
		for _, c := range ctxs {
			k := makeKey(k8s.SourcePasted, file, "", c.Name, "")
			r := Row{
				Ref:    k8s.ClusterRef{Source: k8s.SourcePasted, Path: path, Context: c.Name},
				Key:    k,
				Source: k8s.SourcePasted,
				Server: c.Server,
				Label:  stem,
			}
			if e, ok := byKey[k]; ok {
				r.LastUsed = e.LastUsed
				if e.Name != "" {
					r.Label = e.Name
				}
			} else {
				r.NeedsReview = true
			}
			if len(ctxs) > 1 {
				r.Label += " / " + c.Name
			}
			rows = append(rows, r)
		}
	}

	// Remembered files, plus any listed that have no entry yet.
	paths := map[string]bool{}
	for p := range in.Files {
		paths[p] = true
	}
	for _, e := range in.Entries {
		if e.Source == k8s.SourceFile {
			paths[e.Path] = true
		}
	}
	for p := range paths {
		ctxs := in.Files[p]
		if ctxs == nil {
			for _, e := range in.Entries {
				if e.Source == k8s.SourceFile && e.Path == p {
					rows = append(rows, Row{
						Ref:      k8s.ClusterRef{Source: k8s.SourceFile, Path: p, Context: e.Context},
						Key:      e.Key(),
						Label:    e.Context,
						Source:   k8s.SourceFile,
						Server:   e.Server,
						LastUsed: e.LastUsed,
						Missing:  true,
					})
				}
			}
			continue
		}
		for _, c := range ctxs {
			k := makeKey(k8s.SourceFile, "", p, c.Name, "")
			rows = append(rows, Row{
				Ref:      k8s.ClusterRef{Source: k8s.SourceFile, Path: p, Context: c.Name},
				Key:      k,
				Label:    c.Name,
				Source:   k8s.SourceFile,
				Server:   c.Server,
				LastUsed: byKey[k].LastUsed,
			})
		}
	}

	// The default kubeconfig, keyed by context and server so two "default"
	// contexts on different clusters stay apart.
	for _, c := range in.Kubeconfig {
		k := makeKey(k8s.SourceKubeconfig, "", "", c.Name, c.Server)
		rows = append(rows, Row{
			Ref:      k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: c.Name},
			Key:      k,
			Label:    c.Name,
			Source:   k8s.SourceKubeconfig,
			Server:   c.Server,
			LastUsed: byKey[k].LastUsed,
		})
	}

	if in.InCluster && len(in.Kubeconfig) == 0 {
		k := makeKey(k8s.SourceInCluster, "", "", "", "")
		rows = append(rows, Row{
			Ref:      k8s.ClusterRef{Source: k8s.SourceInCluster},
			Key:      k,
			Label:    inClusterLabel,
			Source:   k8s.SourceInCluster,
			LastUsed: byKey[k].LastUsed,
		})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.LastUsed.Equal(b.LastUsed) {
			return a.LastUsed.After(b.LastUsed)
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.Ref.Path+"\x00"+a.Ref.Context < b.Ref.Path+"\x00"+b.Ref.Context
	})
	return rows
}

// pastedUnreadable makes the rows for a pasted file that cannot be parsed: one
// per remembered context, or a single row when nothing is remembered.
func pastedUnreadable(entries []Entry, file, path, stem string) []Row {
	const problem = "file unreadable"
	var rows []Row
	for _, e := range entries {
		if e.Source != k8s.SourcePasted || e.File != file {
			continue
		}
		label := stem
		if e.Name != "" {
			label = e.Name
		}
		rows = append(rows, Row{
			Ref:      k8s.ClusterRef{Source: k8s.SourcePasted, Path: path, Context: e.Context},
			Key:      e.Key(),
			Label:    label,
			Source:   k8s.SourcePasted,
			LastUsed: e.LastUsed,
			Problem:  problem,
		})
	}
	if rows == nil {
		rows = []Row{{
			Ref:         k8s.ClusterRef{Source: k8s.SourcePasted, Path: path},
			Key:         makeKey(k8s.SourcePasted, file, "", "", ""),
			Label:       stem,
			Source:      k8s.SourcePasted,
			NeedsReview: true,
			Problem:     problem,
		}}
	}
	return rows
}

// Collect reads every source and builds the rows. s may be nil, in which case
// only the default kubeconfig (and in-cluster) is listed. A pasted entry whose
// file is gone is removed from the state.
func Collect(s *Store) ([]Row, error) {
	in := Inputs{}
	// An unreadable default kubeconfig just lists nothing.
	in.Kubeconfig, _, _ = k8s.ListContexts("")
	if len(in.Kubeconfig) == 0 {
		_, err := rest.InClusterConfig()
		in.InCluster = err == nil
	}
	if s == nil {
		return Build(in), nil
	}
	in.PastedDir = s.ClustersDir()

	entries, err := s.Entries()
	if err != nil {
		return nil, err
	}
	files, err := s.PastedFiles()
	if err != nil {
		return nil, err
	}
	in.Pasted = map[string][]k8s.ContextInfo{}
	for _, f := range files {
		ctxs, _, err := k8s.ListContexts(s.PastePath(f))
		if err != nil {
			ctxs = nil
		}
		in.Pasted[f] = ctxs
	}
	in.Files = map[string][]k8s.ContextInfo{}
	for _, e := range entries {
		switch e.Source {
		case k8s.SourceFile:
			if _, done := in.Files[e.Path]; done {
				continue
			}
			ctxs, _, err := k8s.ListContexts(e.Path)
			if err != nil {
				ctxs = nil
			}
			in.Files[e.Path] = ctxs
		case k8s.SourcePasted:
			if _, ok := in.Pasted[e.File]; !ok {
				// Best effort: the row is dropped either way.
				_ = s.Remove(e.Key())
			}
		}
	}
	// Build reads entries after the cleanup so a dropped record never shows.
	for _, e := range entries {
		if e.Source == k8s.SourcePasted {
			if _, ok := in.Pasted[e.File]; !ok {
				continue
			}
		}
		in.Entries = append(in.Entries, e)
	}
	return Build(in), nil
}

// Match returns the rows whose label or context name equals name exactly, as
// the picker draws them: a typed name holds no control characters, so it is
// matched against the names with theirs stripped.
func Match(rows []Row, name string) []Row {
	var out []Row
	for _, r := range rows {
		if Sanitize(r.Label) == name || Sanitize(r.Ref.Context) == name {
			out = append(out, r)
		}
	}
	return out
}
