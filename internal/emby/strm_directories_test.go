package emby

import (
	"fntv-proxy/internal/config"
	"fntv-proxy/internal/handler"
	"os"
	"path/filepath"
	"testing"
)

func TestRedirectReadsMappedSTRMCopy(t *testing.T) {
	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "video.strm"), []byte("http://source.example/video.mkv"), 0600); err != nil {
		t.Fatal(err)
	}
	reader := handler.NewStreamHandler(nil, nil, "redirect", nil, []string{local})
	reader.SetSTRMDirectoryMap([]config.STRMDirectoryMapping{{Source: "/emby-library", Local: local}})
	h := &StreamHandler{emby: &config.EmbyConfig{}, strmReader: reader}
	got, err := h.resolveMediaURL("/emby-library/video.strm")
	if err != nil || got != "http://source.example/video.mkv" {
		t.Fatal(got, err)
	}
}
