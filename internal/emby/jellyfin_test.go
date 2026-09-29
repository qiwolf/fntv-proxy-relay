package emby

import (
	"bytes"
	"fntv-proxy/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJellyfinIndependentServer(t *testing.T) {
	cfg := &config.Config{LogLevel: "error", Emby: config.EmbyConfig{TargetAddr: "http://emby.example", ListenAddr: ":18097"}, Jellyfin: config.EmbyConfig{TargetAddr: "http://jellyfin.example", ListenAddr: ":18096", DeliveryMode: "direct"}, AllowedUpstreams: []string{"source.example"}, AllowedStrmRoots: []string{t.TempDir()}, Media: config.MediaConfig{TokenKey: strings.Repeat("ab", 32), PublicBaseURL: "https://media.example"}}
	cfg.CacheTTL = time.Hour
	e, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.cache.Stop()
	j, err := NewJellyfinServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer j.cache.Stop()
	if j.service != &cfg.Jellyfin || e.service != &cfg.Emby || j.cache == e.cache || j.targetURL.Host != "jellyfin.example" || e.targetURL.Host != "emby.example" {
		t.Fatal("service state leaked")
	}
	if e.streamHandler.direct != nil || j.streamHandler.direct == nil || j.streamHandler.direct.emby != &cfg.Jellyfin {
		t.Fatal("wrong issuer config")
	}
}

func TestJellyfinUUIDAndAuthorization(t *testing.T) {
	sh, _ := testDirect(t)
	const id = "f89202d1-1395-4b0c-a6b5-7cb3738daafe"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/"+id+"/PlaybackInfo" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != `MediaBrowser Client="Jellyfin Web", Token="valid"` && r.URL.Query().Get("api_key") != "valid" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"MediaSources":[{"Id":"`+id+`","Path":"http://source.example/movie","SupportsDirectStream":true}]}`)
	}))
	defer backend.Close()
	sh.targetURL, _ = sh.targetURL.Parse(backend.URL)
	for _, header := range []string{"Authorization", "X-Emby-Token"} {
		r := httptest.NewRequest("GET", "/Videos/"+id+"/stream.mkv?Static=true&MediaSourceId="+id, nil)
		if header == "Authorization" {
			r.Header.Set(header, `MediaBrowser Client="Jellyfin Web", Token="valid"`)
		} else {
			r.Header.Set(header, "valid")
		}
		w := httptest.NewRecorder()
		if !sh.Handle(w, r) || w.Code != 302 {
			t.Fatalf("%s status %d", header, w.Code)
		}
	}
}

func TestJellyfinPlaybackMethods(t *testing.T) {
	_, ph := testDirect(t)
	body := []byte(`{"PlaySessionId":"jf-session","MediaSources":[{"Id":"1234-abcd","Path":"http://source.example/movie","SupportsDirectStream":true,"MediaStreams":[{"Type":"Audio","Codec":"aac"}]}]}`)
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/Items/1234-abcd/PlaybackInfo", nil)
		if !isPlaybackInfoRequest(r) {
			t.Fatal(method)
		}
		resp := &http.Response{StatusCode: 200, Header: http.Header{}, Request: r}
		out, changed, err := ph.Handle(resp, body)
		if err != nil || !changed || !bytes.Contains(out, []byte("/fntv-direct/")) || !bytes.Contains(out, []byte("jf-session")) || bytes.Contains(out, []byte("source.example")) {
			t.Fatalf("invalid playback rewrite %v", err)
		}
	}
}
