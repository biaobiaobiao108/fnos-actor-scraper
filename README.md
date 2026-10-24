# fnactor

`fnactor` 是一个使用 Go 编写、运行在飞牛 OS Docker 上的命令行工具，用于批量为飞牛影视中的本地演员中央档案补充头像和简介。

飞牛影视的演员资料集中保存在应用数据库的 `person` 表中，不是每部影片各自维护的演员 NFO。多部影片引用同一演员时，更新的是共享的中央档案。

## 功能与保护规则

- 默认以只读方式从飞牛影视数据库枚举本地演员；无需挂载电影或剧集目录。
- 数据库只用于读取候选名单。简介和头像只能通过飞牛影视 API 保存，程序不直接修改数据库、WAL/SHM 或飞牛管理的图片目录。
- 仅处理本地人物档案；飞牛官方档案、已有 TMDb/IMDb 标识的在线档案会跳过。
- 头像和简介分别判断，只补各自缺失且未锁定的字段；一项已存在不会妨碍补另一项。两项都无需补充时才跳过；`--overwrite` 只允许覆盖本地档案中未锁定的字段。
- 默认是预览模式。只有显式传入 `--apply` 才会写入飞牛影视。不会创建人物，也不会猜测处理重名。
- 支持 `--actor` 指定单人，也支持通过 `--root` 只读扫描 NFO 并按其中演员名筛选。
- 日文名称支持确认别名和字形候选检索：先查飞牛显示名称和 `original_name`，再查确认别名，最后按简体转繁体、繁体转日文新字体生成候选；Gfriends 使用可重建的 SQLite 名称索引做精确匹配。
- 支持显式后台监控新入库演员；监控首次启动就扫描全库，已有头像/简介按字段跳过，只补缺少项，之后持续处理新演员。
- 头像按 Gfriends、JavDB、Wikidata 顺序准备候选；程序按需查询次级来源（Gfriends 命中头像时跳过 JavDB，仅在仍缺头像或简介时查询 Wikidata），避免请求放大效应。下载、解码或尺寸校验失败会尝试下一候选；全部失败才跳过头像。有可用简介时仍保存简介，并继续处理下一位演员。

### 日文艺名备用检索

部分演员在飞牛资料库中使用简体中文译名，而公开来源使用日文姓名。程序按顺序尝试飞牛显示名称、`original_name`、用户配置的已确认别名、简体转繁体结果和繁体转日文新字体结果；仅生成不同于之前候选的名称。转换只改变汉字字形，不能推导汉字姓名对应的假名读音，因此不会猜测读音或做模糊匹配。

可在缓存根目录创建 `actor-aliases.json`（默认 `/config/actor-aliases.json`）补充确认别名，无需重新编译：

```json
{
  "桥本有菜": ["橋本ありな"],
  "佐仓宁宁": ["佐倉ねね"]
}
```

每个姓名配置 1 至 8 个非空候选名称，整个文件不得超过 1 MiB。程序启动时读取，监控每轮重新读取；格式或内容无效时会报错并停止该轮，不继续写入。只添加你已确认对应同一人物的别名。

Gfriends 的 `Filetree.json` 包含大量艺名与头像路径。程序首次使用时将它缓存到 `/config/gfriends-filetree.json`，并生成 `/config/gfriends-index.db` SQLite 索引；索引记录规范化名称与头像路径，按名称精确查找，并统一大小写、兼容字符及平/片假名。长期进程最多每分钟检查一次文件变化，来源缓存有效期为 24 小时；来源文件或索引版本变化时事务重建，下载、解析、重建失败或零条结果不会覆盖健康索引，并会报告错误供重试。

多目录里的图片按文件名目标姓名分组：取路径 basename，去除查询参数、`.jpg`/`.png` 扩展名和 `AI-Fix-` 前缀，再规范化姓名。规范化名称精确命中后，按完整路径排序保留最多 16 张图片逐张回退；目标姓名不同也继续尝试，并在日志中说明。这个分组依赖受控的文件名证据，不是稳定人物 ID。其他来源仍按候选名称依次查询。日文候选只用于在线来源检索，最终写入目标始终是按飞牛本地档案身份精确确认的原演员。

