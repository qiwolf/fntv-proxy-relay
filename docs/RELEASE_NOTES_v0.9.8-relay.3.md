# v0.9.8-relay.3

本版修复 Emby/Jellyfin 浏览器播放信息压缩兼容问题，并包含正式版之后完善的三种服务配置及 STRM 挂载说明。

## 修复

- 浏览器发送 `Accept-Encoding: gzip, deflate, br, zstd` 时，Jellyfin 可能返回 Brotli 压缩的 PlaybackInfo；旧代理无法解析，会返回 502 / `invalid playback response`。普通文件同样可能受影响，因为 JSON 解析发生在 STRM 判断之前。
- 现在仅对 GET/POST PlaybackInfo 回源请求使用 `Accept-Encoding: identity`，避免协商代理不能解析的压缩格式。其他 API、视频和转码请求保持原有处理方式。
- 保留授权、源站白名单和票据安全检查，不以无条件回源绕过解析错误。
- 新增 Emby/Jellyfin 反代级回归测试，覆盖浏览器压缩请求头、GET/POST、普通文件响应保持不变及其他 API 编码协商不变。

## 镜像与升级

```sh
docker pull ghcr.io/qiwolf/fntv-proxy-relay:0.9.8-relay.3
```

镜像支持 linux/amd64 和 linux/arm64，包含 `/app/fntv-proxy` 和 `/app/fntv-unified`。统一部署继续显式选择后者。部署模式、配置字段、服务密钥和票据格式均未改变；从 relay.2 升级无需重新生成密钥或修改客户端。

备份 Compose、配置和密钥后，将镜像标签改为 `0.9.8-relay.3`，拉取并重建对应主代理。媒体端协议未变，可与 relay.2 媒体容器兼容；也可统一升级媒体容器。重建会短暂断开请求，需重新播放。回滚时改回原固定标签并重建，勿覆盖密钥。

如果此前在 Nginx 的 PlaybackInfo 路径加入 `Accept-Encoding identity` 临时修复，可在主代理升级并验证后移除；保留也不影响正确性。本次 GitHub/镜像发布不自动修改运行中的容器。

## 部署说明补全

参见 [统一部署指南](UNIFIED.md) 和 [直出指南](DIRECT_MEDIA.md) 中的完整配置示例。STRM 宿主机目录可以不同，但代理内的文件路径必须与对应飞牛/Emby/Jellyfin 返回的路径一致；同一个源目录映射多个容器路径可能是不同媒体服务器路径约定所需。

## 验证边界

播放信息压缩修复不等于转码能力修复。普通文件和需要转码的视频仍由原媒体服务器处理。AV1 解码能力不代表支持 AV1 编码；硬件转码失败应检查原服务器的 FFmpeg 日志和硬件编码设置，本项目不自动修改这些设置。

证书仍由用户提供，支持配置路径与热加载；不包含自动签发、续期或同步。已有三端部署能力参见 [relay.2 发布说明](RELEASE_NOTES_v0.9.8-relay.2.md)。
