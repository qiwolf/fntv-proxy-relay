package emby

import (
	"bytes"
	"encoding/json"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/config"
	"fntv-proxy/internal/handler"
	"fntv-proxy/internal/logger"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDirect(t *testing.T) (*StreamHandler, *PlaybackHandler) {
	t.Helper()
	c := cache.NewWithStreamTTL(time.Hour)
	t.Cleanup(c.Stop)
	log := logger.New("error", "")
	e := &config.EmbyConfig{DeliveryMode: "direct"}
	reader := handler.NewStreamHandler(c, log, "relay", []string{"source.example"}, []string{t.TempDir()})
	media, err := handler.NewMediaHandler(reader, bytes.Repeat([]byte{1}, 32), time.Hour, "https://media.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	d := &directIssuer{media: media, reader: reader, emby: e, mediaBase: "https://media.example"}
	u, _ := url.Parse("http://backend.invalid")
	sh := NewStreamHandler(c, log, e, u)
	sh.direct = d
	ph := NewPlaybackHandler(c, log, e)
	ph.direct = d
	return sh, ph
}

func TestDirectStreamAlwaysAuthorizes(t *testing.T) {
	sh, _ := testDirect(t)
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("api_key") != "good" {
			w.WriteHeader(403)
			w.Write([]byte(`{"MediaSources":[{"Id":"s","Path":"http://source.example/movie"}]}`))
			return
		}
		w.Write([]byte(`{"MediaSources":[{"Id":"s","Path":"http://source.example/movie"}]}`))
	}))
	defer backend.Close()
	sh.targetURL, _ = url.Parse(backend.URL)
	sh.cache.Set("s", cache.MediaSource{ID: "s", Path: "http://source.example/movie"})
	for _, tt := range []struct {
		token  string
		status int
	}{{"good", 302}, {"bad", 403}, {"", 401}} {
		r := httptest.NewRequest("GET", "/Videos/123/stream?Static=true&MediaSourceId=s&api_key="+tt.token, nil)
		w := httptest.NewRecorder()
		if !sh.Handle(w, r) || w.Code != tt.status {
			t.Fatalf("%s: %d", tt.token, w.Code)
		}
		if tt.status == 302 && !strings.HasPrefix(w.Header().Get("Location"), "https://media.example/fntv-media/") {
			t.Fatal("not a ticket")
		}
	}
	if calls != 2 {
		t.Fatalf("backend calls %d", calls)
	}
}

func TestDirectPlaybackPreservesAndConceals(t *testing.T) {
	_, ph := testDirect(t)
	body := []byte(`{"PlaySessionId":"keep","Unknown":{"x":1},"MediaSources":[{"Id":"s","Path":"http://source.example/movie?secret=hidden","RequiredHttpHeaders":{"Authorization":"secret"},"SupportsDirectStream":true,"MediaStreams":[{"Type":"Subtitle","Index":2}]}]}`)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}}
	out, modified, err := ph.Handle(resp, body)
	if err != nil || !modified {
		t.Fatalf("%v %v", modified, err)
	}
	if bytes.Contains(out, []byte("secret")) || bytes.Contains(out, []byte("source.example")) {
		t.Fatal("raw source exposed")
	}
	var payload map[string]interface{}
	json.Unmarshal(out, &payload)
	if payload["PlaySessionId"] != "keep" || payload["Unknown"] == nil {
		t.Fatal("unknown fields lost")
	}
	source := payload["MediaSources"].([]interface{})[0].(map[string]interface{})
	streamURL := stringField(source, "DirectStreamUrl")
	if !strings.HasPrefix(streamURL, "/fntv-direct/") || strings.Contains(streamURL, "https:") {
		t.Fatal("Emby Web requires a relative DirectStreamUrl")
	}
	if stringField(source, "Path") != "https://media.example/fntv-media/"+strings.TrimPrefix(streamURL, "/fntv-direct/") {
		t.Fatal("Path and relative stream route must carry the same ticket")
	}
	if source["AddApiKeyToDirectStreamUrl"] != false {
		t.Fatal("Emby API key may be appended to ticket URL")
	}
	if !bytes.Contains(out, []byte("Subtitle")) {
		t.Fatal("subtitles lost")
	}
	resp.StatusCode = 403
	out, modified, err = ph.Handle(resp, body)
	if err != nil || modified || !bytes.Equal(out, body) {
		t.Fatal("HTTP error modified")
	}
	resp.StatusCode = 200
	_, _, err = ph.Handle(resp, []byte(`{"MediaSources":[{"Path":"http://evil.example/movie"}]}`))
	if err == nil {
		t.Fatal("allowlist bypass")
	}
}

