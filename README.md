# FNTV Proxy Relay

## 部署模式详细说明

> 直出功能请使用固定镜像 `0.9.8-relay.2-rc.1`；默认分支代码和 `latest` 镜像仍为稳定版。本节是该预发布版的补充文档。


本项目的“主服务”是飞牛影视代理，不替代飞牛影视本体；“媒体服务”负责鉴权回源和传输视频，不管理媒体库或转码。

| 模式 | 本项目容器数量 | 视频路径 | 配置入口 |
| --- | --- | --- | --- |
| 原有 302 | 1 | 客户端访问源站，要求源站对客户端可达 | `stream_mode: redirect` |
| 同源中继 | 1 | 视频经过原网页入口；入口在 VPS 时仍占 VPS 带宽 | `stream_mode: relay`，见 [中继指南](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/docs/RELAY_DEPLOYMENT.md) |
| 单容器直出 | 1，双监听 | 网页走原入口，视频走独立媒体端口 | `role: all`，见 [直出指南](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/docs/DIRECT_MEDIA.md) |
| 双容器分机直出 | 2，同一镜像 | NAS 主代理签发地址，机房媒体服务直接向客户端传输 | `role: proxy` + `role: media`，见 [直出指南](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/docs/DIRECT_MEDIA.md) |

直出使用固定镜像标签 `0.9.8-relay.2-rc.1`，不要使用仍指向稳定版的 `latest`。分机模式必须在媒体主机实际部署服务，并配置相同播放密钥；只在主代理填写媒体域名不会自动部署远端服务。客户端无需配置密钥。单容器与双容器方案二选一，容器数量不含飞牛影视本体和已有反代。

### 1. 原有 302：让客户端自己访问媒体源

适合 STRM 内的 URL 本来就能被播放器访问的情况。代理在支持的旧播放路径中解析地址并返回重定向，视频由源站传给客户端，代理不负责转发视频正文。飞牛 0.9.8 原生 stream API 在 redirect 模式下保留原响应，不保证每个接口都会产生 302。

如果 STRM 指向只有 NAS 能访问的内网地址，公网客户端仍然打不开；此模式不是内网穿透，也不会自动回退为服务器代理。

配置组合为 `role: proxy`、`delivery_mode: proxy`、`stream_mode: redirect`，只运行主代理，不启动专用媒体监听。使用[基础配置](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/config.yaml.example)，媒体源本身的访问条件由用户负责。

### 2. 同源中继：一个入口，服务器代客户端取流

适合“客户端不能访问内网源站，也无法提供独立公网媒体入口”的情况。主代理把受支持的播放地址改为原域名下的 `/fntv-relay/…`，由主代理读取源站并转发视频。

典型视频返回路径是：`内网源站 → 主代理 → VPS 网页反代 → 客户端`。客户端只访问原入口，部署较简单，但如果入口在 VPS，视频必然占用该 VPS 的带宽；Range/206 支持不会改变这一点。若入口没有 VPS，则不额外经过 VPS。

配置组合为 `role: proxy`、`delivery_mode: proxy`、`stream_mode: relay`。配置 `allowed_upstreams` 和 `allowed_strm_roots`，将既有 STRM 目录只读挂入主代理。完整步骤见[同源中继部署指南](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/docs/RELAY_DEPLOYMENT.md)。

### 3. 单容器直出：一个容器，网页和媒体分两个入口

适合一台主机同时运行主代理与媒体服务，并能提供公网媒体端口的情况。同一个容器启动两个监听：例如 `28005` 接收原网页反代，`49963` 接收客户端媒体请求；端口可配置。

网页与登录仍通过原入口。主代理签发短期播放地址，客户端随后访问媒体域名：`源站 → 本容器媒体监听 → 客户端`。媒体域名必须指向媒体服务的实际公网入口，不能再指回原 VPS 视频反代，否则仍占 VPS 带宽。

配置组合为 `role: all`、`delivery_mode: direct`、`stream_mode: relay`。使用 [`all.config.yaml.example`](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/deploy/direct/all.config.yaml.example) 和 [`compose.all.yaml`](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/deploy/direct/compose.all.yaml)，不要同时再启动一套占用相同端口的主代理。这里是一个容器，不是两个镜像。

