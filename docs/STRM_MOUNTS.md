# 飞牛 / Emby / Jellyfin 的 STRM 挂载怎么写

**媒体服务器看到 STRM 的路径是什么，代理容器里就必须能用同一个路径读到同一文件。不是要求挂载左右两侧相同。**

```yaml
volumes:
  # 格式：代理所在宿主机的真实目录:媒体服务器看到的目录:ro
  - /宿主机目录:/媒体服务器中的目录:ro
```

左侧由代理所在宿主机的存储位置决定；右侧由飞牛、Emby 或 Jellyfin 返回的 STRM 绝对路径决定。`ro` 表示代理只读。代理不会自动发现其他容器的挂载，也不会自动转换文件路径。

## 三种服务器分别看哪里

- 飞牛影视原生运行：看媒体库所选的 STRM 目录。例如库使用 `/vol00/strm`，代理右侧也写 `/vol00/strm`。代理在同一 NAS 上时，左右通常相同。
- Emby 容器运行：看 Emby 的卷映射和媒体库路径。例如 Emby 把宿主机 `/vol00/strm` 挂为 `/mnt/strm`，库使用 `/mnt/strm`，代理右侧就写 `/mnt/strm`，不是宿主机的 `/vol00/strm`。
- Jellyfin 容器运行：规则与 Emby 相同。例如 Jellyfin 的容器内路径为 `/media/strm`，代理右侧就写 `/media/strm`。

如果 Emby/Jellyfin 不是容器安装，以该服务实际看到、返回的文件路径为准；如果库选的是挂载目录下的子目录，可以挂载其父目录，只要完整文件路径最终一致。检查对象是 `.strm` 文件路径，不是 STRM 文本中的 `http://...` 视频地址。

## 同一份目录，三端使用不同路径

假设三个服务都读取宿主机 `/vol00/strm`，但各自看到的路径不同：

| 服务 | 服务侧配置 | 代理必须添加的挂载 |
| --- | --- | --- |
| 飞牛影视 | 原生库目录 `/vol00/strm` | `/vol00/strm:/vol00/strm:ro` |
| Emby | Emby 卷 `/vol00/strm:/mnt/strm`，库目录 `/mnt/strm` | `/vol00/strm:/mnt/strm:ro` |
| Jellyfin | Jellyfin 卷 `/vol00/strm:/media/strm`，库目录 `/media/strm` | `/vol00/strm:/media/strm:ro` |

合并主代理的 Compose 写成：

```yaml
volumes:
  # 飞牛返回 /vol00/strm/电影/a.strm
  - /vol00/strm:/vol00/strm:ro
  # Emby 返回 /mnt/strm/电影/a.strm
  - /vol00/strm:/mnt/strm:ro
  # Jellyfin 返回 /media/strm/电影/a.strm
  - /vol00/strm:/media/strm:ro
```

这是同一份文件的三个只读入口，不会复制三份数据。若三端都使用同一个容器内路径，只挂载一次即可；若只使用其中一个服务，只保留对应挂载。不要把不同内容的两个源目录映射到同一个目标路径。

统一配置中，还要将每个服务的允许目录设置为对应的**右侧路径**：

```yaml
# config.yaml 的局部片段，其他必填字段按统一配置示例保留
services:
  fntv:
    allowed_strm_roots: ["/vol00/strm"]
  emby:
    allowed_strm_roots: ["/mnt/strm"]
  jellyfin:
    allowed_strm_roots: ["/media/strm"]
```

默认程序的配置没有上述 `services` 结构，应把实际使用的右侧目录列入顶层 `allowed_strm_roots`。不要混用两套配置格式。

## 不同目录、远程代理和测试库

如果三个服务使用不同的 STRM 源目录，每行左侧分别写真实源目录；不要因为路径相似而映射到错误文件。Jellyfin 的独立测试库若位于 `/config/relay-strm-test`，需要额外挂载对应宿主机目录到这个路径；迁移媒体库后才移除旧映射，不能直接照抄别人的测试目录。

代理不在媒体服务器的同一宿主机时，左侧必须是**代理宿主机上已经可用的目录**，可先通过现有存储挂载提供同一份 STRM 文件，再映射到服务返回的路径。Docker 的卷映射不会自动访问另一台服务器的本地目录。

仅运行 `role: media` 的机房媒体节点不需要这些 STRM 挂载；它根据票据读取真实视频源。源 URL 替换配置 `strm_path_map` 也不能替代本节的文件挂载。
