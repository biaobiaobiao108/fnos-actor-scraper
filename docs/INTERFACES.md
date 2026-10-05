# 接口与使用说明

## 演员资料存放位置

飞牛影视的演员信息不是独立的演员 NFO 目录。当前在用户 NAS `flymoo` 上核实到：演员资料保存在 FnOS Media 数据库 `trimmedia.db` 的 `person` 表，电影与演员的关系另存在 `item_person` 表；应用图片目录为 `/vol1/@appmeta/trim.media/img`，内部按哈希分级存放图片。

因此默认批量模式读取演员数据库中的本地 person 记录，不扫描影片目录；图片不从文件系统直接替换，而通过飞牛影视 API 上传。只有显式使用 `--root` 时，程序才会读取媒体 NFO 中的演员名作为筛选条件。

当前 NAS 数据库目录为：

```text
/usr/local/apps/@appdata/trim.media/database/
```

目录中除 `trimmedia.db` 外，当时也存在 `trimmedia.db-wal` 和 `trimmedia.db-shm`，所以应将**整个数据库目录以只读方式**挂载进容器。不要只挂主库文件，也不要直接编辑或替换数据库/WAL/SHM。

## 命令行

```text
fnactor [--actor NAME | --root DIR] [--db FILE] [--cache DIR] [--limit N] [--concurrency N] [--refresh] [--apply] [--overwrite]
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--actor NAME` | 无 | 只处理指定演员；此模式不需要数据库挂载 |
| `--root DIR` | 无 | 从此目录递归读取 NFO 名称，并用名称筛选飞牛演员记录 |
| `--db FILE` | `FNOS_DB_PATH` 或 `/fnos-db/trimmedia.db` | 默认批量模式读取的飞牛影视数据库文件 |
| `--cache DIR` | `CACHE_DIR` 或 `/config` | 在线资料缓存目录，应持久化 |
| `--limit N` | `0` | 限制本次处理数量；0 表示不限 |
| `--concurrency N` | `1` | 同时处理演员数，范围为 1–2 |
| `--refresh` | 关闭 | 忽略演员在线资料缓存并重新查询来源 |
| `--apply` | 关闭 | 实际更新飞牛中央演员档案；缺省只预览 |
| `--overwrite` | 关闭 | 与 `--apply` 配合，覆盖已有的本地资料字段；仍保护官方资料与锁定字段 |
| `--help` | — | 显示帮助 |

示例：

```sh
# 默认直接从 person 表枚举本地演员，先预览 20 人
docker compose run --rm fnactor --limit 20

# 确认后写入缺失的头像和简介
docker compose run --rm fnactor --limit 20 --apply

# 单人模式不要求数据库或媒体目录
docker compose run --rm fnactor --actor '三上悠亚'

# 如需只处理某个媒体目录中出现的演员，将该目录只读挂载到 /media
docker compose run --rm fnactor --root /media --limit 20
```

## Docker 部署（飞牛 OS）

1. 复制 `.env.example` 为 `.env`，填写飞牛地址及登录信息。
2. 确认 Compose 中数据库源目录存在：

   ```text
   /usr/local/apps/@appdata/trim.media/database
   ```

   这是用户当前 NAS 上实测路径，不保证适用于其他 FnOS 安装位置。升级/迁移后如目录改变，应通过飞牛应用数据位置重新确认。

3. 默认 Compose 使用只读数据库挂载及可写缓存挂载：

   ```yaml
   volumes:
     - /usr/local/apps/@appdata/trim.media/database:/fnos-db:ro
     - /vol1/docker/fnactor-cache:/config
   ```

4. 先预览小批量任务，再显式启用写入。

默认**不需要挂载任何影视库目录**。之前文档中的 `/vol1/video/movies` 在用户 NAS 上不存在。当前媒体库根目录之一是 `/vol02/1000-1-17f2bfff/整理好的学习资料/No.1Style/`，抽查确认其中有 NFO；它仅可用作 `--root` 的可选输入，不是演员数据目录。其他已配置库包含 `/vol00/WDC WD6400BEVT-22A0RT0/Learning/` 和 `/vol02/1000-1-17f2bfff/整理好的学习资料/UnlimitedLearning_2/`。`/vol00`、`/vol02` 后面的挂载标识可能变化。

如选择 `--root`，请按飞牛媒体库设置中的实际路径增加只读 bind mount，例如：

```yaml
volumes:
  - /vol02/1000-1-17f2bfff/整理好的学习资料/No.1Style:/media:ro
```

无需挂载 `/vol1/@appmeta/trim.media/img`、整个 `/vol1` 或媒体数据库以外的系统目录。数据库目录只读挂载只用于取得本地演员清单，所有保存操作仍经飞牛影视 API。

Compose 使用 host 网络，容器以 `manual` profile 按需运行，不是常驻服务。镜像为：

```text
ghcr.io/biaobiaobiao108/fnos-actor-scraper:latest
```

## 配置项

