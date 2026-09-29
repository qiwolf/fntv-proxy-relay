package management

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	legacy "fntv-proxy/internal/config"
	"fntv-proxy/internal/unified"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

// RuntimeAssets never travels in HTTP responses or logs.
type RuntimeAssets struct {
	Keys                          map[string]string
	CertificatePEM, PrivateKeyPEM []byte
}
type RuntimeStatus struct {
	Running   bool      `json:"running"`
	Mode      string    `json:"mode,omitempty"`
	AppliedAt time.Time `json:"applied_at,omitempty"`
}
type runtimeGeneration struct {
	dir, binary string
	listeners   []string
	mode        string
}
type RuntimeManager struct {
	mu                               sync.Mutex
	dir, legacyBinary, unifiedBinary string
	current                          *runtimeGeneration
	cmd                              *exec.Cmd
	done                             chan struct{}
	appliedAt                        time.Time
	active                           *DocumentStore
}

func NewRuntimeManager(dir, legacyBinary, unifiedBinary string) (*RuntimeManager, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("runtime directory must be absolute")
	}
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	for _, p := range []string{legacyBinary, unifiedBinary} {
		if p != "" && !filepath.IsAbs(p) {
			return nil, errors.New("runtime binary must be absolute")
		}
	}
	active, err := NewDocumentStore(filepath.Join(dir, "active"), 4<<20)
	if err != nil {
		return nil, err
	}
	return &RuntimeManager{dir: dir, legacyBinary: legacyBinary, unifiedBinary: unifiedBinary, active: active}, nil
}