来源缓存与监控状态都记录 `lookupRevision`，由规则版本和该演员的候选名称摘要组成。旧记录缺少版本、规则升级或该演员候选变化时会重新评估，不必删除整个缓存或状态库。

完整来源资料缓存有效期为 30 天；只有头像或简介的部分资料缓存最多复用 7 天，避免阻挡监控的定期重新评估。

## 部署

镜像地址：

```text
ghcr.io/biaobiaobiao108/fnos-actor-scraper:latest
```

### 飞牛 NAS 部署步骤

本项目是按需运行的 CLI 工具。容器启动后只显示简短用法并保持空闲，不会自行扫描演员；你在容器终端输入 `fnactor ...` 后才会执行任务。也可以用 Docker Compose 启动一次性任务，处理完成后容器退出。先在 NAS 选择部署目录、FnOS 数据库目录和持久缓存目录，并确认已安装 Docker/Compose 且能访问 GHCR。

1. SSH 登录飞牛 NAS，准备部署目录和持久化缓存目录：

   ```sh
   mkdir -p ./fnactor-deploy ./fnactor-cache
   cd ./fnactor-deploy
   ```

2. 获取项目文件。NAS 安装了 Git 时可直接克隆：

   ```sh
   git clone https://github.com/biaobiaobiao108/fnos-actor-scraper.git .
   ```

   如果目录中已经有项目文件，更新时在该目录执行 `git pull`。也可以从 GitHub 下载源码压缩包，解压后确保 `compose.yaml`、`.env.example` 等文件位于此目录。

3. 创建实际使用的环境变量文件：

   ```sh
   cp .env.example .env
   chmod 600 .env
   vi .env
   ```

   按下表填写 `.env`。可以只用用户名密码登录，也可以使用 `FNOS_TOKEN`；设置 token 时程序优先使用它。长期监控建议同时配置用户名密码，token 过期后程序才能自动重新登录。不要把包含真实凭据的 `.env` 上传到 GitHub。

   | 变量 | 必填 | 用途与配置建议 |
   | --- | --- | --- |
   | `FNOS_URL` | 是 | 飞牛影视 API 地址。使用 `network_mode: host` 时通常设为 `http://127.0.0.1:5666`，即 NAS 本机回环地址。 |
   | `FNOS_DB_DIR` | 是 | 主机上包含 `trimmedia.db`、WAL 和 SHM 文件的飞牛数据库目录。 |
   | `CACHE_HOST_DIR` | 是 | 主机上用于持久化缓存和监控状态的可写目录。 |
   | `FNOS_USERNAME` | 使用账号密码时必填 | 有权限编辑飞牛影视演员资料的飞牛账号。 |
   | `FNOS_PASSWORD` | 使用账号密码时必填 | 上述账号密码。只保存在 NAS 的 `.env` 中。 |
   | `FNOS_TOKEN` | 可选 | 当前有效的飞牛 API 会话 token。程序设置此项时优先用 token；若收到 HTTP 401，配置了用户名密码时会自动重新登录并重试一次。未设置时则用账号密码登录；只提供 token 时过期需手动更新。 |
   | `UPSTREAM_DELAY_MS` | 否 | 公网上游请求最小间隔，默认 `2000` 毫秒。遇到 403/429 时调大，避免频繁请求。 |
   | `JAVDB_BASE_URL` | 否 | JavDB 可访问镜像的 HTTPS 基础地址，默认 `https://javdb570.com`。只接受公网 HTTPS 地址；镜像更换时在 `.env` 中调整。 |
   | `GOMEMLIMIT` | 否 | Go 运行时内存目标，默认 `128MiB`；Compose 容器硬限制是 `768m`。 |
   | `GOGC` | 否 | Go 垃圾回收目标，默认 `75`。一般保持默认即可。 |
   | `http_proxy`、`https_proxy` | 视网络而定 | 公网资料来源使用的 HTTP/HTTPS 代理。没有代理时留空；需要认证时可填 `http://用户名:密码@代理主机:端口`，特殊字符需进行 URL 编码。 |
   | `no_proxy` | 否 | 公网上游请求的代理绕过列表。默认包含 `localhost,127.0.0.1,::1`；FnOS API 客户端始终直连，不依赖此设置。 |

   `FNOS_DB_PATH` 和 `CACHE_DIR` 已由 Compose 分别设置为 `/fnos-db/trimmedia.db`、`/config`，通常不需要放进 `.env`。主机目录由 `FNOS_DB_DIR` 和 `CACHE_HOST_DIR` 配置。代理变量的大小写形式由 Compose 一并传入容器。

