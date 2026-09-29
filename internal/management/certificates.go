package management

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

// CertificateStore stores each certificate and key together in a private atomic
// document. It must have its own directory, never the configuration directory.
type CertificateStore struct{ documents *DocumentStore }

type CertificateStatus struct {
	Version           uint64    `json:"version"`
	Configured        bool      `json:"configured"`
	Domains           []string  `json:"domains,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitempty"`
	FingerprintSHA256 string    `json:"fingerprint_sha256,omitempty"`
}

type certificateDocument struct {
	CertificatePEM []byte `json:"certificate_pem"`
	PrivateKeyPEM  []byte `json:"private_key_pem"`
	PublicURL      string `json:"public_url"`
}

func NewCertificateStore(dir string) (*CertificateStore, error) {
	d, err := NewDocumentStore(dir, 2<<20)
	if err != nil {
		return nil, err
	}
	return &CertificateStore{documents: d}, nil
}

// inspectCertificate checks identity and dates, not public CA trust or live TLS.
func inspectCertificate(certPEM, keyPEM []byte, publicURL string, checkDates bool) (CertificateStatus, error) {
	var status CertificateStatus
	if len(certPEM) == 0 || len(keyPEM) == 0 || len(certPEM) > 512<<10 || len(keyPEM) > 64<<10 {
		return status, errors.New("certificate and private key are required within size limits")
	}
	if !validURL(publicURL) {
		return status, errors.New("certificate public URL is invalid")
	}
	u, _ := url.Parse(publicURL)
	if u.Scheme != "https" {
		return status, errors.New("certificate requires an HTTPS public URL")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return status, errors.New("certificate or private key is invalid or does not match")
	}
	now := time.Now()
	for i, der := range pair.Certificate {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return status, errors.New("certificate chain is invalid")
		}
		if checkDates && (now.Before(cert.NotBefore) || !now.Before(cert.NotAfter)) {
			return status, errors.New("certificate chain contains an expired or not-yet-valid certificate")
		}
		if i == 0 {
			if cert.VerifyHostname(u.Hostname()) != nil {
				return status, errors.New("certificate does not cover the public URL hostname")
			}
			status.Configured = true
			status.Domains = append([]string(nil), cert.DNSNames...)
			for _, ip := range cert.IPAddresses {
				status.Domains = append(status.Domains, ip.String())
			}
			status.ExpiresAt = cert.NotAfter
			digest := sha256.Sum256(der)
			status.FingerprintSHA256 = hex.EncodeToString(digest[:])
		}
	}
	return status, nil
}

// Import validates completely before writing. Version conflicts and validation
// errors leave the previously stored pair untouched; no key material is returned.
func (s *CertificateStore) Import(expectedVersion uint64, certPEM, keyPEM []byte, publicURL string) (CertificateStatus, error) {
	status, err := inspectCertificate(certPEM, keyPEM, publicURL, true)
	if err != nil {
		return CertificateStatus{}, err
	}
	raw, err := json.Marshal(certificateDocument{certPEM, keyPEM, publicURL})
	if err != nil {
		return CertificateStatus{}, errors.New("could not encode certificate")
	}
	d, err := s.documents.Save(expectedVersion, raw)
	if err != nil {
		return CertificateStatus{}, err
	}
	status.Version = d.Version
	return status, nil
}

func (s *CertificateStore) Status() (CertificateStatus, error) {
	d, err := s.documents.Read()
	if errors.Is(err, ErrDocumentNotFound) {
		return CertificateStatus{}, nil
	}
	if err != nil {
		return CertificateStatus{}, errors.New("could not read certificate storage")
	}
	var stored certificateDocument
	if json.Unmarshal(d.Data, &stored) != nil {
		return CertificateStatus{}, errors.New("certificate storage is invalid")
	}
	// Expired certificates remain inspectable so users can replace them.
	status, err := inspectCertificate(stored.CertificatePEM, stored.PrivateKeyPEM, stored.PublicURL, false)
	if err != nil {
		return CertificateStatus{}, errors.New("certificate storage is invalid")
	}
	status.Version = d.Version
	return status, nil
}

// Material is internal-only; never expose this pair through HTTP responses.
func (s *CertificateStore) Material() ([]byte, []byte, error) {
	d, err := s.documents.Read()
	if err != nil {
		return nil, nil, err
	}
	var stored certificateDocument
	if json.Unmarshal(d.Data, &stored) != nil {
		return nil, nil, errors.New("certificate storage is invalid")
	}
	if _, err := inspectCertificate(stored.CertificatePEM, stored.PrivateKeyPEM, stored.PublicURL, true); err != nil {
		return nil, nil, err
	}
	return stored.CertificatePEM, stored.PrivateKeyPEM, nil
}
