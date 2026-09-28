# 专用媒体服务与直出部署

原有 302、同源中继及独立 Emby 代理均保留，旧配置不需要迁移。新功能将播放授权和媒体传输分开：客户端访问 VPS 上的飞牛入口取得临时播放地址，再直接向专用媒体服务器取数据。媒体服务器应放在希望承担出口流量的网络中；放在家庭网络后再经隧道出机房，仍会消耗家庭/隧道带宽。

本指南适用于 `v0.9.8-relay.2-rc.1` 预发布版，容器标签为 `0.9.8-relay.2-rc.1`，提供 `linux/amd64`、`linux/arm64`。预发布版不覆盖稳定版 `latest`；旧版本不包含本指南新增的直出功能。

## 模式与兼容性

| 配置 | 启用的服务 | 媒体路径 |
| --- | --- | --- |
| 旧配置或 `role: proxy`、`delivery_mode: proxy`、`stream_mode: redirect` | 原飞牛代理，可选 Emby | 旧播放路径保留 302，原生 stream API URL 不改写，不新增监听 |
| `role: proxy`、`delivery_mode: proxy`、`stream_mode: relay` | 原飞牛代理，可选 Emby | 原 `/fntv-relay/` 同源中继 |
| `role: proxy`、`delivery_mode: direct`、`stream_mode: relay` | 原飞牛代理，可选 Emby；不监听媒体端口 | 签发指向另一台媒体服务器的地址 |
| `role: media` | 仅专用媒体监听；没有飞牛网页、登录或 Emby 代理 | 校验票据并回源传输 |
| `role: all`、`delivery_mode: direct`、`stream_mode: relay` | 飞牛代理与独立媒体端口，可选 Emby | 同一进程签发并校验票据 |

`role` 默认 `proxy`，`delivery_mode` 默认 `proxy`，原 `stream_mode` 默认 `redirect`。`delivery_mode` 只控制播放地址签发端：`proxy/all` 可设为 `direct`，并要求 `stream_mode: relay`；纯 `media` 角色不设置该项，保留默认值，不能配置为 `direct`。`role: all` 本身也要求 `stream_mode: relay`。Emby 的处理逻辑独立，不会自动切换到本指南的直出模式。除日志配置外，配置修改均须重启。

证书文件内容可热加载（见下文），不需要重启；证书路径、运行角色和播放密钥仍须重启才能变更。本次不实现证书自动申请、续期或服务器间同步。

## 两台服务器的职责

1. 客户端通过原域名访问 VPS，登录和获取播放信息仍经过原飞牛代理。
2. 代理将受允许列表约束的媒体 URL 编成 AES-256-GCM 加密、带过期时间的票据，返回 `https://media.example.com:49963/fntv-media/<票据>`。
3. 客户端直接连接媒体服务器。后者校验票据及自己的上游允许列表，再将源站数据流式返回客户端；视频正文不经过 VPS。

两端使用相同共享密钥，客户端只获得短期票据，不获得静态密钥。重启后只要密钥不变且票据未过期，已有票据仍可用；轮换密钥会使旧票据失效。票据地址在有效期内也是访问凭证，不能贴入公开日志、截图或工单。

## 配置和容器

完整示例位于 [deploy/direct](../deploy/direct)：`proxy.config.yaml.example` 用于原代理；`media.config.yaml.example` 用于专用媒体服务器。两端都配置 `allowed_upstreams`，精确到非标准端口，重定向后的目标也必须允许。使用 `proxy/all` 的 relay 时还必须配置并只读挂载 `allowed_strm_roots`；`media` 角色无需挂载 STRM，也不必配置 STRM 根目录。

| `media` 配置项 | 说明 |
| --- | --- |
| `listen` | 专用监听，默认 `:49963`；只有 `media/all` 使用 |
| `public_base_url` | 客户端可访问的媒体入口，例如 `https://media.example.com:49963` |
| `token_key` / `token_key_file` | 可选手动密钥，64 位十六进制字符串或文件；均省略时自动生成，不要硬编码 |
| `state_dir` | 自动密钥持久目录，默认 `./data/media`；容器必须挂载持久存储 |
| `token_ttl_seconds` | 未填或为 0 时默认 3600，有效范围 1–86400；应覆盖预期播放与拖动时间 |
| `tls_cert_file` / `tls_key_file` | 媒体监听使用的完整证书链与私钥，两者成对配置；没有默认路径，使用 TLS 必须显式填写容器内路径 |
| `allow_http` | 默认 `false`；仅本地测试或可信 HTTPS 终止代理之后显式设为 `true` |
| `allowed_origins` | 精确网页 Origin，如 `https://fn.example.com`，没有路径或尾部斜杠 |