4. 核对 `.env` 中配置的飞牛影视数据库路径。Compose 将该数据库目录挂载为：

   ```text
   ${FNOS_DB_DIR}:/fnos-db
   ```

   不同飞牛安装的数据库目录可能不同。确认你填写的路径包含数据库文件：

   ```sh
   ls -la /path/to/fnos/database
   ```

   应能看到 `trimmedia.db`。将示例路径替换为 `.env` 中的 `FNOS_DB_DIR`。它应指向包含数据库文件及其 WAL/SHM 的整个目录，不要只挂载单个数据库文件。这个挂载不要设置 `read_only: true`：SQLite 在 `mode=ro` 查询数据库内容时，仍需在 WAL 模式下处理 `-shm` 锁文件。程序自身以只读模式连接数据库，只执行查询，不会更新演员表。`CACHE_HOST_DIR` 指向可写的持久化目录；按上述目录布局可填写 `../fnactor-cache`。

5. 拉取镜像并先做预览。向 `main` 推送代码只运行 CI；推送版本 tag（如 `v0.0.9`）才会构建并发布 `latest`、版本号和提交 SHA 镜像。更新部署时先拉取最新镜像：

   ```sh
   docker compose pull fnactor
   docker compose run --rm fnactor --limit 20
   ```

   如果希望在飞牛 Docker 管理界面保留一个空闲容器，启动后再手动输入命令：

   ```sh
   docker compose --profile manual up -d fnactor
   docker exec -it fnactor sh
   fnactor --limit 20
   ```

   也可以在 Docker 管理界面打开 `fnactor` 的终端，直接运行 `fnactor --limit 20`。容器启动日志只显示用法提示；默认不扫描、不刮削。批量写入必须显式运行带 `--apply` 的命令。

   首次运行建议先用 `--limit 20` 检查候选与计划。命令输出只预览，不会写入档案。单人在线来源诊断可用 `--probe`，例如：

   ```sh
   docker compose run --rm fnactor --actor '三上悠亚' --probe
   ```

   `--probe` 不需要飞牛账号，也不会更改飞牛影视资料。

6. 确认输出无误后再执行写入。建议先对单个演员验证，再扩大范围：

   ```sh
   docker compose run --rm fnactor --actor '三上悠亚' --apply
   docker compose run --rm fnactor --limit 20 --apply
   ```

   `--apply` 才会通过飞牛影视 API 保存信息。覆盖已有且未锁定的本地字段时还需显式加 `--overwrite`；在线档案等受保护记录不会因此被覆盖。

### 后台监控新演员

完成记录若仍缺头像或简介，每 7 天重新评估一次；已完整的演员不做定期重新刮削。重新评估仍通过 API 检查身份和字段锁定，只补可写缺失字段。旧状态缺少 `lookupRevision` 时会自动重新评估一次，后续规则或该演员候选变化也会触发重新评估。

