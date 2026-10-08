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
fnactor [--actor NAME | --root DIR | --watch] [--db FILE] [--cache DIR] [--limit N] [--concurrency N] [--refresh] [--apply] [--overwrite] [--probe] [--watch-interval DURATION]
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
| `--watch` | 关闭 | 持续监控演员库；首次全库扫描并只补缺失字段，必须与 `--apply` 配合，且不能和 `--actor`、`--root`、`--probe`、`--limit`、`--overwrite` 同用；顺序处理 |
| `--watch-interval DURATION` | `1m` | 监控轮询间隔，范围 `10s` 到 `24h` |
| `--help` | — | 显示帮助信息 |

Docker 镜像无参数启动时只显示用法并保持空闲，不会自动批量扫描。在容器终端运行 `fnactor --limit 20` 才开始预览；显式添加 `--apply` 才会写入飞牛。数据库批量模式会跳过纯数字名称记录，因为在线来源按演员姓名检索；watch 模式静默忽略纯数字名称，空轮次会输出“=^._.^= [IDLE] 本轮检查完成，没有需要处理的新增或待重试演员。”常见流程日志使用 `[SCAN]`、`[ACTOR]`、`[OK]`、`[SKIP]`、`[WARN]`、`[RETRY]` 等纯文本状态标记，并用空行分隔主要区块。头像按 Gfriends、JavDB、Wikipedia、Wikidata 的优先级逐个尝试并支持按需跳过已就绪次级来源；JavDB 仅在演员名/别名精确匹配且头像不是占位图时提供头像。简介优先级为 Wikipedia、Wikidata。Minnano-AV 因持续返回 HTTP 403 已移除。

日文姓名候选依次包含飞牛显示名称、`person.original_name`、用户配置的已确认别名、简体转繁体、繁体转日文新字体。转换只生成汉字字形候选，不推测假名读音；使用 `--probe` 时终端显示正在尝试的候选名。候选名称只用于上游查询，不参与飞牛档案匹配。

补充别名文件为缓存根目录下的 `actor-aliases.json`（默认 `/config/actor-aliases.json`），格式为 `{"中文姓名":["日语姓名"]}`。每个姓名对应 1 至 8 个非空候选名称，文件上限 1 MiB。启动时读取，监控每轮重读；无效配置会报错并停止该轮，不能继续写入。别名必须是已确认的姓名对应关系。

Gfriends `Filetree.json` 会缓存为 `/config/gfriends-filetree.json`，并建立可重建的 `/config/gfriends-index.db` SQLite 精确名称索引。索引规范化 Unicode 兼容字符、大小写、标点空格和平/片假名。多目录图片以路径 basename 去掉查询参数、`.jpg`/`.png` 扩展名和 `AI-Fix-` 前缀后的规范化目标姓名分组；规范化名称精确命中后，按完整路径排序保留最多 16 条路径逐张回退；多个不同目标姓名只记录日志，不跳过该来源。该分组是受控文件名证据，不是稳定人物 ID。其他来源按相同候选顺序查询。

长期进程最多每分钟检查一次来源文件变化，Gfriends 缓存有效期为 24 小时。来源文件或索引版本变化时事务重建；下载、解析、重建失败及零条结果均保留健康索引并报告错误供重试。来源缓存和监控状态的 `lookupRevision` 由规则版本与该演员候选名称摘要构成；旧记录缺少版本、规则或候选改变时自动重新评估。

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

监控中标记完成但仍缺头像或简介的记录，每 7 天重新评估；已完整演员不做定期重新刮削。旧状态缺少 `lookupRevision` 时自动重新评估一次，规则升级或该演员候选改变时也会重新评估。重新评估仍先通过 API 检查身份与字段锁定，只补可写的缺失字段。

