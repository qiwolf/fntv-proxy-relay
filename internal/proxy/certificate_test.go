package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func reloadTestPair(t *testing.T, serial int64, expiry time.Time) ([]byte, []byte, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-24 * time.Hour), NotAfter: expiry, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), cert
}

func writeReloadTestFile(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := os.WriteFile(path, value, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCertificateReloadTLSHandshake(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	cert1, key1, leaf1 := reloadTestPair(t, 1, time.Now().Add(time.Hour))
	cert2, key2, leaf2 := reloadTestPair(t, 2, time.Now().Add(time.Hour))
	writeReloadTestFile(t, certPath, cert1)
	writeReloadTestFile(t, keyPath, key1)
	r, err := newCertificateReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.interval = 0
	s := httptest.NewUnstartedServer(http.NotFoundHandler())
	s.TLS = &tls.Config{GetCertificate: r.GetCertificate, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	defer s.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf1)
	roots.AddCert(leaf2)
	check := func(want int64) {
		t.Helper()
		conn, err := tls.Dial("tcp", s.Listener.Addr().String(), &tls.Config{ServerName: "localhost", RootCAs: roots, MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if got := conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64(); got != want {
			t.Fatalf("certificate serial=%d, want %d", got, want)
		}
	}
	check(1)
	writeReloadTestFile(t, certPath, cert2)
	check(1) // Independently replaced certificate must not publish a mismatched pair.
	writeReloadTestFile(t, keyPath, key2)
	check(2) // Same listener, new handshake, new valid certificate.
	writeReloadTestFile(t, certPath, []byte("broken PEM"))
	check(2)
	if err := os.Remove(certPath); err != nil {
		t.Fatal(err)
	}
	check(2)
	expiredCert, expiredKey, _ := reloadTestPair(t, 3, time.Now().Add(-time.Hour))
	writeReloadTestFile(t, certPath, expiredCert)
	writeReloadTestFile(t, keyPath, expiredKey)
	check(2)
}

func TestCertificateReloadStartupRejectsInvalid(t *testing.T) {
	for _, kind := range []string{"missing", "malformed", "mismatch", "expired", "not-yet-valid"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
			cert, key, _ := reloadTestPair(t, 1, time.Now().Add(time.Hour))
			switch kind {
			case "missing":
				if _, err := newCertificateReloader(certPath, keyPath, nil); err == nil {
					t.Fatal("accepted missing files")
				}
				return
			case "malformed":
				cert = []byte("invalid")
			case "mismatch":
				_, key, _ = reloadTestPair(t, 2, time.Now().Add(time.Hour))
			case "expired":
				cert, key, _ = reloadTestPair(t, 3, time.Now().Add(-time.Hour))
			case "not-yet-valid":
				// Exercise future validity through reload's explicit clock without sleeps.
				writeReloadTestFile(t, certPath, cert)
				writeReloadTestFile(t, keyPath, key)
				r := &certificateReloader{certPath: certPath, keyPath: keyPath}
				if err := r.reload(time.Now().Add(-48 * time.Hour)); err == nil {
					t.Fatal("accepted future certificate")
				}
				return
			}
			writeReloadTestFile(t, certPath, cert)
			writeReloadTestFile(t, keyPath, key)
			if _, err := newCertificateReloader(certPath, keyPath, nil); err == nil {
				t.Fatal("accepted invalid certificate")
			}
		})
	}
}

func TestCertificateReloadConcurrentAndExpiredCurrent(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	cert, key, _ := reloadTestPair(t, 1, time.Now().Add(time.Hour))
	writeReloadTestFile(t, certPath, cert)
	writeReloadTestFile(t, keyPath, key)
	r, err := newCertificateReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.interval = 0
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := r.GetCertificate(nil); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	r.current.Leaf.NotAfter = time.Now().Add(-time.Second)
	if _, err := r.GetCertificate(nil); err == nil {
		t.Fatal("served expired cached certificate")
	}
}
