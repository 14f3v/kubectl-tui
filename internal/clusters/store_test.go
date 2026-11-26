package clusters

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenRejectsUnusableDir(t *testing.T) {
	for _, d := range []string{"", "rel/dir"} {
		if _, err := Open(d); !errors.Is(err, ErrNoConfigDir) {
			t.Errorf("Open(%q) err = %v, want ErrNoConfigDir", d, err)
		}
	}
}

func TestSavePasteWritesFileAndGitignore(t *testing.T) {
	s := openTemp(t)
	data := []byte("apiVersion: v1\n")
	file, err := s.SavePaste("dev tokyo", data)
	if err != nil {
		t.Fatal(err)
	}
	if file != "dev-tokyo.yaml" {
		t.Fatalf("file = %q", file)
	}
	got, err := os.ReadFile(s.PastePath(file))
	if err != nil || string(got) != string(data) {
		t.Fatalf("content = %q, err %v", got, err)
	}
	gi, err := os.ReadFile(filepath.Join(s.ClustersDir(), ".gitignore"))
	if err != nil || string(gi) != "*\n" {
		t.Fatalf(".gitignore = %q, err %v", gi, err)
	}
	if runtime.GOOS != "windows" {
		di, _ := os.Stat(s.ClustersDir())
		if di.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %v", di.Mode().Perm())
		}
		fi, _ := os.Stat(s.PastePath(file))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v", fi.Mode().Perm())
		}
	}
}

func TestSavePasteNameCollision(t *testing.T) {
	s := openTemp(t)
	a, _ := s.SavePaste("default", []byte("a"))
	b, _ := s.SavePaste("default", []byte("b"))
	if a != "default.yaml" || b != "default-2.yaml" {
		t.Fatalf("got %q, %q", a, b)
	}
}

func TestSavePasteReusesIdenticalContent(t *testing.T) {
	s := openTemp(t)
	a, _ := s.SavePaste("x", []byte("same"))
	b, err := s.SavePaste("x", []byte("same"))
	if err != nil || a != b {
		t.Fatalf("a=%q b=%q err=%v", a, b, err)
	}
	files, _ := s.PastedFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
}

func TestSavePasteTightensExistingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	s := openTemp(t)
	if err := os.Mkdir(s.ClustersDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.ClustersDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePaste("a", []byte("a")); err != nil {
		t.Fatal(err)
	}
	di, _ := os.Stat(s.ClustersDir())
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", di.Mode().Perm())
	}
}

func TestPastedFilesSkipsLocksAndTemps(t *testing.T) {
	s := openTemp(t)
	if err := os.MkdirAll(s.ClustersDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.yaml", "a.yaml.lock", ".tmp-123", ".gitignore"} {
		if err := os.WriteFile(filepath.Join(s.ClustersDir(), n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.PastedFiles()
	if err != nil || len(got) != 1 || got[0] != "a.yaml" {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestPastedFilesMissingDir(t *testing.T) {
	got, err := openTemp(t).PastedFiles()
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestDeletePaste(t *testing.T) {
	s := openTemp(t)
	f, _ := s.SavePaste("a", []byte("a"))
	if err := s.DeletePaste(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.PastePath(f)); !os.IsNotExist(err) {
		t.Fatalf("still exists: %v", err)
	}
}

func TestSavePasteLeadingDotIsListed(t *testing.T) {
	s := openTemp(t)
	for _, name := range []string{".dev", "..", "..."} {
		file, err := s.SavePaste(name, []byte("apiVersion: v1\n"))
		if err != nil {
			t.Fatal(err)
		}
		if file == "" || file[0] == '.' {
			t.Fatalf("name %q saved as hidden %q", name, file)
		}
	}
	files, err := s.PastedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("pasted files not listed")
	}
}
