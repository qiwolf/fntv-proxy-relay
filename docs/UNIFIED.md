# 三服务统一代理部署指南

适用于正式版 `v0.9.8-relay.3`。同一镜像 `ghcr.io/qiwolf/fntv-proxy-relay:0.9.8-relay.3` 包含默认程序 `/app/fntv-proxy` 和显式选择的统一程序 `/app/fntv-unified`。统一 Compose 必须设置 `entrypoint: ["/app/fntv-unified"]`；默认程序不读取本指南的 `services` 配置。旧 RC 镜像不包含统一程序。现场已完成飞牛、Emby、Jellyfin 三端 STRM 实际播放与数据流核对，但仍需在自己的客户端和网络验收。

## 合并边界

部署仍分两台机器、两个容器：NAS 上的主代理容器处理飞牛影视、Emby、Jellyfin 的登录、媒体库和播放授权；机房媒体容器从允许的源站取视频并传给客户端。不是一个容器跨两台服务器，也不是把视频重新送回 NAS。

三个适配器保留各自协议处理。Emby 与 Jellyfin 的接口路径高度重叠，不能根据 URL 自动猜测服务。主代理同时提供两种明确入口：

| 访问方式 | 如何选择服务 | 候选示例 |
| --- | --- | --- |
| 域名入口 | 请求 Host 精确匹配配置的域名 | 三个域名共用 `28008` |
| IP 直连 | 每个固定监听端口只接一种服务 | 飞牛 `28015`、Emby `18197`、Jellyfin `18196` |
| 媒体入口 | 已签名票据绑定服务标识 | 三种服务共用机房 `49967` |

IP 直连继续可用，但同一 IP、同一端口不能可靠区分三种登录服务。未知 Host 不应落入某个默认服务。若前置反向代理提供 HTTPS，必须保留原始 Host；不要把三个域名都改成同一个上游 Host。

这些端口是与旧入口并行的示例值，可改成自己的空闲端口。先保留原入口和独立媒体服务，不自动切换生产域名。其他用户或其他 NAS 的服务不会自动加入统一配置。

## 媒体隔离

播放地址形式为 `https://media.example.com:49967/fntv-media/<服务标识>/<票据>`，三个服务标识为 `fntv`、`emby`、`jellyfin`。客户端不需要额外配置媒体密钥或媒体服务器地址。

每个服务使用独立密钥、源站白名单和浏览器来源白名单。票据与服务标识绑定，不能把飞牛票据移到 Emby 路径使用。源 URL、源站密码不应明文出现在签发的媒体 URL 中。媒体服务还会检查取流重定向目标；不要把整个内网加入白名单。

两端同一服务必须使用相同密钥文件，不同服务使用不同密钥。文件内容为 32 字节随机密钥的 64 位十六进制表示，权限 `0600`，父目录 `0700`。统一程序要求每个服务显式配置 `token_key_file`，不使用默认程序的自动生成机制，也不自动同步。仅在首次部署时生成，再通过可信加密通道复制到另一端；不要覆盖已有密钥：

```sh
mkdir -p secrets
chmod 700 secrets
(umask 077; set -C; openssl rand -hex 32 > secrets/fntv.key)
(umask 077; set -C; openssl rand -hex 32 > secrets/emby.key)
(umask 077; set -C; openssl rand -hex 32 > secrets/jellyfin.key)
chmod 600 secrets/*.key
```

容器运行用户必须能读取这些文件。不要把真实密钥、证书私钥或真实配置提交到 Git。票据有效期默认 3600 秒，范围 1–86400 秒；到期后重连或拖动可能需要重新获取播放地址。票据不是一次性凭证，原服务器撤销权限不会立即撤销已签发票据。

## 文件与启动

先按 [三端 STRM 挂载详解](STRM_MOUNTS.md) 核对路径：左侧是代理宿主机实际目录，右侧是对应媒体服务器看到的目录，不要求左右相同。三端使用不同路径时，同一份源目录可以只读映射到多个目标路径。

