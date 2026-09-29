# v0.9.8-relay.5 — 管理界面直接访问

- 管理程序默认监听 `0.0.0.0:18764`，映射端口后访问 `http://服务器IP:18764`。
- 不要求 HTTPS、证书、域名或前置反代；取消 Host 白名单。
- IP、主机名、域名均可使用。保留管理员登录及自动跨站请求防护，无需额外配置。
- 旧 `-public-url` 参数保留兼容，不再限制访问；原有播放代理入口及视频证书逻辑不变。
- Compose 示例和说明同步更新。HTTP 管理入口建议仅在可信内网或 VPN 使用。

镜像：`ghcr.io/qiwolf/fntv-proxy-relay:0.9.8-relay.5`，支持 amd64 / arm64。

[管理说明](https://github.com/qiwolf/fntv-proxy-relay/blob/v0.9.8-relay.5/docs/WEBUI.md) · [Compose 示例](https://github.com/qiwolf/fntv-proxy-relay/blob/v0.9.8-relay.5/compose.manager.yaml.example)

升级不自动启用管理入口，不迁移旧配置，也不升级现有生产容器。
