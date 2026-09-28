package handler

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mediaFixture(t *testing.T, upstream string) *MediaHandler {
	t.Helper()
	h, _ := newRelayHandler(t, upstream)
	m, err := NewMediaHandler(h, bytes.Repeat([]byte{42}, 32), time.Hour, "", []string{"https://ui.example"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func requireMediaAbort(t *testing.T, m *MediaHandler, r *http.Request) {
	t.Helper()
	w := httptest.NewRecorder()
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("expected connection abort")
		}
		if w.Body.Len() != 0 || len(w.Header()) != 0 {
			t.Error("unauthorized response emitted")
		}
	}()
	m.ServeHTTP(w, r)
}

func TestMediaTicketConfidentialityAndRejection(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
	defer up.Close()
	m := mediaFixture(t, up.URL)
	now := time.Unix(10000, 0)
	m.now = func() time.Time { return now }
	secret := strings.Replace(up.URL, "http://", "http://username:secret-password@", 1) + "/private?signature=secret-query"
	issued, err := m.IssueURL(secret)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued, mediaPathPrefix))
	for _, value := range []string{"username", "secret-password", "secret-query", up.URL} {
		if bytes.Contains(raw, []byte(value)) || strings.Contains(issued, value) {
			t.Fatal("plaintext exposed")
		}
	}
	second, _ := m.IssueURL(secret)
	if second == issued {
		t.Fatal("nonce reused")
	}
	raw[len(raw)-1] ^= 1
	for _, path := range []string{"/", "/health", mediaPathPrefix, mediaPathPrefix + strings.Repeat("x", maxMediaTokenBytes+1), mediaPathPrefix + base64.RawURLEncoding.EncodeToString(raw)} {
		requireMediaAbort(t, m, httptest.NewRequest("GET", path, nil))
	}
	other, _ := NewMediaHandler(m.stream, bytes.Repeat([]byte{1}, 32), time.Hour, "", nil)
	other.now = m.now
	requireMediaAbort(t, other, httptest.NewRequest("GET", issued, nil))
	for _, method := range []string{"POST", "DELETE"} {
		requireMediaAbort(t, m, httptest.NewRequest(method, issued, nil))
	}
	now = now.Add(time.Hour)
	requireMediaAbort(t, m, httptest.NewRequest("GET", issued, nil))
	if calls != 0 {
		t.Fatal("unauthenticated upstream request")
	}
}

func TestMediaRangeReplayHeadAndCORS(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Range") != "bytes=2-5" {
			t.Error("missing Range")
		}
		w.Header().Set("Content-Range", "bytes 2-5/10")
		w.Header().Set("Content-Length", "4")
		w.Header().Set("Cache-Control", "public, max-age=9999")
		w.WriteHeader(206)
		if r.Method != "HEAD" {
			_, _ = io.WriteString(w, "2345")
		}
	}))
	defer up.Close()
	m := mediaFixture(t, up.URL)
	issued, _ := m.IssueURL(up.URL)
	for _, method := range []string{"GET", "GET", "HEAD"} {
		r := httptest.NewRequest(method, issued, nil)
		r.Header.Set("Range", "bytes=2-5")
		r.Header.Set("Origin", "https://ui.example")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 206 || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Access-Control-Allow-Origin") != "https://ui.example" {
			t.Fatalf("bad response: %v", w.Result())
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
		if method == "GET" && w.Body.String() != "2345" {
			t.Fatal("GET body")
		}
	}
	r := httptest.NewRequest("OPTIONS", issued, nil)
	r.Header.Set("Origin", "https://ui.example")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Headers", "range, if-range")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 204 || calls != 3 {
		t.Fatal("preflight contacted upstream")
	}
	r.Header.Set("Access-Control-Request-Headers", "authorization")
	requireMediaAbort(t, m, r)
	r.Header.Set("Origin", "https://evil.example")
	requireMediaAbort(t, m, r)
	r = httptest.NewRequest("GET", issued, nil)
	r.Header.Set("Origin", "null")
	requireMediaAbort(t, m, r)
}

func TestMediaRedirectAllowlistAndIndependentInstance(t *testing.T) {
	blockedCalls := 0
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { blockedCalls++ }))
	defer blocked.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, blocked.URL, 302) }))
	defer up.Close()
	issuer := mediaFixture(t, up.URL)
	gateway := mediaFixture(t, up.URL)
	issued, _ := issuer.IssueURL(up.URL)
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, httptest.NewRequest("GET", issued, nil))
	if w.Code != 502 || blockedCalls != 0 {
		t.Fatal("redirect escaped allowlist")
	}
	if _, err := issuer.IssueURL(blocked.URL); err == nil {
		t.Fatal("issued disallowed URL")
	}
	delete(gateway.stream.allowedUpstreams, strings.TrimPrefix(up.URL, "http://"))
	requireMediaAbort(t, gateway, httptest.NewRequest("GET", issued, nil))
}

func TestMediaConfigurationValidation(t *testing.T) {
	m := mediaFixture(t, "http://127.0.0.1:1234")
	for _, ttl := range []time.Duration{0, time.Millisecond, 25 * time.Hour} {
		if _, err := NewMediaHandler(m.stream, bytes.Repeat([]byte{1}, 32), ttl, "", nil); err == nil {
			t.Fatal("accepted invalid ttl")
		}
	}
	for _, origin := range []string{"*", "null", "https://example/path", "https://user:pass@example"} {
		if _, err := NewMediaHandler(m.stream, bytes.Repeat([]byte{1}, 32), time.Hour, "", []string{origin}); err == nil {
			t.Fatal("accepted invalid origin")
		}
	}
}

func TestMediaUnauthenticatedConnectionHasNoHTTPResponse(t *testing.T) {
	m := mediaFixture(t, "http://127.0.0.1:1234")
	server := httptest.NewServer(m)
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(server.URL + "/")
	if resp != nil {
		resp.Body.Close()
		t.Fatal("unauthenticated client received HTTP response")
	}
	if err == nil {
		t.Fatal("expected closed connection")
	}
}

func TestMediaAllowedRedirect(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/video", 302)
			return
		}
		_, _ = io.WriteString(w, "video")
	}))
	defer up.Close()
	m := mediaFixture(t, up.URL)
	issued, _ := m.IssueURL(up.URL)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", issued, nil))
	if w.Code != 200 || w.Body.String() != "video" {
		t.Fatal("allowed redirect failed")
	}
}

func TestMediaRedirectDoesNotLeakBasicAuthAcrossAuthorities(t *testing.T) {
	var authorization string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "video")
	}))
	defer destination.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer up.Close()
	m := mediaFixture(t, up.URL)
	destURL, _ := url.Parse(destination.URL)
	m.stream.allowedUpstreams[destURL.Host] = struct{}{}
	upURL, _ := url.Parse(up.URL)
	upURL.User = url.UserPassword("media", "upstream-secret")
	issued, _ := m.IssueURL(upURL.String())
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", issued, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("redirect status %d", w.Code)
	}
	if authorization != "" {
		t.Fatal("upstream credentials leaked to different authority")
	}
}
