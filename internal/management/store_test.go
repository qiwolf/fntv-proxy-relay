package management

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T) *DocumentStore {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s, e := NewDocumentStore(filepath.Join(root, "private"), 128)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestDocumentStoreVersionsRestore(t *testing.T) {
	s := testStore(t)
	if _, e := s.Read(); !errors.Is(e, ErrDocumentNotFound) {
		t.Fatal(e)
	}
	if _, e := s.Save(0, json.RawMessage(`{"one":1}`)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Save(0, json.RawMessage(`{}`)); !errors.Is(e, ErrVersionConflict) {
		t.Fatal(e)
	}
	if _, e := s.Save(1, json.RawMessage(`{"two":2}`)); e != nil {
		t.Fatal(e)
	}
	b, e := s.Backups()
	if e != nil || len(b) != 1 || b[0] != 1 {
		t.Fatal(b, e)
	}
	d, e := s.Restore(2, 1)
	if e != nil || d.Version != 3 || string(d.Data) != `{"one":1}` {
		t.Fatal(d, e)
	}
	for _, name := range []string{"current.json", "backup-1.json", ".lock"} {
		i, e := os.Stat(filepath.Join(s.dir, name))
		if e != nil || i.Mode().Perm() != 0600 {
			t.Fatal(name, i, e)
		}
	}
	i, _ := os.Stat(s.dir)
	if i.Mode().Perm() != 0700 {
		t.Fatal(i.Mode())
	}
}
func TestDocumentStoreConcurrentInstances(t *testing.T) {
	s := testStore(t)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, e := NewDocumentStore(s.dir, 128)
			if e != nil {
				t.Error(e)
				return
			}
			_, e = other.Save(0, json.RawMessage(`{}`))
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, ErrVersionConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal(wins.Load())
	}
}
func TestDocumentStoreRejectInvalidAndCorrupt(t *testing.T) {
	s := testStore(t)
	if _, e := s.Save(0, json.RawMessage(`"`+strings.Repeat("a", 129)+`"`)); e == nil {
		t.Fatal("accepted oversized valid JSON")
	}
	for _, data := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`{} {}`), json.RawMessage(`"` + string(make([]byte, 200)) + `"`)} {
		if _, e := s.Save(0, data); e == nil {
			t.Fatal("accepted invalid document")
		}
	}
	path := filepath.Join(s.dir, "current.json")
	bad := []byte(`{"version":`)
	if e := os.WriteFile(path, bad, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Save(0, json.RawMessage(`{}`)); e == nil {
		t.Fatal("overwrote corrupt document")
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != string(bad) {
		t.Fatal("corrupt original changed")
	}
}

func TestDocumentStoreCorruptBackupPreserved(t *testing.T) {
	s := testStore(t)
	if _, e := s.Save(0, json.RawMessage(`{"value":1}`)); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.dir, "backup-1.json")
	if e := os.WriteFile(path, []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Save(1, json.RawMessage(`{"value":2}`)); e == nil {
		t.Fatal("overwrote corrupt backup")
	}
	current, e := s.Read()
	if e != nil || current.Version != 1 {
		t.Fatal(current, e)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "broken" {
		t.Fatal("backup changed")
	}
}

func TestWithVersionLocksDependentAction(t *testing.T) {
	s := testStore(t)
	if _, e := s.Save(0, json.RawMessage(`{}`)); e != nil {
		t.Fatal(e)
	}
	called := false
	if e := s.WithVersion(0, func(Document) error { called = true; return nil }); !errors.Is(e, ErrVersionConflict) || called {
		t.Fatal("stale callback executed", e)
	}
	other, e := NewDocumentStore(s.dir, 128)
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() { finished <- s.WithVersion(1, func(d Document) error { close(entered); <-release; return nil }) }()
	<-entered
	saved := make(chan error, 1)
	go func() { _, e := other.Save(1, json.RawMessage(`{"next":true}`)); saved <- e }()
	select {
	case e := <-saved:
		close(release)
		t.Fatal("save bypassed dependent action", e)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if e := <-saved; e != nil {
		t.Fatal(e)
	}
}
func TestDocumentStoreRejectSymlinks(t *testing.T) {
	s := testStore(t)
	target := filepath.Join(t.TempDir(), "outside")
	if e := os.WriteFile(target, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, filepath.Join(s.dir, "current.json")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Save(0, json.RawMessage(`{}`)); e == nil {
		t.Fatal("accepted symlink")
	}
	link := filepath.Join(t.TempDir(), "linked")
	if e := os.Symlink(s.dir, link); e != nil {
		t.Fatal(e)
	}
	if _, e := NewDocumentStore(filepath.Join(link, "nested"), 128); e == nil {
		t.Fatal("accepted symlink ancestor")
	}
}
