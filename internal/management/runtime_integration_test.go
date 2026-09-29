package management

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func httpApplyRuntime(t *testing.T, root string, m *RuntimeManager, c Config, assets RuntimeAssets) {
	t.Helper()
	store, e := NewDocumentStore(filepath.Join(root, "config"), 0)
	if e != nil {
		t.Fatal(e)
	}
	keys, e := NewKeyStore(filepath.Join(root, "keys"))
	if e != nil {
		t.Fatal(e)
	}
	if e = keys.importKeys(assets.Keys); e != nil {
		t.Fatal(e)
	}
	token := strings.Repeat("a", 64)
	api, e := NewAPI(store, keys, token, "management.test")
	if e != nil {
		t.Fatal(e)
	}
	api.Runtime = m
	request := func(path string, v interface{}) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(v)
		req := httptest.NewRequest("POST", "http://management.test"+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		api.ServeHTTP(rr, req)
		return rr
	}
	raw, _ := json.Marshal(c)
	rr := request("/api/config", Document{Data: raw})
	if rr.Code != 200 {
		t.Fatalf("HTTP config save failed %d: %s", rr.Code, rr.Body.String())
	}
	var doc Document
	if e = json.Unmarshal(rr.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	rr = request("/api/apply", map[string]uint64{"version": doc.Version})
	if rr.Code != 200 {
		t.Fatalf("HTTP apply failed %d: %s", rr.Code, rr.Body.String())
	}
	rr = request("/api/apply", map[string]uint64{"version": doc.Version + 1})
	if rr.Code != 409 {
		t.Fatal("stale HTTP apply accepted")
	}
	if !m.Status().Running {
		t.Fatal("stale apply disrupted runtime")
	}
}

// Opt-in real binary test: RUN_MANAGEMENT_INTEGRATION=1 go test ./internal/management -run TestRuntimeBinary -v
func TestRuntimeBinaryFourModes(t *testing.T) {
	if os.Getenv("RUN_MANAGEMENT_INTEGRATION") != "1" {
		t.Skip("set RUN_MANAGEMENT_INTEGRATION=1 to build and exercise child binaries")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	legacyBin, unifiedBin := filepath.Join(root, "legacy"), filepath.Join(root, "unified")
	for _, b := range []struct{ out, pkg string }{{legacyBin, "./cmd"}, {unifiedBin, "./cmd/unified"}} {
		cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
		cmd.Dir = "../.."
		if output, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("build failed: %s %v", output, e)
		}
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=1-3" {
			t.Error("Range not forwarded")
		}
		w.Header().Set("Content-Range", "bytes 1-3/5")
		w.Header().Set("Content-Length", "3")
		w.WriteHeader(206)
		if r.Method != "HEAD" {
			io.WriteString(w, "123")
		}
	}))
	defer source.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": source.URL + "/movie"})
	}))
	defer origin.Close()
	src, _ := url.Parse(source.URL)
	port := func() int {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		return p
	}
	for _, mode := range []string{"redirect", "relay", "single", "split"} {
		t.Run(mode, func(t *testing.T) {
			mainPort, mediaPort := port(), port()
			front := "http://127.0.0.1:" + strconv.Itoa(mainPort)
			mediaURL := "http://127.0.0.1:" + strconv.Itoa(mediaPort)
			c := Config{Version: 1, Mode: mode, Listen: Listener{Address: "127.0.0.1", Port: mainPort}, Services: map[string]ServiceConfig{"fntv": {Enabled: true, Target: origin.URL, AllowedUpstreams: []string{src.Host}, STRMDirectories: []DirectoryMapping{{Source: root, Local: root}}}}}
			assets := RuntimeAssets{Keys: map[string]string{"fntv": strings.Repeat("ab", 32)}}
			if mode == "relay" {
				c.PublicBaseURL = front
			}
			if mode == "single" {
				c.MediaListen = &Listener{Address: "127.0.0.1", Port: mediaPort}
				c.PublicBaseURL = mediaURL
			}
			if mode == "split" {
				c.Role = "proxy"
				c.PublicBaseURL = mediaURL
				s := c.Services["fntv"]
				s.Hosts = []string{"fn.test"}
				c.Services["fntv"] = s
				mc := Config{Version: 1, Mode: "split", Role: "media", Listen: Listener{Address: "127.0.0.1", Port: mediaPort}, PublicBaseURL: mediaURL, Services: map[string]ServiceConfig{"fntv": {Enabled: true, AllowedUpstreams: []string{src.Host}}}}
				mm, e := NewRuntimeManager(filepath.Join(root, mode+"-media"), legacyBin, unifiedBin)
				if e != nil {
					t.Fatal(e)
				}
				defer mm.Stop()
				if e = mm.Apply(mc, assets); e != nil {
					t.Fatal(e)
				}
			}
			m, e := NewRuntimeManager(filepath.Join(root, mode), legacyBin, unifiedBin)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Stop()
			httpApplyRuntime(t, filepath.Join(root, mode+"-api"), m, c, assets)
			req, _ := http.NewRequest("POST", front+"/v/api/v1/stream", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Host = "fn.test"
			resp, e := http.DefaultClient.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			var payload map[string]string
			e = json.NewDecoder(resp.Body).Decode(&payload)
			resp.Body.Close()
			if e != nil {
				t.Fatal(e)
			}
			ticket := payload["url"]
			if ticket == "" {
				t.Fatal("missing video URL")
			}
			if mode == "single" || mode == "split" {
				if !strings.HasPrefix(ticket, mediaURL+"/") {
					t.Fatal("video did not use independent media listener")
				}
			}
			if strings.HasPrefix(ticket, "/") {
				ticket = front + ticket
			}
			if mode == "relay" && !strings.HasPrefix(ticket, front+"/") {
				t.Fatal("same-origin video escaped original listener")
			}
			if mode == "redirect" && !strings.HasPrefix(ticket, source.URL+"/") {
				t.Fatal("redirect did not retain direct source")
			}
			for _, method := range []string{"GET", "HEAD"} {
				req, _ = http.NewRequest(method, ticket, nil)
				req.Header.Set("Range", "bytes=1-3")
				r, e := http.DefaultClient.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				b, _ := io.ReadAll(r.Body)
				r.Body.Close()
				if r.StatusCode != 206 || r.Header.Get("Content-Range") != "bytes 1-3/5" {
					t.Fatalf("bad range status %d", r.StatusCode)
				}
				if method == "GET" && string(b) != "123" {
					t.Fatal("bad video bytes")
				}
			}
			m.Stop()
			if e = m.Resume(); e != nil {
				t.Fatal(e)
			}
			if !m.Status().Running {
				t.Fatal("resume failed")
			}
		})
	}
}
