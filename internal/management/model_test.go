package management

import (
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{Version: 1, Mode: "redirect", Listen: Listener{Address: "0.0.0.0", Port: 28005}, Services: map[string]ServiceConfig{"fntv": {Enabled: true, Target: "http://192.0.2.1:8005"}}}
}

func TestConfigurationModes(t *testing.T) {
	for _, mode := range []string{"redirect", "relay", "single", "split"} {
		t.Run(mode, func(t *testing.T) {
			c := testConfig()
			c.Mode = mode
			if mode != "redirect" {
				c.PublicBaseURL = "https://media.example.com:49967"
			}
			if mode == "single" {
				c.MediaListen = &Listener{Port: 49967}
			}
			if mode == "split" {
				c.Role = "proxy"
			}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigurationRejections(t *testing.T) {
	tests := map[string]func(*Config){
		"version":            func(c *Config) { c.Version = 2 },
		"mode":               func(c *Config) { c.Mode = "all" },
		"role":               func(c *Config) { c.Role = "proxy" },
		"port":               func(c *Config) { c.Listen.Port = 65536 },
		"bind hostname":      func(c *Config) { c.Listen.Address = "localhost" },
		"unknown service":    func(c *Config) { c.Services["unknown"] = ServiceConfig{} },
		"none enabled":       func(c *Config) { c.Services["fntv"] = ServiceConfig{} },
		"target credentials": func(c *Config) { s := c.Services["fntv"]; s.Target = "http://user:secret@host"; c.Services["fntv"] = s },
		"target port":        func(c *Config) { s := c.Services["fntv"]; s.Target = "http://host:65536"; c.Services["fntv"] = s },
		"relative path": func(c *Config) {
			s := c.Services["fntv"]
			s.STRMDirectories = []DirectoryMapping{{"relative", "/media"}}
			c.Services["fntv"] = s
		},
		"traversal": func(c *Config) {
			s := c.Services["fntv"]
			s.STRMDirectories = []DirectoryMapping{{"/media/../other", "/media"}}
			c.Services["fntv"] = s
		},
		"source URL": func(c *Config) {
			s := c.Services["fntv"]
			s.AllowedUpstreams = []string{"http://host:80"}
			c.Services["fntv"] = s
		},
		"unsupported relay": func(c *Config) {
			c.Mode = "relay"
			c.PublicBaseURL = "https://entry.example.com"
			c.Services["emby"] = ServiceConfig{Enabled: true, Target: "http://host:8096"}
		},
		"single multi": func(c *Config) {
			c.Mode = "single"
			c.PublicBaseURL = "https://video.example.com"
			c.MediaListen = &Listener{Port: 49967}
			c.Services["emby"] = ServiceConfig{Enabled: true, Target: "http://host:8096"}
		},
		"media STRM": func(c *Config) {
			c.Mode = "split"
			c.Role = "media"
			c.PublicBaseURL = "https://video.example.com"
			c.Services["fntv"] = ServiceConfig{Enabled: true, STRMDirectories: []DirectoryMapping{{"/a", "/b"}}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := testConfig()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestDecodeManagementConfig(t *testing.T) {
	base := `{"version":1,"mode":"redirect","listen":{"address":"0.0.0.0","port":28005},"services":{"fntv":{"enabled":true,"target":"http://host:8005"}}}`
	if _, err := DecodeConfigJSON(strings.NewReader(base)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{base + base, strings.Replace(base, `"port":28005`, `"port":"28005"`, 1), strings.Replace(base, `"version":1`, `"version":1,"secret":"oops"`, 1)} {
		if _, err := DecodeConfigJSON(strings.NewReader(s)); err == nil {
			t.Fatal("expected strict JSON rejection")
		}
	}
	yaml := "version: 1\nmode: redirect\nlisten:\n  port: 28005\nservices:\n  fntv:\n    enabled: true\n    target: http://host:8005\n"
	if _, err := DecodeConfig(strings.NewReader(yaml)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{yaml + "unknown: value\n", yaml + "---\n" + yaml, strings.Replace(yaml, "mode: redirect", "mode: 123", 1), strings.Replace(yaml, "port: 28005", "port: \"28005\"", 1), strings.Replace(yaml, "enabled: true", "enabled: yes", 1)} {
		if _, err := DecodeConfig(strings.NewReader(s)); err == nil {
			t.Fatal("expected strict YAML rejection")
		}
	}
}

func TestDisabledServiceDoesNotRequirePaths(t *testing.T) {
	c := testConfig()
	c.Services["emby"] = ServiceConfig{STRMDirectories: []DirectoryMapping{{"", ""}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestIPv6Listener(t *testing.T) {
	l := Listener{Address: "::", Port: 49967}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	if l.String() != "[::]:49967" {
		t.Fatal(l.String())
	}
}
