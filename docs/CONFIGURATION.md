# 默认程序配置参数详解

适用于正式版默认入口 `/app/fntv-proxy` 和 [完整示例](../config.yaml.example)、[飞牛同源中继示例](../deploy/fnos/config.yaml.example)。`/app/fntv-unified` 使用另一套 `services` 格式，不能直接套用本页 YAML；参见 [统一配置字段](UNIFIED.md#配置字段)。

## 先区分三个地址

- `listen`：本代理接收客户端或前置反代请求的地址。
- `target`：代理连接的原飞牛影视 / Emby / Jellyfin 本体地址。
- `media.public_base_url`：直出模式下客户端自动获取的独立媒体入口，不用于登录。

Docker host 网络中的 `127.0.0.1` 指宿主机；桥接网络中的它指代理容器自身。Emby/Jellyfin 的程序默认目标都为 `http://127.0.0.1:8096`，只是默认值，不表示同一服务可自动识别成两种协议。同机安装时须分别填实际映射端口。

## 顶层字段

| 字段 | 默认值 / 单位 | 含义及作用范围 |
| --- | --- | --- |
| `listen` | `:28005` | 飞牛代理监听；省略 IP 表示所有接口，可指定本机 IP 限制监听 |
| `target` | `http://127.0.0.1:8005` | 原飞牛影视地址；不要填代理自己的端口 |
| `log_level` | `info` | trace/debug/info/warn/error；详细日志可能含敏感源 URL，限制访问 |
| `log_dir` | `./logs` | 进程看到的日志目录；容器持久化需另加目录挂载 |
| `cache_ttl` | `60` 分钟 | 直链缓存时间；不是票据寿命，也不是视频缓存大小 |
| `role` | `proxy` | proxy 运行主代理；media 仅媒体服务；all 两者同时。media 角色不能启用 Emby/Jellyfin 代理 |
| `delivery_mode` | `proxy` | 仅控制飞牛。proxy 按 stream_mode 运行；direct 签发独立媒体地址，需要 stream_mode: relay 及 media 配置 |
| `stream_mode` | `redirect` | 仅控制飞牛。redirect 保留原有源站直连行为；relay 同源流式回源。原生 stream API 在 redirect 下保留原响应，不保证所有接口均产生 302 |
| `public_base_url` | 空 | 飞牛同源 `/fntv-relay/…` 返回地址的外部 HTTP(S) origin；空值用相对路径。不是媒体节点地址 |
| `allowed_upstreams` | 空列表 | relay/direct 可访问的真实视频源 host[:port]；每次跳转重新检查，非标准端口须写明；不是登录后端列表 |
| `allowed_strm_roots` | 空列表 | 代理允许读取的 STRM 根目录；填卷映射右侧，与服务器返回文件路径一致，符号链接越界拒绝。纯媒体角色无需挂载 STRM |

默认程序中 direct 使用的 `media`、回源白名单及 STRM 根目录由启用的适配器共用；需要每个服务独立密钥和允许列表时使用统一模式。relay/direct 缺少必要允许列表会启动失败，不能通过扩大到任意目标来排错。

## Emby 与 Jellyfin 字段

分别放在 `emby:`、`jellyfin:` 下；两者都默认关闭，互不替代。顶层 `stream_mode: relay` **不会**把 Emby/Jellyfin 改成同源中继。

| 字段 | 默认值 / 单位 | 具体含义 |
| --- | --- | --- |
| `enabled` | `false` | true 额外启动该协议代理；不会关闭原飞牛监听 |
| `listen` | Emby `:8095`；Jellyfin `:8098` | 客户端连接的该服务代理端口，不能与其他服务冲突 |
| `target` | `http://127.0.0.1:8096` | 各自原服务器的实际地址；必须按部署修改 |
| `delivery_mode` | `redirect` | redirect 将可处理的源地址重定向给客户端；direct 使用顶层 media 签发独立媒体票据。不支持 relay 值 |
| `cache_ttl` | 省略或 <=0 继承顶层；分钟 | 该适配器直链缓存时间；0 不是禁用缓存。direct 静态播放仍按当前请求向后端核验权限 |
| `proxy_error_strategy` | `origin` | 旧 redirect 处理失败时 origin 交回原服务器处理，reject 返回错误。不是自动切到专用媒体节点，也不能放宽 direct 授权或签发检查 |
| `strm_path_map` | 空列表 | 可选 `旧字符串 => 新字符串`；对 STRM 内 URL 按顺序取第一条匹配并替换首次出现，无匹配则原样。不修改 STRM 文件，不是 Docker 文件路径映射 |

direct 只适用于可处理的 STRM / 远程静态媒体。普通文件、转码等仍可能由原服务器传输；不能把成功播放等同于直出成功。完整部署见 [Emby](EMBY_DIRECT.md)、[Jellyfin](JELLYFIN_DIRECT.md)。

## 独立媒体配置与路径

`media` 字段的默认值、密钥、票据有效期（**秒**）、证书路径和角色约束见 [直出配置表](DIRECT_MEDIA.md)。证书由用户提供，只有热加载，不含自动签发、续期或同步。不要仅修改 `delivery_mode: direct` 就认为远端服务已经部署。

挂载规则和三端例子见 [STRM 挂载详解](STRM_MOUNTS.md)。改监听、后端、角色、密钥等部署参数后应重新启动对应容器，不应把证书热加载理解为所有配置均可在线切换。
