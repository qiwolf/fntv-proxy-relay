package main

import (
	"context"
	"flag"
	"fntv-proxy/internal/management"
	webui "fntv-proxy/ui-prototype"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18764", "management listener")
	public := flag.String("public-url", "", "HTTPS origin, required for non-loopback binding")
	data := flag.String("data-dir", "./management-data", "private persistent management directory")
	legacy := flag.String("legacy-binary", "/app/fntv-proxy", "legacy runtime executable")
	unified := flag.String("unified-binary", "/app/fntv-unified", "unified runtime executable")
	flag.Parse()
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil {
		log.Fatal("listener must contain IP and port")
	}
	expectedHost := *listen
	if *public != "" {
		u, e := url.Parse(*public)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			log.Fatal("public-url must be an HTTPS origin")
		}
		expectedHost = u.Host
	} else if !net.ParseIP(host).IsLoopback() {
		log.Fatal("non-loopback management requires an HTTPS public-url and trusted reverse proxy")
	}
	dir, err := filepath.Abs(*data)
	if err != nil {
		log.Fatal("invalid state path")
	}
	store, err := management.NewDocumentStore(filepath.Join(dir, "config"), 0)
	if err != nil {
		log.Fatal(err)
	}
	lease, err := management.AcquireManagerLease(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer lease.Close()
	keys, err := management.NewKeyStore(filepath.Join(dir, "keys"))
	if err != nil {
		log.Fatal(err)
	}
	api, err := management.NewAPI(store, keys, strings.Repeat("unused", 8), expectedHost)
	if err != nil {
		log.Fatal(err)
	}
	api.Admin, err = management.NewAdminAuth(filepath.Join(dir, "administrator"))
	if err != nil {
		log.Fatal(err)
	}
	api.Certificates, err = management.NewCertificateStore(filepath.Join(dir, "certificates"))
	if err != nil {
		log.Fatal(err)
	}
	api.Runtime, err = management.NewRuntimeManager(filepath.Join(dir, "runtime"), *legacy, *unified)
	if err != nil {
		log.Fatal(err)
	}
	defer api.Runtime.Stop()
	api.Audit, err = management.NewDocumentStore(filepath.Join(dir, "audit"), 1<<20)
	if err != nil {
		log.Fatal(err)
	}
	if err = api.Runtime.Resume(); err != nil {
		log.Print("Previously applied runtime could not resume; review configuration in the management UI.")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != expectedHost {
			http.Error(w, "host denied", 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method denied", 405)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if name != "index.html" && name != "reviewed-ui.js" && name != "setup-wizard.js" && name != "playback-modes.js" && name != "app.js" && name != "live.css" {
			http.NotFound(w, r)
			return
		}
		raw, e := webui.Files.ReadFile(name)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if name == "index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			raw = []byte(strings.Replace(string(raw), "</body>", "<script src=\"app.js\"></script></body>", 1))
			raw = []byte(strings.Replace(string(raw), "</head>", "<link rel=\"stylesheet\" href=\"live.css\"></head>", 1))
			raw = []byte(strings.Replace(string(raw), "<header class=\"topbar\"><div>", "<header class=\"topbar\"><div class=\"topbar-state\">", 1))
			raw = []byte(strings.Replace(string(raw), "</span></div><div class=\"actions\"><span id=\"draftLabel\"", "</span><span id=\"draftLabel\"", 1))
		} else if name == "live.css" {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		if r.Method == "GET" {
			_, _ = w.Write(raw)
		}
	})
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopped
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Print("Relay management ready; open the management address to set up the single administrator.")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Print("management stopped: ", err)
	}
}
