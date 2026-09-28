# FNTV Proxy Relay

## 部署模式与直出预发布版

| 模式 | 本项目容器数 | 视频流量路径 |
| --- | --- | --- |
| 原有 302 | 1 | 客户端直接访问源站，要求源站可达 |
| 同源 relay | 1 | 视频经过原网页入口；入口在 VPS 时仍占 VPS 带宽 |
| 单容器直出 | 1，双监听 | 同一容器的独立媒体端口直接传输 |
| 双容器分机直出 | 2，同一镜像 | NAS 主代理签发地址，机房媒体服务传输视频 |

后两种模式使用预发布镜像 `ghcr.io/qiwolf/fntv-proxy-relay:0.9.8-relay.2-rc.1`；`main` 代码与 `latest` 镜像仍为稳定版。请阅读[完整模式选择及分机配置指南](https://github.com/qiwolf/fntv-proxy-relay/blob/fbc249059165499bbe1caff04720d87217cd180d/docs/DIRECT_MEDIA.md)，并使用指南链接的配套配置。

主代理不是飞牛影视本体，媒体服务不负责媒体库或转码。分机部署需要实际运行远端媒体容器、配置相同播放密钥及可达的公网媒体入口；只填写域名不会自动部署。单容器与双容器方案二选一，客户端无需填写密钥。媒体证书由用户提供，支持热加载，不包含自动签发、续期或同步。

飞牛影视 / Emby 代理工具——自动解析 `.strm` 文件，可选 302 重定向或服务端流式回源。

本分支重点解决：飞牛影视把 `.strm` 解析得到的内网媒体 URL 直接交给公网客户端，导致客户端无法连接的问题。代理会把飞牛影视 0.9.8 stream API 响应中的允许地址改写为同源临时令牌，再由服务器支持 Range 地流式回源。

> [!IMPORTANT]
> 本仓库派生自 [jimboo7339/fntv-proxy](https://github.com/jimboo7339/fntv-proxy)。上游当前未提供明确 LICENSE；公开源码或容器不等于授予再分发或商业使用许可，详见 [NOTICE.md](NOTICE.md)。

- 兼容旧版飞牛影视代理路径，并适配**飞牛影视 0.9.8** 的 `/v/api/v1/stream`
- 可选启用 **Emby 302 代理**（与飞牛代理独立运行，互不影响）
- 已测试夸克网盘生成的 strm
- 已测试 115 网盘生成的 strm [#1](https://github.com/jimboo7339/fntv-proxy/issues/1)
- 如有问题请提 issue

## 功能

### 飞牛影视

- ✅ 透明代理飞牛影视服务
- ✅ 自动缓存 PlaybackInfo 中的 `.strm` MediaSource
- ✅ 拦截视频流请求，返回 302 重定向到真实 URL
- ✅ 可选 relay 模式，支持 GET/HEAD、Range/If-Range 和 206 透传
- ✅ relay 模式逐跳校验上游允许列表，并限制 `.strm` 可读根目录
- ✅ 兼容飞牛影视 0.9.8 `POST /v/api/v1/stream`，将 JSON 中内网直链改写为同源临时令牌代理
- ✅ 支持日志级别配置
- ✅ 缓存过期时间可配置
- ✅ 优雅关闭

### Emby（可选）

- ✅ 独立端口代理 Emby 服务（默认 `:8095` → `:8096`）
- ✅ 改写 PlaybackInfo，注入 `DirectStreamUrl` 并禁用转码
- ✅ 拦截 `/stream`、`/universal` 请求，302 到 strm 真实直链
- ✅ 支持本地 `.strm` 文件、远程 URL、115 直链解析
- ✅ 可选 strm 内 URL 路径映射（`strm_path_map`）
- ✅ 本地媒体自动回源，代理失败可配置回源策略

## 快速开始

1. 复制 `config.yaml.example` 为 `config.yaml`，按实际环境修改目标地址
2. 运行 `./fntv-proxy` 或使用 Docker Compose
3. 将播放器地址指向代理端口（飞牛 `:28005`，Emby `:8095`）

> `config.yaml` 已加入 `.gitignore`，本地内网地址不会被提交到仓库。

完整的网络结构、Docker Compose、Nginx、安全限制、验收与回滚步骤见 [STRM Relay 部署指南](docs/RELAY_DEPLOYMENT.md)。

## 容器镜像

GitHub Release 标签会自动构建 `linux/amd64`、`linux/arm64` 镜像并发布到 GHCR：

```bash
docker pull ghcr.io/qiwolf/fntv-proxy-relay:latest
```

生产环境建议固定版本标签，不要长期使用 `latest`。

## 配置文件

复制示例配置并编辑：

```bash
cp config.yaml.example config.yaml
```

`config.yaml` 示例：

```yaml
# 飞牛影视代理
listen: ":28005"
target: "http://127.0.0.1:8005"

# 日志级别: trace / debug / info / warn / error
log_level: "info"
log_dir: "./logs"

# 直链缓存过期时间（分钟），默认 60
cache_ttl: 60

# redirect（默认）或 relay
stream_mode: "relay"
public_base_url: "https://fn.example.com"
allowed_upstreams:
  - "192.168.1.20:5244"
  - "media-cdn.example.com"
allowed_strm_roots:
  - "/vol00/strm"

# Emby 302 代理（默认关闭，不影响飞牛代理）
emby:
  enabled: false
  listen: ":8095"
  target: "http://127.0.0.1:8096"
  cache_ttl: 60
  proxy_error_strategy: "origin"   # origin: 失败回源 | reject: 返回错误
  # strm 内 URL 路径映射（可选）
  # strm_path_map:
  #   - "https://old-host:8094 => http://localhost:8095"
```

### Emby 配置说明

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `emby.enabled` | 是否启用 Emby 代理 | `false` |
| `emby.listen` | Emby 代理监听地址 | `:8095` |
| `emby.target` | Emby 源站地址 | `http://127.0.0.1:8096` |
| `emby.cache_ttl` | Emby 直链缓存（分钟），0 表示使用全局 `cache_ttl` | `0` |
| `emby.proxy_error_strategy` | 代理失败策略：`origin` 回源 / `reject` 报错 | `origin` |
| `emby.strm_path_map` | strm 文件内 URL 片段替换，格式 `旧地址 => 新地址` | 无 |

启用 Emby 后，客户端应连接代理地址（如 `http://服务器IP:8095`），而非直连 Emby 端口。

### 飞牛 relay 配置说明

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `stream_mode` | `redirect` 返回 302；`relay` 从服务器回源并流式转发 | `redirect` |
| `allowed_upstreams` | relay 必填，精确列出 `host` 或 `host:port`；每次重定向都重新检查 | 无 |
| `allowed_strm_roots` | relay 必填，可读 `.strm` 根目录；使用真实路径阻止符号链接逃逸 | 无 |
| `public_base_url` | 0.9.8 stream API 临时代理地址的公网基础 URL；留空时返回相对路径 | 空 |

`allowed_upstreams` 中不带端口的主机名只允许 HTTP/HTTPS 默认端口。内网源站使用非标准端口时必须写明端口。relay 不会转发客户端的 `Authorization` 或 Cookie，但 `.strm` URL 自身携带的 Basic Auth 仍由 Go HTTP 客户端使用。更改 relay 相关配置后需重启进程。

## Docker Compose 配置

```yaml
services:
  fntv-proxy:
    image: jimboo7339/fntv-proxy:latest
    container_name: fntv-proxy
    ports:
      - "28005:28005"   # 飞牛影视代理
      - "8095:8095"     # Emby 代理（启用 emby.enabled 时需要）
    volumes:
      # 挂载 strm 文件目录（根据实际路径修改）
      # 前后路径必须一致：宿主机是什么路径，容器内就是什么路径
      - /vol00/strm:/vol00/strm:ro
      - /vol01/strm:/vol01/strm:ro
      # 挂载配置文件（用于热重载）
      - ./config.yaml:/app/config.yaml:ro
    environment:
      - CONFIG=/app/config.yaml
    restart: unless-stopped
```

**注意**：strm 路径一定要挂载到 Docker 容器中，否则播放失败，找不到 strm 文件。

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `CONFIG` | 配置文件路径 | `./config.yaml` |
| `TZ` | 时区 | `Asia/Shanghai` |
| `FNTV_CACHE_TTL` | 直链缓存过期时间（分钟） | `60` |
| `FNTV_STREAM_MODE` | 飞牛流处理模式：`redirect` / `relay` | `redirect` |
| `FNTV_EMBY_ENABLED` | 是否启用 Emby 代理 | `false` |
| `FNTV_EMBY_LISTEN` | Emby 代理监听地址 | `:8095` |
| `FNTV_EMBY_TARGET` | Emby 源站地址 | `http://127.0.0.1:8096` |

**注意**：`FNTV_CACHE_TTL` 优先级高于配置文件中的 `cache_ttl`。

## 日志级别说明

| 级别 | 输出位置 | 说明 |
|------|---------|------|
| `trace` | 文件 | **最详细**，请求查询、敏感头和体会脱敏（排查问题用） |
| `debug` | 控制台 + 文件 | 记录所有请求和响应 |
| `info` | 控制台 | 只输出关键信息（推荐生产环境） |
| `warn` | 控制台 | 只输出警告和错误 |
| `error` | 控制台 | 只输出错误 |

### trace 级别使用示例

```yaml
log_level: "trace"
log_dir: "./logs"
```

日志文件将包含：

```
=== REQUEST ===
Method: GET
URL: /Items/xxx/PlaybackInfo
Headers:
  User-Agent: xxx
  Authorization: [REDACTED]
Body: [REDACTED, 123 bytes]
===============
=== RESPONSE ===
Request: GET /Items/xxx/PlaybackInfo
Status: 200
Headers:
  Content-Type: application/json
================
```

## 目录结构

```
fntv-proxy/
├── cmd/
│   └── main.go                 # 入口
├── internal/
│   ├── config/
│   │   ├── config.go           # 配置管理（热重载）
│   │   └── emby.go             # Emby 配置
│   ├── proxy/
│   │   └── server.go           # 飞牛影视代理服务器
│   ├── emby/
│   │   ├── server.go           # Emby 代理服务器
│   │   ├── playback.go         # PlaybackInfo 改写
│   │   ├── stream.go           # 302 重定向
│   │   └── path.go             # 路径解析
│   ├── handler/
│   │   ├── playback.go         # 飞牛 PlaybackInfo 处理
│   │   ├── stream.go           # 飞牛 Stream 处理
│   │   └── linktype.go         # 直链类型识别（HLS / FILE）
│   ├── cache/
│   │   └── cache.go            # MediaSource 缓存
│   └── logger/
│       └── logger.go           # 日志
├── config.yaml.example         # 配置示例（复制为 config.yaml 使用）
├── docker-compose.yml
├── Dockerfile
└── README.md
```

## 工作原理

### 飞牛影视

```
1. 播放器 → PlaybackInfo → 代理缓存 MediaSource（含 .strm 路径）
                        ↓
2. 播放器 → stream.mp4 / stream.MOV → 代理
                        ↓
3. 代理查缓存 → 读取 .strm → 请求获取真实 URL
                        ↓
4. 代理返回 302 → 播放器 → 真实 URL 播放
```

### Emby

```
1. 播放器 → PlaybackInfo → 代理改写 JSON（DirectStreamUrl + 禁用转码）
                        ↓
2. 播放器 → /Videos/{id}/stream → 代理
                        ↓
3. 代理查缓存 → 读取 .strm / 远程 URL → 跟随重定向链
                        ↓
4. 代理返回 302 → 播放器 → 网盘真实直链播放
```

本地媒体（非 strm）会重定向到 `/original`，由 Emby 源站直接提供。

## 夸克网盘与 HLS 直链说明

使用 **OpenList 夸克 TV 驱动** 生成 strm 并走 302 播放时，夸克 CDN 对同一库里的不同影片可能返回**两种不同直链**。代理只做透明 302 转发，**不会**把 HLS 转成 mp4，也**无法**凭空补全媒体库元数据。

### 直链类型对比

| 类型 | 日志标记 | 典型 URL 特征 | 播放 | 时长/码率/分辨率 |
|------|----------|---------------|------|------------------|
| **FILE** | `📡 直链类型: FILE` | 无 `.m3u8`，夸克 CDN hash 路径直链 | ✅ | ✅ 通常可获取 |
| **HLS** | `📡 直链类型: HLS (m3u8)` | 含 `media.m3u8`（如 `video-play-*.drive.quark.cn/.../media.m3u8`） | ✅（播放器需支持 HLS） | ❌ 通常无法获取 |

strm 文件名可能是 `第01集.mp4`，但 smartstrm 跟随重定向后，夸克实际返回的不一定是文件直链，也可能是 **HLS 转码流**。

### 日志示例

**HLS（无媒体信息）：**
```
✅ [Emby] 最终直链: https://video-play-hp-zb.drive.quark.cn/.../media.m3u8?...
📡 [Emby] 直链类型: HLS (m3u8) → 预计无文件元数据（时长/码率/分辨率）
```

**FILE（有媒体信息）：**
```
✅ [Emby] 最终直链: https://video-play-p-zb.drive.quark.cn/.../6a36772f...?flag=ho&...
📡 [Emby] 直链类型: FILE → 预计可获取文件元数据
```

飞牛代理输出格式相同，前缀为 `📡 直链类型:`（无 `[Emby]`）。

### 为什么 HLS 拿不到媒体信息？

1. **strm 只是 URL 指针**，飞牛/Emby 扫描时需要 probe 真实媒体
2. **mp4/mov 文件直链**可被 ffprobe 解析 → 有时长、分辨率、码率
3. **m3u8 是播放列表**（指向多个 `.ts` 分片）→ 不是单个文件，probe 无法像扫文件那样得到完整元数据
4. 飞牛原版对这些片源也会显示 **HLS 模式**，与是否走本代理无关

### 这是代理的问题吗？

**不是。** 根因在夸克 + 夸克 TV 驱动的返回策略：部分内容只给 HLS 流。本工具仅做 302，不改变夸克返回的链接类型。

### 可行应对

| 方案 | 说明 |
|------|------|
| 看日志确认 | 播放时看 `HLS (m3u8)` 还是 `FILE`，即可解释 Emby/飞牛里有没有时长信息 |
| TMDB 刮削 | 用外部元数据补时长、分辨率（不依赖文件 probe） |
| 驱动/配置 | 若 OpenList 支持强制下载直链，部分片源可变为 FILE 类型 |
| m3u8 反代 | 复杂方案（如解析 playlist），仍难完整补码率/分辨率 |

## 常见问题

### Q: 支持哪些视频格式？
**A:** 飞牛代理支持 `stream.mp4`、`stream.MOV` 等所有格式。Emby 代理支持 `/stream`、`/universal` 路径。

### Q: 缓存多久过期？
**A:** 默认 60 分钟，可通过 `cache_ttl`（飞牛）或 `emby.cache_ttl`（Emby）配置。

### Q: 如何查看详细日志？
**A:** 修改 `log_level: debug` 或 `trace`，debug/trace 级别会写入 `./logs` 目录。

### Q: Emby 和飞牛能同时用吗？
**A:** 可以。两者使用不同端口，默认飞牛 `:28005`、Emby `:8095`，在同一进程中并行运行。

### Q: 只想要 Emby 代理，不需要飞牛？
**A:** 飞牛代理始终运行；Emby 需设置 `emby.enabled: true`。飞牛代理仍会监听 `:28005`，如不需要可忽略该端口。

### Q: 为什么部分夸克剧集没有时长、清晰度？
**A:** 播放日志若显示 `直链类型: HLS (m3u8)`，说明夸克返回的是 HLS 流而非文件直链，飞牛/Emby 通常无法获取文件级元数据。详见上方 [夸克网盘与 HLS 直链说明](#夸克网盘与-hls-直链说明)。

### Q: Emby 播放失败怎么排查？
**A:**
1. 确认客户端连接的是代理端口（8095），不是 Emby 原端口（8096）
2. 确认 strm 文件目录已挂载到容器/代理所在机器
3. 将 `log_level` 改为 `debug` 查看 `[Emby]` 前缀日志
4. 检查 `strm_path_map` 是否需要替换 strm 内的 URL 地址

## 声明

1. 飞牛代理主要针对 **夸克网盘** 在 **openlist** 的 **夸克 TV 驱动** 挂载下实现 302
2. Emby 代理参考 [qmediasync](https://github.com/qicfan/qmediasync) 的 emby302 思路实现，适用于 strm / 网盘直链场景
3. 只要 strm 文件中的地址能正常下载，即可通过本工具实现第三方播放器播放
4. 经测试 **CapyPlayer**、**Vidhub**、**爆米花** 下播放器正常播放

## 贡献者

<a href="https://github.com/jimboo7339/fntv-proxy/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=jimboo7339/fntv-proxy" />
</a>

## 项目 Star 数增长趋势

[![Star History Chart](https://api.star-history.com/svg?repos=jimboo7339/fntv-proxy&type=Date)](https://star-history.com/#jimboo7339/fntv-proxy&Date)
