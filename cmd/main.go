package main

import (
	"fntv-proxy/internal/config"
	"fntv-proxy/internal/emby"
	"fntv-proxy/internal/proxy"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := os.Getenv("CONFIG")
	if err := config.Load(configPath); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	fntvServer, err := proxy.NewServer(config.Global)
	if err != nil {
		log.Fatalf("创建飞牛代理服务器失败: %v", err)
	}

	var embyServer *emby.Server
	if config.Global.Emby.IsEnabled() {
		embyServer, err = emby.NewServer(config.Global)
		if err != nil {
			log.Fatalf("创建 Emby 代理服务器失败: %v", err)
		}
	}
	var jellyfinServer *emby.Server
	if config.Global.Jellyfin.IsEnabled() {
		jellyfinServer, err = emby.NewJellyfinServer(config.Global)
		if err != nil {
			log.Fatalf("创建 Jellyfin 代理服务器失败: %v", err)
		}
	}

	log.Printf("🚀 FNTV Proxy 启动")
	log.Printf("   服务角色: %s, 媒体路径: %s", config.Global.GetRole(), config.Global.GetDeliveryMode())
	if config.Global.GetRole() != "media" && config.Global.GetFNTVEnabled() {
		log.Printf("   飞牛监听: %s", config.Global.GetListenAddr())
	}
	if config.Global.GetRole() != "proxy" {
		log.Printf("   专用媒体监听: %s", config.Global.GetMedia().Listen)
	}
	config.Watch(func() { fntvServer.Reload() })
	if config.Global.Emby.IsEnabled() {
		log.Printf("   Emby监听: %s", config.Global.Emby.GetListenAddr())
		log.Printf("   Emby目标: %s", config.Global.Emby.GetTargetAddr())
	}
	log.Printf("   日志级别: %s", config.Global.GetLogLevel())
	if jellyfinServer != nil {
		log.Printf("   Jellyfin监听: %s", config.Global.Jellyfin.GetListenAddr())
		log.Printf("   Jellyfin目标: %s", config.Global.Jellyfin.GetTargetAddr())
	}
	log.Printf("   缓存TTL: %v", config.Global.GetCacheTTL())

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("🛑 正在关闭...")
		if embyServer != nil {
			embyServer.Stop()
		}
		if jellyfinServer != nil {
			jellyfinServer.Stop()
		}
		fntvServer.Stop()
	}()

	results := make(chan error, 3)
	if embyServer != nil {
		go func() {
			results <- embyServer.Start()
		}()
	}

	if jellyfinServer != nil {
		go func() {
			results <- jellyfinServer.Start()
		}()
	}

	if config.Global.GetFNTVEnabled() || config.Global.GetRole() != "proxy" {
		go func() { results <- fntvServer.Start() }()
	} else if embyServer == nil && jellyfinServer == nil {
		log.Fatal("至少启用一个服务")
	}
	if err := <-results; err != nil && err != http.ErrServerClosed {
		log.Fatalf("飞牛代理启动失败: %v", err)
	}
}
