package config

import "testing"

func TestJellyfinValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		valid  bool
	}{
		{"direct", func(c *Config) {}, true},
		{"missing roots", func(c *Config) { c.AllowedStrmRoots = nil }, false},
		{"missing upstream", func(c *Config) { c.AllowedUpstreams = nil }, false},
		{"missing origin", func(c *Config) { c.Media.PublicBaseURL = "" }, false},
		{"bad key", func(c *Config) { c.Media.TokenKey = "invalid" }, false},
		{"bad mode", func(c *Config) { c.Jellyfin.DeliveryMode = "unknown" }, false},
		{"media role", func(c *Config) { c.Role = "media" }, false},
		{"disabled", func(c *Config) {
			c.Jellyfin.Enabled = false
			c.Media.PublicBaseURL = ""
			c.AllowedStrmRoots = nil
			c.AllowedUpstreams = nil
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := directConfig()
			c.Role = "proxy"
			c.DeliveryMode = "proxy"
			c.StreamMode = "redirect"
			c.Jellyfin = EmbyConfig{Enabled: true, DeliveryMode: "direct"}
			tc.mutate(c)
			if err := c.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
