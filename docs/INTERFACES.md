# 接口与使用说明

## 飞牛影视演员资料的位置

飞牛影视演员不是每个演员单独一个 NFO 文件。当前用户 NAS `flymoo` 上核实到，中央演员资料保存在 FnOS Media 数据库 `trimmedia.db` 的 `person` 表，影片与人物的关联保存在 `item_person` 表；头像文件由应用存放在哈希图片目录中。

当前数据库目录：

```text
/usr/local/apps/@appdata/trim.media/database/
```

其中除 `trimmedia.db` 外，还可能存在 `trimmedia.db-wal` 和 `trimmedia.db-shm`。因此批量模式应将**整个数据库目录以只读方式**挂载到容器，例如 `/fnos-db`。不要只挂载主库文件，也不要直接编辑、替换或复制覆盖数据库/WAL/SHM。

默认批量模式从 `person` 表读取本地人物，不扫描电影目录。程序不挂载飞牛应用图片目录，而是通过飞牛 API 上传图片。用户 NAS 上的 `/vol1/video/movies` 不存在；默认无需媒体目录。若显式用 `--root` 从 NFO 筛选演员，需要挂载飞牛媒体库配置中实际存在且含有 NFO 的目录，并只读访问。

## CLI

```text
fnactor [--actor NAME | --root DIR] [--db FILE] [--cache DIR] [--limit N] [--concurrency N] [--refresh] [--apply] [--overwrite] [--probe]
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--actor NAME` | 无 | 仅处理指定演员；不需要数据库挂载 |
| `--root DIR` | 无 | 递归读取此目录 NFO 演员名，并筛选任务；只读模式 |
| `--db FILE` | `FNOS_DB_PATH` 或 `/fnos-db/trimmedia.db` | 默认批量输入的飞牛影视数据库文件 |
| `--cache DIR` | `CACHE_DIR` 或 `/config` | 在线来源缓存目录，建议持久化 |
| `--limit N` | `0` | 限制本次处理人数；`0` 表示不限制 |
| `--concurrency N` | `1` | 演员任务并发；范围 1–2 |
| `--refresh` | 关闭 | 忽略资料来源缓存并重新查询 |
| `--probe` | 关闭 | 需要 `--actor`；抓取在线资料并处理头像，不登录、不写飞牛 |
| `--apply` | 关闭 | 实际调用飞牛 API 保存资料；缺省为预览 |
| `--overwrite` | 关闭 | 覆盖已有的本地未锁定字段；必须与 `--apply` 配合 |
| `--help` | — | 显示帮助信息 |

示例：

```sh
# 从本地 person 记录中取前 20 人并预览
docker compose run --rm fnactor --limit 20

# 确认后才写入缺失的简介和头像
docker compose run --rm fnactor --limit 20 --apply

# 只预览一个演员
docker compose run --rm fnactor --actor '三上悠亚'

# 在部署环境实测上游和图片转换，不需要飞牛登录凭据
docker compose run --rm fnactor --actor '三上悠亚' --probe

# 显式覆盖本地人物中已有且未锁定的字段
docker compose run --rm fnactor --actor '三上悠亚' --overwrite --apply
```

每次实际写入前先检查预览结果。官方人物、在线人物、有外部身份标识或被锁定的字段受保护，`--overwrite` 不会取消这些保护。

## 环境变量

| 名称 | 必需 | 用途 |
| --- | --- | --- |
| `FNOS_URL` | 是 | 飞牛影视 API 地址；host 网络推荐 `http://127.0.0.1:5666` |
| `FNOS_USERNAME` | 与密码配合 | 飞牛登录用户名 |
| `FNOS_PASSWORD` | 与用户名配合 | 飞牛登录密码；仅保存在秘密配置中 |
| `FNOS_TOKEN` | 可选 | 当前有效的飞牛 API token；如果设置则使用有效 token 认证 |
| `TPDB_API_TOKEN` | 可选 | ThePornDB performer API token；没有配置时跳过该来源 |
| `FNOS_DB_PATH` | 批量模式需要 | 容器内数据库文件路径，Compose 默认 `/fnos-db/trimmedia.db` |
| `CACHE_DIR` | 否 | 在线来源缓存根目录，默认 `/config` |
| `UPSTREAM_DELAY_MS` | 否 | 公网上游最小请求间隔，默认 2000 毫秒；请求仍经过全局串行队列 |
| `HTTP_PROXY` / `http_proxy` | 否 | 公网来源 HTTP 代理 |
| `HTTPS_PROXY` / `https_proxy` | 否 | 公网来源 HTTPS 代理 |
| `NO_PROXY` / `no_proxy` | 否 | 不经代理访问的地址；至少包含 `localhost,127.0.0.1` |
| `GOMEMLIMIT` | 否 | Go 运行时内存目标；容器推荐 `640MiB` |

代理变量应由部署环境提供，切勿写进源码、镜像或文档中的真实凭据示例。代理仅供公网来源使用；飞牛 API 必须直连本机 loopback。`.env` 含敏感信息，应限制读取权限并确认 Git 忽略。

## Docker 部署（飞牛 OS）

