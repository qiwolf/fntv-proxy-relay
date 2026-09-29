package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/config"
	"fntv-proxy/internal/handler"
	"fntv-proxy/internal/logger"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Server 代理服务器
type Server struct {
	config          *config.Config
	logger          *logger.Logger
	cache           *cache.Cache
	playbackHandler *handler.PlaybackHandler
	streamHandler   *handler.StreamHandler
	proxy           *httputil.ReverseProxy
	mediaHandler    *handler.MediaHandler
	httpServer      *http.Server
	mu              sync.Mutex
	servers         []*http.Server
	stopping        bool
	started         bool
	stopCache       sync.Once
	prepareOnce     sync.Once
}

// NewServer 创建代理服务器
func NewServer(cfg *config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// 创建日志
	log := logger.New(cfg.GetLogLevel(), cfg.LogDir)

	// 解析目标地址
	targetURL, err := url.Parse(cfg.GetTargetAddr())
	if err != nil {
		return nil, err
	}

	// 创建缓存（直链缓存使用 cache_ttl，MediaSource 不过期）
	ttl := cfg.GetCacheTTL()
	if ttl <= 0 {
		ttl = time.Hour
	}
	c := cache.NewWithStreamTTL(ttl)

	// 创建处理器
	ph := handler.NewPlaybackHandler(c, log)
	sh := handler.NewStreamHandler(c, log, cfg.GetStreamMode(), cfg.GetAllowedUpstreams(), cfg.GetAllowedStrmRoots(), cfg.GetPublicBaseURL())

	// 创建反向代理
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	s := &Server{
		config:          cfg,
		logger:          log,
		cache:           c,
		playbackHandler: ph,
		streamHandler:   sh,
		proxy:           proxy,
	}
	if cfg.GetRole() != "proxy" || cfg.GetDeliveryMode() == "direct" {
		media := cfg.GetMedia()
		key, err := media.ResolveKeyBytes()
		if err != nil {
			c.Stop()
			return nil, err
		}
		s.mediaHandler, err = handler.NewScopedMediaHandler(sh, key, time.Duration(media.TokenTTLSeconds)*time.Second, media.PublicBaseURL, media.AllowedOrigins, media.TenantID)
		if err != nil {
			c.Stop()
			return nil, err
		}
		if cfg.GetDeliveryMode() == "direct" && cfg.GetRole() != "media" {
			sh.SetDirectRelay(s.mediaHandler.IssueURL)
		}
	}
	return s, nil
}

// Start 启动服务器
func (s *Server) Start() error {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return http.ErrServerClosed
	}
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("server already started")
	}
	s.prepare()

	// Bind every requested listener before serving any traffic. A failed media
	// listener must not leave a seemingly healthy, half-started deployment.
	type endpoint struct {
		server    *http.Server
		tlsConfig *tls.Config
	}
	var endpoints []endpoint
	if s.config.GetRole() != "media" {
		s.httpServer = newHTTPServer(s.config.GetListenAddr(), s.Handler())
		endpoints = append(endpoints, endpoint{server: s.httpServer})
	}
	if s.config.GetRole() != "proxy" {
		media := s.config.GetMedia()
		e := endpoint{server: newHTTPServer(media.Listen, s.mediaHandler)}
		if media.TLSCertFile != "" {
			var err error
			e.tlsConfig, err = LoadTLSConfig(media.TLSCertFile, media.TLSKeyFile, func(message string) { s.logger.Warn("%s", message) })
			if err != nil {
				s.mu.Unlock()
				return fmt.Errorf("load media TLS certificate: %w", err)
			}
		}
		endpoints = append(endpoints, e)
	}
	var listeners []net.Listener
	for _, e := range endpoints {
		ln, err := net.Listen("tcp", e.server.Addr)
		if err != nil {
			for _, bound := range listeners {
				_ = bound.Close()
			}
			s.mu.Unlock()
			return fmt.Errorf("listen %s: %w", e.server.Addr, err)
		}
		if e.tlsConfig != nil {
			ln = tls.NewListener(ln, e.tlsConfig)
		}
		listeners = append(listeners, ln)
	}
	s.started = true
	results := make(chan error, len(endpoints))
	for i, e := range endpoints {
		s.servers = append(s.servers, e.server)
		go func(server *http.Server, ln net.Listener) { results <- server.Serve(ln) }(e.server, listeners[i])
	}
	s.mu.Unlock()
	err := <-results
	_ = s.Stop()
	return err
}

// Handler embeds the proxy without starting an additional TCP listener.
func (s *Server) Handler() http.Handler      { s.prepare(); return s.loggingMiddleware(s.proxy) }
func (s *Server) MediaHandler() http.Handler { return s.mediaHandler }
func (s *Server) prepare() {
	s.prepareOnce.Do(func() {
		originalDirector := s.proxy.Director
		s.proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = req.URL.Host
			if s.isStreamAPIRequest(req) {
				req.Header.Set("Accept-Encoding", "identity")
			}
		}

		// 设置ModifyResponse
		s.proxy.ModifyResponse = s.handleResponse
	})
}

// LoadTLSConfig validates the initial pair and hot-reloads replacements.
func LoadTLSConfig(certFile, keyFile string, warn func(string)) (*tls.Config, error) {
	c, err := newCertificateReloader(certFile, keyFile, warn)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: c.GetCertificate, NextProtos: []string{"http/1.1"}}, nil
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
}

