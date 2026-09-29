package management

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the versioned management document, NOT either runtime's YAML format.
// It contains references/settings only; private keys must stay in managed storage.
type Config struct {
	Version       int                      `json:"version" yaml:"version"`
	Mode          string                   `json:"mode" yaml:"mode"`
	Transport     string                   `json:"transport,omitempty" yaml:"transport,omitempty"`
	Role          string                   `json:"role,omitempty" yaml:"role,omitempty"`
	Listen        Listener                 `json:"listen" yaml:"listen"`
	MediaListen   *Listener                `json:"media_listen,omitempty" yaml:"media_listen,omitempty"`
	PublicBaseURL string                   `json:"public_base_url,omitempty" yaml:"public_base_url,omitempty"`
	Services      map[string]ServiceConfig `json:"services" yaml:"services"`
}

type Listener struct {
	Address string `json:"address" yaml:"address"`
	Port    int    `json:"port" yaml:"port"`
}

type ServiceConfig struct {
	Enabled          bool               `json:"enabled" yaml:"enabled"`
	Target           string             `json:"target,omitempty" yaml:"target,omitempty"`
	Hosts            []string           `json:"hosts,omitempty" yaml:"hosts,omitempty"`
	Listen           *Listener          `json:"listen,omitempty" yaml:"listen,omitempty"`
	AllowedUpstreams []string           `json:"allowed_upstreams,omitempty" yaml:"allowed_upstreams,omitempty"`
	STRMDirectories  []DirectoryMapping `json:"strm_directories,omitempty" yaml:"strm_directories,omitempty"`
}

type DirectoryMapping struct {
	Source string `json:"source" yaml:"source"`
	Local  string `json:"local" yaml:"local"`
}

func (l Listener) Validate() error {
	if l.Port < 1 || l.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if l.Address != "" && net.ParseIP(l.Address) == nil {
		return fmt.Errorf("bind address must be an IP address (empty means all interfaces)")
	}
	return nil
}

func (l Listener) String() string { return net.JoinHostPort(l.Address, strconv.Itoa(l.Port)) }

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\t ") {
		return false
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}

func validDirectory(s string) bool {
	return strings.HasPrefix(s, "/") && s != "/" && path.Clean(s) == s && !strings.ContainsAny(s, "\\\x00\r\n")
}

func validHost(s string) bool {
	if s == "" || strings.ContainsAny(s, "/@?#\\* \t\r\n") {
		return false
	}
	u, e := url.Parse("http://" + s)
	return e == nil && u.Host == s && validURL("http://"+s)
}

