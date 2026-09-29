// Package unified provides opt-in, isolated multi-service listeners.
package unified

import (
	"fmt"
	legacy "fntv-proxy/internal/config"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Role               string             `yaml:"role"`
	Listen             string             `yaml:"listen"`
	TLSCertFile        string             `yaml:"tls_cert_file"`
	TLSKeyFile         string             `yaml:"tls_key_file"`
	AllowHTTP          bool               `yaml:"allow_http"`
	MediaPublicBaseURL string             `yaml:"media_public_base_url"`
	Services           map[string]Service `yaml:"services"`
}
type Service struct {
	DirectListen     string                        `yaml:"direct_listen"`
	Type             string                        `yaml:"type"`
	Hosts            []string                      `yaml:"hosts"`
	Target           string                        `yaml:"target"`
	TokenKeyFile     string                        `yaml:"token_key_file"`
	AllowedUpstreams []string                      `yaml:"allowed_upstreams"`
	AllowedStrmRoots []string                      `yaml:"allowed_strm_roots"`
	STRMDirectoryMap []legacy.STRMDirectoryMapping `yaml:"strm_directory_map"`
	AllowedOrigins   []string                      `yaml:"allowed_origins"`
	TokenTTLSeconds  int                           `yaml:"token_ttl_seconds"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var c Config
	if err = dec.Decode(&c); err != nil {
		return nil, err
	}
	var extra interface{}
	if err = dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("configuration must contain exactly one YAML document")
	}
	if err = c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

var serviceID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// hostKey intentionally ignores ports: one listener routes by explicit hostname.
func hostKey(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "/@?#\\ \t\r\n") {
		return "", fmt.Errorf("invalid host")
	}
	host := raw
	if strings.Contains(raw, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(raw)
		if err != nil {
			return "", fmt.Errorf("invalid host:port")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid host port")
		}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || strings.Contains(host, "*") {
		return "", fmt.Errorf("invalid host")
	}
	return host, nil
}

func (c *Config) Validate() error {
	if c.Role != "proxy" && c.Role != "media" {
		return fmt.Errorf("role must be proxy or media")
	}
	_, port, err := net.SplitHostPort(c.Listen)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || n < 1 || n > 65535 {
		return fmt.Errorf("listen must be host:port")
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return fmt.Errorf("TLS certificate and key are required together")
	}
	if c.TLSCertFile == "" && !c.AllowHTTP {
		return fmt.Errorf("TLS is required unless allow_http is explicitly enabled")
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("services is required")
	}
	hosts := map[string]string{}
	for id, s := range c.Services {
		if !serviceID.MatchString(id) {
			return fmt.Errorf("invalid service ID %q", id)
		}
		if s.Type != "fntv" && s.Type != "emby" && s.Type != "jellyfin" {
			return fmt.Errorf("service %s: unknown type", id)
		}
		if s.TokenKeyFile == "" {
			return fmt.Errorf("service %s: token_key_file is required", id)
		}
		if c.Role == "proxy" {
			if len(s.Hosts) == 0 && s.DirectListen == "" {
				return fmt.Errorf("service %s: hosts or direct_listen is required", id)
			}
			u, err := url.Parse(s.Target)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("service %s: target must be an HTTP(S) URL without credentials/query/fragment", id)
			}
		}
		if s.DirectListen != "" {
			if c.Role != "proxy" {
				return fmt.Errorf("service %s: direct_listen is proxy-only", id)
			}
			_, p, err := net.SplitHostPort(s.DirectListen)
			n, e := strconv.Atoi(p)
			if err != nil || e != nil || n < 1 || n > 65535 {
				return fmt.Errorf("service %s: invalid direct_listen", id)
			}
		}
		for _, h := range s.Hosts {
			k, err := hostKey(h)
			if err != nil {
				return fmt.Errorf("service %s: %w", id, err)
			}
			if owner, exists := hosts[k]; exists {
				return fmt.Errorf("duplicate host %s in %s and %s", k, owner, id)
			}
			hosts[k] = id
		}
		if err := c.legacy(id, s).Validate(); err != nil {
			return fmt.Errorf("service %s: %w", id, err)
		}
	}
	return nil
}

func (c *Config) legacy(id string, s Service) *legacy.Config {
	ttl := s.TokenTTLSeconds
	if ttl == 0 {
		ttl = 3600
	}
	cfg := &legacy.Config{Role: c.Role, ListenAddr: c.Listen, TargetAddr: s.Target, StreamMode: "relay", LogLevel: "info", LogDir: "./logs", CacheTTL: time.Hour, AllowedUpstreams: s.AllowedUpstreams, AllowedStrmRoots: s.AllowedStrmRoots,
		Media: legacy.MediaConfig{TenantID: id, Listen: c.Listen, PublicBaseURL: c.MediaPublicBaseURL, TokenKeyFile: s.TokenKeyFile, TokenTTLSeconds: ttl, AllowedOrigins: s.AllowedOrigins, AllowHTTP: c.AllowHTTP, TLSCertFile: c.TLSCertFile, TLSKeyFile: c.TLSKeyFile}}
	cfg.STRMDirectoryMap = append([]legacy.STRMDirectoryMapping(nil), s.STRMDirectoryMap...)
	if c.Role == "proxy" {
		cfg.DeliveryMode = "direct"
		if s.Type == "emby" {
			cfg.Emby = legacy.EmbyConfig{Enabled: true, DeliveryMode: "direct", TargetAddr: s.Target}
		}
		if s.Type == "jellyfin" {
			cfg.Jellyfin = legacy.EmbyConfig{Enabled: true, DeliveryMode: "direct", TargetAddr: s.Target}
		}
	}
	return cfg
}
