package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/logger"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const maxRedirects = 10
const relayPathPrefix = "/fntv-relay/"

// StreamHandler handles FNTV stream requests either by legacy redirect or by
// relaying bytes from the URL stored in the .strm file.
type StreamHandler struct {
	cache            *cache.Cache
	logger           *logger.Logger
	client           *http.Client
	mode             string
	allowedUpstreams map[string]struct{}
	allowedStrmRoots []string
	publicBaseURL    string
	directURL        func(string) (string, error)
}

// SetDirectRelay selects an external media gateway. Configure before serving.
func (h *StreamHandler) SetDirectRelay(issue func(string) (string, error)) {
	h.directURL = issue
}

func NewStreamHandler(c *cache.Cache, l *logger.Logger, mode string, allowedUpstreams, allowedStrmRoots []string, publicBaseURL ...string) *StreamHandler {
	h := &StreamHandler{
		cache:            c,
		logger:           l,
		mode:             strings.ToLower(strings.TrimSpace(mode)),
		allowedUpstreams: make(map[string]struct{}, len(allowedUpstreams)),
		allowedStrmRoots: append([]string(nil), allowedStrmRoots...),
	}
	if len(publicBaseURL) > 0 {
		h.publicBaseURL = strings.TrimRight(strings.TrimSpace(publicBaseURL[0]), "/")
	}
	if h.mode == "" {
		h.mode = "redirect"
	}
	for _, upstream := range allowedUpstreams {
		if normalized := normalizeAllowedUpstream(upstream); normalized != "" {
			h.allowedUpstreams[normalized] = struct{}{}
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	h.client = &http.Client{CheckRedirect: h.checkRedirect, Transport: transport}
	return h
}

func (h *StreamHandler) Handle(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if strings.HasPrefix(r.URL.Path, relayPathPrefix) {
		return h.handleAPIRelay(w, r)
	}
	if !isStreamRequest(r) {
		return false
	}
	mediaSourceID := r.URL.Query().Get("MediaSourceId")
	source, found := h.findInCache(r, mediaSourceID)
	if !found || !strings.EqualFold(filepath.Ext(source.Path), ".strm") {
		return false
	}
	h.logger.Info("🎬 拦截到视频流请求: %s", r.URL.Path)
	if h.mode == "relay" {
		return h.handleRelay(w, r, source)
	}
	return h.handleRedirect(w, r, source)
}

func (h *StreamHandler) handleAPIRelay(w http.ResponseWriter, r *http.Request) bool {
	token := strings.TrimPrefix(r.URL.Path, relayPathPrefix)
	if len(token) != 32 || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return true
	}
	if _, err := hex.DecodeString(token); err != nil {
		http.NotFound(w, r)
		return true
	}
	entry, found := h.cache.GetStreamURL("api:" + token)
	if !found {
		http.Error(w, "relay URL expired", http.StatusGone)
		return true
	}
	return h.relayURL(w, r, entry.URL)
}

func (h *StreamHandler) handleRedirect(w http.ResponseWriter, r *http.Request, source cache.MediaSource) bool {
	if streamURL, found := h.cache.GetStreamURL(source.ID); found {
		h.logger.Info("✅ 从缓存获取直链: %s", safeURLForLog(streamURL.URL))
		w.Header().Set("Location", streamURL.URL)
		w.WriteHeader(http.StatusFound)
		return true
	}
	strmURL, err := h.readStrm(source.Path, false)
	if err != nil {
		h.logger.Error("❌ 读取.strm失败: %v", err)
		return false
	}
	finalURL, err := h.resolveURL(strmURL, r)
	if err != nil {
		h.logger.Error("❌ 解析URL失败")
		return false
	}
	h.logger.Info("✅ 最终地址: %s", safeURLForLog(finalURL))
	h.cache.SetStreamURL(source.ID, finalURL)
	w.Header().Set("Location", finalURL)
	w.WriteHeader(http.StatusFound)
	return true
}

func (h *StreamHandler) handleRelay(w http.ResponseWriter, r *http.Request, source cache.MediaSource) bool {
	strmURL, err := h.readStrm(source.Path, true)
	if err != nil {
		h.logger.Warn("relay denied .strm path: %v", err)
		http.Error(w, "media source is not allowed", http.StatusForbidden)
		return true
	}
	// Resolve the .strm URL for every relay request. Final CDN URLs are often
	// signed and short-lived, so caching them can break later Range seeks.
	if h.directURL != nil {
		location, err := h.directURL(strmURL)
		if err != nil {
			http.Error(w, "media source is not allowed", http.StatusForbidden)
			return true
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusFound)
		return true
	}
	return h.relayURL(w, r, strmURL)
}

func (h *StreamHandler) relayURL(w http.ResponseWriter, r *http.Request, upstreamURL string) bool {
	parsed, err := url.Parse(upstreamURL)
	if err != nil || !h.isAllowedURL(parsed) {
		h.logger.Warn("relay denied upstream: %s", safeURLForLog(upstreamURL))
		http.Error(w, "media upstream is not allowed", http.StatusForbidden)
		return true
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, parsed.String(), nil)
	if err != nil {
		http.Error(w, "invalid media upstream", http.StatusBadGateway)
		return true
	}
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since", "User-Agent"} {
		copyRequestHeader(req.Header, r.Header, name)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "fntv-proxy-relay/1")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Error("relay upstream request failed for %s", safeURLForLog(upstreamURL))
		http.Error(w, "media upstream unavailable", http.StatusBadGateway)
		return true
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil {
		h.logger.Info("relay upstream: %s status=%d", safeURLForLog(resp.Request.URL.String()), resp.StatusCode)
	}
	copyRelayResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return true
	}
	if _, err := io.Copy(w, resp.Body); err != nil && !errors.Is(err, r.Context().Err()) {
		h.logger.Warn("relay stream interrupted: %v", err)
	}
	return true
}

