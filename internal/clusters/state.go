package clusters

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/14f3v/kubectl-tui/internal/k8s"
)

// StateVersion is the state.yaml schema this build writes.
const StateVersion = 1

const stateFile = "state.yaml"

// Entry is one remembered cluster. It holds paths, names and positions only,
// never credentials.
type Entry struct {
	Source       k8s.Source `json:"source"`
	File         string     `json:"file,omitempty"` // pasted
	Path         string     `json:"path,omitempty"` // file
	Name         string     `json:"name,omitempty"` // pasted
	Context      string     `json:"context,omitempty"`
	Server       string     `json:"server,omitempty"` // kubeconfig: part of the key
	LastUsed     time.Time  `json:"lastUsed,omitempty"`
	Namespace    string     `json:"namespace"`
	Kind         string     `json:"kind,omitempty"`
	ApprovedExec []string   `json:"approvedExec,omitempty"`

	// raw is the entry as read, so fields a newer version added survive a rewrite.
	raw json.RawMessage
}

// Key identifies an entry. Fields that do not apply to a source stay empty.
type Key struct {
	Source                      k8s.Source
	File, Path, Context, Server string
}

// Key returns the entry's identity: Server only for kubeconfig, File only for
// pasted, Path only for file.
func (e Entry) Key() Key {
	return makeKey(e.Source, e.File, e.Path, e.Context, e.Server)
}

func makeKey(src k8s.Source, file, path, context, server string) Key {
	k := Key{Source: src, Context: context}
	switch src {
	case k8s.SourcePasted:
		k.File = file
	case k8s.SourceFile:
		k.Path = path
	case k8s.SourceKubeconfig:
		k.Server = server
	}
	return k
}

// KeyFor returns the key of the entry that records ref. server is the cluster's
// API server, used only for kubeconfig refs.
func KeyFor(ref k8s.ClusterRef, server string) Key {
	return makeKey(ref.Source, filepath.Base(ref.Path), ref.Path, ref.Context, server)
}

// State is the contents of state.yaml.
type State struct {
	Version  int     `json:"version"`
	Clusters []Entry `json:"clusters"`

	// unknown holds entries of a source this build does not know, verbatim.
	unknown []json.RawMessage
}

type stateDoc struct {
	Version  int               `json:"version"`
	Clusters []json.RawMessage `json:"clusters"`
}

func knownSource(s k8s.Source) bool {
	switch s {
	case k8s.SourcePasted, k8s.SourceFile, k8s.SourceKubeconfig, k8s.SourceInCluster:
		return true
	}
	return false
}

func (s *Store) statePath() string { return filepath.Join(s.dir, stateFile) }

// LoadState reads state.yaml. A missing file is an empty state; a malformed one
// is renamed to state.yaml.bad and an empty state returned, so a bad file never
// blocks launch.
func (s *Store) LoadState() (State, error) {
	data, err := os.ReadFile(s.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: StateVersion}, nil
	}
	if err != nil {
		return State{Version: StateVersion}, err
	}
	var doc stateDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		if err := os.Rename(s.statePath(), s.statePath()+".bad"); err != nil {
			return State{Version: StateVersion}, err
		}
		return State{Version: StateVersion}, nil
	}
	st := State{Version: doc.Version}
	if st.Version == 0 {
		st.Version = StateVersion
	}
	for _, raw := range doc.Clusters {
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil || !knownSource(e.Source) {
			st.unknown = append(st.unknown, raw)
			continue
		}
		e.raw = raw
		st.Clusters = append(st.Clusters, e)
	}
	return st, nil
}

// marshalEntry writes e over its original fields, keeping any it does not know.
func marshalEntry(e Entry) (json.RawMessage, error) {
	known, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if len(e.raw) == 0 {
		return known, nil
	}
	var out, mine map[string]json.RawMessage
	if json.Unmarshal(e.raw, &out) != nil {
		return known, nil
	}
	_ = json.Unmarshal(known, &mine)
	for _, f := range []string{"source", "file", "path", "name", "context", "server", "lastUsed", "namespace", "kind", "approvedExec"} {
		delete(out, f)
	}
	for f, v := range mine {
		out[f] = v
	}
	return json.Marshal(out)
}

func (s *Store) saveState(st State) error {
	doc := stateDoc{Version: st.Version, Clusters: []json.RawMessage{}}
	for _, e := range st.Clusters {
		raw, err := marshalEntry(e)
		if err != nil {
			return err
		}
		doc.Clusters = append(doc.Clusters, raw)
	}
	doc.Clusters = append(doc.Clusters, st.unknown...)
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return writeAtomic(s.statePath(), data)
}

// Update re-reads state.yaml, applies fn to the entry for k (created if absent)
// and writes the file atomically. Only that entry changes, so a second kubetui's
// entries survive; the last writer wins per entry.
func (s *Store) Update(k Key, fn func(*Entry)) error {
	st, err := s.LoadState()
	if err != nil {
		return err
	}
	for i := range st.Clusters {
		if st.Clusters[i].Key() == k {
			fn(&st.Clusters[i])
			return s.saveState(st)
		}
	}
	e := Entry{Source: k.Source, File: k.File, Path: k.Path, Context: k.Context, Server: k.Server}
	fn(&e)
	st.Clusters = append(st.Clusters, e)
	return s.saveState(st)
}

// Remove deletes the entry for k. An absent entry is not an error.
func (s *Store) Remove(k Key) error {
	st, err := s.LoadState()
	if err != nil {
		return err
	}
	kept := st.Clusters[:0]
	for _, e := range st.Clusters {
		if e.Key() != k {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(st.Clusters) {
		return nil
	}
	st.Clusters = kept
	return s.saveState(st)
}

// Entries returns the entries of known sources.
func (s *Store) Entries() ([]Entry, error) {
	st, err := s.LoadState()
	return st.Clusters, err
}