需要持续自动刮削时，显式启动单独的监控服务。首次运行立即扫描整个本地演员库；已有头像和简介分别跳过，只补缺失且未锁定的字段。首轮完成后继续轮询新入库演员。监控状态保存在 `/config/fnactor-state.db`，来源缓存仍保存在 `/config/actors` 和 Gfriends JSON 文件中，两者都由可写的 `/config` 持久化卷保存。旧版 `watch-state.json` 不导入；首次使用 SQLite 状态库时会重新扫描全库，已有资料仍逐字段跳过。失败任务会在 SQLite 中记录次数、错误和下次重试时间，重试间隔逐步延长，最长 24 小时；到期重试会提前唤醒监控，不必等完整轮询周期。遇到上游 403/429 时，本轮停止并将当前演员记为待重试。监控模式必须显式带 `--apply`，因此启动监控服务代表授权它自动写入缺失资料。

```sh
# 启动后台监控服务（开启后会随 Docker 重启恢复）
docker compose --profile watch up -d fnactor-watch

# 查看日志
docker logs -f fnactor-watch

# 停止监控
docker compose --profile watch stop fnactor-watch
```

首次启用后会在 Docker 日志中显示全库扫描开始、逐位演员的刮削结果和后续监控信息。终端使用 `[SCAN]`、`[ACTOR]`、`[OK]`、`[SKIP]`、`[WARN]`、`[RETRY]` 等纯文本标记区分状态，并在演员区块和监控轮次之间留空行；空轮次使用 `=^._.^=` 小猫标记。watch 每轮静默忽略纯数字名称；没有新增或到达重试时间的演员时，输出“=^._.^= [IDLE] 本轮检查完成，没有需要处理的新增或待重试演员。”监控服务以前台方式运行，程序写入标准输出/错误输出，Docker 会收集到该容器的“运行日志”；Compose 为此服务配置 `json-file` 日志驱动，并保留最近 3 个、每个最多 10 MiB 的日志文件。若要重置监控记录，先停止 `fnactor-watch`，再删除 `/config/fnactor-state.db` 及其 `-wal`、`-shm` 文件；下次启动会重新扫描全库，但已完整的字段仍会跳过。普通 `fnactor` 服务仍为空闲模式，不会因更新而自动开始刮削。

### Compose 挂载与网络说明

默认批量模式通过 SQLite 只读连接从飞牛影视 `person` 数据库表枚举本地演员，不需要媒体目录，也不需要挂载飞牛私有图片目录。只有使用 `--root` 扫描 NFO 筛选任务时，才需在 `compose.yaml` 的 `volumes` 增加媒体目录只读 bind mount，并把容器内路径传给 `--root`。请使用实际包含 NFO 文件的媒体目录。

Compose 使用 `network_mode: host`，这样容器可访问 NAS 本机的 `http://127.0.0.1:5666` API。FnOS API 客户端禁用代理并直连；公网抓取请求可通过 `http_proxy`、`https_proxy` 走代理，`no_proxy` 仅控制这些公网请求是否绕过代理。

`.env` 内的口令和令牌是敏感信息，应限制文件权限并确保不提交到 Git。

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
| `--root DIR` | 只读扫描目录中的 NFO，并按演员名筛选任务；根路径不是目录或不可读时报错，候选按名称排序后应用 `--limit` |
| `--cache DIR` | 在线资料缓存目录；默认取 `CACHE_DIR` 或 `/config` |
| `--limit N` | 本次最多处理人数；`0` 表示不限 |
| `--concurrency N` | 演员任务并发数；默认 1，最高 2 |
| `--refresh` | 忽略在线资料缓存并重新查询来源 |
| `--probe` | 仅实测来源抓取和头像处理，不登录或写入飞牛；需要 `--actor` |
| `--apply` | 实际通过飞牛 API 保存资料；缺省仅预览 |
| `--overwrite` | 覆盖本地档案中已有且未锁定的字段；必须搭配 `--apply` |
| `--watch` | 持续监控演员库；首次扫描全库并只补缺失字段，需同时使用 `--apply`，不能和单次筛选参数组合 |
| `--watch-interval` | 监控轮询周期；默认 `1m`，范围 `10s` 到 `24h` |
| `--help` | 显示命令帮助 |

