package unified

import (
	"fntv-proxy/internal/config"
	"testing"
)

func TestLegacyAdapterPreservesSTRMDirectoryMap(t *testing.T) {
	c := Config{Role: "proxy"}
	for _, kind := range []string{"fntv", "emby", "jellyfin"} {
		s := Service{Type: kind, STRMDirectoryMap: []config.STRMDirectoryMapping{{Source: "/nas/strm", Local: "/proxy/copy"}}}
		got := c.legacy(kind, s)
		if len(got.STRMDirectoryMap) != 1 || got.STRMDirectoryMap[0].Local != "/proxy/copy" {
			t.Fatal("mapping lost", kind)
		}
	}
}
