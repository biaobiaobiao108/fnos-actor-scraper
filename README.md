# fnactor

`fnactor` 是运行在飞牛 OS Docker 中的批量演员资料刮削工具。它为飞牛影视中的本地演员档案查找头像和简介，并将资料写回飞牛影视的中央演员档案。

飞牛影视的演员不是独立 NFO 文件：演员资料保存在应用数据库的 `person` 表中，头像文件由飞牛影视放在应用管理的哈希图片目录。一个演员被多部影片引用时，共用同一条中央档案。

## 处理规则

- 默认从飞牛影视数据库只读读取本地演员名单；无需挂载电影、剧集或 `No.1Style` 媒体目录。
- 数据库只用于列举演员。程序通过飞牛影视 API 保存头像与简介，不直接写数据库、WAL/SHM 文件或图片目录。
- 仅处理本地档案；带 TMDb/IMDb 标识或飞牛官方标记的在线资料会跳过。
- 默认只补缺失头像和简介，资料齐全时重复运行会跳过。`--overwrite` 仅覆盖本地档案中未锁定的字段。
- 默认预览；实际写入必须显式传 `--apply`。不创建演员档案，重名或找不到目标时跳过。

## 部署

镜像：

```text
ghcr.io/biaobiaobiao108/fnos-actor-scraper:latest
```

复制 `.env.example` 为 `.env`，填写 `FNOS_URL` 和登录用户名/密码（或当前有效的 `FNOS_TOKEN`）。密码以明文保存在 `.env`，请限制文件权限并勿提交到 Git。
如果访问公网来源需要代理，可在 `.env` 中设置 `http_proxy` 与 `https_proxy`；Compose 会同时传入大小写两种环境变量。`no_proxy` 默认包含 NAS 地址，保证飞牛 API 连接直达 NAS。

默认 Compose 会挂载以下目录：

- `/usr/local/apps/@appdata/trim.media/database` → `/fnos-db`，只读，包含 `trimmedia.db` 及 SQLite WAL/SHM 文件。它是当前在用户 NAS `flymoo` 上核实的 FnOS Media 数据库位置。
- `/vol1/docker/fnactor-cache` → `/config`，可写，用于持久化在线资料缓存。

Compose 不挂载影视库或飞牛私有图片目录。`/vol1/video/movies` 在用户的 NAS 上不存在。数据库路径是 FnOS 内部实现，升级或迁移后可能变化；部署前请通过飞牛应用配置确认。不要用数据库 bind mount 代替飞牛 API 写入。

容器使用 host 网络访问飞牛 Web API，按需运行而非常驻服务。Compose 用 `manual` profile，需手动启动，不会随开机自动刮削。

## 使用

```sh
# 默认批量模式：枚举飞牛影视中的本地演员，先预览前 20 人
docker compose run --rm fnactor --limit 20

# 确认预览结果后，补全前 20 人的缺失资料
docker compose run --rm fnactor --limit 20 --apply

# 单人预览，不需要挂载数据库
docker compose run --rm fnactor --actor '三上悠亚'

# 显式覆盖本地档案的已有头像/简介
docker compose run --rm fnactor --actor '三上悠亚' --overwrite --apply
```

| 参数 | 说明 |
| --- | --- |
| `--actor NAME` | 只处理指定演员 |
| `--db FILE` | FnOS 数据库文件，默认 `FNOS_DB_PATH` 或 `/fnos-db/trimmedia.db` |
| `--root DIR` | 可选地从该目录只读扫描 NFO，按其中演员名筛选任务 |
| `--cache DIR` | 缓存目录，默认 `/config` |
| `--limit N` | 限制本次演员数；0 表示不限 |
| `--concurrency N` | 演员处理并发数，默认 1，最高 2 |
| `--refresh` | 忽略来源缓存并重新刮削 |
| `--apply` | 实际保存到飞牛影视；缺省只预览 |
| `--overwrite` | 覆盖本地演员档案中已有且未锁定的字段，必须搭配 `--apply` |

如需用 `--root` 选择媒体库中的演员，需要额外把**实际含 NFO 的媒体路径**只读挂到容器目录（例如 `/media`），再运行 `--root /media`。用户 NAS 上已核实的一个媒体库路径为 `/vol02/1000-1-17f2bfff/整理好的学习资料/No.1Style/`；它只是可选的 NFO 名称来源，不是飞牛演员资料路径。该卷标识可能变化，实际路径以飞牛媒体库设置为准。

## 批量速度与来源限流

默认只处理一个演员。在线来源按顺序查询；在线资料及头像请求由全局队列串行发送，默认间隔至少 2 秒，遇到 429/常见 5xx 或网络错误会有限重试并遵守 `Retry-After`。最高 `--concurrency 2`，不会绕过上游队列。可通过 `UPSTREAM_DELAY_MS` 设置 500–60000 毫秒的请求间隔；建议保持默认值，遇到 429/403 时暂停任务并提高间隔。

## 内存上限

Compose 将容器内存限制为 768 MiB。上游 HTTP 响应体默认限制为 16 MiB，头像原图限制为 10 MiB、1600 万像素，处理后图片限制为 4 MiB。Gfriends 文件树在同一进程内共享解析结果；sharp 的原生缓存限制为 32 MiB、并行线程为 1。结合默认单演员并发，避免多个大图片同时解码造成不可预测的内存峰值。可用 `docker stats fnactor` 查看任务期间的容器内存。

演员资料缓存位于 `/config/actors`。缓存命中时不会重新请求资料来源；`--refresh` 会强制重新查询，建议谨慎用于全库批量任务。

## 在线来源与实现

在线来源包括 Gfriends、Minnano-AV 和 Wikipedia 中文/日文。头像处理成 640×960 JPEG，再经飞牛 API 上传。当前飞牛演员编辑接口是 Web 前端使用的内部 API，可能随 FnOS 更新而变化。

更多部署、接口和兼容说明见 [接口与使用说明](docs/INTERFACES.md) 与 [实现与架构](docs/ARCHITECTURE.md)。项目协作规则见 [AGENTS.md](AGENTS.md)。

## 本地开发

需要 Bun：

```sh
bun install --frozen-lockfile
bunx tsc --noEmit
bun run build
bun run fnactor -- --help
```

GitHub Actions 在 main push、版本标签或手动触发时构建 `linux/amd64`、`linux/arm64` 镜像并发布到 GHCR。镜像以 `oven/bun:alpine` 为基础。
