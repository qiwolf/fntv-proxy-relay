package handler

import (
	"fntv-proxy/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestSTRMDirectoryDifferentMachineMount(t *testing.T) {
	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "movie.strm"), []byte("http://video.internal:19798/dav/movie.mkv\n"), 0600); err != nil {
		t.Fatal(err)
	}
	h := &StreamHandler{allowedStrmRoots: []string{local}}
	h.SetSTRMDirectoryMap([]config.STRMDirectoryMapping{{Source: "/nas/remote-mount", Local: local}})
	for _, enforce := range []bool{true, false} {
		got, err := h.readStrm("/nas/remote-mount/movie.strm", enforce)
		if err != nil || got != "http://video.internal:19798/dav/movie.mkv" {
			t.Fatal(got, err)
		}
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.strm")
	if err := os.WriteFile(secret, []byte("http://secret.internal"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(local, "escape.strm")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/nas/remote-mount/escape.strm", "/nas/remote-mount/../secret.strm", "/nas/remote-mount-other/movie.strm"} {
		if _, err := h.readStrm(p, false); err == nil {
			t.Fatalf("unsafe mapped read accepted: %s", p)
		}
	}
	h.allowedStrmRoots = []string{outside}
	if _, err := h.ReadAllowedStrm("/nas/remote-mount/movie.strm"); err == nil {
		t.Fatal("mapping bypassed allowed roots")
	}
}