## 限流与内存

程序按顺序访问在线资料来源，并让所有公网来源请求和头像下载共用串行队列，默认请求间隔至少 2 秒。演员任务并发默认为 1，最高为 2。上游响应体限制为 16 MiB；头像下载限制为 10 MiB、1600 万像素，处理后的 JPEG 限制为 4 MiB。

容器建议配置 `mem_limit: 768m`，Go 运行时使用 `GOMEMLIMIT=128MiB`。Go 运行时内存软上限不等于进程的绝对硬限制；容器 cgroup 限制仍是最终边界。可通过 `docker stats fnactor` 观察真实任务峰值。批量读取演员库时只保留身份字段和头像/简介是否已有内容，不将完整简介留在任务列表；头像按单张下载、校验、解码和转换，最多 1600 万像素。

429、常见 5xx 和临时网络问题最多进行两次重试（共三次请求），并遵守 `Retry-After`，等待时间最多 60 秒。最终收到 403/429 后，单次批处理停止后续演员；watch 则记录当前演员的重试状态并结束本轮，到达重试时间后再继续。错误响应体最多读取 512 字节后丢弃，不写入日志；日志中的上游 URL 会移除查询参数。恢复后可将 `UPSTREAM_DELAY_MS` 调高后再继续。

上游只允许无用户信息的 HTTPS 公网地址，DNS 解析结果会拒绝私网、保留及特殊用途 IP；直连时固定到已校验的公网 IP。配置代理时由代理连接外部目标，因此应使用可信代理。FnOS API 客户端禁用代理并直连，`NO_PROXY` 不影响飞牛 API。

来源资料缓存单项超过 1 MiB 时仍可用于当前处理，但跳过磁盘缓存写入。Gfriends 文件树解析限制嵌套深度为 256 层；超限会使本次索引重建失败并保留原有健康索引。

## 实现概览

- Go CLI 默认单次运行，另有必须显式启用的持续监控模式；Docker 默认启动仍为空闲，不会自动刮削。
- 使用标准库 `net/http`、`encoding/json`、`encoding/xml` 访问结构化资料来源。
- 使用 `modernc.org/sqlite` 只读访问飞牛影视 `person` 表，并将监控状态保存在独立的 `/config/fnactor-state.db` 数据库中。
- 使用 `golang.org/x/image/webp` 和标准库 JPEG 解码器处理头像，转换为 640×960 JPEG 后经飞牛 API 上传。
- 头像按单张处理；下载/解码/尺寸失败会回退到下一来源，全部失败才跳过头像，并保留可写入的简介，不会终止整个批次。监控模式把处理状态、重试次数和下次重试时间保存在 `/config/fnactor-state.db`，首次扫描全库、之后按数据库轮询新记录。
- 在线来源包括 Gfriends、JavDB 和 Wikidata。头像按 Gfriends、JavDB 演员搜索卡片、Wikidata 的优先级逐个尝试，当前候选处理失败会回退到下一来源；简介采用 Wikidata。JavDB 只按精确演员名/别名匹配，不采信占位头像；它不提供可靠简介。缓存存放在 `/config`。
- 飞牛 API 是其 Web 前端使用的内部接口，飞牛版本升级时可能变化；请求会附带前端客户端标识和签名，版本变化时需对照 NAS 前端资源核验。

详细架构、接口、部署配置与故障排查见[实现与架构](docs/ARCHITECTURE.md)和[接口与使用说明](docs/INTERFACES.md)。项目协作规范见 [AGENTS.md](AGENTS.md)。

## 本地开发

需要 Go 工具链。常用开发命令：

```sh
go mod download
go build ./...
go vet ./...
go run . --help
```

不要在提交内容中包含 `.env`、登录口令、token 或代理凭据。GitHub Actions 在 `main` 的 push 和 pull request 上运行 CI；推送版本 tag 时构建并发布多架构容器镜像。
