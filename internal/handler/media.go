package handler

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const mediaPathPrefix = "/fntv-media/"
const maxMediaURLBytes = 8192
const maxMediaTokenBytes = 16384
const maxMediaTicketTTL = 24 * time.Hour

// MediaHandler is a stateless media-only gateway. Possession of a ticket grants
// access until expiry, including repeated Range requests required for seeking.
// All instances sharing a key must be trusted to issue tickets.
type MediaHandler struct {
	stream  *StreamHandler
	aead    cipher.AEAD
	ttl     time.Duration
	baseURL string
	origins map[string]bool
	now     func() time.Time
}

type mediaTicket struct {
	URL     string `json:"u"`
	Issued  int64  `json:"i"`
	Expires int64  `json:"e"`
}

func NewMediaHandler(h *StreamHandler, key []byte, ttl time.Duration, publicBaseURL string, allowedOrigins []string) (*MediaHandler, error) {
	if h == nil || h.mode != "relay" || len(key) != 32 || ttl < time.Second || ttl > maxMediaTicketTTL {
		return nil, errors.New("media gateway requires relay handler, 32-byte key and ticket TTL between 1 second and 24 hours")
	}
	base := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return nil, errors.New("media public base URL must be an HTTP(S) origin")
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	m := &MediaHandler{stream: h, aead: aead, ttl: ttl, baseURL: base, origins: map[string]bool{}, now: time.Now}
	for _, origin := range allowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(origin, "*") {
			return nil, errors.New("media CORS origins must be exact HTTP(S) origins")
		}
		m.origins[origin] = true
	}
	return m, nil
}

func (m *MediaHandler) IssueURL(upstream string) (string, error) {
	u, err := url.Parse(upstream)
	if err != nil || len(upstream) > maxMediaURLBytes || !m.stream.isAllowedURL(u) {
		return "", errors.New("media upstream is not allowed")
	}
	now := m.now()
	payload, err := json.Marshal(mediaTicket{upstream, now.Unix(), now.Add(m.ttl).Unix()})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := m.aead.Seal(nonce, nonce, payload, []byte(mediaPathPrefix+"v1"))
	token := base64.RawURLEncoding.EncodeToString(sealed)
	if len(token) > maxMediaTokenBytes {
		return "", errors.New("media ticket exceeds size limit")
	}
	return m.baseURL + mediaPathPrefix + token, nil
}

func (m *MediaHandler) ticket(r *http.Request) (mediaTicket, bool) {
	var t mediaTicket
	if !strings.HasPrefix(r.URL.Path, mediaPathPrefix) || r.URL.RawQuery != "" {
		return t, false
	}
	token := strings.TrimPrefix(r.URL.Path, mediaPathPrefix)
	if len(token) == 0 || len(token) > maxMediaTokenBytes {
		return t, false
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(sealed) < m.aead.NonceSize()+m.aead.Overhead() {
		return t, false
	}
	payload, err := m.aead.Open(nil, sealed[:m.aead.NonceSize()], sealed[m.aead.NonceSize():], []byte(mediaPathPrefix+"v1"))
	if err != nil || json.Unmarshal(payload, &t) != nil {
		return t, false
	}
	now := m.now().Unix()
	if t.Issued > now || t.Expires <= now || t.Expires <= t.Issued || t.Expires-t.Issued > int64(maxMediaTicketTTL/time.Second) || len(t.URL) > maxMediaURLBytes {
		return t, false
	}
	u, err := url.Parse(t.URL)
	return t, err == nil && m.stream.isAllowedURL(u)
}

// ServeHTTP must be mounted directly, without a recovery middleware that turns
// ErrAbortHandler into a response. Invalid requests close/reset the connection.
// TLS handshakes and the open port remain observable; this is access control,
// not protocol camouflage or a guarantee about a network operator's policy.
func (m *MediaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t, ok := m.ticket(r)
	if !ok {
		panic(http.ErrAbortHandler)
	}
	origin := r.Header.Get("Origin")
	if origin != "" && !m.origins[origin] {
		panic(http.ErrAbortHandler)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		panic(http.ErrAbortHandler)
	}
	if r.Method == http.MethodOptions {
		method := r.Header.Get("Access-Control-Request-Method")
		if origin == "" || (method != http.MethodGet && method != http.MethodHead) {
			panic(http.ErrAbortHandler)
		}
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			switch strings.ToLower(strings.TrimSpace(header)) {
			case "", "range", "if-range", "if-none-match", "if-modified-since":
			default:
				panic(http.ErrAbortHandler)
			}
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Add("Vary", "Origin")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Accept-Ranges, Content-Length, ETag, Last-Modified")
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "Range, If-Range, If-None-Match, If-Modified-Since")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m.stream.relayURL(mediaNoStoreWriter{w}, r, t.URL)
}

// Upstream cache directives cannot make a bearer-ticket response public.
type mediaNoStoreWriter struct{ http.ResponseWriter }

func (w mediaNoStoreWriter) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.ResponseWriter.WriteHeader(status)
}
func (w mediaNoStoreWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
