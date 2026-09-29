package emby

import (
	"encoding/json"
	"errors"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/config"
	"fntv-proxy/internal/handler"
	"fntv-proxy/internal/logger"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type directIssuer struct {
	media     *handler.MediaHandler
	reader    *handler.StreamHandler
	emby      *config.EmbyConfig
	mediaBase string
}

func newDirectIssuer(cfg *config.Config, service *config.EmbyConfig, c *cache.Cache, log *logger.Logger) (*directIssuer, error) {
	m := cfg.GetMedia()
	key, err := m.ResolveKeyBytes()
	if err != nil {
		return nil, err
	}
	reader := handler.NewStreamHandler(c, log, "relay", cfg.GetAllowedUpstreams(), cfg.GetAllowedStrmRoots())
	reader.SetSTRMDirectoryMap(cfg.GetSTRMDirectoryMap())
	media, err := handler.NewScopedMediaHandler(reader, key, time.Duration(m.TokenTTLSeconds)*time.Second, m.PublicBaseURL, m.AllowedOrigins, m.TenantID)
	if err != nil {
		return nil, err
	}
	return &directIssuer{media: media, reader: reader, emby: service, mediaBase: strings.TrimRight(strings.TrimSpace(m.PublicBaseURL), "/")}, nil
}

// eligible deliberately excludes HLS/live/transcoded sources. These retain Emby's behavior.
func eligible(source map[string]interface{}) bool {
	p, _ := source["Path"].(string)
	if p == "" || source["IsInfiniteStream"] == true || source["RequiresOpening"] == true {
		return false
	}
	if strings.EqualFold(stringField(source, "Container"), "hls") || strings.EqualFold(stringField(source, "Container"), "m3u8") {
		return false
	}
	u, _ := url.Parse(p)
	if u != nil && strings.HasSuffix(strings.ToLower(u.Path), ".m3u8") {
		return false
	}
	return isRemoteURL(p) || strings.HasSuffix(strings.ToLower(p), ".strm")
}
func stringField(m map[string]interface{}, key string) string { s, _ := m[key].(string); return s }
func (d *directIssuer) issue(source map[string]interface{}) (string, error) {
	if d == nil {
		return "", errors.New("media issuer unavailable")
	}
	p := stringField(source, "Path")
	if !isRemoteURL(p) {
		var err error
		p, err = d.reader.ReadAllowedStrm(p)
		if err != nil {
			return "", errors.New("media source is not allowed")
		}
	}
	return d.media.IssueURL(d.emby.MapStrmPath(strings.TrimSpace(p)))
}

func (h *PlaybackHandler) handleDirect(resp *http.Response, body []byte) ([]byte, bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, false, nil
	}
	data := body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		var err error
		data, err = decompressGzip(body)
		if err != nil {
			return nil, false, errors.New("invalid playback encoding")
		}
	}
	var payload map[string]interface{}
	if json.Unmarshal(data, &payload) != nil {
		return nil, false, errors.New("invalid playback response")
	}
	// Never issue tickets from application-level error responses.
	if code, ok := payload["ErrorCode"]; ok && code != nil && code != "" {
		return body, false, nil
	}
	sources, _ := payload["MediaSources"].([]interface{})
	modified := false
	for _, raw := range sources {
		source, ok := raw.(map[string]interface{})
		if !ok || !eligible(source) {
			continue
		}
		// Honor the backend's device/profile decision; never force transcoding off.
		if source["SupportsDirectStream"] == false && source["SupportsDirectPlay"] == false {
			continue
		}
		ticket, err := h.direct.issue(source)
		if err != nil {
			return nil, false, errors.New("media source is not allowed")
		}
		source["Path"] = ticket
		// Emby Web passes this through apiClient.getUrl, which prefixes even
		// absolute URLs. Keep the stream route relative; only its redirect goes
		// to the media server. The opaque ticket remains the sole authority.
		u, err := url.Parse(ticket)
		if err != nil || !strings.HasPrefix(u.Path, h.direct.media.PathPrefix()) {
			return nil, false, errors.New("invalid media ticket URL")
		}
		source["DirectStreamUrl"] = "/fntv-direct/" + strings.TrimPrefix(u.Path, "/fntv-media/")
		source["AddApiKeyToDirectStreamUrl"] = false
		source["SupportsDirectPlay"] = false
		source["SupportsDirectStream"] = true
		delete(source, "RequiredHttpHeaders")
		modified = true
	}
	if !modified {
		return body, false, nil
	}
	resp.Header.Set("Cache-Control", "private, no-store")
	out, err := json.Marshal(payload)
	return out, true, err
}

