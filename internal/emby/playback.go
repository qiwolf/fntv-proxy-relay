package emby

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/config"
	"fntv-proxy/internal/logger"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// PlaybackHandler 处理 Emby PlaybackInfo
type PlaybackHandler struct {
	cache  *cache.Cache
	logger *logger.Logger
	emby   *config.EmbyConfig
	direct *directIssuer
}

// NewPlaybackHandler 创建 PlaybackInfo 处理器
func NewPlaybackHandler(c *cache.Cache, l *logger.Logger, emby *config.EmbyConfig) *PlaybackHandler {
	return &PlaybackHandler{cache: c, logger: l, emby: emby}
}

type playbackInfoResp struct {
	ItemID       string                   `json:"ItemId"`
	MediaSources []map[string]interface{} `json:"MediaSources"`
}

// Handle 改写 PlaybackInfo 响应并缓存 MediaSource
func (h *PlaybackHandler) Handle(resp *http.Response, body []byte) ([]byte, bool, error) {
	if h.emby.GetDeliveryMode() == "direct" {
		return h.handleDirect(resp, body)
	}
	h.logger.Info("🎯 [Emby] 拦截 PlaybackInfo")

	displayBody := body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		if decompressed, err := decompressGzip(body); err == nil {
			displayBody = decompressed
		}
	}

	var playback playbackInfoResp
	if err := json.Unmarshal(displayBody, &playback); err != nil {
		h.logger.Warn("[Emby] JSON 解析失败: %v", err)
		return body, false, nil
	}

	req := resp.Request
	itemID := playback.ItemID
	if itemID == "" {
		itemID = extractPlaybackItemID(req.URL.Path)
	}
	_, apiKey := getAPIKey(req)

	modified := false
	for _, source := range playback.MediaSources {
		path, _ := source["Path"].(string)
		id, _ := source["Id"].(string)
		if id == "" || path == "" {
			continue
		}

		curItemID := itemID
		if msItemID, ok := source["ItemId"].(string); ok && msItemID != "" {
			curItemID = msItemID
		} else if msItemID := jsonInt64(source["ItemId"]); msItemID > 0 {
			curItemID = fmt.Sprintf("%d", msItemID)
		}

		if isInfinite, ok := source["IsInfiniteStream"].(bool); ok && isInfinite {
			continue
		}

		if isLocalPath(path) && !strings.HasSuffix(strings.ToLower(path), ".strm") {
			continue
		}

		protocol, _ := source["Protocol"].(string)
		h.cache.Set(id, cache.MediaSource{
			ID:       id,
			ItemID:   curItemID,
			Path:     path,
			Protocol: protocol,
		})
		h.logger.Info("📄 [Emby] 缓存 MediaSource: id=%s, item=%s", id, curItemID)

		if apiKey != "" {
			directURL := fmt.Sprintf(
				"/Videos/%s/stream?MediaSourceId=%s&api_key=%s&Static=true",
				curItemID, id, apiKey,
			)
			source["SupportsDirectPlay"] = true
			source["SupportsDirectStream"] = true
			source["DirectStreamUrl"] = directURL
		}
		source["SupportsTranscoding"] = false
		delete(source, "TranscodingUrl")
		delete(source, "TranscodingSubProtocol")
		delete(source, "TranscodingContainer")

		if decoded, err := url.QueryUnescape(path); err == nil {
			source["Path"] = decoded
		}

		modified = true
	}

	if !modified {
		return body, false, nil
	}

	newBody, err := json.Marshal(playback)
	if err != nil {
		h.logger.Warn("[Emby] JSON 序列化失败: %v", err)
		return body, false, nil
	}

	h.logger.Info("✅ [Emby] PlaybackInfo 已改写为直链播放")
	return newBody, true, nil
}

func jsonInt64(v interface{}) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func decompressGzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