省略手动播放密钥时程序自动生成，必须持久化 `media.state_dir`。用户提供媒体域名的证书链及私钥；按示例挂载整个证书目录，或者自行指定容器内证书路径。只支持证书热加载，不负责签发、续期或同步。STRM 目录仍需只读挂载。

### 4. 分机直出：主代理签发地址，独立媒体容器负责传输

适合主代理留在 A 地 NAS，而希望 B 地服务器承担视频传输的情况。两端运行**同一个镜像、不同角色**，合计两个容器；无需在 B 地再安装飞牛影视或复制媒体库。

| 配置或资源 | A 地主代理 | B 地媒体服务 |
| --- | --- | --- |
| 角色 | `role: proxy` | `role: media` |
| 地址签发 | `delivery_mode: direct` | 不设置 `delivery_mode`，保留默认 |
| 流模式 | `stream_mode: relay` | `stream_mode: relay` |
| 连接目标 | 飞牛影视本体 | STRM 中的实际媒体源 URL |
| 端口示例 | 28005，供原反代访问 | 49963，供客户端直接访问 |
| STRM 目录 | 只读挂载，并配置允许根目录 | 不需要挂载 |
| 播放密钥 | 签发票据 | 必须与主代理使用同一个密钥 |
| 媒体证书 | 无需加载远端媒体证书 | 配置证书链和私钥路径 |
| 配置示例 | [proxy 配置](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/deploy/direct/proxy.config.yaml.example) / [Compose](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/deploy/direct/compose.proxy.yaml) | [media 配置](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/deploy/direct/media.config.yaml.example) / [Compose](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/deploy/direct/compose.media.yaml) |

主代理将 `media.public_base_url` 设置为 B 地公网媒体入口。两端配置精确的上游允许列表，并安全共享同一密钥；不能让两端各自随机生成后直接配对。公网 DNS、防火墙和端口映射必须让客户端实际到达 B 地媒体容器。只在主代理填一个域名不会部署远端服务。

视频返回路径是 `源站 → B 地媒体容器 → 客户端`，A 地主代理和原网页 VPS 不传输这些视频正文。前提是 B 地容器能访问 STRM 里原本的 URL；本项目不会自动转换另一处网络的私有地址。若 B 地容器实际放在家中，再经隧道连接机房，家庭线路和隧道仍会承载视频，不能把它理解为“媒体直接从机房源站出网”。

### 如何选择，以及不包含什么

- 客户端能访问源站：可用原有 302。
- 客户端不能访问源站，且没有独立公网媒体入口：用同源中继，接受原入口承担视频带宽。
- 有独立媒体入口，主代理和媒体服务在同一主机：用单容器直出。
- 希望另一台主机承担媒体出口：用分机直出。

直出模式下客户端仍登录原飞牛入口，不需要配置服务器密钥；但客户端必须信任媒体 HTTPS 证书并能访问媒体端口。票据过期后拖动或重连可能需要重新获取播放地址，媒体入口故障不会自动回退到 VPS。

当前直出仅针对可处理的直接媒体文件 URL，不保证转码、本地媒体或 HLS/DASH 分片都绕过原入口。**Emby 仍是独立的原有代理逻辑，尚未接入这套媒体票据直出**，不能直接套用本表配置实现 Emby 分机直出。

本说明适用于 `0.9.8-relay.2-rc.1` 镜像。GitHub 的版本标签页面是发布时的固定源码快照，不随文档分支更新；文档补充见[当前仓库首页](https://github.com/qiwolf/fntv-proxy-relay)与 [Release 页面](https://github.com/qiwolf/fntv-proxy-relay/releases/tag/v0.9.8-relay.2-rc.1)。完整启动命令、密钥权限、证书路径、验收与回滚见[部署指南](https://github.com/qiwolf/fntv-proxy-relay/blob/d299c5f68688d0beb5734c7630dc705fa1ed98bd/docs/DIRECT_MEDIA.md)。


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