var directStreamPath = regexp.MustCompile(`(?i)^/(?:emby/)?videos/([a-z0-9_-]+)/stream(?:\.[a-z0-9]+)?$`)

func (h *StreamHandler) handleDirect(w http.ResponseWriter, r *http.Request) bool {
	if h.handleTicketRedirect(w, r) {
		return true
	}
	match := directStreamPath.FindStringSubmatch(r.URL.Path)
	if match == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	// Only a static stream is eligible; transcoding requests remain with Emby.
	if !strings.EqualFold(r.URL.Query().Get("Static"), "true") {
		return false
	}
	source, status := h.authorizedSource(match[1], r)
	if status != 0 {
		http.Error(w, "media authorization failed", status)
		return true
	}
	if !eligible(source) {
		return false
	}
	if source["SupportsDirectStream"] == false && source["SupportsDirectPlay"] == false {
		return false
	}
	ticket, err := h.direct.issue(source)
	if err != nil {
		http.Error(w, "media source is not allowed", http.StatusForbidden)
		return true
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Location", ticket)
	w.WriteHeader(http.StatusFound)
	return true
}

var directTicketToken = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// This route transports an already-issued bearer ticket, not an upstream URL.
// The destination is always the configured media origin, where the ticket's
// signature, expiry and upstream allowlist are validated before any media I/O.
func (h *StreamHandler) handleTicketRedirect(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/emby")
	if path != "/fntv-direct" && !strings.HasPrefix(path, "/fntv-direct/") {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	if h.direct == nil || h.direct.mediaBase == "" || h.direct.media == nil {
		http.Error(w, "media issuer unavailable", http.StatusServiceUnavailable)
		return true
	}
	prefix := "/fntv-direct/" + strings.TrimPrefix(h.direct.media.PathPrefix(), "/fntv-media/")
	token := strings.TrimPrefix(path, prefix)
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || len(token) < 40 || len(token) > 16384 || !directTicketToken.MatchString(token) {
		http.Error(w, "invalid media ticket", http.StatusBadRequest)
		return true
	}
	w.Header().Set("Location", h.direct.mediaBase+h.direct.media.PathPrefix()+token)
	w.WriteHeader(http.StatusFound)
	return true
}

func (h *StreamHandler) authorizedSource(item string, r *http.Request) (map[string]interface{}, int) {
	_, token := getAPIKey(r)
	auth := r.Header.Get("Authorization")
	embyAuth := r.Header.Get("X-Emby-Authorization")
	if token == "" && auth == "" && embyAuth == "" {
		return nil, http.StatusUnauthorized
	}
	u := h.targetURL.ResolveReference(&url.URL{Path: "/Items/" + item + "/PlaybackInfo"})
	q := url.Values{}
	if token != "" {
		q.Set("api_key", token)
	}
	requested := r.URL.Query().Get("MediaSourceId")
	if requested != "" {
		q.Set("MediaSourceId", requested)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, http.StatusBadGateway
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("X-Emby-Authorization", embyAuth)
	client := *h.client
	client.Timeout = 15 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, resp.StatusCode
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, http.StatusBadGateway
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(body) > 4*1024*1024 {
		return nil, http.StatusBadGateway
	}
	var payload struct {
		MediaSources []map[string]interface{}
		ErrorCode    interface{}
	}
	if json.Unmarshal(body, &payload) != nil || (payload.ErrorCode != nil && payload.ErrorCode != "") {
		return nil, http.StatusBadGateway
	}
	if requested == "" && len(payload.MediaSources) != 1 {
		return nil, http.StatusNotFound
	}
	for _, source := range payload.MediaSources {
		if requested == "" || stringField(source, "Id") == requested {
			return source, 0
		}
	}
	return nil, http.StatusNotFound
}