监控是显式启用的持续模式。首次启动立即扫描当前本地演员库；已有头像和简介按字段分别跳过，只补缺失且未锁定字段。之后按轮询间隔发现新 GUID 并自动处理。启动 Compose 监控服务即明确授权 `--apply` 写入缺失资料。已处理 GUID 和重试状态保存在 `/config/fnactor-state.db`，演员资料缓存继续使用 JSON 文件。旧版 `/config/watch-state.json` 不导入，首次启用 SQLite 状态库会从全库扫描；停止监控后删除 SQLite 数据库及其 WAL/SHM 文件也会重新扫描全库，但现有字段仍会跳过。失败按逐渐延长的间隔重试，最长间隔 24 小时。主要状态日志使用纯文本标签并在区块之间留空行，错误和重试信息仍输出到标准错误。日志由 Docker 收集，可在飞牛 Docker 的该容器“运行日志”中查看。Compose 为日志配置 `json-file` 驱动，最多保留 3 个、每个 10 MiB。暂停和恢复示例：

```sh
docker compose --profile watch up -d fnactor-watch
docker logs -f fnactor-watch
docker compose --profile watch stop fnactor-watch
```

`fnactor-watch` 使用 `restart: unless-stopped`，会在 Docker 重启后恢复运行；普通 `fnactor` 空闲服务仍不会自动刮削。

## 环境变量

| 名称 | 必需 | 用途 |
| --- | --- | --- |
| `FNOS_URL` | 是 | 飞牛影视 API 地址；host 网络推荐 `http://127.0.0.1:5666` |
| `FNOS_USERNAME` | 与密码配合 | 飞牛登录用户名 |
| `FNOS_PASSWORD` | 与用户名配合 | 飞牛登录密码；仅保存在秘密配置中 |
| `FNOS_TOKEN` | 可选 | 优先使用当前有效的飞牛 API token；配合账号密码可在 HTTP 401 后自动重新登录 |
| `FNOS_DB_PATH` | 批量模式需要 | 容器内数据库文件路径，Compose 默认 `/fnos-db/trimmedia.db` |
| `CACHE_DIR` | 否 | 在线来源缓存根目录，默认 `/config` |
| `UPSTREAM_DELAY_MS` | 否 | 公网上游最小请求间隔，默认 2000 毫秒；请求仍经过全局串行队列 |
| `JAVDB_BASE_URL` | 否 | JavDB 公网 HTTPS 镜像基础地址，默认 `https://javdb570.com` |
| `HTTP_PROXY` / `http_proxy` | 否 | 公网来源 HTTP 代理 |
| `HTTPS_PROXY` / `https_proxy` | 否 | 公网来源 HTTPS 代理 |
| `NO_PROXY` / `no_proxy` | 否 | 不经代理访问的地址；默认包含 `localhost,127.0.0.1,::1` |
| `GOMEMLIMIT` | 否 | Go 运行时内存目标；容器推荐 `640MiB` |

代理变量应由部署环境提供，切勿写进源码、镜像或文档中的真实凭据示例。代理仅供公网来源使用；飞牛 API 必须直连本机 loopback。`.env` 含敏感信息，应限制读取权限并确认 Git 忽略。会话 token 返回 HTTP 401 时，如果同时配置了账号密码，程序会自动重新登录并重试该请求一次；只配置 token 时需手动提供新 token。

## Docker 部署（飞牛 OS）

1. 从 `.env.example` 创建 `.env`，填写飞牛认证信息和所需代理环境变量；不要提交该文件。
2. 确认 FnOS 数据库目录实际存在。用户当前 NAS 上的已核实路径为 `/usr/local/apps/@appdata/trim.media/database`，其他设备/升级后可能不同。
3. 将数据库**目录**挂载至 `/fnos-db`，并将可写缓存目录持久化到 `/config`。数据库连接为 SQLite `mode=ro`，但 Docker 挂载目录本身不能只读，否则 WAL 模式需要的 `-shm` 锁文件无法处理。
4. 使用 host 网络并把 `FNOS_URL` 设置为 `http://127.0.0.1:5666`；确保 `NO_PROXY` 包含 `localhost,127.0.0.1`。
5. 容器设置 `GOMEMLIMIT=640MiB` 和 `mem_limit: 768m`。Go 内存目标不是 cgroup 硬上限；后者才是容器的硬边界。
6. 先小批量预览，确认人物和待补字段，再使用 `--apply` 实际更新。

数据库挂载示例：

