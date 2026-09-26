package handler

import (
	"encoding/json"
	"fmt"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/logger"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelayPreservesRangeAndStreamsPartialContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=2-5" {
			t.Errorf("Range = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("client Authorization leaked upstream: %q", got)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 2-5/10")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "2345")
	}))
	defer upstream.Close()

	h, sourceID := newRelayHandler(t, upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/Videos/item/stream.mkv?MediaSourceId="+sourceID, nil)
	req.Header.Set("Range", "bytes=2-5")
	req.Header.Set("Authorization", "Bearer client-secret")
	rec := httptest.NewRecorder()
	if !h.Handle(rec, req) {
		t.Fatal("request was not handled")
	}
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("status/body = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", got)
	}
}

func TestRelayHEADHasNoBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %s", r.Method)
		}
		w.Header().Set("Content-Length", "42")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h, sourceID := newRelayHandler(t, upstream.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/Videos/item/stream.mkv?MediaSourceId="+sourceID, nil)
	h.Handle(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "42" {
		t.Fatalf("unexpected HEAD response: code=%d len=%d headers=%v", rec.Code, rec.Body.Len(), rec.Header())
	}
}

func TestRelayUsesBasicAuthEmbeddedInStrmURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "media" || password != "secret" {
			t.Errorf("BasicAuth = %q %q %v", user, password, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	authURL := *u
	authURL.User = url.UserPassword("media", "secret")
	h, sourceID := newHandler(t, "relay", authURL.String()+"/movie.mkv", []string{u.Host})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/Videos/item/stream.mkv?MediaSourceId="+sourceID, nil)
	h.Handle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRelayRejectsRedirectOutsideAllowlist(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("blocked server must not be reached")
	}))
	defer blocked.Close()
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blocked.URL+"/secret?token=hidden", http.StatusFound)
	}))
	defer entry.Close()
	h, sourceID := newRelayHandler(t, entry.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/Videos/item/stream.mkv?MediaSourceId="+sourceID, nil)
	h.Handle(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestRelayRejectsStrmOutsideAllowedRoot(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	strm := filepath.Join(outside, "movie.strm")
	if err := os.WriteFile(strm, []byte("http://127.0.0.1/movie.mkv"), 0600); err != nil {
		t.Fatal(err)
	}
	c := cache.NewWithTTL(time.Minute)
	t.Cleanup(c.Stop)
	c.Set("source", cache.MediaSource{ID: "source", Path: strm})
	h := NewStreamHandler(c, logger.New("error", ""), "relay", []string{"127.0.0.1"}, []string{allowed})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/Videos/item/stream.mkv?MediaSourceId=source", nil)
	h.Handle(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestPathWithinRootsRejectsSymlinkEscape(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "movie.strm")
	if err := os.WriteFile(target, []byte("http://127.0.0.1/movie.mkv"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "movie.strm")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	allowedPath, err := pathWithinRoots(link, []string{allowed})
	if err != nil {
		t.Fatal(err)
	}
	if allowedPath {
		t.Fatal("symlink escaping allowed root was accepted")
	}
}

func TestRedirectModeRemainsAvailable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h, sourceID := newHandler(t, "redirect", upstream.URL, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/Videos/item/stream.mkv?MediaSourceId="+sourceID, nil)
	h.Handle(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != upstream.URL {
		t.Fatalf("status/location = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSafeURLForLogRemovesCredentialsAndQuery(t *testing.T) {
	got := safeURLForLog("https://user:pass@example.com:8443/media/a.mkv?token=secret")
	if got != "https://example.com:8443/media/a.mkv" || strings.Contains(got, "secret") || strings.Contains(got, "pass") {
		t.Fatalf("unsafe log URL: %q", got)
	}
}

func TestRewriteFnOSStreamAPIAndRelayRange(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=0-2" {
			t.Errorf("Range = %q", got)
		}
		w.Header().Set("Content-Range", "bytes 0-2/3")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "abc")
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := cache.NewWithTTL(time.Minute)
	t.Cleanup(c.Stop)
	h := NewStreamHandler(c, logger.New("error", ""), "relay", []string{u.Host}, []string{t.TempDir()}, "https://fn.example.com")
	body := []byte(`{"code":0,"data":{"file_stream":{"url":"` + upstream.URL + `/movie.mp4?token=secret"},"qualities":[{"url":"` + upstream.URL + `/movie.mp4?token=secret"}]}}`)
	rewritten, count, err := h.RewriteStreamAPIResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("rewrite count = %d", count)
	}
	var response struct {
		Data struct {
			FileStream struct {
				URL string `json:"url"`
			} `json:"file_stream"`
			Qualities []struct {
				URL string `json:"url"`
			} `json:"qualities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rewritten, &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.FileStream.URL != response.Data.Qualities[0].URL || !strings.HasPrefix(response.Data.FileStream.URL, "https://fn.example.com"+relayPathPrefix) {
		t.Fatalf("unexpected rewritten URLs: %+v", response.Data)
	}
	relay, err := url.Parse(response.Data.FileStream.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, relay.RequestURI(), nil)
	req.Header.Set("Range", "bytes=0-2")
	rec := httptest.NewRecorder()
	if !h.Handle(rec, req) {
		t.Fatal("relay request was not handled")
	}
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "abc" || rec.Header().Get("Content-Range") != "bytes 0-2/3" {
		t.Fatalf("relay status/body/headers = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
}

func newRelayHandler(t *testing.T, upstreamURL string) (*StreamHandler, string) {
	t.Helper()
	u, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	return newHandler(t, "relay", upstreamURL+"/media.mkv?token=secret", []string{u.Host})
}

func newHandler(t *testing.T, mode, strmURL string, allowed []string) (*StreamHandler, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "movie.strm")
	if err := os.WriteFile(path, []byte(strmURL), 0600); err != nil {
		t.Fatal(err)
	}
	c := cache.NewWithTTL(time.Minute)
	t.Cleanup(c.Stop)
	sourceID := fmt.Sprintf("source-%s", mode)
	c.Set(sourceID, cache.MediaSource{ID: sourceID, Path: path})
	return NewStreamHandler(c, logger.New("error", ""), mode, allowed, []string{root}), sourceID
}
