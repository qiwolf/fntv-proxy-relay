# 飞牛影视 STRM Relay 部署指南

## 1. 适用场景

飞牛影视解析 `.strm` 后，把内网媒体 URL 直接返回给播放器；公网客户端无法访问该内网地址时，本代理会将 URL 改写成同源临时令牌，并在服务端代为回源。

```text
客户端
  -> HTTPS 公网反向代理
  -> fntv-proxy-relay :28005
  -> 飞牛影视 :8005

媒体请求
  -> /fntv-relay/<临时令牌>
  -> allowlist 校验
  -> 内网 WebDAV/媒体源
  -> 200/206/416 流式返回
```

已适配飞牛影视 0.9.8 的 `POST /v/api/v1/stream` 响应，同时保留旧版 PlaybackInfo/stream 处理方式。未来飞牛影视接口变化时需要重新验证。

## 2. 准备配置

```bash
cp config.yaml.example config.yaml
```

最小 relay 配置：

```yaml
listen: ":28005"
target: "http://127.0.0.1:8005"
log_level: "info"
cache_ttl: 60

stream_mode: "relay"
public_base_url: "https://fn.example.com"
allowed_upstreams:
  - "192.168.1.20:5244"
allowed_strm_roots:
  - "/vol00/strm"

emby:
  enabled: false
```

安全要求：

- `allowed_upstreams` 必须只列出真实媒体源，非标准端口需明确写出；代理会对每一次跳转重新校验。
- `allowed_strm_roots` 只挂载需要读取的 STRM 目录，并使用只读挂载。
- 不要把真实 `config.yaml`、口令、Cookie 或带鉴权参数的 URL 提交到 Git。
- 公网入口必须启用 HTTPS；建议另加访问控制、速率限制和请求体大小限制。
- `public_base_url` 必须是客户端实际访问的外部地址，不能填写内网 IP。

## 3. Docker Compose

本仓库发布的镜像地址为：

```yaml
services:
  fntv-proxy-relay:
    image: ghcr.io/qiwolf/fntv-proxy-relay:latest
    container_name: fntv-proxy-relay
    network_mode: host
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - /vol00/strm:/vol00/strm:ro
    environment:
      CONFIG: /app/config.yaml
      TZ: Asia/Shanghai
    restart: unless-stopped
```

如果飞牛影视不在容器宿主机上，可改用端口映射并把 `target` 配成实际地址：

```yaml
ports:
  - "28005:28005"
```

启动与查看日志：

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100 fntv-proxy-relay
```

## 4. 公网反向代理

Nginx 最小示例：

```nginx
location / {
    proxy_pass http://FNOS_IP:28005;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
}
```

不要再把同一域名的部分路径转回飞牛影视 `:8005`，否则 `/fntv-relay/` 可能绕过中间件。

## 5. 验收

1. 通过公网域名登录飞牛影视，而不是直接访问 `:8005`。
2. 播放一个 `.strm` 项目并拖动进度条。
3. 浏览器请求应出现 `/fntv-relay/<令牌>`。
4. 代理日志应记录 URL 改写以及媒体源 `206 Partial Content`。
5. 客户端响应中不应出现内网媒体地址、Basic Auth、Cookie 或原始查询参数。

基础检查：

```bash
curl -fsS https://fn.example.com/v >/dev/null
docker inspect -f '{{.State.Status}} restarts={{.RestartCount}}' fntv-proxy-relay
docker compose logs --tail=200 fntv-proxy-relay
```

## 6. 升级与回滚

生产环境不要长期只使用浮动的 `latest`，应固定版本标签，例如 `0.9.8-relay.1`。

```bash
docker pull ghcr.io/qiwolf/fntv-proxy-relay:0.9.8-relay.1
docker compose up -d
```

回滚时把 Compose 中的镜像标签改回上一个已验证版本，再运行 `docker compose up -d`。配置格式变更前先备份 `config.yaml`。

## 7. GitHub 容器发布

仓库自带 `.github/workflows/docker-image.yml`：

- 推送 `v*` 标签时先执行 Go race tests 和 vet；
- 测试通过后构建 `linux/amd64` 与 `linux/arm64`；
- 发布到 `ghcr.io/<仓库所有者>/<仓库名>`；
- 同时生成 provenance 与 SBOM；
- 使用 GitHub 内置 `GITHUB_TOKEN`，无需保存个人访问令牌。

首次发布后，在 GitHub Packages 页面确认容器包可见性。若仓库公开但容器包仍为 private，需要在 Package settings 中单独调整；公开会允许匿名拉取。