```yaml
volumes:
  - type: bind
    source: /usr/local/apps/@appdata/trim.media/database
    target: /fnos-db
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
- 图片按单张处理，转换为 640×960 JPEG。下载、解码或尺寸失败时跳过当前候选并尝试下一来源；全部失败才跳过头像。如果简介可用仍可写入简介，并继续下一位演员。飞牛 API 登录、读取或保存失败仍应记录错误；监控会保留失败演员以便后续轮询重试。
- Go `GOMEMLIMIT=640MiB` 是运行时内存目标；Compose `mem_limit: 768m` 是容器 cgroup 上限。缓存与并发必须有界，二者都不能替代逐张处理和及时释放大型 buffer。
- 使用 `docker stats fnactor` 监控实际 NAS 内存占用；发生容器 OOM 时，先降低并发并定位未受限的响应体或图像缓冲，再考虑调整容器限制。

## 飞牛 API 与在线来源

当前适配的飞牛影视内部 API 路径：

| 方法与路径 | 用途 |
| --- | --- |
| 所有飞牛 API 请求头 | 携带前端要求的 `X-Trim-Client: web`、`X-Trim-Client-Version: 631`、按路径/请求内容计算的 `authx` 签名；登录后另带原始 `Authorization` token |
| `POST /v/api/v1/user/loginByPassword?channel=v2` | 优先使用的飞牛用户登录接口；发送 `username`、SHA-256 十六进制密码摘要和 `app_name=trimemedia-web` |
| `POST /v/api/v1/login` | 兼容旧版飞牛登录；仅当 v2 接口明确返回 `Invalid Params` 时调用，发送用户名、原始密码和 `app_name=trimemedia-web`，取得会话 token 后继续 API 调用 |
| `POST /v/api/v1/person/search` | 按名称查找 person |
| `POST /v/api/v1/person/getEditDetail` | 读取档案、官方标记和字段锁定信息 |
| `POST /v/api/v1/image/temp/upload` | 上传 multipart 头像，字段为 `file` 和 `image_type=poster`，取得临时图片标识 |
| `POST /v/api/v1/person/saveEditDetail` | 保存中央演员档案 |

接口参数和响应以当前飞牛前端实现为准；它们不是公开稳定 API。程序必须在保存前重新校验档案保护状态。升级后若请求失败，应核对新版前端调用，不要绕过 API 写数据库。

在线资料来源包括 Gfriends、JavDB、Wikipedia 和 Wikidata。JavDB 的演员搜索卡片可返回独立演员头像，详情页主要是作品列表；本项目只使用精确匹配演员别名后的卡片头像，并忽略 `actor_unknow` 占位图。结构化接口使用 Go 标准库 JSON/XML。头像只从允许的 HTTPS 来源下载，通过格式、字节数和像素数检查后再上传。MDC-NG 还使用 Graphis 作为优先图片源；其演员日志显示 Graphis 对部分演员可用、对其他演员无结果。Graphis 条款限制未经书面许可的自动化采集，因此本项目不直接抓取 Graphis；头像覆盖优先使用 Gfriends/JavDB，Wikipedia/Wikidata 作为补充。MDC-NG 公开仓库未包含后端源码，Graphis 的请求实现无法从公开代码核验。

## 故障排查

- **数据库打不开**：确认 `/fnos-db/trimmedia.db` 及可能的 WAL/SHM 文件可见，并检查 Docker 是否错误地把数据库目录设为只读。SQLite 连接自身是只读的，但 WAL 模式需要目录可写以处理 `-shm` 锁文件。
- **数据库 schema 不兼容**：飞牛可能更新了内部数据库结构；确认实际 `person` 列名后更新适配，不要直接猜列或改库。
- **登录失败**：检查 `FNOS_URL`、账户密码/token 和编辑权限；host 网络推荐回环 API 地址，并确认代理绕过列表含 `127.0.0.1`。
- **公网来源超时**：检查容器代理变量、代理可达性和 DNS；避免把飞牛 API 地址放进代理链路。
- **出现 403/429**：暂停任务，等待后调高 `UPSTREAM_DELAY_MS` 再运行。
- **头像失败**：检查来源图片链接、下载大小/格式/像素限制和 FnOS 上传权限。
- **官方/在线人物被跳过**：这是预期保护行为，覆盖选项不会解除保护。
- **内存偏高**：用 `docker stats fnactor` 观测；确认并发不超过 2，图片逐张处理，响应体与缓存都有界。
