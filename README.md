# fnactor

`fnactor` 是一个使用 Go 编写、运行在飞牛 OS Docker 上的命令行工具，用于批量为飞牛影视中的本地演员中央档案补充头像和简介。

飞牛影视的演员资料集中保存在应用数据库的 `person` 表中，不是每部影片各自维护的演员 NFO。多部影片引用同一演员时，更新的是共享的中央档案。

## 功能与保护规则

- 默认以只读方式从飞牛影视数据库枚举本地演员；无需挂载电影或剧集目录。
- 数据库只用于读取候选名单。简介和头像只能通过飞牛影视 API 保存，程序不直接修改数据库、WAL/SHM 或飞牛管理的图片目录。
- 仅处理本地人物档案；飞牛官方档案、已有 TMDb/IMDb 标识的在线档案会跳过。
- 默认只补缺少的简介和头像。资料已齐全时跳过；`--overwrite` 只允许覆盖本地档案中未锁定的字段。
- 默认是预览模式。只有显式传入 `--apply` 才会写入飞牛影视。不会创建人物，也不会猜测处理重名。
- 支持 `--actor` 指定单人，也支持通过 `--root` 只读扫描 NFO 并按其中演员名筛选。

## 部署

镜像地址：

```text
ghcr.io/biaobiaobiao108/fnos-actor-scraper:latest
```

复制 `.env.example` 为 `.env`，填写飞牛登录信息或有效的 `FNOS_TOKEN`。在 NAS 上建议使用 host 网络并将 `FNOS_URL` 设为 `http://127.0.0.1:5666`，以便直接访问本机飞牛 API。公网来源可以通过 `http_proxy`、`https_proxy` 配置代理；确保 `NO_PROXY` 包含 `127.0.0.1,localhost`，避免飞牛 API 请求经过公网代理。大小写代理变量均可配置。

`.env` 内的口令和令牌是敏感信息，应限制文件权限并确保不提交到 Git。

默认批量模式需要将飞牛影视数据库目录整体只读挂载到 `/fnos-db`，因为 SQLite 可能需要同目录中的 WAL/SHM 文件：

```text
/usr/local/apps/@appdata/trim.media/database:/fnos-db:ro
```

该目录是当前用户 NAS 上核实的位置，其他安装或系统升级后可能不同。缓存目录 `/config` 可写并建议持久化。无需挂载飞牛私有图片目录或整个影视库；只有使用 `--root` 时才需要将包含 NFO 的实际媒体目录只读挂载进容器。用户 NAS 上的 `/vol1/video/movies` 不存在，请以飞牛媒体库设置中的实际路径为准。

这是按需运行的 CLI 任务，不是常驻服务。部署后建议先执行预览，再对少量演员执行 `--apply`。

## 使用

```sh
# 默认批量模式：枚举本地演员，预览前 20 人
docker compose run --rm fnactor --limit 20

# 确认后补全前 20 人的缺失资料
docker compose run --rm fnactor --limit 20 --apply

# 单人预览
docker compose run --rm fnactor --actor '三上悠亚'

# 在线来源和头像处理诊断，不要求飞牛账号，也不会修改资料
docker compose run --rm fnactor --actor '三上悠亚' --probe

# 显式覆盖已有的、未锁定的本地资料
docker compose run --rm fnactor --actor '三上悠亚' --overwrite --apply
```

| 参数 | 说明 |
| --- | --- |
| `--actor NAME` | 只处理指定演员 |
| `--db FILE` | 飞牛影视数据库文件；默认取 `FNOS_DB_PATH` 或 `/fnos-db/trimmedia.db` |
| `--root DIR` | 可选地只读扫描目录中的 NFO，并按演员名筛选任务 |
| `--cache DIR` | 在线资料缓存目录；默认取 `CACHE_DIR` 或 `/config` |
| `--limit N` | 本次最多处理人数；`0` 表示不限 |
| `--concurrency N` | 演员任务并发数；默认 1，最高 2 |
| `--refresh` | 忽略在线资料缓存并重新查询来源 |
| `--probe` | 仅实测来源抓取和头像处理，不登录或写入飞牛；需要 `--actor` |
| `--apply` | 实际通过飞牛 API 保存资料；缺省仅预览 |
| `--overwrite` | 覆盖本地档案中已有且未锁定的字段；必须搭配 `--apply` |
| `--help` | 显示命令帮助 |

## 限流与内存

程序按顺序访问在线资料来源，并让所有公网来源请求和头像下载共用串行队列，默认请求间隔至少 2 秒。演员任务并发默认为 1，最高为 2。上游响应体限制为 16 MiB；头像下载限制为 10 MiB、1600 万像素，处理后的 JPEG 限制为 4 MiB。

容器建议配置 `mem_limit: 768m`，Go 运行时使用 `GOMEMLIMIT=640MiB`。Go 运行时内存软上限不等于进程的绝对硬限制；容器 cgroup 限制仍是最终边界。可通过 `docker stats fnactor` 观察真实任务峰值。缓存应按演员逐项保存和复用，避免反复加载大型来源数据；头像按单张下载、校验、解码和转换，避免并行保留多张原图与解码像素缓冲。

遇到 403/429 时应暂停批量任务，不要提高并发或增加激进重试。可将 `UPSTREAM_DELAY_MS` 调高后再继续。

## 实现概览

- Go CLI 单次运行，结束后退出。
- 使用标准库 `net/http`、`encoding/json`、`encoding/xml`，以 `goquery` 解析来源 HTML。
- 使用 `modernc.org/sqlite` 只读访问飞牛影视 `person` 表。
- 使用 `golang.org/x/image/webp` 和标准库 JPEG 解码器处理头像，转换为 640×960 JPEG 后经飞牛 API 上传。
- 在线来源包括 Gfriends、Minnano-AV 和 Wikipedia 中文/日文；缓存存放在 `/config`。
- 飞牛 API 是其 Web 前端使用的内部接口，飞牛版本升级时可能变化。

详细架构、接口、部署配置与故障排查见[实现与架构](docs/ARCHITECTURE.md)和[接口与使用说明](docs/INTERFACES.md)。项目协作规范见 [AGENTS.md](AGENTS.md)。

## 本地开发

需要 Go 工具链。常用开发命令：

```sh
go mod download
go build ./...
go vet ./...
go run . --help
```

不要在提交内容中包含 `.env`、登录口令、token 或代理凭据。GitHub Actions 负责构建并发布多架构容器镜像。
