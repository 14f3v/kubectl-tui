// Package clusters stores the kubeconfigs a user pastes into kubetui. They live
// in kubetui's own folder; kubetui never writes ~/.kube/config, $KUBECONFIG
// files or a --kubeconfig file.
package clusters

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/14f3v/kubectl-tui/internal/action/cp"
)

// ErrNoConfigDir means kubetui has no absolute folder to keep files in.
var ErrNoConfigDir = errors.New("no usable config directory")

const pasteExt = ".yaml"

// Store is kubetui's config folder; pasted kubeconfigs live in <dir>/clusters.
type Store struct {
	dir string
	// mu serializes this process's read-modify-writes of state.yaml: they run
	// in background commands that can overlap, and two unordered rewrites would
	// lose one's change.
	mu sync.Mutex
}

// Open returns a Store rooted at dir, which must be an absolute path.
func Open(dir string) (*Store, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, ErrNoConfigDir
	}
	return &Store{dir: dir}, nil
}

// Dir returns the config folder.
func (s *Store) Dir() string { return s.dir }

// ClustersDir returns the folder holding pasted kubeconfigs.
func (s *Store) ClustersDir() string { return filepath.Join(s.dir, "clusters") }

// PastePath returns the full path of a pasted file's base name.
func (s *Store) PastePath(file string) string { return filepath.Join(s.ClustersDir(), file) }

// SavePaste stores data under a file name derived from name and returns the
// base name. A file that already holds exactly data is reused; a different one
// with the same name makes this a "-2", "-3"... file.
func (s *Store) SavePaste(name string, data []byte) (string, error) {
	dir := s.ClustersDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// MkdirAll leaves an existing folder's mode alone, so tighten it.
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); errors.Is(err, os.ErrNotExist) {
		if err := writeAtomic(gi, []byte("*\n")); err != nil {
			return "", err
		}
	}
	base := slug(name)
	for n := 1; ; n++ {
		file := base + pasteExt
		if n > 1 {
			file = fmt.Sprintf("%s-%d%s", base, n, pasteExt)
		}
		existing, err := os.ReadFile(filepath.Join(dir, file))
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := writeAtomic(filepath.Join(dir, file), data); err != nil {
				return "", err
			}
			return file, nil
		case err != nil:
			return "", err
		case bytes.Equal(existing, data):
			return file, nil
		}
	}
}

// PastedFiles lists the base names of pasted kubeconfigs, sorted. Lock files,
// temp files and .gitignore are not kubeconfigs and are skipped.
func (s *Store) PastedFiles() ([]string, error) {
	entries, err := os.ReadDir(s.ClustersDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, pasteExt) {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// DeletePaste removes a pasted kubeconfig.
func (s *Store) DeletePaste(file string) error { return os.Remove(s.PastePath(file)) }

// maxStem caps a pasted file's stem in bytes, far below any file system's
// limit on a name, so no name makes the save fail as "file name too long".
const maxStem = 64

// slug makes a file-name-safe stem on every OS: lowercase, spaces to dashes,
// at most maxStem bytes, cut at a rune boundary before the Windows rules trim
// any dot or space the cut leaves at the end. Leading dots are trimmed so the
// file is never hidden from PastedFiles.
func slug(name string) string {
	s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
	if len(s) > maxStem {
		cut := maxStem
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	s = cp.WindowsFileName(s)
	if s = strings.TrimLeft(s, "."); s == "" {
		return "cluster"
	}
	return s
}

// writeAtomic writes data to path through a temp file in the same folder, so a
// crash never leaves a half-written file. The file is 0600 on Unix; on Windows
// the mode only toggles the read-only attribute, which is never set here.
func writeAtomic(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err = os.Chmod(tmp, 0o600); err != nil {
			return err
		}
	}
	// Windows refuses to rename over a file another process (an antivirus scan)
	// briefly holds open, so retry there.
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 5
	}
	for i := 0; ; i++ {
		if err = os.Rename(tmp, path); err == nil || i+1 >= attempts {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
