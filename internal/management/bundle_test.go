package management

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func bundleKeys(t *testing.T) *KeyStore {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewKeyStore(filepath.Join(root, "keys"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func bundleConfig() Config {
	c := testConfig()
	c.Mode = "split"
	c.Role = "proxy"
	c.PublicBaseURL = "https://media.example.com:49967"
	s := c.Services["fntv"]
	s.STRMDirectories = []DirectoryMapping{{Source: "/secret-strm", Local: "/private-mount"}}
	s.AllowedUpstreams = []string{"video.internal:19798"}
	c.Services["fntv"] = s
	return c
}
func TestBundleRoundTrip(t *testing.T) {
	s := bundleKeys(t)
	if err := s.Generate([]string{"fntv"}); err != nil {
		t.Fatal(err)
	}
	raw, err := ExportBundle(bundleConfig(), s, "test password long")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "video.internal") || strings.Contains(string(raw), "private-mount") {
		t.Fatal("plaintext data leaked")
	}
	b, err := OpenBundle(raw, "test password long")
	if err != nil {
		t.Fatal(err)
	}
	c := b.Config()
	if c.Role != "media" || c.Services["fntv"].Target != "" || len(c.Services["fntv"].STRMDirectories) != 0 {
		t.Fatal("proxy settings leaked")
	}
	destination := bundleKeys(t)
	if err := b.ImportKeys(destination); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Material([]string{"fntv"})
	got, _ := destination.Material([]string{"fntv"})
	if a["fntv"] != got["fntv"] {
		t.Fatal("key mismatch")
	}
	if err := b.ImportKeys(destination); err != nil {
		t.Fatal("idempotent import", err)
	}
	if _, err := OpenBundle(raw, "incorrect password"); err == nil {
		t.Fatal("wrong password accepted")
	}
	var envelope bundleEnvelope
	_ = json.Unmarshal(raw, &envelope)
	envelope.Ciphertext[0] ^= 1
	modified, _ := json.Marshal(envelope)
	if _, err := OpenBundle(modified, "test password long"); err == nil {
		t.Fatal("tampering accepted")
	}
}
func TestBundleConflictsAreAtomic(t *testing.T) {
	s := bundleKeys(t)
	_ = s.Generate([]string{"fntv", "emby"})
	c := bundleConfig()
	c.Services["emby"] = ServiceConfig{Enabled: true, Target: "http://emby.internal:8096"}
	raw, err := ExportBundle(c, s, "test password long")
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenBundle(raw, "test password long")
	if err != nil {
		t.Fatal(err)
	}
	destination := bundleKeys(t)
	_ = destination.Generate([]string{"fntv"})
	before, _ := destination.Material([]string{"fntv"})
	if err := b.ImportKeys(destination); err == nil {
		t.Fatal("conflicting import accepted")
	}
	after, _ := destination.Material([]string{"fntv"})
	status, _ := destination.Status()
	if before["fntv"] != after["fntv"] || status["emby"] {
		t.Fatal("partial import")
	}
}
func TestBundleRejectsUnsupportedInputs(t *testing.T) {
	s := bundleKeys(t)
	_ = s.Generate([]string{"fntv"})
	if _, err := ExportBundle(testConfig(), s, "test password long"); err == nil {
		t.Fatal("redirect export accepted")
	}
	if _, err := ExportBundle(bundleConfig(), s, "short"); err == nil {
		t.Fatal("weak password accepted")
	}
	if _, err := OpenBundle(make([]byte, bundleLimit+1), "test password long"); err == nil {
		t.Fatal("oversize accepted")
	}
	if _, err := OpenBundle([]byte(`{"version":2}`), "test password long"); err == nil {
		t.Fatal("unknown version accepted")
	}
}