// Stop 停止服务器
func (s *Server) Stop() error {
	s.mu.Lock()
	s.stopping = true
	servers := append([]*http.Server(nil), s.servers...)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var firstErr error
	for _, server := range servers {
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	s.stopCache.Do(s.cache.Stop)
	return firstErr
}

// Reload 重新加载配置
func (s *Server) Reload() {
	// 更新日志级别
	s.logger.SetLevel(s.config.GetLogLevel())
	s.logger.Info("配置已重载，新日志级别: %s", s.config.GetLogLevel())
}

// handleResponse 处理响应
func (s *Server) handleResponse(resp *http.Response) error {
	if s.isStreamAPIRequest(resp.Request) {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		newBody, count, err := s.streamHandler.RewriteStreamAPIResponse(body)
		if err != nil {
			s.logger.Warn("fnOS stream API response rewrite failed")
			// Never return the original private URL/credentials when ticket
			// issuance fails. The reverse proxy returns a generic 502.
			return fmt.Errorf("media response rewrite failed")
		}
		if count > 0 {
			s.logger.Info("rewrote %d fnOS stream API URL(s) to relay", count)
			resp.Header.Set("Cache-Control", "private, no-store")
		}
		resp.Body = io.NopCloser(bytes.NewReader(newBody))
		resp.ContentLength = int64(len(newBody))
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBody)))
		resp.Header.Del("Content-Encoding")
		return nil
	}
	// 只处理 PlaybackInfo，其他响应直接透传（避免读取大文件到内存）
	if !s.isPlaybackInfoRequest(resp.Request) {
		return nil
	}

	// 读取响应体（PlaybackInfo 数据量小，可以安全读取）
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()

	// trace级别：记录响应体
	if s.logger.GetLevel() <= logger.TraceLevel {
		s.logResponseBody(resp, body)
	}

	// 处理 PlaybackInfo
	newBody, err := s.playbackHandler.Handle(resp, body)
	if err != nil {
		s.logger.Error("处理PlaybackInfo失败: %v", err)
	}
	resp.Body = io.NopCloser(bytes.NewBuffer(newBody))
	return nil
}

func (s *Server) isStreamAPIRequest(req *http.Request) bool {
	return req != nil && req.Method == http.MethodPost && strings.EqualFold(req.URL.Path, "/v/api/v1/stream")
}

// logResponseBody 记录响应体（trace级别）
func (s *Server) logResponseBody(resp *http.Response, body []byte) {
	s.logger.Trace("=== RESPONSE BODY ===")
	s.logger.Trace("Request: %s %s", resp.Request.Method, resp.Request.URL.Path)
	s.logger.Trace("Status: %d", resp.StatusCode)
	s.logger.Trace("Headers:")
	for name, values := range resp.Header {
		if isSensitiveHeader(name) {
			s.logger.Trace("  %s: [REDACTED]", name)
			continue
		}
		for _, v := range values {
			s.logger.Trace("  %s: %s", name, v)
		}
	}
	s.logger.Trace("Body: [REDACTED, %d bytes]", len(body))
	s.logger.Trace("=====================")
}

// isPlaybackInfoRequest 检查是否是PlaybackInfo
func (s *Server) isPlaybackInfoRequest(req *http.Request) bool {
	// 支持 GET 和 POST 请求
	if req.Method != "POST" && req.Method != "GET" {
		return false
	}
	return len(req.URL.Path) > 0 &&
		contains(req.URL.Path, "/PlaybackInfo")
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// loggingMiddleware 日志中间件
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 记录请求（debug级别）
		s.logger.Debug("请求: %s %s", r.Method, safeRequestPath(r))

		// trace级别：记录完整请求信息（包括body）
		if s.logger.GetLevel() <= logger.TraceLevel {
			s.logRequest(r)
		}

		// 检查是否是视频流请求
		if s.streamHandler.Handle(w, r) {
			return // 已处理，直接返回
		}

		// 继续处理
		next.ServeHTTP(w, r)
	})
}

// logRequest 记录完整请求（trace级别），返回读取的body用于恢复
func (s *Server) logRequest(r *http.Request) []byte {
	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.logger.Trace("读取请求体失败: %v", err)
	}
	// 恢复请求体，供后续处理使用
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	// 记录请求详情
	s.logger.Trace("=== REQUEST ===")
	s.logger.Trace("Method: %s", r.Method)
	s.logger.Trace("URL: %s", safeRequestPath(r))
	s.logger.Trace("Headers:")
	for name, values := range r.Header {
		if isSensitiveHeader(name) {
			s.logger.Trace("  %s: [REDACTED]", name)
			continue
		}
		for _, v := range values {
			s.logger.Trace("  %s: %s", name, v)
		}
	}
	if len(body) > 0 {
		s.logger.Trace("Body: [REDACTED, %d bytes]", len(body))
	}
	s.logger.Trace("===============")

	return body
}

func isSensitiveHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-emby-token", "x-mediabrowser-token", "referer", "location":
		return true
	default:
		return false
	}
}

func safeRequestPath(r *http.Request) string {
	for _, prefix := range []string{"/fntv-relay/", "/fntv-media/"} {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return prefix + "[REDACTED]"
		}
	}
	return r.URL.EscapedPath()
}
