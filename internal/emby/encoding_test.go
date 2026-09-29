package emby

import (
	"fntv-proxy/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPlaybackEncodingNegotiation(t *testing.T) {
	for _, name := range []string{"emby", "jellyfin"} {
		t.Run(name, func(t *testing.T) {
			const body = `{"MediaSources":[{"Id":"local","Path":"/media/movie.mkv","SupportsDirectStream":true}]}`
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := "gzip, deflate, br, zstd"
				if isPlaybackInfoRequest(r) {
					want = "identity"
				}
				if got := r.Header.Get("Accept-Encoding"); got != want {
					t.Errorf("encoding=%q want=%q", got, want)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			}))
			defer backend.Close()
			cfg := &config.Config{LogLevel: "error", Emby: config.EmbyConfig{TargetAddr: backend.URL, DeliveryMode: "direct"}, Jellyfin: config.EmbyConfig{TargetAddr: backend.URL, DeliveryMode: "direct"}, AllowedUpstreams: []string{"source.example"}, AllowedStrmRoots: []string{t.TempDir()}, Media: config.MediaConfig{TokenKey: strings.Repeat("ab", 32), PublicBaseURL: "https://media.example"}}
			var s *Server
			cfg.CacheTTL = time.Hour
			var err error
			if name == "emby" {
				s, err = NewServer(cfg)
			} else {
				s, err = NewJellyfinServer(cfg)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Stop()
			for _, method := range []string{"GET", "POST"} {
				for _, path := range []string{"/Items/local/PlaybackInfo", "/System/Info/Public"} {
					req := httptest.NewRequest(method, path, nil)
					req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, req)
					if w.Code != 200 || w.Body.String() != body {
						t.Fatalf("%s %s status=%d body mismatch=%v", method, path, w.Code, w.Body.String() != body)
					}
				}
			}
		})
	}
}