样例位于 `deploy/unified/`。分别将 `proxy.yaml.example` 和 `media.yaml.example` 复制成所在机器的 `config.yaml`，修改地址、域名和挂载。主代理必须只读挂载 STRM，容器内路径应与对应媒体服务器返回的路径一致，并受各服务的 STRM 根目录白名单限制。机房只运行媒体角色时不需要 STRM 挂载。

分别在 NAS 和机房创建独立部署目录，从同一版本的 `deploy/unified/` 复制对应 Compose 与 YAML 示例。两端使用同一正式镜像，无需安装 Go 或本地构建：

```sh
docker pull ghcr.io/qiwolf/fntv-proxy-relay:0.9.8-relay.3
```

Compose 样例使用 Linux 的 host 网络，文件相对路径以各自 Compose 所在目录为准；启动前检查所有监听端口空闲。若使用桥接网络，需要自行发布相应端口，且 `127.0.0.1` 不再代表宿主机。两端各有自己的 `config.yaml`，不能将主代理配置原样放到媒体节点。

```sh
cp proxy.yaml.example config.yaml
# 修改配置、STRM 挂载并准备密钥后：
docker compose -f compose.proxy.yaml config --quiet
docker compose -f compose.proxy.yaml up -d --no-build
# 在机房媒体主机执行：
cp media.yaml.example config.yaml
# 修改配置并准备相同密钥、有效证书后：
docker compose -f compose.media.yaml config --quiet
docker compose -f compose.media.yaml up -d --no-build
```

## 配置字段

统一配置严格校验字段，不接受未知字段或多个 YAML 文档；不使用旧程序的顶层 `emby`、`jellyfin`、`stream_mode`、`media` 等结构。

| 字段 | 说明 |
| --- | --- |
| `role` | 必填 `proxy` 或 `media`，不支持 `all` |
| `listen` | 必填 `host:port`；proxy 为 Host 分流入口，media 为统一媒体监听 |
| `allow_http` | 默认 false；无证书时必须显式 true，仅适合内网或可信 TLS 反代后方 |
| `tls_cert_file` / `tls_key_file` | 成对指定容器内路径；TLS 对该进程全部监听生效 |
| `media_public_base_url` | 客户端可达的媒体 HTTPS 基址；两端按示例设为相同值 |
| `services.<id>` | 服务配置映射；ID 两端一致，格式为小写字母开头的 1–32 位小写字母、数字或短横线 |
| `type` | `fntv`、`emby`、`jellyfin`；ID 是隔离范围，type 是协议适配器 |
| `hosts` | proxy 域名列表，不写协议或路径，不支持通配符；匹配时忽略端口和大小写，不能重复 |
| `direct_listen` | proxy 可选独立 IP 入口；每个监听固定一种服务，媒体角色不可配置 |
| `target` | proxy 必填对应服务器 HTTP(S) 地址，不允许内嵌凭据、查询串或 fragment |
| `token_key_file` | 两端必填该服务密钥文件；修改或轮换需重启并使旧票据失效 |
| `token_ttl_seconds` | 0/省略为 3600，允许 1–86400 |
| `allowed_upstreams` | 两端该服务源站白名单；填写 host 或 host:port，非标准端口必须写明 |
| `allowed_strm_roots` | proxy 可读 STRM 根目录，与只读挂载一致；media 不需要 |
| `allowed_origins` | media 浏览器来源白名单，精确协议、域名/IP、端口；不是用户鉴权 |

proxy 每个服务至少设置 `hosts` 或 `direct_listen` 之一；共享 `listen` 仍需填写。可仅启用需要的服务，或使用不同 ID 配置同类型的不同服务器；两端的 ID、密钥和允许列表需要逐一对应。统一模式当前固定为直出，不支持逐服务切成旧 redirect/relay，后者继续使用默认程序。

## 证书和网络