也可用 `FNTV_MEDIA_TOKEN_KEY` 提供密钥；避免同时配置多个来源。使用文件时，权限必须为 `0600`，禁止组用户及其他用户访问，否则程序拒绝启动。两端应安全同步同一密钥，不要各自生成不同密钥；不要提交密钥、证书私钥或真实配置，也不要打印密钥到终端、日志或工单。

单机 `all` 模式不需要手工生成：省略 `token_key` 和 `token_key_file`，程序首次启动生成 `state_dir/token.key`，文件权限 0600、目录 0700。重启读取同一文件；已有文件损坏、权限不安全或指定的手动文件不存在时会拒绝启动，不自动覆盖。旧 proxy 模式不创建密钥。自动模式需要支持硬链接和目录同步的本地文件系统；不支持时明确报错，不退回不安全写入。

不同主机不能各自生成不同密钥后直接配合使用；本次不实现配对或自动同步。分机部署仍沿用显式共享密钥，也可以安全复制首次自动生成的密钥后作为另一端的 `token_key_file`。

以下为分机部署的可选手工生成方式（只执行一次，不覆盖已有密钥）：

```sh
mkdir -p secrets
chmod 700 secrets
(umask 077; set -C; openssl rand -hex 32 > secrets/media_key)
chmod 600 secrets/media_key
```

通过可信加密通道将此文件安全传送到第二台主机的同一相对位置，再执行 `chmod 600 secrets/media_key`。在两台 Linux 主机分别用 `stat -c '%a %n' secrets/media_key` 确认权限为 `600`；仅检查权限，不输出内容。容器运行用户也必须有读取权限。

下载本目录的 Compose 和配置示例，在每台主机以保存这些文件的目录为工作目录。使用发布镜像无需 Go、源码或本地构建环境：

```sh
# 原代理主机
cp proxy.config.yaml.example proxy.config.yaml
# 修改目标、允许列表、STRM 路径、公网媒体域名；准备 secrets/media_key
docker compose -f compose.proxy.yaml pull
docker compose -f compose.proxy.yaml up -d --no-build

# 媒体主机（单独执行）
cp media.config.yaml.example media.config.yaml
# 准备同一个 secrets/media_key，以及 certs/fullchain.pem 和 certs/privkey.pem
docker compose -f compose.media.yaml pull
docker compose -f compose.media.yaml up -d --no-build
```

Compose 固定使用 `ghcr.io/qiwolf/fntv-proxy-relay:0.9.8-relay.2-rc.1`，不拉取旧的 `latest`。如需本地构建，保留完整仓库并以 `deploy/direct` 为工作目录，取消对应 Compose 的 `build` 两行注释，将 `image` 改为 `fntv-proxy-relay:direct-local`，再运行 `docker compose -f compose.all.yaml up -d --build`（分机模式替换文件名）。

密钥文件和证书目录只读绑定挂载；Linux 上密钥绑定挂载保留宿主机 `0600` 权限。不要直接替换为默认 `0444` 的 Compose/Docker secret 挂载，这会触发权限检查失败；其他容器平台也需验证挂载后的实际权限。所有绑定挂载的源配置文件、密钥文件必须提前准备，否则 Docker 可能创建同名目录导致启动失败。示例中的内部主机名必须在容器内可解析且可达；容器里的 `127.0.0.1` 不是宿主机。若使用局域网路径，确保容器路由与 DNS 正确。

单台飞牛主机双监听可直接使用 `all.config.yaml.example` 和 `compose.all.yaml`：

```sh
cp all.config.yaml.example all.config.yaml
# 修改飞牛目标、媒体源、STRM 路径与域名，准备证书；密钥自动生成
docker compose -f compose.all.yaml pull
docker compose -f compose.all.yaml up -d --no-build
```

该配置用 `role: all` 同时发布 `28005` 与 `49963`：原 VPS 继续访问 `28005`，公网媒体端口映射到 `49963`。它是分机部署的替代方案，不要与已有占用同端口的代理容器同时启动；切换前备份原配置并安排原容器停止与新容器启动。不要把网页入口转发到专用媒体端口。飞牛主机若仍通过家庭到机房的隧道回源和出公网，这种部署只绕开 VPS，并不会绕开该隧道。

`compose.all.yaml` 将 `./data` 持久化到 `/app/data`。升级、重建容器必须保留该目录；丢失密钥会使原有票据失效。不要将数据目录或证书私钥打包进镜像或提交到 Git。

## 证书热加载

程序不负责证书签发、续期或同步。用户可以选择以下任一方式提供现有证书，客户端无需导入播放密钥，也无需为受信任且名称匹配的 HTTPS 证书另做配置：

