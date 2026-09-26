# v0.9.8-relay.1

首个 STRM 服务端中继版本。

## 新功能

- 适配飞牛影视 0.9.8 `POST /v/api/v1/stream`，递归改写允许的内网媒体 URL。
- 新增 `/fntv-relay/<临时令牌>` 服务端流式回源，避免向公网客户端暴露内网地址与上游鉴权。
- 支持 GET、HEAD、Range、If-Range，并透传 200、206、416 及媒体相关响应头。
- 每一次上游跳转均执行 allowlist 校验，降低 SSRF 风险。
- `.strm` 读取限制在配置的真实目录内，阻止路径和符号链接逃逸。
- 保留旧版 PlaybackInfo/stream 代理和可选 Emby 302 功能。

## 容器

- GitHub Actions 自动测试并发布 `linux/amd64`、`linux/arm64` GHCR 镜像。
- 镜像包含 provenance 与 SBOM。
- 推荐生产环境固定使用 `0.9.8-relay.1` 标签。

## 升级提示

relay 模式必须配置：

- `stream_mode: relay`
- `allowed_upstreams`
- `allowed_strm_roots`
- 公网部署建议配置 `public_base_url`

STRM 宿主机目录必须以相同路径只读挂载到容器。完整步骤见 `docs/RELAY_DEPLOYMENT.md`。

## 已知边界

- 飞牛影视接口不是公开稳定 API，后续版本可能需要适配。
- 上游项目当前未提供明确 LICENSE，本派生仓库不擅自重新授权；详见 `NOTICE.md`。