// prepare creates immutable generations so a failed application cannot overwrite
// the previous process's keys, certificate or YAML.
func (m *RuntimeManager) prepare(c Config, a RuntimeAssets) (generation *runtimeGeneration, retErr error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(m.dir, "generation-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	g := &runtimeGeneration{dir: dir, binary: m.legacyBinary, mode: c.Mode, listeners: []string{c.Listen.String()}}
	write := func(name string, b []byte) (string, error) {
		p := filepath.Join(dir, name)
		return p, os.WriteFile(p, b, 0600)
	}
	keys := map[string]string{}
	if c.Mode == "single" || c.Mode == "split" {
		for id, s := range c.Services {
			if !s.Enabled {
				continue
			}
			b, e := hex.DecodeString(a.Keys[id])
			if e != nil || len(b) != 32 {
				return nil, fmt.Errorf("%s: shared key missing or invalid", id)
			}
			keys[id], err = write(id+".key", []byte(a.Keys[id]))
			if err != nil {
				return nil, err
			}
		}
	}
	cert, key := "", ""
	localTLS := c.Transport == "https"
	if localTLS {
		if _, err = inspectCertificate(a.CertificatePEM, a.PrivateKeyPEM, c.PublicBaseURL, true); err != nil {
			return nil, err
		}
		cert, err = write("certificate.pem", a.CertificatePEM)
		if err != nil {
			return nil, err
		}
		key, err = write("private-key.pem", a.PrivateKeyPEM)
		if err != nil {
			return nil, err
		}
	}
	var raw []byte
	roots := func(s ServiceConfig) []string {
		r := []string{}
		for _, p := range s.STRMDirectories {
			r = append(r, p.Local)
		}
		return r
	}
	mappings := func(s ServiceConfig) []legacy.STRMDirectoryMapping {
		var out []legacy.STRMDirectoryMapping
		for _, d := range s.STRMDirectories {
			out = append(out, legacy.STRMDirectoryMapping{Source: d.Source, Local: d.Local})
		}
		return out
	}
	if c.Mode == "split" {
		g.binary = m.unifiedBinary
		u := unified.Config{Role: c.Role, Listen: c.Listen.String(), AllowHTTP: !localTLS, TLSCertFile: cert, TLSKeyFile: key, MediaPublicBaseURL: c.PublicBaseURL, Services: map[string]unified.Service{}}
		for id, s := range c.Services {
			if !s.Enabled {
				continue
			}
			v := unified.Service{Type: id, Hosts: s.Hosts, Target: s.Target, TokenKeyFile: keys[id], AllowedUpstreams: s.AllowedUpstreams, AllowedStrmRoots: roots(s), STRMDirectoryMap: mappings(s), TokenTTLSeconds: 3600}
			if s.Listen != nil {
				v.DirectListen = s.Listen.String()
				g.listeners = append(g.listeners, v.DirectListen)
			}
			u.Services[id] = v
		}
		if err = u.Validate(); err != nil {
			return nil, err
		}
		raw, err = yaml.Marshal(u)
	} else {
		f := c.Services["fntv"]
		primaryID := "fntv"
		if !f.Enabled {
			for _, id := range []string{"emby", "jellyfin"} {
				if c.Services[id].Enabled {
					primaryID = id
					f = c.Services[id]
					break
				}
			}
		}
		if len(f.Hosts) > 0 {
			return nil, errors.New("hostname routing requires split mode")
		}
		if f.Listen != nil {
			return nil, errors.New("FNTV uses the main listener in this mode")
		}
		v := map[string]interface{}{"fntv_enabled": c.Services["fntv"].Enabled, "role": "proxy", "listen": c.Listen.String(), "target": f.Target, "stream_mode": "redirect", "delivery_mode": "proxy", "log_level": "info", "log_dir": filepath.Join(dir, "logs"), "cache_ttl": 60, "allowed_upstreams": f.AllowedUpstreams, "allowed_strm_roots": roots(f), "public_base_url": c.PublicBaseURL}
		v["strm_directory_map"] = mappings(f)
		if c.Mode != "redirect" {
			v["stream_mode"] = "relay"
		}
		if c.Mode == "single" {
			v["role"] = "all"
			v["delivery_mode"] = "direct"
			v["media"] = map[string]interface{}{"listen": c.MediaListen.String(), "public_base_url": c.PublicBaseURL, "token_key_file": keys[primaryID], "allow_http": !localTLS, "tls_cert_file": cert, "tls_key_file": key, "token_ttl_seconds": 3600}
			g.listeners = append(g.listeners, c.MediaListen.String())
		}
		for _, id := range []string{"emby", "jellyfin"} {
			s := c.Services[id]
			if !s.Enabled {
				continue
			}
			if s.Listen == nil && id != primaryID {
				return nil, fmt.Errorf("%s requires its own listener", id)
			}
			if id != primaryID && len(s.STRMDirectories) > 0 && !reflect.DeepEqual(s.STRMDirectories, f.STRMDirectories) {
				return nil, errors.New("legacy multi-service mode shares STRM directories; use identical mappings or split mode")
			}
			if len(s.Hosts) > 0 {
				return nil, errors.New("hostname routing requires split mode")
			}
			listen := c.Listen.String()
			if id != primaryID {
				listen = s.Listen.String()
				g.listeners = append(g.listeners, listen)
			}
			delivery := "redirect"
			if c.Mode == "single" {
				delivery = "direct"
			}
			v[id] = map[string]interface{}{"enabled": true, "listen": listen, "target": s.Target, "delivery_mode": delivery}
		}
		raw, err = yaml.Marshal(v)
		if err == nil {
			p := viper.New()
			p.SetConfigType("yaml")
			err = p.ReadConfig(bytes.NewReader(raw))
			if err == nil {
				var l legacy.Config
				err = p.Unmarshal(&l)
				if err == nil {
					err = l.Validate()
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if g.binary == "" {
		return nil, errors.New("runtime binary is not configured")
	}
	_, err = write("config.yaml", raw)
	return g, err
}

func (m *RuntimeManager) start(g *runtimeGeneration) error {
	// Refuse occupied listeners before starting; prevents mistaking another process
	// for this generation's readiness.
	for _, addr := range g.listeners {
		l, e := net.Listen("tcp", addr)
		if e != nil {
			return errors.New("runtime listener is occupied or unavailable")
		}
		l.Close()
	}
	cmd := exec.Command(g.binary)
	cmd.Dir = g.dir
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CONFIG=") && !strings.HasPrefix(e, "FNTV_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "CONFIG="+filepath.Join(g.dir, "config.yaml"))
	// Child output may contain upstream credentials; never forward it to UI/logs.
	if err := cmd.Start(); err != nil {
		return errors.New("runtime process could not start")
	}
	m.cmd = cmd
	m.done = make(chan struct{})
	done := m.done
	go func() { _ = cmd.Wait(); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return errors.New("runtime exited during startup")
		default:
		}
		ready := true
		for _, a := range g.listeners {
			h, p, _ := net.SplitHostPort(a)
			if h == "" || h == "0.0.0.0" {
				h = "127.0.0.1"
			}
			if h == "::" {
				h = "::1"
			}
			conn, e := net.DialTimeout("tcp", net.JoinHostPort(h, p), 100*time.Millisecond)
			if e != nil {
				ready = false
			} else {
				conn.Close()
			}
		}
		if ready {
			time.Sleep(150 * time.Millisecond)
			select {
			case <-done:
				return errors.New("runtime exited during startup")
			default:
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.stop()
	return errors.New("runtime startup timed out")
}
func (m *RuntimeManager) stop() {
	if m.cmd == nil {
		return
	}
	_ = m.cmd.Process.Signal(os.Interrupt)
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
		_ = m.cmd.Process.Kill()
		<-m.done
	}
	m.cmd = nil
	m.done = nil
}
func (m *RuntimeManager) Stop() { m.mu.Lock(); defer m.mu.Unlock(); m.stop() }
func (m *RuntimeManager) Status() RuntimeStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := RuntimeStatus{AppliedAt: m.appliedAt}
	if m.current != nil {
		s.Mode = m.current.mode
	}
	if m.done != nil {
		select {
		case <-m.done:
		default:
			s.Running = true
		}
	}
	return s
}

// Apply verifies configuration before stopping the old runtime. Startup failure
// restarts its immutable previous generation. TCP readiness is not playback proof.
func (m *RuntimeManager) Apply(c Config, a RuntimeAssets) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, err := m.prepare(c, a)
	if err != nil {
		return err
	}
	old := m.current
	defer func() {
		if m.current != g {
			_ = os.RemoveAll(g.dir)
		}
	}()
	m.stop()
	if err = m.start(g); err != nil {
		m.stop()
		if old != nil {
			if e := m.start(old); e != nil {
				return errors.New("application failed and previous runtime could not restart")
			}
		}
		return err
	}
	snapshot, _ := json.Marshal(runtimeSnapshot{Config: c, Assets: a})
	doc, e := m.active.Read()
	if errors.Is(e, ErrDocumentNotFound) {
		e = nil
	}
	if e == nil {
		_, e = m.active.Save(doc.Version, snapshot)
	}
	if e != nil {
		m.stop()
		if old != nil {
			if restore := m.start(old); restore != nil {
				return errors.New("application persistence failed and previous runtime could not restart")
			}
		}
		return errors.New("could not persist active runtime; previous runtime restored")
	}
	m.current = g
	m.appliedAt = time.Now().UTC()
	if old != nil {
		_ = os.RemoveAll(old.dir)
	}
	return nil
}

type runtimeSnapshot struct {
	Config Config        `json:"config"`
	Assets RuntimeAssets `json:"assets"`
}

// Resume restores only the last successfully started and persisted runtime,
// never the editable configuration document.
func (m *RuntimeManager) Resume() error {
	doc, err := m.active.Read()
	if errors.Is(err, ErrDocumentNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var s runtimeSnapshot
	if err = json.Unmarshal(doc.Data, &s); err != nil {
		return errors.New("active runtime snapshot is invalid")
	}
	return m.Apply(s.Config, s.Assets)
}