主代理样例使用 HTTP，适合内网或可信 HTTPS 反代后方；不要把明文登录入口直接暴露到不可信网络。公网媒体入口使用 HTTPS，配置证书链和私钥路径，并把整个证书目录只读挂载，便于原子替换后热加载。

主代理配置的 TLS 同时应用于共享入口和各固定端口。若要保留普通 IP 直连，应使用样例中的内网 HTTP，由前置代理为域名入口终止 HTTPS；不要给仅覆盖域名的证书搭配裸 IP HTTPS 地址。

本程序不签发、不续期、不同步证书。管理员或现有证书工具负责更新文件，证书需覆盖媒体域名。密钥是服务器之间共享的播放票据密钥，不是客户端要安装的证书。

公网 `49967` 应映射到机房媒体主机的 `49967`，不能映射到 NAS 主代理。机房必须能访问各服务白名单中的真实源站。浏览器播放需要精确配置实际访问主代理的 Origin（协议、域名或 IP、端口），原生客户端与浏览器的跨域行为不同；不要为了排错直接放开所有来源。

## 前置 HTTPS 反向代理

公网登录入口建议由已有 VPS 反代终止 HTTPS，再把三个域名转发至 NAS 的共享 `28008`。务必保留 Host 并支持 WebSocket；以下片段放在已有有效证书的 Nginx 配置中，替换域名及 NAS 地址。`map` 位于 `http` 上下文：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}
server {
    listen 443 ssl;
    server_name fntv.example.com emby.example.com jellyfin.example.com;
    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;
    location / {
        proxy_pass http://NAS_IP:28008;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 3600s;
    }
}
```

证书必须覆盖三个登录域名，媒体证书另覆盖媒体域名。不要将媒体域名也指回此 VPS，否则视频仍占 VPS 带宽。没有域名时保留三个 IP 固定端口；公网媒体仍推荐域名 HTTPS，裸 IP HTTPS 需要包含该 IP 且受客户端信任的证书。

## 逐端验收

先验证原服务继续可用，再分别通过三个候选 IP 端口登录原账号。每端选择已确认的 STRM 条目，实际起播、拖动、续播，并核对机房媒体连接和字节增长；仅容器启动、`302` 或 `206` 请求成功不等于客户端验收完成。还需检查无效登录、无效票据、跨服务票据和不允许的源站被拒绝，以及返回地址没有泄漏源站凭据。

STRM 直出不等于转码迁移。客户端不能解码、需要转码，或媒体条目是普通文件时，可能仍走原媒体服务器；验收应记录播放方式，不把这种回退误认为机房直出。原有账号和媒体库权限继续由各自服务器决定。

## 升级和回滚

1. 记录原镜像标签/摘要、监听、反代和端口映射，备份 Compose、配置与受保护密钥。升级默认程序时沿用原配置即可，不强制切换统一入口。
2. 迁移统一模式时使用新部署目录和本指南 `services` 结构、显式统一 entrypoint、空闲测试端口。旧配置不能直接交给统一程序。先启动机房媒体服务并确认网络，再启动主代理。
3. 三端分别验证起播、拖动、权限及实际视频路径，通过后才修改正式反代或原端口。保留旧容器停止状态和原配置，不能让两个进程争用同一端口。
4. 旧单服务票据与统一带服务范围票据不兼容，即使复用密钥也不能保留原播放 URL。切换、改变服务 ID 或轮换密钥后退出影片重新播放；不要把旧播放地址自动无鉴权转换成新票据。
5. 回滚时停止统一主代理，恢复原反代/监听和原镜像配置；按需恢复旧媒体映射并启动旧媒体服务。客户端连回原入口并重新播放。只撤销本次新增映射，不删其他服务规则、密钥或媒体库。

发布镜像本身不会替你切换生产端口。正式版支持不等于你的环境已完成迁移；请保存每端的客户端版本、播放类型及网络证据。三端实播验证不能替代长时播放、并发或 IPv6 专项测试。
