package management

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func certificateFixture(t *testing.T, name string, start, end time.Time) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, DNSNames: []string{name}, NotBefore: start, NotAfter: end, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	k, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k})
}

func TestCertificateImportAndStatus(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "certs")
	s, err := NewCertificateStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.Status()
	if err != nil || status.Configured {
		t.Fatal(status, err)
	}
	cert, key := certificateFixture(t, "video.example.com", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	status, err = s.Import(0, cert, key, "https://video.example.com:49967")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.Version != 1 || len(status.FingerprintSHA256) != 64 || len(status.Domains) != 1 {
		t.Fatal(status)
	}
	got, err := s.Status()
	if err != nil || got.FingerprintSHA256 != status.FingerprintSHA256 {
		t.Fatal(got, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "certificate_pem") {
		t.Fatal("secret response")
	}
	info, err := os.Stat(filepath.Join(dir, "current.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if _, err = s.Import(0, cert, key, "https://video.example.com"); err != ErrVersionConflict {
		t.Fatalf("expected version conflict, got %v", err)
	}
}

func TestCertificateFailurePreservesPrevious(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewCertificateStore(filepath.Join(root, "certs"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert, key := certificateFixture(t, "video.example.com", now.Add(-time.Hour), now.Add(time.Hour))
	old, err := s.Import(0, cert, key, "https://video.example.com")
	if err != nil {
		t.Fatal(err)
	}
	expired, expiredKey := certificateFixture(t, "video.example.com", now.Add(-2*time.Hour), now.Add(-time.Hour))
	future, futureKey := certificateFixture(t, "video.example.com", now.Add(time.Hour), now.Add(2*time.Hour))
	tests := []struct {
		name      string
		cert, key []byte
		url       string
	}{
		{"mismatch", cert, expiredKey, "https://video.example.com"},
		{"expired", expired, expiredKey, "https://video.example.com"},
		{"future", future, futureKey, "https://video.example.com"},
		{"hostname", cert, key, "https://other.example.com"},
		{"http", cert, key, "http://video.example.com"},
		{"invalid", []byte("bad"), key, "https://video.example.com"},
		{"missing", cert, nil, "https://video.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Import(1, tc.cert, tc.key, tc.url); err == nil {
				t.Fatal("expected rejection")
			}
			got, err := s.Status()
			if err != nil || got.Version != 1 || got.FingerprintSHA256 != old.FingerprintSHA256 {
				t.Fatal("old pair changed", got, err)
			}
		})
	}
}
