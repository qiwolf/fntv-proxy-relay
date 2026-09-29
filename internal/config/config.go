package config

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// Config 配置结构
type Config struct {
	Role             string        `mapstructure:"role"`
	DeliveryMode     string        `mapstructure:"delivery_mode"`
	Media            MediaConfig   `mapstructure:"media"`
	ListenAddr       string        `mapstructure:"listen"`
	TargetAddr       string        `mapstructure:"target"`
	LogLevel         string        `mapstructure:"log_level"`
	LogDir           string        `mapstructure:"log_dir"`
	CacheTTL         time.Duration `mapstructure:"cache_ttl"` // 直链缓存 TTL（复用原有配置名）
	StreamMode       string        `mapstructure:"stream_mode"`
	AllowedUpstreams []string      `mapstructure:"allowed_upstreams"`
	AllowedStrmRoots []string      `mapstructure:"allowed_strm_roots"`
	PublicBaseURL    string        `mapstructure:"public_base_url"`
	Emby             EmbyConfig    `mapstructure:"emby"`
	Jellyfin         EmbyConfig    `mapstructure:"jellyfin"`
	mutex            sync.RWMutex
}

// Global 全局配置实例
var Global = &Config{
	ListenAddr: ":28005",
	TargetAddr: "http://127.0.0.1:8005",
	LogLevel:   "info",
	LogDir:     "./logs",
	CacheTTL:   60 * time.Minute, // 默认直链缓存1小时
	StreamMode: "redirect",
}

// Load 加载配置
func Load(configPath string) error {
	viper.SetConfigType("yaml")

	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.SetConfigName("config")
		viper.AddConfigPath(".")
		viper.AddConfigPath("/app/configs/")
		viper.AddConfigPath("/etc/fntv-proxy/")
	}

	// 设置默认值
	viper.SetDefault("listen", ":28005")
	viper.SetDefault("target", "http://127.0.0.1:8005")
	viper.SetDefault("log_level", "info")
	viper.SetDefault("log_dir", "./logs")
	viper.SetDefault("cache_ttl", 60)
	viper.SetDefault("stream_mode", "redirect")
	viper.SetDefault("role", "proxy")
	viper.SetDefault("delivery_mode", "proxy")
	viper.SetDefault("media.listen", ":49963")
	viper.SetDefault("media.state_dir", "./data/media")
	viper.SetDefault("media.token_ttl_seconds", 3600)
	viper.SetDefault("media.allow_http", false)
	for _, key := range []string{"allowed_upstreams", "allowed_strm_roots", "public_base_url", "media.public_base_url", "media.token_key", "media.token_key_file", "media.tls_cert_file", "media.tls_key_file", "media.allowed_origins"} {
		if err := viper.BindEnv(key); err != nil {
			return err
		}
	}
	viper.SetDefault("emby.enabled", false)
	viper.SetDefault("emby.delivery_mode", "redirect")
	viper.SetDefault("emby.listen", ":8095")
	viper.SetDefault("emby.target", "http://127.0.0.1:8096")
	viper.SetDefault("emby.proxy_error_strategy", EmbyErrorStrategyOrigin)
	viper.SetDefault("jellyfin.enabled", false)
	viper.SetDefault("jellyfin.delivery_mode", "redirect")
	viper.SetDefault("jellyfin.listen", ":8098")
	viper.SetDefault("jellyfin.target", "http://127.0.0.1:8096")
	viper.SetDefault("jellyfin.proxy_error_strategy", EmbyErrorStrategyOrigin)

	// 环境变量覆盖
	viper.SetEnvPrefix("FNTV")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// 读取配置文件
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return err
		}
		log.Println("⚠️ 未找到配置文件，使用默认配置")
	}

	// 解析到结构体
	if err := viper.Unmarshal(Global); err != nil {
		return err
	}

	// 转换 cache_ttl 为 Duration（用于直链缓存）
	Global.CacheTTL = time.Duration(viper.GetInt("cache_ttl")) * time.Minute
	initEmbyDefaults()
	Global.Jellyfin.parsePathMap()
	if err := Global.Validate(); err != nil {
		return err
	}

	log.Printf("✅ 配置加载完成: %s", viper.ConfigFileUsed())
	log.Printf("📦 直链缓存TTL: %v", Global.CacheTTL)
	return nil
}