func TestDirectRelativeTicketRedirect(t *testing.T) {
	sh, ph := testDirect(t)
	out, _, err := ph.Handle(&http.Response{StatusCode: 200, Header: http.Header{}}, []byte(`{"MediaSources":[{"Path":"http://source.example/movie"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ MediaSources []map[string]interface{} }
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	relative := stringField(payload.MediaSources[0], "DirectStreamUrl")
	want := stringField(payload.MediaSources[0], "Path")
	for _, prefix := range []string{"", "/emby"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := httptest.NewRecorder()
			if !sh.Handle(w, httptest.NewRequest(method, prefix+relative, nil)) || w.Code != 302 || w.Header().Get("Location") != want || w.Body.Len() != 0 {
				t.Fatalf("relative redirect failed: %s %s: %d", method, prefix, w.Code)
			}
		}
	}
	for _, target := range []string{relative + "?target=https://evil.example", relative + "?", relative + "/extra", "/fntv-direct/https://evil.example", "/fntv-direct/", "/fntv-direct", "/fntv-direct/" + strings.Repeat("a", 16385), "/fntv-direct/%61" + strings.Repeat("a", 50)} {
		w := httptest.NewRecorder()
		if !sh.Handle(w, httptest.NewRequest("GET", target, nil)) || w.Code != 400 || w.Header().Get("Location") != "" {
			t.Fatalf("invalid ticket route not rejected: %d", w.Code)
		}
	}
	for _, method := range []string{"POST", "PUT", "DELETE", "OPTIONS"} {
		w := httptest.NewRecorder()
		if !sh.Handle(w, httptest.NewRequest(method, relative, nil)) || w.Code != 405 || w.Header().Get("Location") != "" {
			t.Fatal("non-read request accepted")
		}
	}
}

func TestScopedDirectRelativeTicketRedirect(t *testing.T) {
	sh, ph := testDirect(t)
	m, err := handler.NewScopedMediaHandler(sh.direct.reader, bytes.Repeat([]byte{1}, 32), time.Hour, "https://media.example", nil, "emby")
	if err != nil {
		t.Fatal(err)
	}
	sh.direct.media = m
	out, _, err := ph.Handle(&http.Response{StatusCode: 200, Header: http.Header{}}, []byte(`{"MediaSources":[{"Path":"http://source.example/movie"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ MediaSources []map[string]interface{} }
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	relative := stringField(payload.MediaSources[0], "DirectStreamUrl")
	if !strings.HasPrefix(relative, "/fntv-direct/emby/") {
		t.Fatal("relative route lost scope")
	}
	w := httptest.NewRecorder()
	if !sh.Handle(w, httptest.NewRequest("GET", relative, nil)) || w.Code != 302 || w.Header().Get("Location") != stringField(payload.MediaSources[0], "Path") {
		t.Fatal("scoped redirect mismatch")
	}
	for _, invalid := range []string{strings.Replace(relative, "/emby/", "/jellyfin/", 1), strings.Replace(relative, "/emby/", "/", 1), relative + "/extra"} {
		w := httptest.NewRecorder()
		if !sh.Handle(w, httptest.NewRequest("GET", invalid, nil)) || w.Code != 400 || w.Header().Get("Location") != "" {
			t.Fatal("wrong scoped route accepted")
		}
	}
}

func TestDirectPathsAndBackendErrors(t *testing.T) {
	sh, _ := testDirect(t)
	for _, p := range []string{"/Videos/1/Subtitles/0/stream", "/Videos/1/master.m3u8", "/Audio/1/stream", "/Videos/1/stream/extra", "/Videos/1/universal"} {
		if sh.Handle(httptest.NewRecorder(), httptest.NewRequest("GET", p+"?Static=true", nil)) {
			t.Fatalf("intercepted %s", p)
		}
	}
	for _, status := range []int{302, 500, 200} {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write([]byte("not JSON")) }))
		sh.targetURL, _ = url.Parse(backend.URL)
		w := httptest.NewRecorder()
		sh.Handle(w, httptest.NewRequest("GET", "/Videos/1/stream?Static=true&api_key=x", nil))
		backend.Close()
		if w.Code != 502 {
			t.Fatalf("status %d got %d", status, w.Code)
		}
	}
}

func TestEmbyDirectConfig(t *testing.T) {
	if (&config.EmbyConfig{}).GetDeliveryMode() != "redirect" {
		t.Fatal("default changed")
	}
	c := &config.Config{DeliveryMode: "proxy", StreamMode: "redirect", Emby: config.EmbyConfig{Enabled: true, DeliveryMode: "direct"}}
	if c.Validate() == nil {
		t.Fatal("missing allowlist accepted")
	}
	c.AllowedUpstreams = []string{"source.example:80"}
	c.AllowedStrmRoots = []string{"/media"}
	if c.Validate() == nil {
		t.Fatal("missing media origin accepted")
	}
	c.Media.PublicBaseURL = "https://media.example"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.GetDeliveryMode() != "proxy" || c.GetStreamMode() != "redirect" {
		t.Fatal("Emby direct mode changed FNTV delivery")
	}
	c.Emby.DeliveryMode = "unknown"
	if c.Validate() == nil {
		t.Fatal("unknown Emby delivery mode accepted")
	}
}

func TestDirectRejectsLocalStrmOutsideRootsAndSymlinks(t *testing.T) {
	sh, _ := testDirect(t)
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.strm")
	if err := os.WriteFile(outside, []byte("http://source.example/movie"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.strm")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	sh.direct.reader = handler.NewStreamHandler(sh.cache, sh.logger, "relay", []string{"source.example"}, []string{root})
	for _, path := range []string{outside, link} {
		if _, err := sh.direct.issue(map[string]interface{}{"Path": path}); err == nil {
			t.Fatal("outside path accepted")
		}
	}
}

func TestDirectPreservesLocalAndTranscoding(t *testing.T) {
	_, ph := testDirect(t)
	for _, body := range []string{
		`{"MediaSources":[{"Path":"/media/movie.mkv","Unknown":7}]}`,
		`{"MediaSources":[{"Path":"http://source.example/movie","SupportsDirectStream":false,"SupportsDirectPlay":false,"TranscodingUrl":"/Videos/1/master.m3u8"}]}`,
	} {
		out, modified, err := ph.Handle(&http.Response{StatusCode: 200, Header: http.Header{}}, []byte(body))
		if err != nil || modified || string(out) != body {
			t.Fatal("unsupported source changed")
		}
	}
}
