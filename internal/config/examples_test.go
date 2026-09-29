package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestDirectExamplesLoad(t *testing.T) {
	old := Global
	defer func() { Global = old; viper.Reset() }()
	keyPath := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("ab", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FNTV_MEDIA_TOKEN_KEY_FILE", keyPath)
	for _, role := range []string{"proxy", "media", "all"} {
		t.Run(role, func(t *testing.T) {
			viper.Reset()
			Global = &Config{}
			if err := Load(filepath.Join("..", "..", "deploy", "direct", role+".config.yaml.example")); err != nil {
				t.Fatal(err)
			}
			if Global.GetRole() != role {
				t.Fatalf("role %s", Global.GetRole())
			}
		})
	}
	for _, service := range []string{"emby", "jellyfin"} {
		t.Run(service, func(t *testing.T) {
			viper.Reset()
			Global = &Config{}
			if err := Load(filepath.Join("..", "..", "deploy", service+"-direct-proxy", "config.yaml.example")); err != nil {
				t.Fatal(err)
			}
			if Global.GetRole() != "proxy" {
				t.Fatalf("role %s", Global.GetRole())
			}
		})
	}
}
