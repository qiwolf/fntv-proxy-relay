package proxy

import (
	"encoding/json"
	"errors"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/config"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func directConfig(t *testing.T, upstream string) *config.Config {
	t.Helper()
	u, _ := url.Parse(upstream)
	return &config.Config{Role: "proxy", DeliveryMode: "direct", ListenAddr: "127.0.0.1:0", TargetAddr: upstream, LogLevel: "error", LogDir: t.TempDir(), CacheTTL: time.Minute, StreamMode: "relay", AllowedUpstreams: []string{u.Host}, AllowedStrmRoots: []string{t.TempDir()}, Media: config.MediaConfig{PublicBaseURL: "http://media.example", TokenKey: strings.Repeat("ab", 32), TokenTTLSeconds: 3600, AllowHTTP: true}}
}

func directServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s
}

func TestDirectAPISeparateGatewayRangeAndHEAD(t *testing.T) {
	var calls atomic.Int32
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Range") != "bytes=1-3" {
			t.Error("Range not propagated")
		}
		w.Header().Set("Content-Range", "bytes 1-3/5")
		w.Header().Set("Content-Length", "3")
		w.WriteHeader(206)
		if r.Method != "HEAD" {
			_, _ = io.WriteString(w, "123")
		}
	}))
	defer media.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": media.URL + "/movie?secret=test-secret"})
	}))
	defer origin.Close()
	gatewayCfg := directConfig(t, media.URL)
	gatewayCfg.Role = "media"
	gatewayCfg.DeliveryMode = "proxy"
	gateway := directServer(t, gatewayCfg)
	gatewayHTTP := httptest.NewServer(gateway.mediaHandler)
	defer gatewayHTTP.Close()
	cfg := directConfig(t, media.URL)
	cfg.TargetAddr = origin.URL
	cfg.Media.PublicBaseURL = gatewayHTTP.URL
	s := directServer(t, cfg)
	s.proxy.ModifyResponse = s.handleResponse
	front := httptest.NewServer(s.loggingMiddleware(s.proxy))
	defer front.Close()
	resp, err := http.Post(front.URL+"/v/api/v1/stream", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var payload map[string]string
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	ticket := payload["url"]
	if !strings.HasPrefix(ticket, gatewayHTTP.URL+"/fntv-media/") || strings.Contains(ticket, "test-secret") || calls.Load() != 0 {
		t.Fatal("not opaque or premature media fetch")
	}
	if resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("ticket response cached")
	}
	for _, method := range []string{"GET", "HEAD"} {
		req, _ := http.NewRequest(method, ticket, nil)
		req.Header.Set("Range", "bytes=1-3")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 206 || r.Header.Get("Content-Range") != "bytes 1-3/5" {
			t.Fatal("bad media response")
		}
		if method == "GET" && string(data) != "123" {
			t.Fatal("bad GET bytes")
		}
		if method == "HEAD" && len(data) != 0 {
			t.Fatal("HEAD body")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("wrong upstream count")
	}
}

