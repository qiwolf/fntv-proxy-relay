package config

import (
	"strings"
	"sync"
	"time"
)

// EmbyConfig Emby 302 代理配置
type EmbyConfig struct {
	Enabled            bool     `mapstructure:"enabled"`
	DeliveryMode       string   `mapstructure:"delivery_mode"`
	ListenAddr         string   `mapstructure:"listen"`
	TargetAddr         string   `mapstructure:"target"`
	CacheTTLMinutes    int      `mapstructure:"cache_ttl"`
	LocalMediaRoot     string   `mapstructure:"local_media_root"`
	ProxyErrorStrategy string   `mapstructure:"proxy_error_strategy"`
	StrmPathMap        []string `mapstructure:"strm_path_map"`

	mutex   sync.RWMutex
	pathMap [][2]string
}

const (
	EmbyErrorStrategyOrigin = "origin"
	EmbyErrorStrategyReject = "reject"
)

func (e *EmbyConfig) GetDeliveryMode() string {
	v := strings.ToLower(strings.TrimSpace(e.DeliveryMode))
	if v == "" {
		return "redirect"
	}
	return v
}

// initEmbyDefaults 初始化 Emby 配置默认值
func initEmbyDefaults() {
	if Global.Emby.ListenAddr == "" {
		Global.Emby.ListenAddr = ":8095"
	}
	if Global.Emby.TargetAddr == "" {
		Global.Emby.TargetAddr = "http://127.0.0.1:8096"
	}
	if Global.Emby.ProxyErrorStrategy == "" {
		Global.Emby.ProxyErrorStrategy = EmbyErrorStrategyOrigin
	}
	Global.Emby.parsePathMap()
}

// parsePathMap 解析 strm 路径映射
func (e *EmbyConfig) parsePathMap() {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	e.pathMap = make([][2]string, 0, len(e.StrmPathMap))
	for _, entry := range e.StrmPathMap {
		parts := strings.Split(entry, "=>")
		if len(parts) != 2 {
			continue
		}
		from := strings.TrimSpace(parts[0])
		to := strings.TrimSpace(parts[1])
		if from != "" && to != "" {
			e.pathMap = append(e.pathMap, [2]string{from, to})
		}
	}
}

// IsEnabled 是否启用 Emby 代理
func (e *EmbyConfig) IsEnabled() bool {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.Enabled
}

// GetListenAddr 获取 Emby 监听地址
func (e *EmbyConfig) GetListenAddr() string {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.ListenAddr
}

// GetTargetAddr 获取 Emby 目标地址
func (e *EmbyConfig) GetTargetAddr() string {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.TargetAddr
}

// GetCacheTTL 获取 Emby 缓存 TTL，未配置时使用全局 cache_ttl
func (e *EmbyConfig) GetCacheTTL(globalTTL time.Duration) time.Duration {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	if e.CacheTTLMinutes > 0 {
		return time.Duration(e.CacheTTLMinutes) * time.Minute
	}
	return globalTTL
}

// GetProxyErrorStrategy 获取代理错误处理策略
func (e *EmbyConfig) GetProxyErrorStrategy() string {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.ProxyErrorStrategy
}

// MapStrmPath 对 strm 内 URL 做路径映射
func (e *EmbyConfig) MapStrmPath(path string) string {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	for _, m := range e.pathMap {
		if strings.Contains(path, m[0]) {
			return strings.Replace(path, m[0], m[1], 1)
		}
	}
	return path
}
