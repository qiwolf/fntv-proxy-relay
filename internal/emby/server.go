package emby

import (
	"bytes"
	"context"
	"fntv-proxy/internal/cache"
	"fntv-proxy/internal/config"
	"fntv-proxy/internal/handler"
	"fntv-proxy/internal/logger"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server Emby 302 代理服务器
type Server struct {
	config          *config.Config
	service         *config.EmbyConfig
	name            string
	logger          *logger.Logger
	cache           *cache.Cache
	playbackHandler *PlaybackHandler
	streamHandler   *StreamHandler
	proxy           *httputil.ReverseProxy
	targetURL       *url.URL
	httpServer      *http.Server
	prepareOnce     sync.Once
	stopCache       sync.Once
}

// NewServer 创建 Emby 代理服务器
func NewServer(cfg *config.Config) (*Server, error) {
	return newServer(cfg, &cfg.Emby, "Emby")
}

// NewJellyfinServer uses the shared protocol without copying configuration locks
// or mutating Emby's configuration, cache or running server.
func NewJellyfinServer(cfg *config.Config) (*Server, error) {
	return newServer(cfg, &cfg.Jellyfin, "Jellyfin")
}

func newServer(cfg *config.Config, service *config.EmbyConfig, name string) (*Server, error) {
	log := logger.New(cfg.GetLogLevel(), cfg.LogDir)

	targetURL, err := url.Parse(service.GetTargetAddr())
	if err != nil {
		return nil, err
	}

	ttl := service.GetCacheTTL(cfg.GetCacheTTL())
	c := cache.NewWithStreamTTL(ttl)

	ph := NewPlaybackHandler(c, log, service)
	sh := NewStreamHandler(c, log, service, targetURL)
	if len(cfg.GetSTRMDirectoryMap()) > 0 {
		sh.strmReader = handler.NewStreamHandler(c, log, "redirect", cfg.GetAllowedUpstreams(), cfg.GetAllowedStrmRoots())
		sh.strmReader.SetSTRMDirectoryMap(cfg.GetSTRMDirectoryMap())
	}
	if service.GetDeliveryMode() == "direct" {
		issuer, err := newDirectIssuer(cfg, service, c, log)
		if err != nil {
			c.Stop()
			return nil, err
		}
		ph.direct, sh.direct = issuer, issuer
	}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	return &Server{
		config:          cfg,
		service:         service,
		name:            name,
		logger:          log,
		cache:           c,
		playbackHandler: ph,
		streamHandler:   sh,
		proxy:           proxy,
		targetURL:       targetURL,
	}, nil
}

// Start 启动 Emby 代理
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr: s.service.GetListenAddr(), Handler: s.Handler(),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second,
	}
	s.logger.Info("[%s] proxy listener: %s", s.name, s.service.GetListenAddr())
	return s.httpServer.ListenAndServe()
}

// Handler embeds this adapter without an additional listener.
func (s *Server) Handler() http.Handler {
	s.prepareOnce.Do(func() {
		originalDirector := s.proxy.Director
		s.proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = s.targetURL.Host
			// PlaybackInfo is inspected and may be rewritten. Do not negotiate
			// browser encodings (for example Brotli) that our parser cannot decode.
			// Other responses, including media streams, retain normal negotiation.
			if isPlaybackInfoRequest(req) {
				req.Header.Set("Accept-Encoding", "identity")
			}
		}
		s.proxy.ModifyResponse = s.handleResponse
	})
	return s.loggingMiddleware(s.proxy)
}

// Stop 优雅关闭
func (s *Server) Stop() error {
	defer s.stopCache.Do(s.cache.Stop)
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleResponse(resp *http.Response) error {
	if !isPlaybackInfoRequest(resp.Request) {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()

	newBody, modified, err := s.playbackHandler.Handle(resp, body)
	if err != nil {
		s.logger.Error("[%s] 处理 PlaybackInfo 失败: %v", s.name, err)
		if s.service.GetDeliveryMode() == "direct" {
			return err
		}
		newBody = body
	}

	if modified {
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		resp.ContentLength = int64(len(newBody))
		resp.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
	}

	resp.Body = io.NopCloser(bytes.NewBuffer(newBody))
	return nil
}

func isPlaybackInfoRequest(req *http.Request) bool {
	if req.Method != http.MethodPost && req.Method != http.MethodGet {
		return false
	}
	return strings.Contains(strings.ToLower(req.URL.Path), "/playbackinfo")
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Debug("[%s] 请求: %s %s", s.name, r.Method, r.URL.Path)

		if s.streamHandler.Handle(w, r) {
			return
		}

		next.ServeHTTP(w, r)
	})
}
