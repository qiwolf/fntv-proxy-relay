package management

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func store(t *testing.T) *KeyStore {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "private")
	s, e := NewKeyStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestGenerateReuseAndPermissions(t *testing.T) {
	s := store(t)
	if e := s.Generate([]string{"fntv", "emby"}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.dir, "keys.json")
	before, _ := os.ReadFile(path)
	if e := s.Generate([]string{"fntv"}); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("existing keys changed")
	}
	i, _ := os.Stat(path)
	if i.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions")
	}
	st, e := s.Status()
	if e != nil || !st["fntv"] || !st["emby"] || st["jellyfin"] {
		t.Fatal(st, e)
	}
}
func TestInvalidRequestDoesNotWrite(t *testing.T) {
	s := store(t)
	if s.Generate([]string{"fntv", "../escape"}) == nil {
		t.Fatal("accepted invalid service")
	}
	st, _ := s.Status()
	if st["fntv"] {
		t.Fatal("partial write")
	}
}
func TestCorruptionPreserved(t *testing.T) {
	s := store(t)
	p := filepath.Join(s.dir, "keys.json")
	os.WriteFile(p, []byte("broken"), 0600)
	if s.Generate([]string{"fntv"}) == nil {
		t.Fatal("replaced corrupt store")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "broken" {
		t.Fatal("data changed")
	}
}
func TestConcurrentGeneration(t *testing.T) {
	s := store(t)
	var wg sync.WaitGroup
	for _, id := range []string{"fntv", "emby", "jellyfin"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			other, e := NewKeyStore(s.dir)
			if e != nil {
				t.Error(e)
				return
			}
			if e := other.Generate([]string{id}); e != nil {
				t.Error(e)
			}
		}(id)
	}
	wg.Wait()
	st, e := s.Status()
	if e != nil || !st["fntv"] || !st["emby"] || !st["jellyfin"] {
		t.Fatal(st, e)
	}
}
func TestRejectSymlink(t *testing.T) {
	s := store(t)
	target := filepath.Join(t.TempDir(), "other")
	os.WriteFile(target, []byte("{}"), 0600)
	os.Symlink(target, filepath.Join(s.dir, "keys.json"))
	if _, e := s.Status(); e == nil {
		t.Fatal("followed symlink")
	}
}

func TestKeyStoreRejectAncestorSymlink(t *testing.T) {
	s := store(t)
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "link")
	if e = os.Symlink(s.dir, link); e != nil {
		t.Fatal(e)
	}
	if _, e = NewKeyStore(filepath.Join(link, "child")); e == nil {
		t.Fatal("accepted ancestor symlink")
	}
}