| 名称 | 必需 | 用途 |
| --- | --- | --- |
| `FNOS_URL` | 是 | FnOS Web 地址，例如 `https://192.168.1.10:5667` |
| `FNOS_USERNAME` | 二选一 | 飞牛登录用户名 |
| `FNOS_PASSWORD` | 二选一 | 飞牛登录密码 |
| `FNOS_TOKEN` | 二选一 | 当前有效的 API token，优先于用户名/密码 |
| `FNOS_DB_PATH` | 批量模式需要 | 容器内数据库文件，Compose 默认 `/fnos-db/trimmedia.db` |
| `CACHE_DIR` | 否 | 在线资料缓存目录，默认 `/config` |
| `UPSTREAM_DELAY_MS` | 否 | 上游 HTTP 请求最小间隔，默认 2000 毫秒，限制在 500–60000 毫秒 |
| `http_proxy` / `https_proxy` | 否 | 公网来源访问代理；Compose 同时导出大写变量名 |
| `no_proxy` | 否 | 代理绕过列表，默认包含本机、NAS 与代理所在主机 |

密码以明文保存在 `.env`，应限制文件权限并勿提交 Git。远程访问 FnOS 时必须使用 HTTPS 和有效证书；当前 Compose 使用 host 网络，建议将 `FNOS_URL` 设为 `http://127.0.0.1:5666`。该连接经容器所在 NAS 的 loopback 访问，不经过代理或 LAN；飞牛自签名 HTTPS 证书不会拦截 API 请求。程序仅允许 localhost/127.0.0.1 使用 HTTP。

## 批量并发与上游限流

批量处理演员的默认并发为 1，最高 2。一个演员的多个来源按顺序查询；所有在线资料请求和头像下载共用一个串行队列，默认请求间隔至少 2 秒，同一时刻最多一个上游响应体在下载。理论请求频率不高于每分钟 30 次，实际会更慢，因为还需等待来源响应。

遇到 429、常见 5xx 或网络错误时，程序进行有限退避并优先遵守 `Retry-After`。若上游返回 429/403，应停止批处理并调高 `UPSTREAM_DELAY_MS`（例如 5000–10000），稍后再续跑。不要对全库任务盲目启用 `--refresh`，它会重新查询已缓存的演员。

## 图片与内存控制

- 上游响应体默认最大 16 MiB；头像原图最多 10 MiB，解码输入最多 1600 万像素，上传 JPEG 最多 4 MiB。
- sharp 的原生缓存上限为 32 MiB、内部线程数为 1；Gfriends 文件树在进程内共享，多个演员不会重复下载和解析。
- 默认单演员并发；Compose 已将容器 cgroup 内存上限设为 768 MiB，异常峰值会限制在容器内。可用 `docker stats fnactor` 观察实际占用；如果未来数据规模明显增加，应依据观测调整此值。

## 接口与数据源

飞牛影视当前 Web API：

| 请求 | 用途 |
| --- | --- |
| `POST /v/api/v1/user/loginByPassword?channel=v2` | SHA-256 密码登录 |
| `POST /v/api/v1/person/search` | 单人或 NFO 模式按姓名搜索 person |
| `POST /v/api/v1/person/getEditDetail` | 读取演员编辑详情、官方标记和字段锁定状态 |
| `POST /v/api/v1/image/temp/upload` | 上传处理后的头像并取得 `hash_path` |
| `POST /v/api/v1/person/saveEditDetail` | 保存中央演员档案 |

本地演员列表由 SQLite 只读查询取得，条件是 `trim_id` 以 `LOCAL_PERSON_` 开头且没有 TMDb/IMDb ID；写入前还通过 API 再次读取并验证编辑详情。SQLite 查询按当前 `person` schema 检查必需字段，不匹配时应报错停止。

在线来源包括 Gfriends（头像）、Minnano-AV（演员资料）及 Wikipedia 中文/日文（头像与摘要）。来源缓存保存到 `/config/actors`，Gfriends 文件树缓存有效期为 24 小时。

上述飞牛 API 和数据库结构均为 FnOS 内部实现，非稳定公开接口。升级后若登录、查询或保存失败，应先核对新版前端/API/schema；不可通过直接写数据库绕过接口。

## 故障排查

- 数据库打不开：确认数据库目录以只读方式挂载到 `/fnos-db`，且主库与 `-wal`、`-shm` 文件可见；确认 `FNOS_DB_PATH` 指向主库。
- schema 不兼容：检查当前 `person` 表字段；不要自行猜列名并继续运行。
- `401/未登录`：检查飞牛地址、账户凭据或刷新 API token，并确认账户有媒体资料编辑权限。
- 官方/在线资料受保护：这是预期行为；`--overwrite` 也不会覆盖。
- 演员没有头像或简介来源：尝试单人 `--refresh`，检查 HTTPS 出网和上游是否限流。
- 头像上传失败：检查 FnOS API 权限；头像会转换为 2:3 JPEG 后上传。