- 按示例放置：宿主机 `./certs/fullchain.pem` 是 PEM 格式完整证书链，`./certs/privkey.pem` 是匹配的 PEM 私钥。Compose 将整个 `./certs` 目录只读挂载为 `/run/certs`，配置显式填写对应路径。
- 自定义位置：例如把宿主机 `/srv/media-tls` 只读挂载到容器 `/etc/media-tls`，配置填写 `tls_cert_file: "/etc/media-tls/site-chain.pem"` 和 `tls_key_file: "/etc/media-tls/site-key.pem"`。文件名可以自定，两处路径必须与挂载后的实际位置一致。

`/run/certs/fullchain.pem`、`/run/certs/privkey.pem` 只是示例约定，不是程序隐式默认值。非容器部署则直接填写程序所在主机上的实际路径。私钥应限制权限（建议 `0600`）并确保运行用户可读；不要将私钥放进公开目录、镜像、Git 或发布附件。使用证书续期工具时，请在工具侧完成续期及本地文件更新；若续期发生在另一台服务器，仍需自行安全传送，本程序不会代办。

将证书和私钥放在配置的路径，挂载整个证书目录（只读），不要单独绑定两个文件：外部工具原子替换文件时，单文件绑定可能仍指向旧 inode。程序启动时必须有一对当前有效的证书与私钥。

之后在新 TLS 握手时检查文件内容，最多每秒检查一次；完整、匹配且在有效期内的新证书自动用于新连接。已有播放连接不重启、不打断。更新过程中只换好一个文件、文件缺失或损坏时，记录警告并保留旧有效证书；旧证书也过期后拒绝新握手，不降级为 HTTP。不靠文件时间戳判断更新。

这里的热加载只读取本地更新后的文件，不会申请证书、自动续期或从其他服务器复制文件。证书内容更新不需要重启；变更证书路径需要重启。两文件应先在同一目录准备好再尽快替换；短暂不匹配会继续使用旧有效证书，直到下一次检查读到完整有效的新证书。

## 公网与安全边界

媒体域名应解析到实际媒体出口公网地址，TCP 映射指向媒体服务，证书必须匹配域名且客户端信任。VPS 的原网页域名与路由无需改变。浏览器从 HTTPS 页面加载媒体时应使用 HTTPS 媒体地址，不可把 `allow_http: true` 当成公网 HTTPS 的替代。若经过 TLS 终止代理，只向可信内部网络暴露 HTTP 监听，并让代理保留 Range 等请求头。

专用入口只处理带有效票据的媒体请求，不提供网页、登录或目录列表。对于到达应用鉴权层的合法 HTTP 请求，无效或缺失票据会中止连接、不返回网页；这不表示所有输入都零响应：畸形协议、将明文 HTTP 发到 TLS 端口等，可能在应用鉴权之前由 HTTP/TLS 协议栈返回错误。有效票据支持 GET、HEAD、Range/206 和浏览器跨域所需的鉴权 OPTIONS。Origin 允许列表不是票据的替代，也不是网络访问控制。

开放端口和 TLS 握手仍可被检测；不返回网页不等于服务不可识别，也不保证符合机房规则。上线前应确认机房允许这种服务。TLS 终止代理可能自行返回错误页面，需单独审查其行为。保持精确上游允许列表，不能把服务作为任意 URL 的开放代理。

## 验收、限制与回滚

先保留原配置和镜像，再以测试账号取得真实播放地址。验收应覆盖：旧模式仍可播放；专用端口不提供飞牛页面；缺失、篡改及过期票据拒绝；有效 HEAD、完整 GET、Range 和拖动正常；允许 Origin 的跨域播放正常；媒体回源重定向不能逃逸允许列表。最后从真正外网客户端持续播放并对照两端带宽，确认视频正文确实不经过 VPS。只看到容器运行或端口可连接不算播放验收。

直出只针对可处理的直接媒体文件 URL，不递归改写 HLS/DASH 清单中的分片、密钥或嵌套 URL；转码、本地媒体等仍可能走原有服务路径。长时间播放期间，已有连接与过期后的新请求不同：拖动、断线重连等若已过期，需要重新获取播放地址。客户端对跨域、TLS 和重定向的兼容性也必须实测。

媒体入口不可达时不会自动回退到 VPS 中继，以免不知情地重新占用 VPS 带宽。需要回滚时，将原代理恢复为 `role: proxy`、`delivery_mode: proxy`，保留原 `stream_mode`、允许列表与 STRM 配置，重启并重新获取播放地址。确认无人使用后再停止专用媒体服务、撤销端口映射；旧票据不会自动转换成同源地址。