// Watch 监听配置变化（支持 Docker 卷挂载）
func Watch(onChange func()) {
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		log.Println("⚠️ 未找到配置文件，跳过监听")
		return
	}

	// 使用轮询方式检测文件变化（兼容 Docker bind mount）
	go pollConfigChanges(configFile, onChange)
}

// pollConfigChanges 轮询检测配置文件变化
func pollConfigChanges(configFile string, onChange func()) {
	var lastModTime time.Time

	// 获取初始修改时间
	if info, err := os.Stat(configFile); err == nil {
		lastModTime = info.ModTime()
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		info, err := os.Stat(configFile)
		if err != nil {
			continue
		}

		// 检测到修改
		if info.ModTime().After(lastModTime) {
			lastModTime = info.ModTime()
			handleConfigChange(configFile, onChange)
		}
	}
}

// handleConfigChange 处理配置变更
func handleConfigChange(configFile string, onChange func()) {
	log.Printf("📝 配置文件发生变化: %s", configFile)

	// 重新读取配置文件
	viper.SetConfigFile(configFile)
	if err := viper.ReadInConfig(); err != nil {
		log.Printf("❌ 读取配置文件失败: %v", err)
		return
	}

	// Runtime topology and secrets are immutable. Only logging is hot-reloaded.
	level := strings.ToLower(strings.TrimSpace(viper.GetString("log_level")))
	switch level {
	case "trace", "debug", "info", "warn", "error":
	default:
		log.Print("❌ log_level must be trace, debug, info, warn or error")
		return
	}
	Global.SetLogLevel(level)
	log.Println("✅ log_level 已热重载；其他配置（包括服务角色、监听和密钥）需重启生效")

	if onChange != nil {
		onChange()
	}
}

// GetListenAddr 获取监听地址（线程安全）
func (c *Config) GetListenAddr() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.ListenAddr
}

// GetTargetAddr 获取目标地址
func (c *Config) GetTargetAddr() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.TargetAddr
}

// GetLogLevel 获取日志级别
func (c *Config) GetLogLevel() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.LogLevel
}

// GetCacheTTL 获取直链缓存TTL
func (c *Config) GetCacheTTL() time.Duration {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.CacheTTL
}

func (c *Config) GetStreamMode() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return strings.ToLower(strings.TrimSpace(c.StreamMode))
}

func (c *Config) GetAllowedUpstreams() []string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return append([]string(nil), c.AllowedUpstreams...)
}

func (c *Config) GetAllowedStrmRoots() []string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return append([]string(nil), c.AllowedStrmRoots...)
}

func (c *Config) GetPublicBaseURL() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return strings.TrimRight(strings.TrimSpace(c.PublicBaseURL), "/")
}

// Validate rejects unsafe relay configurations before the listener starts.
func (c *Config) Validate() error {
	if mode := c.Jellyfin.GetDeliveryMode(); mode != "redirect" && mode != "direct" {
		return fmt.Errorf("jellyfin.delivery_mode must be redirect or direct")
	}
	if c.Jellyfin.Enabled && c.Jellyfin.GetDeliveryMode() == "direct" && len(c.AllowedStrmRoots) == 0 {
		return fmt.Errorf("allowed_strm_roots is required for Jellyfin direct delivery")
	}
	if mode := c.Emby.GetDeliveryMode(); mode != "redirect" && mode != "direct" {
		return fmt.Errorf("emby.delivery_mode must be redirect or direct")
	}
	if c.Emby.Enabled && c.Emby.GetDeliveryMode() == "direct" && len(c.AllowedStrmRoots) == 0 {
		return fmt.Errorf("allowed_strm_roots is required for Emby direct delivery")
	}
	mode := strings.ToLower(strings.TrimSpace(c.StreamMode))
	if c.GetRole() == "media" {
		mode = "relay"
		c.StreamMode = mode
	}
	if mode == "" {
		mode = "redirect"
		c.StreamMode = mode
	}
	if mode != "redirect" && mode != "relay" {
		return fmt.Errorf("stream_mode must be redirect or relay")
	}
	if mode == "relay" && c.GetRole() != "media" {
		if len(c.AllowedUpstreams) == 0 {
			return fmt.Errorf("allowed_upstreams is required in relay mode")
		}
		if len(c.AllowedStrmRoots) == 0 {
			return fmt.Errorf("allowed_strm_roots is required in relay mode")
		}
	}
	return c.validateMedia()
}

// SetLogLevel 设置日志级别（热重载用）
func (c *Config) SetLogLevel(level string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.LogLevel = level
}
