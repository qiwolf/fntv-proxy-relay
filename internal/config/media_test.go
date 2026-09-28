package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/viper"
)

func directConfig() *Config {
	return &Config{Role: "all", DeliveryMode: "direct", StreamMode: "relay", AllowedUpstreams: []string{"media:80"}, AllowedStrmRoots: []string{"/media"}, Media: MediaConfig{PublicBaseURL: "https://media.example:49963", TokenKey: strings.Repeat("ab", 32), TLSCertFile: "cert.pem", TLSKeyFile: "key.pem"}}
}

func TestMediaValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
		ok     bool
	}{
		{"valid", func(c *Config) {}, true},
		{"proxy issuance no TLS", func(c *Config) { c.Role = "proxy"; c.Media.TLSCertFile = ""; c.Media.TLSKeyFile = "" }, true},
		{"media only no roots", func(c *Config) { c.Role = "media"; c.DeliveryMode = "proxy"; c.AllowedStrmRoots = nil }, true},
		{"all legacy delivery", func(c *Config) { c.DeliveryMode = "proxy" }, true},
		{"all redirect rejected", func(c *Config) { c.DeliveryMode = "proxy"; c.StreamMode = "redirect" }, false},
		{"bad role", func(c *Config) { c.Role = "unknown" }, false},
		{"bad delivery", func(c *Config) { c.DeliveryMode = "unknown" }, false},
		{"redirect", func(c *Config) { c.StreamMode = "redirect" }, false},
		{"automatic key", func(c *Config) { c.Media.TokenKey = "" }, true},
		{"bad listen", func(c *Config) { c.Media.Listen = "localhost" }, false},
		{"bad port", func(c *Config) { c.Media.Listen = ":99999" }, false},
		{"half tls", func(c *Config) { c.Media.TLSKeyFile = "" }, false},
		{"plaintext denied", func(c *Config) { c.Media.TLSKeyFile = ""; c.Media.TLSCertFile = "" }, false},
		{"plaintext explicit", func(c *Config) {
			c.Media.TLSKeyFile = ""
			c.Media.TLSCertFile = ""
			c.Media.AllowHTTP = true
			c.Media.PublicBaseURL = "http://media.example:49963"
		}, true},
		{"bad ttl", func(c *Config) { c.Media.TokenTTLSeconds = -1 }, false},
		{"max ttl", func(c *Config) { c.Media.TokenTTLSeconds = 86400 }, true},
		{"min ttl", func(c *Config) { c.Media.TokenTTLSeconds = 1 }, true},
		{"ttl too long", func(c *Config) { c.Media.TokenTTLSeconds = 86401 }, false},
		{"wildcard cors", func(c *Config) { c.Media.AllowedOrigins = []string{"*"} }, false},
		{"path in base", func(c *Config) { c.Media.PublicBaseURL += "/path" }, false},
		{"media emby", func(c *Config) { c.Role = "media"; c.DeliveryMode = "proxy"; c.Emby.Enabled = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := directConfig()
			tc.change(c)
			err := c.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate=%v want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestKeyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(p, []byte(strings.Repeat("ab", 32)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := MediaConfig{TokenKeyFile: p}
	if b, err := m.KeyBytes(); err != nil || len(b) != 32 {
		t.Fatalf("key: %v", err)
	}
	m.TokenKey = strings.Repeat("ab", 32)
	if _, err := m.KeyBytes(); err == nil {
		t.Fatal("accepted conflicting secrets")
	}
	m.TokenKey = ""
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.KeyBytes(); err == nil {
		t.Fatal("accepted unprotected file")
	}
}

func TestEnvironmentKeyWithoutYAML(t *testing.T) {
	old := Global
	defer func() { Global = old; viper.Reset() }()
	viper.Reset()
	Global = &Config{}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("log_level: info\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FNTV_MEDIA_TOKEN_KEY", strings.Repeat("cd", 32))
	t.Setenv("FNTV_MEDIA_STATE_DIR", "/private/media-state")
	t.Setenv("FNTV_ALLOWED_UPSTREAMS", "media:80,cdn:443")
	t.Setenv("FNTV_ALLOWED_STRM_ROOTS", "/media,/strm")
	t.Setenv("FNTV_PUBLIC_BASE_URL", "https://proxy.example")
	t.Setenv("FNTV_ROLE", "media")
	t.Setenv("FNTV_DELIVERY_MODE", "proxy")
	t.Setenv("FNTV_MEDIA_ALLOW_HTTP", "true")
	t.Setenv("FNTV_MEDIA_LISTEN", ":49964")
	t.Setenv("FNTV_MEDIA_TOKEN_TTL_SECONDS", "1234")
	if err := Load(p); err != nil {
		t.Fatal(err)
	}
	if Global.Media.TokenKey != strings.Repeat("cd", 32) {
		t.Fatal("environment key not loaded")
	}
	if Global.GetMedia().StateDir != "/private/media-state" {
		t.Fatal("environment state directory not loaded")
	}
	if Global.GetRole() != "media" || Global.GetDeliveryMode() != "proxy" || !Global.Media.AllowHTTP || Global.Media.Listen != ":49964" || Global.Media.TokenTTLSeconds != 1234 {
		t.Fatal("environment fields not loaded")
	}
	if len(Global.AllowedUpstreams) != 2 || Global.AllowedUpstreams[0] != "media:80" || len(Global.AllowedStrmRoots) != 2 || Global.AllowedStrmRoots[0] != "/media" || Global.PublicBaseURL != "https://proxy.example" {
		t.Fatal("legacy environment fields not loaded")
	}
}

func TestAutomaticKeyConcurrentPersistence(t *testing.T) {
	m := MediaConfig{StateDir: filepath.Join(t.TempDir(), "state")}
	const workers = 20
	keys := make([][]byte, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) { defer wg.Done(); keys[i], errs[i] = m.ResolveKeyBytes() }(i)
	}
	wg.Wait()
	for i := range keys {
		if errs[i] != nil || len(keys[i]) != 32 || !bytes.Equal(keys[0], keys[i]) {
			t.Fatalf("concurrent resolution %d failed: %v", i, errs[i])
		}
	}
	again, err := m.ResolveKeyBytes()
	if err != nil || !bytes.Equal(again, keys[0]) {
		t.Fatal("key changed on restart")
	}
	for path, mode := range map[string]os.FileMode{m.StateDir: 0700, filepath.Join(m.StateDir, "token.key"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("wrong permissions for %s", path)
		}
	}
	entries, err := os.ReadDir(m.StateDir)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary files leaked")
	}
}

func TestAutomaticKeyRejectsUnsafeExistingState(t *testing.T) {
	for _, kind := range []string{"invalid", "public-key", "public-directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "token.key")
			original := strings.Repeat("ab", 32)
			if kind == "invalid" {
				original = "invalid-key"
			}
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public-key":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "public-directory":
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".real", path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := (MediaConfig{StateDir: dir}).ResolveKeyBytes(); err == nil {
				t.Fatal("unsafe key accepted")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != original {
				t.Fatal("existing key changed")
			}
		})
	}
}

func TestAutomaticKeyManualPrecedenceAndNoValidationWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	c := directConfig()
	c.Media.TokenKey = ""
	c.Media.StateDir = dir
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("validation wrote state")
	}
	if (&Config{}).GetMedia().StateDir != "./data/media" {
		t.Fatal("incorrect state default")
	}
	m := MediaConfig{StateDir: dir, TokenKey: strings.Repeat("ab", 32)}
	if _, err := m.ResolveKeyBytes(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("manual key wrote state")
	}
	m.TokenKey = ""
	m.TokenKeyFile = filepath.Join(t.TempDir(), "missing")
	if _, err := m.ResolveKeyBytes(); err == nil {
		t.Fatal("missing explicit key silently generated")
	}
	if _, err := os.Stat(m.TokenKeyFile); !os.IsNotExist(err) {
		t.Fatal("explicit key file generated")
	}
	if err := os.WriteFile(m.TokenKeyFile, []byte(strings.Repeat("cd", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResolveKeyBytes(); err != nil {
		t.Fatal(err)
	}
	m.TokenKey = strings.Repeat("ab", 32)
	if _, err := m.ResolveKeyBytes(); err == nil {
		t.Fatal("conflicting keys accepted")
	}
}

func TestReloadOnlyLogLevel(t *testing.T) {
	old := Global
	defer func() { Global = old; viper.Reset() }()
	viper.Reset()
	Global = directConfig()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("role: media\nlog_level: debug\nmedia:\n  token_key: changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	handleConfigChange(p, func() { called = true })
	if !called || Global.GetLogLevel() != "debug" || Global.Role != "all" || Global.Media.TokenKey != strings.Repeat("ab", 32) {
		t.Fatal("reload changed runtime configuration")
	}
	if err := os.WriteFile(p, []byte("log_level: invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	handleConfigChange(p, nil)
	if Global.GetLogLevel() != "debug" {
		t.Fatal("invalid logging level applied")
	}
	if err := os.WriteFile(p, []byte("log_level: trace\n"), 0600); err != nil {
		t.Fatal(err)
	}
	handleConfigChange(p, nil)
	if Global.GetLogLevel() != "trace" {
		t.Fatal("trace level not applied")
	}
}
