package proxy

import (
	"encoding/json"
	"fntv-proxy/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlaybackInfoThenRangeRelayEndToEnd(t *testing.T) {
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=1-3" {
			t.Fatalf("Range = %q", got)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 1-3/5")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "123")
	}))
	defer media.Close()

	root := t.TempDir()
	strmPath := filepath.Join(root, "sample.strm")
	if err := os.WriteFile(strmPath, []byte(media.URL+"/sample.mp4"), 0600); err != nil {
		t.Fatal(err)
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/item/PlaybackInfo" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ItemId": "item",
			"MediaSources": []map[string]any{{
				"Id": "source", "Path": strmPath, "Protocol": "File",
			}},
		})
	}))
	defer origin.Close()

	mediaURL, err := url.Parse(media.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		TargetAddr:       origin.URL,
		LogLevel:         "error",
		LogDir:           t.TempDir(),
		CacheTTL:         time.Minute,
		StreamMode:       "relay",
		AllowedUpstreams: []string{mediaURL.Host},
		AllowedStrmRoots: []string{root},
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	originalDirector := s.proxy.Director
	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = originURL.Host
	}
	s.proxy.ModifyResponse = s.handleResponse
	proxyServer := httptest.NewServer(s.loggingMiddleware(s.proxy))
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/Items/item/PlaybackInfo")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PlaybackInfo status = %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, proxyServer.URL+"/Videos/item/stream.mkv?MediaSourceId=source", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=1-3")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusPartialContent || string(body) != "123" {
		t.Fatalf("stream status/body = %d %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 1-3/5" {
		t.Fatalf("Content-Range = %q", got)
	}
}
