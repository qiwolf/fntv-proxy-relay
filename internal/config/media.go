package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MediaConfig describes the independent, authenticated media endpoint.
type MediaConfig struct {
	Listen          string   `mapstructure:"listen"`
	PublicBaseURL   string   `mapstructure:"public_base_url"`
	TokenKey        string   `mapstructure:"token_key"`
	TokenKeyFile    string   `mapstructure:"token_key_file"`
	StateDir        string   `mapstructure:"state_dir"`
	TokenTTLSeconds int      `mapstructure:"token_ttl_seconds"`
	TLSCertFile     string   `mapstructure:"tls_cert_file"`
	TLSKeyFile      string   `mapstructure:"tls_key_file"`
	AllowHTTP       bool     `mapstructure:"allow_http"`
	AllowedOrigins  []string `mapstructure:"allowed_origins"`
}

func (c *Config) GetRole() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	value := strings.ToLower(strings.TrimSpace(c.Role))
	if value == "" {
		return "proxy"
	}
	return value
}

func (c *Config) GetDeliveryMode() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	value := strings.ToLower(strings.TrimSpace(c.DeliveryMode))
	if value == "" {
		return "proxy"
	}
	return value
}

func (c *Config) GetMedia() MediaConfig {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	m := c.Media
	if m.StateDir == "" {
		m.StateDir = "./data/media"
	}
	if m.Listen == "" {
		m.Listen = ":49963"
	}
	if m.TokenTTLSeconds == 0 {
		m.TokenTTLSeconds = 3600
	}
	m.PublicBaseURL = strings.TrimRight(strings.TrimSpace(m.PublicBaseURL), "/")
	m.AllowedOrigins = append([]string(nil), m.AllowedOrigins...)
	return m
}

// ResolveKeyBytes loads explicit keys or atomically creates a persistent local key.
// Independent issuer/media deployments must share a key; generated keys are local.
func (m MediaConfig) ResolveKeyBytes() ([]byte, error) {
	if m.TokenKey != "" || m.TokenKeyFile != "" {
		return m.KeyBytes()
	}
	dir := m.StateDir
	if dir == "" {
		dir = "./data/media"
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create media state directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("media state directory must be a private directory (use mode 0700)")
	}
	path := filepath.Join(dir, "token.key")
	read := func() ([]byte, error) {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("cannot read generated media token key file")
		}
		return (MediaConfig{TokenKeyFile: path}).KeyBytes()
	}
	if _, err := os.Lstat(path); err == nil {
		return read()
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot inspect generated media token key file")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("cannot generate media token key")
	}
	f, err := os.CreateTemp(dir, ".token-key-*")
	if err != nil {
		return nil, fmt.Errorf("cannot create media token key file")
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot write media token key file")
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot persist media token key file")
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("cannot close media token key file")
	}
	// Linking a fully written temporary file prevents partial reads and never
	// overwrites a key published by another process racing this startup.
	if err := os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("cannot publish media token key file")
	}
	d, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot persist media state directory")
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil || closeErr != nil {
		return nil, fmt.Errorf("cannot persist media state directory")
	}
	return read()
}

// KeyBytes never includes key material or filesystem errors in returned errors.
func (m MediaConfig) KeyBytes() ([]byte, error) {
	if m.TokenKey != "" && m.TokenKeyFile != "" {
		return nil, fmt.Errorf("media.token_key and media.token_key_file are mutually exclusive")
	}
	value := strings.TrimSpace(m.TokenKey)
	if m.TokenKeyFile != "" {
		info, err := os.Stat(m.TokenKeyFile)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("cannot read media token key file")
		}
		if info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("media token key file must not be accessible by group or others (use mode 0600)")
		}
		data, err := os.ReadFile(m.TokenKeyFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read media token key file")
		}
		value = strings.TrimSpace(string(data))
	}
	key, err := hex.DecodeString(value)
	if err != nil || len(value) != 64 || len(key) != 32 {
		return nil, fmt.Errorf("media token key must be exactly 64 hexadecimal characters")
	}
	return key, nil
}

func validateOrigin(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || strings.Contains(raw, "*") {
		return fmt.Errorf("must be an absolute origin without path, credentials, query or fragment")
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return fmt.Errorf("must use HTTPS (HTTP requires media.allow_http)")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid port")
		}
	}
	return nil
}

func (c *Config) validateMedia() error {
	role, delivery := c.GetRole(), c.GetDeliveryMode()
	if role != "proxy" && role != "media" && role != "all" {
		return fmt.Errorf("role must be proxy, media or all")
	}
	if delivery != "proxy" && delivery != "direct" {
		return fmt.Errorf("delivery_mode must be proxy or direct")
	}
	if role == "media" && c.Emby.Enabled {
		return fmt.Errorf("emby cannot be enabled with role media")
	}
	if role == "all" && c.GetStreamMode() != "relay" {
		return fmt.Errorf("role all requires stream_mode relay")
	}
	if delivery == "direct" && (role == "media" || c.GetStreamMode() != "relay") {
		return fmt.Errorf("direct delivery requires role proxy/all and stream_mode relay")
	}
	if role == "proxy" && delivery == "proxy" {
		return nil
	}
	m := c.GetMedia()
	if m.TokenKey != "" || m.TokenKeyFile != "" {
		if _, err := m.KeyBytes(); err != nil {
			return err
		}
	}
	if m.TokenTTLSeconds <= 0 || m.TokenTTLSeconds > 86400 {
		return fmt.Errorf("media.token_ttl_seconds must be between 1 and 86400")
	}
	if len(c.AllowedUpstreams) == 0 {
		return fmt.Errorf("allowed_upstreams is required for media delivery")
	}
	if delivery == "direct" {
		if err := validateOrigin(strings.TrimSpace(c.Media.PublicBaseURL), m.AllowHTTP); err != nil {
			return fmt.Errorf("media.public_base_url: %w", err)
		}
	}
	for _, origin := range m.AllowedOrigins {
		if err := validateOrigin(origin, m.AllowHTTP); err != nil {
			return fmt.Errorf("media.allowed_origins: %w", err)
		}
	}
	if role == "media" || role == "all" {
		_, port, err := net.SplitHostPort(m.Listen)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 1 || n > 65535 {
			return fmt.Errorf("media.listen must be a valid host:port")
		}
		if (m.TLSCertFile == "") != (m.TLSKeyFile == "") {
			return fmt.Errorf("media TLS certificate and key must be configured together")
		}
		if m.TLSCertFile == "" && !m.AllowHTTP {
			return fmt.Errorf("media listener requires TLS or explicit media.allow_http")
		}
	}
	return nil
}
