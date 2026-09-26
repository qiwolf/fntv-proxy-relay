package config

import "testing"

func TestValidateRelayRequiresAllowLists(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		ok   bool
	}{
		{name: "redirect remains compatible", cfg: &Config{StreamMode: "redirect"}, ok: true},
		{name: "relay complete", cfg: &Config{StreamMode: "relay", AllowedUpstreams: []string{"media:80"}, AllowedStrmRoots: []string{"/media"}}, ok: true},
		{name: "relay missing upstream", cfg: &Config{StreamMode: "relay", AllowedStrmRoots: []string{"/media"}}},
		{name: "relay missing root", cfg: &Config{StreamMode: "relay", AllowedUpstreams: []string{"media:80"}}},
		{name: "unknown mode", cfg: &Config{StreamMode: "passthrough"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err == nil) != tt.ok {
				t.Fatalf("Validate() error = %v, ok=%v", err, tt.ok)
			}
		})
	}
}
