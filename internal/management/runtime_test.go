package management

import (
	"fntv-proxy/internal/unified"
	"gopkg.in/yaml.v3"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeHelper(t *testing.T) {
	if os.Getenv("RUN_RUNTIME_HELPER") != "1" {
		return
	}
	raw, err := os.ReadFile(os.Getenv("CONFIG"))
	if err != nil {
		os.Exit(31)
	}
	var cfg struct {
		Listen string `yaml:"listen"`
		Target string `yaml:"target"`
	}
	if yaml.Unmarshal(raw, &cfg) != nil {
		os.Exit(32)
	}
	if cfg.Target == "http://fail.test" {
		os.Exit(33)
	}
	l, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		os.Exit(34)
	}
	for {
		c, e := l.Accept()
		if e != nil {
			os.Exit(35)
		}
		c.Close()
	}
}

func TestRuntimeApplyRollback(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	helper := filepath.Join(root, "helper")
	quoted := "'" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "'"
	if e = os.WriteFile(helper, []byte("#!/bin/sh\nRUN_RUNTIME_HELPER=1 exec "+quoted+" -test.run=^TestRuntimeHelper$\n"), 0700); e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	m, e := NewRuntimeManager(filepath.Join(root, "runtime"), helper, helper)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Stop()
	c := testConfig()
	c.Listen = Listener{Address: "127.0.0.1", Port: port}
	if e = m.Apply(c, RuntimeAssets{}); e != nil {
		t.Fatal(e)
	}
	old := m.current
	s := c.Services["fntv"]
	s.Target = "http://fail.test"
	c.Services["fntv"] = s
	if e = m.Apply(c, RuntimeAssets{}); e == nil {
		t.Fatal("failed generation accepted")
	}
	if !m.Status().Running || m.current != old {
		t.Fatal("previous generation not restored")
	}
	m.Stop()
	resumed, e := NewRuntimeManager(filepath.Join(root, "runtime"), helper, helper)
	if e != nil {
		t.Fatal(e)
	}
	defer resumed.Stop()
	if e = resumed.Resume(); e != nil {
		t.Fatal(e)
	}
	if !resumed.Status().Running {
		t.Fatal("last successful configuration not restored")
	}
}

func TestRuntimePrepareModes(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m, e := NewRuntimeManager(filepath.Join(root, "runtime"), "/not-started/legacy", "/not-started/unified")
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"redirect", "relay", "single", "split"} {
		t.Run(mode, func(t *testing.T) {
			c := testConfig()
			c.Mode = mode
			s := c.Services["fntv"]
			s.AllowedUpstreams = []string{"source.test:5244"}
			s.STRMDirectories = []DirectoryMapping{{Source: "/strm", Local: "/strm"}}
			c.Services["fntv"] = s
			if mode != "redirect" {
				c.PublicBaseURL = "http://media.example.com:49967"
			}
			if mode == "single" {
				c.MediaListen = &Listener{Port: 49967}
			}
			if mode == "split" {
				c.Role = "proxy"
				s.Hosts = []string{"fn.example.com"}
				c.Services["fntv"] = s
			}
			g, err := m.prepare(c, RuntimeAssets{Keys: map[string]string{"fntv": strings.Repeat("ab", 32)}})
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(g.dir, "config.yaml"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("configuration not private")
			}
			if mode == "split" {
				if _, err = unified.Load(filepath.Join(g.dir, "config.yaml")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRuntimeInvalidApplyPreservesState(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m, e := NewRuntimeManager(filepath.Join(root, "runtime"), "/missing-runtime", "/missing-runtime")
	if e != nil {
		t.Fatal(e)
	}
	if err := m.Apply(testConfig(), RuntimeAssets{}); err == nil {
		t.Fatal("missing binary accepted")
	}
	if m.Status().Running || m.current != nil {
		t.Fatal("failed runtime reported applied")
	}
	c := testConfig()
	s := c.Services["fntv"]
	s.STRMDirectories = []DirectoryMapping{{Source: "/source", Local: "/local"}}
	c.Services["fntv"] = s
	if _, err := m.prepare(c, RuntimeAssets{}); err != nil {
		t.Fatal("directory mapping rejected", err)
	}
}

func TestRuntimeTransportValidation(t *testing.T) {
	c := testConfig()
	c.Transport = "https"
	if c.Validate() == nil {
		t.Fatal("login TLS silently accepted")
	}
	c.Transport = "ftp"
	if c.Validate() == nil {
		t.Fatal("invalid transport")
	}
}

func TestRelayRelativePublicAddress(t *testing.T) {
	c := testConfig()
	c.Mode = "relay"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeStandaloneEmby(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m, e := NewRuntimeManager(filepath.Join(root, "runtime"), "/not-started/legacy", "/not-started/unified")
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"emby", "jellyfin"} {
		for _, mode := range []string{"redirect", "single"} {
			c := testConfig()
			c.Mode = mode
			c.Services = map[string]ServiceConfig{id: {Enabled: true, Target: "http://localhost:8096", AllowedUpstreams: []string{"source.test"}, STRMDirectories: []DirectoryMapping{{Source: "/strm", Local: "/strm"}}}}
			if mode == "single" {
				c.MediaListen = &Listener{Port: 49967}
				c.PublicBaseURL = "http://media.test:49967"
			}
			g, e := m.prepare(c, RuntimeAssets{Keys: map[string]string{id: strings.Repeat("ab", 32)}})
			if e != nil {
				t.Fatal(e)
			}
			raw, e := os.ReadFile(filepath.Join(g.dir, "config.yaml"))
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(raw), "fntv_enabled: false") {
				t.Fatal("FNTV listener unintentionally enabled")
			}
		}
	}
}
