package unified

import (
	"context"
	"crypto/tls"
	"fmt"
	"fntv-proxy/internal/emby"
	"fntv-proxy/internal/proxy"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Server struct {
	cfg      *Config
	handlers map[string]http.Handler
	stops    []func() error
	http     *http.Server
	direct   []*http.Server
	tls      *tls.Config
	once     sync.Once
}

func New(c *Config) (*Server, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	s := &Server{cfg: c, handlers: map[string]http.Handler{}}
	success := false
	defer func() {
		if !success {
			_ = s.Stop()
		}
	}()
	if c.TLSCertFile != "" {
		var err error
		s.tls, err = proxy.LoadTLSConfig(c.TLSCertFile, c.TLSKeyFile, func(m string) { log.Print(m) })
		if err != nil {
			return nil, fmt.Errorf("TLS: %w", err)
		}
	}
	for id, service := range c.Services {
		cfg := c.legacy(id, service)
		var h http.Handler
		if c.Role == "media" || service.Type == "fntv" {
			p, err := proxy.NewServer(cfg)
			if err != nil {
				return nil, fmt.Errorf("service %s: %w", id, err)
			}
			s.stops = append(s.stops, p.Stop)
			if c.Role == "media" {
				h = p.MediaHandler()
			} else {
				h = p.Handler()
			}
		} else {
			var p *emby.Server
			var err error
			if service.Type == "emby" {
				p, err = emby.NewServer(cfg)
			} else {
				p, err = emby.NewJellyfinServer(cfg)
			}
			if err != nil {
				return nil, fmt.Errorf("service %s: %w", id, err)
			}
			s.stops = append(s.stops, p.Stop)
			h = p.Handler()
		}
		if c.Role == "media" {
			s.handlers[id] = h
		} else {
			for _, host := range service.Hosts {
				k, _ := hostKey(host)
				s.handlers[k] = h
			}
		}
		if service.DirectListen != "" {
			s.direct = append(s.direct, &http.Server{Addr: service.DirectListen, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10})
		}
	}
	s.http = &http.Server{Addr: c.Listen, Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	success = true
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var key string
	if s.cfg.Role == "media" {
		// The selected handler independently authenticates both tenant and ticket.
		if !strings.HasPrefix(r.URL.Path, "/fntv-media/") {
			panic(http.ErrAbortHandler)
		}
		rest := strings.TrimPrefix(r.URL.Path, "/fntv-media/")
		var found bool
		key, _, found = strings.Cut(rest, "/")
		if !found {
			panic(http.ErrAbortHandler)
		}
	} else {
		var err error
		key, err = hostKey(r.Host)
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	h, ok := s.handlers[key]
	if !ok {
		if s.cfg.Role == "media" {
			panic(http.ErrAbortHandler)
		}
		http.NotFound(w, r)
		return
	}
	h.ServeHTTP(w, r)
}
func (s *Server) Start() error {
	servers := append([]*http.Server{s.http}, s.direct...)
	listeners := make([]net.Listener, 0, len(servers))
	for _, srv := range servers {
		ln, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			for _, bound := range listeners {
				_ = bound.Close()
			}
			return err
		}
		if s.tls != nil {
			ln = tls.NewListener(ln, s.tls)
		}
		listeners = append(listeners, ln)
	}
	results := make(chan error, len(servers))
	for i, srv := range servers {
		go func(srv *http.Server, ln net.Listener) { results <- srv.Serve(ln) }(srv, listeners[i])
	}
	err := <-results
	_ = s.Stop()
	return err
}
func (s *Server) Stop() error {
	var first error
	s.once.Do(func() {
		if s.http != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, srv := range append([]*http.Server{s.http}, s.direct...) {
				if err := srv.Shutdown(ctx); err != nil {
					_ = srv.Close()
					if first == nil {
						first = err
					}
				}
			}
		}
		for _, stop := range s.stops {
			if err := stop(); err != nil && first == nil {
				first = err
			}
		}
	})
	return first
}