func TestDirectLegacySTRMRedirectAndFailClosed(t *testing.T) {
	cfg := directConfig(t, "http://127.0.0.1:12345")
	s := directServer(t, cfg)
	path := filepath.Join(cfg.AllowedStrmRoots[0], "video.strm")
	if err := os.WriteFile(path, []byte("http://127.0.0.1:12345/private?secret=hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	s.cache.Set("source", cache.MediaSource{ID: "source", Path: path})
	w := httptest.NewRecorder()
	s.loggingMiddleware(s.proxy).ServeHTTP(w, httptest.NewRequest("GET", "/Videos/item/stream.mkv?MediaSourceId=source", nil))
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "http://media.example/fntv-media/") || strings.Contains(w.Header().Get("Location"), "hidden") {
		t.Fatal("legacy direct not encrypted redirect")
	}
	s.streamHandler.SetDirectRelay(func(string) (string, error) { return "", errors.New("secret-must-not-leak") })
	response := &http.Response{StatusCode: http.StatusOK, Request: httptest.NewRequest("POST", "/v/api/v1/stream", nil), Body: io.NopCloser(strings.NewReader(`{"url":"http://127.0.0.1:12345/private?secret=hidden"}`)), Header: make(http.Header)}
	if err := s.handleResponse(response); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("rewrite failure not generic/fail-closed")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"url":"http://127.0.0.1:12345/private?secret=hidden"}`)
	}))
	defer origin.Close()
	target, _ := url.Parse(origin.URL)
	s.proxy.Director = func(r *http.Request) { r.URL.Scheme = target.Scheme; r.URL.Host = target.Host }
	s.proxy.ModifyResponse = s.handleResponse
	front := httptest.NewServer(s.loggingMiddleware(s.proxy))
	defer front.Close()
	r, err := http.Post(front.URL+"/v/api/v1/stream", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusBadGateway || strings.Contains(string(data), "hidden") || strings.Contains(string(data), "secret") {
		t.Fatal("failed rewrite exposed original response")
	}
}

func directFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestMediaRoleListenerAndStop(t *testing.T) {
	cfg := directConfig(t, "http://127.0.0.1:12345")
	cfg.Role = "media"
	cfg.DeliveryMode = "proxy"
	cfg.Media.Listen = directFreeAddr(t)
	// An occupied proxy address proves role media never binds the web listener.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	cfg.ListenAddr = occupied.Addr().String()
	s := directServer(t, cfg)
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", cfg.Media.Listen, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("media listener did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + cfg.Media.Listen + "/")
	if resp != nil {
		resp.Body.Close()
		t.Fatal("media role served unauthenticated web response")
	}
	if err == nil {
		t.Fatal("expected transport error")
	}
	if err = s.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("start exit %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not exit")
	}
}

func TestAllRoleAtomicBindFailureAndStopBeforeStart(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	cfg := directConfig(t, "http://127.0.0.1:12345")
	cfg.Role = "all"
	cfg.ListenAddr = directFreeAddr(t)
	cfg.Media.Listen = occupied.Addr().String()
	s := directServer(t, cfg)
	if err = s.Start(); err == nil {
		t.Fatal("expected bind failure")
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		t.Fatalf("first socket leaked: %v", err)
	}
	ln.Close()
	other := directServer(t, directConfig(t, "http://127.0.0.1:12345"))
	if err = other.Stop(); err != nil {
		t.Fatal(err)
	}
	if err = other.Start(); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("start after stop %v", err)
	}
}

func TestLegacyRedirectModePreservesNativeAPIURL(t *testing.T) {
	cfg := directConfig(t, "http://127.0.0.1:12345")
	cfg.DeliveryMode = "proxy"
	cfg.StreamMode = "redirect"
	s := directServer(t, cfg)
	body := []byte(`{"url":"http://127.0.0.1:12345/native?token=existing"}`)
	got, count, err := s.streamHandler.RewriteStreamAPIResponse(body)
	if err != nil || count != 0 || string(got) != string(body) {
		t.Fatal("legacy redirect mode unexpectedly rewrote native response")
	}
}

func TestAllRoleActualDualListeners(t *testing.T) {
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=1-3" {
			t.Error("missing Range")
		}
		w.Header().Set("Content-Length", "3")
		w.Header().Set("Content-Range", "bytes 1-3/5")
		w.WriteHeader(http.StatusPartialContent)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "123")
		}
	}))
	defer media.Close()
	var expectedHost string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != expectedHost {
			t.Errorf("director Host = %q, expected %q", r.Host, expectedHost)
		}
		if r.Header.Get("Accept-Encoding") != "" && r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("stream API compression not suppressed")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"url": media.URL + "/video"})
	}))
	defer origin.Close()
	u, _ := url.Parse(origin.URL)
	expectedHost = u.Host
	cfg := directConfig(t, media.URL)
	cfg.Role = "all"
	cfg.TargetAddr = origin.URL
	cfg.ListenAddr = directFreeAddr(t)
	cfg.Media.Listen = directFreeAddr(t)
	cfg.Media.PublicBaseURL = "http://" + cfg.Media.Listen
	s := directServer(t, cfg)
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", cfg.ListenAddr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("proxy listener failed to start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Post("http://"+cfg.ListenAddr+"/v/api/v1/stream", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	err = json.NewDecoder(resp.Body).Decode(&payload)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload["url"], cfg.Media.PublicBaseURL+"/fntv-media/") {
		t.Fatal("incorrect media endpoint")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, _ := http.NewRequest(method, payload["url"], nil)
		req.Header.Set("Range", "bytes=1-3")
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusPartialContent || r.Header.Get("Content-Range") != "bytes 1-3/5" {
			t.Fatal("incorrect Range response")
		}
		if method == http.MethodGet && string(data) != "123" {
			t.Fatal("incorrect bytes")
		}
		if method == http.MethodHead && len(data) != 0 {
			t.Fatal("HEAD body")
		}
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	for _, addr := range []string{cfg.ListenAddr, cfg.Media.Listen} {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Fatalf("listener %s remains open", addr)
		}
	}
}

func TestStreamAPINonSuccessPassThrough(t *testing.T) {
	const body = `{"error":"login required","url":"http://127.0.0.1:12345/unmodified"}`
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, body)
	}))
	defer origin.Close()
	cfg := directConfig(t, "http://127.0.0.1:12345")
	cfg.TargetAddr = origin.URL
	s := directServer(t, cfg)
	s.proxy.ModifyResponse = s.handleResponse
	s.streamHandler.SetDirectRelay(func(string) (string, error) {
		t.Error("non-success response rewritten")
		return "", errors.New("unexpected")
	})
	front := httptest.NewServer(s.loggingMiddleware(s.proxy))
	defer front.Close()
	r, err := http.Post(front.URL+"/v/api/v1/stream", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized || string(data) != body {
		t.Fatal("upstream error not preserved")
	}
}