1. 从 `.env.example` 创建 `.env`，填写飞牛认证信息和所需代理环境变量；不要提交该文件。
2. 确认 FnOS 数据库目录实际存在。用户当前 NAS 上的已核实路径为 `/usr/local/apps/@appdata/trim.media/database`，其他设备/升级后可能不同。
3. 将数据库**目录**只读挂载至 `/fnos-db`，并将可写缓存目录持久化到 `/config`。
4. 使用 host 网络并把 `FNOS_URL` 设置为 `http://127.0.0.1:5666`；确保 `NO_PROXY` 包含 `localhost,127.0.0.1`。
5. 容器设置 `GOMEMLIMIT=640MiB` 和 `mem_limit: 768m`。Go 内存目标不是 cgroup 硬上限；后者才是容器的硬边界。
6. 先小批量预览，确认人物和待补字段，再使用 `--apply` 实际更新。

数据库挂载示例：

```yaml
volumes:
  - type: bind
    source: /usr/local/apps/@appdata/trim.media/database
    target: /fnos-db
    read_only: true
  - type: bind
    source: /vol1/docker/fnactor-cache
    target: /config
```

飞牛应用数据库 schema 可能变化。若应用目录迁移，先按实际安装位置更新 bind mount，不要创建一个空目录来掩盖挂载失败。除可选 `--root` 外，无需挂载影视库、`/vol1/@appmeta/trim.media/img` 或整个系统卷。

## 上游速率和重试

在线资料查询和头像下载共用一个串行队列，默认两次公网请求间至少 2 秒；演员处理并发默认 1，最高 2。因此提高演员并发不会让多个上游请求同时发出。包含响应处理时间后，实际速度通常更慢。

429、常见 5xx 和临时网络问题仅进行有限重试，并遵守 `Retry-After`。遇到 403/429 时暂停批量任务；恢复后可将 `UPSTREAM_DELAY_MS` 提高到例如 5000–10000 毫秒。避免对全库盲目使用 `--refresh`，这会使已缓存演员重新请求在线来源。

## 图片与内存边界

- 通用上游响应上限 16 MiB，必须流式计数并在超限时停止读取。
- 头像文件最多 10 MiB，图像最多 16,000,000 像素，处理后 JPEG 最多 4 MiB。
- 图片按单张处理，转换为 640×960 JPEG。下载、解码、转换或上传失败时应记录失败并继续/停止策略按错误类型处理，不能将无效数据写入人物档案。
- Go `GOMEMLIMIT=640MiB` 是运行时内存目标；Compose `mem_limit: 768m` 是容器 cgroup 上限。缓存与并发必须有界，二者都不能替代逐张处理和及时释放大型 buffer。
- 使用 `docker stats fnactor` 监控实际 NAS 内存占用；发生容器 OOM 时，先降低并发并定位未受限的响应体或图像缓冲，再考虑调整容器限制。

## 飞牛 API 与在线来源

当前适配的飞牛影视内部 API 路径：

| 方法与路径 | 用途 |
| --- | --- |
| `POST /v/api/v1/user/loginByPassword?channel=v2` | 飞牛用户登录 |
| `POST /v/api/v1/person/search` | 按名称查找 person |
| `POST /v/api/v1/person/getEditDetail` | 读取档案、官方标记和字段锁定信息 |
| `POST /v/api/v1/image/temp/upload` | 上传头像并取得临时图片标识 |
| `POST /v/api/v1/person/saveEditDetail` | 保存中央演员档案 |

接口参数和响应以当前飞牛前端实现为准；它们不是公开稳定 API。程序必须在保存前重新校验档案保护状态。升级后若请求失败，应核对新版前端调用，不要绕过 API 写数据库。

在线资料来源包括 Gfriends、Minnano-AV、Wikipedia 和 Wikidata。ThePornDB 提供额外演员简介与头像，但需要单独的 `TPDB_API_TOKEN`。HTML 使用 goquery 解析；结构化接口使用 Go 标准库 JSON/XML。头像只从允许的 HTTPS 来源下载，通过格式、字节数和像素数检查后再上传。MDC-NG 也列有 Graphis 演员来源；Graphis 页面明确限制未经授权转载其图片，因此本项目不复制该站图片。

## 故障排查

- **数据库打不开**：检查整个数据库目录是否只读挂载，确认 `/fnos-db/trimmedia.db` 及可能的 WAL/SHM 文件可见。
- **数据库 schema 不兼容**：飞牛可能更新了内部数据库结构；确认实际 `person` 列名后更新适配，不要直接猜列或改库。
- **登录失败**：检查 `FNOS_URL`、账户密码/token 和编辑权限；host 网络推荐回环 API 地址，并确认代理绕过列表含 `127.0.0.1`。
- **公网来源超时**：检查容器代理变量、代理可达性和 DNS；避免把飞牛 API 地址放进代理链路。
- **出现 403/429**：暂停任务，等待后调高 `UPSTREAM_DELAY_MS` 再运行。
- **头像失败**：检查来源图片链接、下载大小/格式/像素限制和 FnOS 上传权限。
- **官方/在线人物被跳过**：这是预期保护行为，覆盖选项不会解除保护。
- **内存偏高**：用 `docker stats fnactor` 观测；确认并发不超过 2，图片逐张处理，响应体与缓存都有界。
