package proxy

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"sync"
	"time"
)

// certificateReloader checks the mounted directory at the next TLS handshake.
// Files may be replaced independently; only a complete valid pair is published.
// Existing TLS connections are never interrupted by certificate replacement.
type certificateReloader struct {
	mu                sync.Mutex
	certPath, keyPath string
	current           *tls.Certificate
	digest            [32]byte
	checked           time.Time
	interval          time.Duration
	warned            bool
	warn              func(string)
}

func newCertificateReloader(certPath, keyPath string, warn func(string)) (*certificateReloader, error) {
	r := &certificateReloader{certPath: certPath, keyPath: keyPath, interval: time.Second, warn: warn}
	if err := r.reload(time.Now()); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certificateReloader) reload(now time.Time) error {
	certPEM, err := os.ReadFile(r.certPath)
	if err != nil {
		return errors.New("cannot read media TLS certificate")
	}
	keyPEM, err := os.ReadFile(r.keyPath)
	if err != nil {
		return errors.New("cannot read media TLS private key")
	}
	hash := sha256.New()
	hash.Write(certPEM)
	hash.Write([]byte{0})
	hash.Write(keyPEM)
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	if r.current != nil && digest == r.digest {
		return nil
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return errors.New("media TLS certificate/key pair is invalid")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return errors.New("media TLS certificate is not currently valid")
	}
	pair.Leaf = leaf
	r.current = &pair
	r.digest = digest
	return nil
}

func (r *certificateReloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.checked) >= r.interval {
		r.checked = now
		if err := r.reload(now); err != nil {
			if !r.warned && r.warn != nil {
				r.warn("media TLS reload failed; retaining last valid certificate")
			}
			r.warned = true
		} else {
			r.warned = false
		}
	}
	if r.current == nil || now.Before(r.current.Leaf.NotBefore) || !now.Before(r.current.Leaf.NotAfter) {
		return nil, errors.New("no currently valid media TLS certificate")
	}
	return r.current, nil
}
