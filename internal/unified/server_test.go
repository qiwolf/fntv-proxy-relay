package unified

import (
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentedExamplesUseStrictSchema(t *testing.T) {
	for _, name := range []string{"proxy", "media"} {
		f, err := os.Open("../../deploy/unified/" + name + ".yaml.example")
		if err != nil {
			t.Fatal(err)
		}
		decoder := yaml.NewDecoder(f)
		decoder.KnownFields(true)
		var cfg Config
		err = decoder.Decode(&cfg)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.Role != name || len(cfg.Services) != 3 {
			t.Fatalf("incomplete %s example", name)
		}
	}
}

func fixture(t *testing.T, role string) *Config {
	t.Helper()
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte(strings.Repeat("ab", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	return &Config{Role: role, Listen: "127.0.0.1:29000", AllowHTTP: true, MediaPublicBaseURL: "http://media.test:49000", Services: map[string]Service{
		"one": {Type: "fntv", Hosts: []string{"ONE.test"}, Target: "http://127.0.0.1:8005", TokenKeyFile: key, AllowedUpstreams: []string{"127.0.0.1:19798"}, AllowedStrmRoots: []string{t.TempDir()}},
	}}
}

func TestHostDispatch(t *testing.T) {
	hits := map[string]int{}
	s := &Server{cfg: &Config{Role: "proxy"}, handlers: map[string]http.Handler{}}
	for _, host := range []string{"one.test", "two.test"} {
		host := host
		s.handlers[host] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits[host]++; w.WriteHeader(204) })
	}
	for _, host := range []string{"ONE.test", "one.test:443", "one.test.", "two.test:80", "unknown.test", "one.test.evil", "one.test:bad", ""} {
		req := httptest.NewRequest("GET", "http://test/Items/1/PlaybackInfo", nil)
		req.Host = host
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		expected := 404
		if host == "ONE.test" || host == "one.test:443" || host == "one.test." || host == "two.test:80" {
			expected = 204
		}
		if w.Code != expected {
			t.Errorf("%q: %d != %d", host, w.Code, expected)
		}
	}
	if hits["one.test"] != 3 || hits["two.test"] != 1 {
		t.Fatal(hits)
	}
}
func TestMediaDispatch(t *testing.T) {
	s := &Server{cfg: &Config{Role: "media"}, handlers: map[string]http.Handler{"one": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}}
	for _, path := range []string{"/fntv-media/one/token", "/fntv-media/two/token", "/fntv-media/one", "/other/one/token"} {
		func() {
			defer func() {
				got := recover()
				if path != "/fntv-media/one/token" && got != http.ErrAbortHandler {
					t.Errorf("%s should abort, got %v", path, got)
				}
				if path == "/fntv-media/one/token" && got != nil {
					t.Errorf("unexpected panic %v", got)
				}
			}()
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			expected := 404
			if path == "/fntv-media/one/token" {
				expected = 204
			}
			if w.Code != expected {
				t.Fatal(path, w.Code)
			}
		}()
	}
}
func TestDuplicateHostsAndDedicatedOnly(t *testing.T) {
	c := fixture(t, "proxy")
	s := c.Services["one"]
	s.Hosts = []string{"one.test:443"}
	c.Services["two"] = s
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate accepted")
	}
	delete(c.Services, "two")
	s.Hosts = nil
	s.DirectListen = "127.0.0.1:29001"
	c.Services["one"] = s
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestDirectListenerIgnoresHost(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "selected-one") }))
	defer backend.Close()
	c := fixture(t, "proxy")
	service := c.Services["one"]
	service.Target = backend.URL
	service.DirectListen = "127.0.0.1:29001"
	c.Services["one"] = service
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	req := httptest.NewRequest("GET", "http://unknown.test/", nil)
	w := httptest.NewRecorder()
	s.direct[0].Handler.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "selected-one" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestBindFailureClosesEarlierListener(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := free.Addr().String()
	free.Close()
	c := fixture(t, "proxy")
	c.Listen = addr
	service := c.Services["one"]
	service.DirectListen = busy.Addr().String()
	c.Services["one"] = service
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if err = s.Start(); err == nil {
		t.Fatal("collision accepted")
	}
	retry, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal("shared listener leaked", err)
	}
	retry.Close()
}