// RewriteStreamAPIResponse rewrites allowlisted absolute media URLs returned
// by fnOS 0.9.8 POST /v/api/v1/stream to same-origin relay URLs.
func (h *StreamHandler) RewriteStreamAPIResponse(body []byte) ([]byte, int, error) {
	if h.mode != "relay" {
		return body, 0, nil
	}
	var payload any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return body, 0, err
	}
	rewritten := make(map[string]string)
	count, err := h.rewriteJSONURLs(&payload, rewritten)
	if err != nil || count == 0 {
		return body, count, err
	}
	result, err := json.Marshal(payload)
	if err != nil {
		return body, 0, err
	}
	return result, count, nil
}

func (h *StreamHandler) rewriteJSONURLs(value *any, rewritten map[string]string) (int, error) {
	switch current := (*value).(type) {
	case map[string]any:
		total := 0
		for key, child := range current {
			count, err := h.rewriteJSONURLs(&child, rewritten)
			if err != nil {
				return total, err
			}
			current[key] = child
			total += count
		}
		return total, nil
	case []any:
		total := 0
		for i, child := range current {
			count, err := h.rewriteJSONURLs(&child, rewritten)
			if err != nil {
				return total, err
			}
			current[i] = child
			total += count
		}
		return total, nil
	case string:
		parsed, err := url.Parse(current)
		if err != nil || !h.isAllowedURL(parsed) {
			return 0, nil
		}
		if replacement, ok := rewritten[current]; ok {
			*value = replacement
			return 1, nil
		}
		if h.directURL != nil {
			replacement, err := h.directURL(current)
			if err != nil {
				return 0, err
			}
			rewritten[current] = replacement
			*value = replacement
			return 1, nil
		}
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return 0, err
		}
		token := hex.EncodeToString(random)
		replacement := h.publicBaseURL + relayPathPrefix + token
		if h.publicBaseURL == "" {
			replacement = relayPathPrefix + token
		}
		h.cache.SetStreamURL("api:"+token, current)
		rewritten[current] = replacement
		*value = replacement
		return 1, nil
	default:
		return 0, nil
	}
}

// ReadAllowedStrm applies the same root and symlink restrictions to other issuers.
func (h *StreamHandler) ReadAllowedStrm(path string) (string, error) {
	return h.readStrm(path, true)
}

func (h *StreamHandler) readStrm(path string, enforceRoots bool) (string, error) {
	if enforceRoots {
		allowed, err := pathWithinRoots(path, h.allowedStrmRoots)
		if err != nil {
			return "", err
		}
		if !allowed {
			return "", fmt.Errorf("path is outside allowed_strm_roots")
		}
	}
	return ReadStrmFile(path)
}

func pathWithinRoots(path string, roots []string) (bool, error) {
	realPath, err := filepath.EvalSymlinks(filepath.Clean(strings.ReplaceAll(path, "\\", "/")))
	if err != nil {
		return false, err
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return false, err
	}
	for _, root := range roots {
		realRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		realRoot, err = filepath.Abs(realRoot)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(realRoot, realPath)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true, nil
		}
	}
	return false, nil
}

func normalizeAllowedUpstream(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		return strings.ToLower(parsed.Host)
	}
	return strings.ToLower(value)
}

func (h *StreamHandler) isAllowedURL(u *url.URL) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	if _, exact := h.allowedUpstreams[strings.ToLower(u.Host)]; exact {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if _, hostOnly := h.allowedUpstreams[host]; !hostOnly {
		return false
	}
	port := u.Port()
	return port == "" || (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443")
}

func (h *StreamHandler) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("too many redirects")
	}
	if h.mode == "relay" && !h.isAllowedURL(req.URL) {
		return fmt.Errorf("redirect upstream %s is not allowed", safeURLForLog(req.URL.String()))
	}
	return nil
}

func (h *StreamHandler) resolveURL(urlStr string, originalReq *http.Request) (string, error) {
	req, err := http.NewRequestWithContext(originalReq.Context(), http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	copyRequestHeader(req.Header, originalReq.Header, "User-Agent")
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String(), nil
	}
	return urlStr, nil
}

func copyRequestHeader(dst, src http.Header, name string) {
	if value := src.Get(name); value != "" {
		dst.Set(name, value)
	}
}

func copyRelayResponseHeaders(dst, src http.Header) {
	for _, name := range []string{"Accept-Ranges", "Cache-Control", "Content-Disposition", "Content-Length", "Content-Range", "Content-Type", "ETag", "Last-Modified"} {
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}

func safeURLForLog(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "[invalid-url]"
	}
	host := u.Hostname()
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	return u.Scheme + "://" + host + u.EscapedPath()
}

func (h *StreamHandler) findInCache(r *http.Request, mediaSourceID string) (cache.MediaSource, bool) {
	if mediaSourceID != "" {
		if source, found := h.cache.Get(mediaSourceID); found {
			return source, true
		}
	}
	itemID := extractItemID(r.URL.Path)
	if itemID != "" {
		return h.cache.GetByItemID(itemID)
	}
	return cache.MediaSource{}, false
}

func extractItemID(path string) string {
	path = strings.TrimPrefix(path, "/emby")
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if (part == "videos" || part == "Items") && i+1 < len(parts) && len(parts[i+1]) == 32 {
			return parts[i+1]
		}
	}
	return ""
}

func isStreamRequest(r *http.Request) bool {
	path := strings.ToLower(r.URL.Path)
	return strings.Contains(path, "/stream.") || strings.Contains(path, "/master.m3u8")
}