// Validate gates currently supported combinations, not future planned capabilities.
// It deliberately does not promise reachability, mount availability or playback.
func (c Config) Validate() error {
	if c.Transport != "" && c.Transport != "http" && c.Transport != "https" {
		return fmt.Errorf("transport must be http or https")
	}
	if c.Transport == "https" && c.Mode != "single" && !(c.Mode == "split" && c.Role == "media") {
		return fmt.Errorf("HTTPS listener is only available for the media service; terminate login TLS at a reverse proxy")
	}
	if c.Version != 1 {
		return fmt.Errorf("unsupported management configuration version")
	}
	if c.Mode != "redirect" && c.Mode != "relay" && c.Mode != "single" && c.Mode != "split" {
		return fmt.Errorf("mode must be redirect, relay, single or split")
	}
	if c.Mode == "split" {
		if c.Role != "proxy" && c.Role != "media" {
			return fmt.Errorf("split mode requires proxy or media role")
		}
	} else if c.Role != "" {
		return fmt.Errorf("role is only applicable to split mode")
	}
	if err := c.Listen.Validate(); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if c.Mode == "single" {
		if c.MediaListen == nil {
			return fmt.Errorf("single mode requires media_listen")
		}
		if err := c.MediaListen.Validate(); err != nil {
			return fmt.Errorf("media_listen: %w", err)
		}
		if c.Listen.Port == c.MediaListen.Port {
			return fmt.Errorf("main and media listener ports must differ")
		}
	} else if c.MediaListen != nil {
		return fmt.Errorf("media_listen is only applicable to single mode")
	}
	if c.Mode != "redirect" && !(c.Mode == "relay" && c.PublicBaseURL == "") && !validURL(c.PublicBaseURL) {
		return fmt.Errorf("public_base_url must be an HTTP(S) URL without credentials, query or fragment")
	}
	if c.Mode == "redirect" && c.PublicBaseURL != "" {
		return fmt.Errorf("redirect mode does not use public_base_url")
	}
	count := 0
	hosts := map[string]bool{}
	ports := map[int]bool{c.Listen.Port: true}
	if c.MediaListen != nil {
		ports[c.MediaListen.Port] = true
	}
	for id, s := range c.Services {
		if !validService(id) {
			return fmt.Errorf("unknown service %q", id)
		}
		if !s.Enabled {
			continue
		}
		count++
		if c.Mode == "relay" && id != "fntv" {
			return fmt.Errorf("%s: same-origin relay is not supported by the current runtime", id)
		}
		if c.Role == "media" {
			if s.Target != "" || len(s.Hosts) > 0 || s.Listen != nil || len(s.STRMDirectories) > 0 {
				return fmt.Errorf("%s: media role does not accept login targets, hosts, service listeners or STRM directories", id)
			}
		} else {
			if !validURL(s.Target) {
				return fmt.Errorf("%s: target must be an HTTP(S) URL without credentials, query or fragment", id)
			}
			for _, h := range s.Hosts {
				if !validHost(h) {
					return fmt.Errorf("%s: invalid login host", id)
				}
				u, _ := url.Parse("http://" + h)
				key := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
				if hosts[key] {
					return fmt.Errorf("duplicate login host %q", key)
				}
				hosts[key] = true
			}
			if s.Listen != nil {
				if err := s.Listen.Validate(); err != nil {
					return fmt.Errorf("%s listen: %w", id, err)
				}
				if ports[s.Listen.Port] {
					return fmt.Errorf("%s: listener port is already used", id)
				}
				ports[s.Listen.Port] = true
			}
			seen := map[string]bool{}
			for _, d := range s.STRMDirectories {
				if !validDirectory(d.Source) || !validDirectory(d.Local) {
					return fmt.Errorf("%s: STRM directories must be normalized absolute paths, not filesystem root", id)
				}
				if seen[d.Source] {
					return fmt.Errorf("%s: duplicate STRM source directory", id)
				}
				seen[d.Source] = true
			}
		}
		for _, h := range s.AllowedUpstreams {
			if !validHost(h) {
				return fmt.Errorf("%s: video sources must be host or host:port, not URLs", id)
			}
		}
	}
	if count == 0 {
		return fmt.Errorf("at least one service must be enabled")
	}
	if c.Mode == "single" && count > 1 {
		return fmt.Errorf("single-container multi-service delivery is not yet verified; select one service or split mode")
	}
	return nil
}

func DecodeConfig(r io.Reader) (*Config, error) {
	// YAML normally coerces numeric scalars into strings. Management input uses
	// exact field types so malformed imports do not silently change meaning.
	raw, err := io.ReadAll(io.LimitReader(r, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 1024*1024 {
		return nil, fmt.Errorf("configuration exceeds 1 MiB")
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	if len(node.Content) != 1 {
		return nil, fmt.Errorf("configuration must be an object")
	}
	if err := strictYAML(node.Content[0], reflect.TypeOf(Config{})); err != nil {
		return nil, err
	}
	d := yaml.NewDecoder(strings.NewReader(string(raw)))
	d.KnownFields(true)
	var c Config
	if err := d.Decode(&c); err != nil {
		return nil, err
	}
	var extra interface{}
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one configuration document")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func strictYAML(n *yaml.Node, t reflect.Type) error {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	bad := func() error { return fmt.Errorf("line %d: configuration value has incorrect type", n.Line) }
	switch t.Kind() {
	case reflect.Struct:
		if n.Kind != yaml.MappingNode {
			return bad()
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			fields[strings.Split(f.Tag.Get("yaml"), ",")[0]] = f.Type
		}
		for i := 0; i < len(n.Content); i += 2 {
			if ft, ok := fields[n.Content[i].Value]; ok {
				if err := strictYAML(n.Content[i+1], ft); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			return bad()
		}
		for i := 0; i < len(n.Content); i += 2 {
			if n.Content[i].Tag != "!!str" {
				return bad()
			}
			if err := strictYAML(n.Content[i+1], t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			return bad()
		}
		for _, v := range n.Content {
			if err := strictYAML(v, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.String:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
			return bad()
		}
	case reflect.Bool:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
			return bad()
		}
	case reflect.Int:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!int" {
			return bad()
		}
	}
	return nil
}

func DecodeConfigJSON(r io.Reader) (*Config, error) {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	var c Config
	if err := d.Decode(&c); err != nil {
		return nil, err
	}
	var extra interface{}
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one configuration document")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}
