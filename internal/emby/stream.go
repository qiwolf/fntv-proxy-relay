package emby

import (
	"encoding/json"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/config"
	"fntv-proxy/internal/handler"
	"fntv-proxy/internal/logger"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// StreamHandler 处理 Emby 视频流 302 重定向
type StreamHandler struct {
	cache      *cache.Cache
	logger     *logger.Logger
	emby       *config.EmbyConfig
	targetURL  *url.URL
	client     *http.Client
	direct     *directIssuer
	strmReader *handler.StreamHandler
}

// NewStreamHandler 创建流处理器
func NewStreamHandler(c *cache.Cache, l *logger.Logger, emby *config.EmbyConfig, targetURL *url.URL) *StreamHandler {
	return &StreamHandler{
		cache:     c,
		logger:    l,
		emby:      emby,
		targetURL: targetURL,
		client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Handle 拦截 stream/universal 请求并 302 到真实直链
func (h *StreamHandler) Handle(w http.ResponseWriter, r *http.Request) bool {
	if h.emby.GetDeliveryMode() == "direct" {
		return h.handleDirect(w, r)
	}
	if !isStreamRequest(r) {
		return false
	}

	h.logger.Info("🎬 [Emby] 拦截流请求")

	mediaSourceID := r.URL.Query().Get("MediaSourceId")
	source, found := h.findInCache(r, mediaSourceID)
	if !found {
		itemID := extractItemID(r.URL.Path)
		if itemID != "" {
			source, found = h.fetchFromBackend(itemID, mediaSourceID, r)
		}
	}
	if !found {
		h.logger.Warn("[Emby] 未找到 MediaSource")
		return false
	}

	embyPath := source.Path

	if isLocalPath(embyPath) && !strings.HasSuffix(strings.ToLower(embyPath), ".strm") {
		h.logger.Info("[Emby] 本地媒体，回源")
		redirectToOriginal(w, r)
		return true
	}

	if streamURL, ok := h.cache.GetStreamURL(source.ID); ok {
		h.logger.Info("✅ [Emby] 从缓存获取直链: %s", logger.SafeURL(streamURL.URL))
		h.logDirectLinkType(streamURL.URL)
		http.Redirect(w, r, streamURL.URL, http.StatusFound)
		return true
	}

	mediaURL, err := h.resolveMediaURL(embyPath)
	if err != nil {
		h.logger.Error("[Emby] 解析媒体地址失败")
		return h.handleError(w, r)
	}
	h.logger.Info("📄 [Emby] 媒体地址: %s", logger.SafeURL(mediaURL))

	finalURL, err := h.resolveFinalURL(mediaURL, r)
	if err != nil {
		h.logger.Error("[Emby] 解析直链失败")
		return h.handleError(w, r)
	}

	h.logger.Info("✅ [Emby] 最终直链: %s", logger.SafeURL(finalURL))
	h.logDirectLinkType(finalURL)
	h.cache.SetStreamURL(source.ID, finalURL)
	http.Redirect(w, r, finalURL, http.StatusFound)
	return true
}

func (h *StreamHandler) logDirectLinkType(finalURL string) {
	linkType := handler.ClassifyDirectLink(finalURL)
	h.logger.Info("📡 [Emby] 直链类型: %s → %s", linkType, handler.DirectLinkMetaHint(linkType))
}

func (h *StreamHandler) handleError(w http.ResponseWriter, r *http.Request) bool {
	if h.emby.GetProxyErrorStrategy() == config.EmbyErrorStrategyReject {
		http.Error(w, "代理失败", http.StatusInternalServerError)
		return true
	}
	return false
}

func (h *StreamHandler) resolveMediaURL(embyPath string) (string, error) {
	mediaURL := embyPath
	read := handler.ReadStrmFile
	if h.strmReader != nil {
		read = h.strmReader.ReadAllowedStrm
	}

	if strings.HasPrefix(embyPath, "nfs:") && strings.HasSuffix(strings.ToLower(embyPath), ".strm") {
		content, err := read(embyPath)
		if err != nil {
			return "", err
		}
		mediaURL = content
	} else if strings.HasSuffix(strings.ToLower(embyPath), ".strm") && !isRemoteURL(embyPath) {
		content, err := read(embyPath)
		if err != nil {
			return "", err
		}
		mediaURL = content
	}

	return h.emby.MapStrmPath(strings.TrimSpace(mediaURL)), nil
}

func (h *StreamHandler) resolveFinalURL(originLink string, r *http.Request) (string, error) {
	link := originLink
	if !strings.Contains(link, "smartstrm") &&
		(strings.Contains(link, "115/newurl") || strings.Contains(link, "115/url")) {
		if strings.Contains(link, "?") {
			link += "&force=1"
		} else {
			link += "?force=1"
		}
	}

	req, err := http.NewRequest(http.MethodGet, link, nil)
	if err != nil {
		return "", err
	}

	ua := r.Header.Get("User-Agent")
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	} else {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return originLink, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusSeeOther {
		if location := resp.Header.Get("Location"); location != "" {
			return location, nil
		}
	}

	return originLink, nil
}

func (h *StreamHandler) findInCache(r *http.Request, mediaSourceID string) (cache.MediaSource, bool) {
	if mediaSourceID != "" {
		if source, ok := h.cache.Get(mediaSourceID); ok {
			return source, true
		}
	}
	if itemID := extractItemID(r.URL.Path); itemID != "" {
		if source, ok := h.cache.GetByItemID(itemID); ok {
			return source, true
		}
	}
	return cache.MediaSource{}, false
}

func (h *StreamHandler) fetchFromBackend(itemID, mediaSourceID string, r *http.Request) (cache.MediaSource, bool) {
	_, apiKey := getAPIKey(r)
	if apiKey == "" {
		return cache.MediaSource{}, false
	}

	u := h.targetURL.ResolveReference(&url.URL{Path: "/Items/" + itemID + "/PlaybackInfo"})
	q := u.Query()
	q.Set("api_key", apiKey)
	if mediaSourceID != "" {
		q.Set("MediaSourceId", mediaSourceID)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return cache.MediaSource{}, false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return cache.MediaSource{}, false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return cache.MediaSource{}, false
	}

	var result struct {
		ItemID       string `json:"ItemId"`
		MediaSources []struct {
			ID       string `json:"Id"`
			Path     string `json:"Path"`
			Protocol string `json:"Protocol"`
		} `json:"MediaSources"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return cache.MediaSource{}, false
	}

	for _, ms := range result.MediaSources {
		if mediaSourceID != "" && ms.ID != mediaSourceID {
			continue
		}
		source := cache.MediaSource{
			ID:       ms.ID,
			ItemID:   result.ItemID,
			Path:     ms.Path,
			Protocol: ms.Protocol,
		}
		h.cache.Set(ms.ID, source)
		return source, true
	}
	return cache.MediaSource{}, false
}

func isStreamRequest(r *http.Request) bool {
	path := strings.ToLower(r.URL.Path)
	if strings.Contains(path, "/subtitles") {
		return false
	}
	return strings.Contains(path, "/stream") ||
		strings.Contains(path, "/universal")
}
