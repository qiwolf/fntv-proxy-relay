package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fntv-proxy/internal/config"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMediaListenerTLSAndShutdown(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "encrypted transport") }))
	defer source.Close()
	u, _ := url.Parse(source.URL)
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certServer.TLS.Certificates[0]
	certServer.Close()
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	cfg := &config.Config{Role: "media", LogLevel: "error", CacheTTL: time.Minute, AllowedUpstreams: []string{u.Host}, Media: config.MediaConfig{Listen: addr, TokenKey: strings.Repeat("ab", 32), TokenTTLSeconds: 60, TLSCertFile: certPath, TLSKeyFile: keyPath}}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	defer func() {
		s.Stop()
		select {
		case err := <-done:
			if err != http.ErrServerClosed {
				t.Errorf("shutdown: %v", err)
			}
		case <-time.After(6 * time.Second):
			t.Error("server did not stop")
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		ready := s.started
		s.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TLS listener failed to start")
		}
		time.Sleep(time.Millisecond)
	}
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(parsed)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	ticket, err := s.mediaHandler.IssueURL(source.URL + "/movie")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("https://" + addr + ticket)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || string(body) != "encrypted transport" {
		t.Fatalf("TLS response %d %q %v", resp.StatusCode, body, err)
	}
	if s.httpServer != nil {
		t.Fatal("media role unexpectedly created web listener")
	}
	if resp, err := client.Get("https://" + addr + "/"); err == nil {
		resp.Body.Close()
		t.Fatal("unauthenticated TLS request got HTTP response")
	}
}
