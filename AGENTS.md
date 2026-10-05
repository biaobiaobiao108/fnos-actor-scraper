# fnactor 项目协作说明

## 项目用途

`fnactor` 是一个用 TypeScript 和 Bun 编写的命令行程序，以 Docker 一次性运行在飞牛 OS 上。它批量查找飞牛影视中的本地演员档案，从 Gfriends、Minnano-AV、Wikipedia 查询头像和简介，再更新飞牛影视中央演员档案。

演员档案是集中维护的 person 记录，不是每部影片各自维护的演员 NFO。程序默认从飞牛影视 SQLite 数据库的 `person` 表只读枚举本地演员；头像与简介通过飞牛影视 Web API 写回。默认无需挂载电影/剧集目录。

## 架构与文件职责

- `src/index.ts`：CLI、任务来源选择、演员档案保护和跳过规则、来源调度、缓存、预览与写入。
- `src/fnos-db.ts`：使用 Bun 内置 SQLite，以只读模式从 `person` 表读取本地演员列表。只读数据库，绝不通过 SQL 写入。
- `src/fnos.ts`：飞牛影视登录、person 搜索、编辑详情、头像上传及演员资料保存 API。
- `src/nfo.ts`：可选的只读 NFO 演员名扫描器，仅供用户通过 `--root` 指定目录时使用；不允许新增 NFO 写入功能。
- `src/providers/`：Gfriends、Minnano-AV、Wikipedia 在线资料来源。
- `src/image.ts`：验证公网 HTTPS 图片地址，将头像处理为 640×960 JPEG 后交给飞牛上传。
- `src/util.ts`：名称归一化、字符串工具、上游串行限速及重试。
- `/config/actors`：在线演员资料缓存。缓存可删除重建，不等同于飞牛已保存的演员头像。
- `Dockerfile`：`oven/bun:alpine` 多阶段镜像；运行时包含 Bun 和 sharp 生产依赖，入口命令为 `fnactor`。
- `compose.yaml`：飞牛 NAS 的手动运行配置；数据库只读挂载到 `/fnos-db`，缓存持久化到 `/vol1/docker/fnactor-cache`。
- `.github/workflows/publish-image.yml`：推送 `main` 或版本标签时，构建并发布 amd64/arm64 GHCR 镜像。
- `README.md`、`docs/ARCHITECTURE.md`、`docs/INTERFACES.md`：用户部署、使用、API 与实现说明；改行为时须一并维护。

## 关键数据规则

- FnOS 当前没有每个演员独立的 NFO 目录。飞牛影视把演员资料存为数据库 person 记录，图片以哈希路径保存在应用管理的图片目录中。
- 默认批量输入是 FnOS person 表内的本地演员；`--actor` 用于指定单个演员；`--root` 是可选的 NFO 名称来源，不应再作为默认必需挂载。
- 只把 `trim_id` 以 `LOCAL_PERSON_` 开头、无 TMDb/IMDb ID 且非官方的记录视为可更新候选；处理前仍须通过 API 读取编辑详情并再次检查保护标志。
- 不创建 person 记录、不猜测重名。只对唯一精确匹配的演员操作。
- 默认只补缺少的简介/头像；完整资料重复运行时跳过。`--overwrite --apply` 可覆盖本地资料，但不得覆盖官方资料或字段锁定内容。
- 缺省为预览模式。只有显式 `--apply` 才调用 API 保存数据。
- 列举演员时，数据库挂载必须只读；修改演员、头像必须经飞牛 API。不要直接改数据库、WAL、SHM 或哈希图片文件。
- 不要在用户 NAS 上执行真实写入，除非用户明确要求本次写入操作。

## 飞牛路径与部署事实

已于 2026-10-05 只读检查用户的 `flymoo`（系统主机名 `OecT`）：

- FnOS Media 数据库目录：`/usr/local/apps/@appdata/trim.media/database`；主库为 `trimmedia.db`，当时同时有 `trimmedia.db-wal` 和 `trimmedia.db-shm`。容器应把整个数据库目录只读挂载到 `/fnos-db`，不可只挂单个数据库文件。
- 应用图片元数据位于 `/vol1/@appmeta/trim.media/img` 的哈希子目录；程序不挂载此目录，头像通过 API 上传。
- 影视根目录 `/vol1/video/movies` 不存在。当前媒体库目录在 `/vol02/...`、`/vol00/...` 等位置；默认程序无需挂载它们。
- `/vol00`、`/vol02` 下的动态卷名仅适用于当前 NAS，不能作为通用安装假设。
- 旧 iStoreOS 路径 `/mnt/docker_disk` 不适用于飞牛 NAS；当前 Compose 使用 `/vol1/docker/fnactor-cache` 作为缓存目录。

数据库 schema 和私有 Web API 都属于 FnOS 内部实现，可能随系统升级变化。数据库只读查询必须验证 `person` 表需要的列；schema 不符时给出错误，不能尝试写库或猜字段。API 改动需查验飞牛前端实现，文档注明内部 API 的兼容限制。

## 上游限流

- 所有公开资料来源和头像下载都必须使用 `src/util.ts` 的 `fetchUpstream`；不允许 provider 绕过限速直接调用 `fetch()`。
- 全局队列串行下载响应体，默认请求间隔 2000 毫秒。`UPSTREAM_DELAY_MS` 被限制在 500–60000 毫秒。
- 对 429、常见 5xx 和网络失败进行有限退避，尽量遵守 `Retry-After`。收到 403/429 时不要增加激进重试；建议停止批量任务并调高间隔。
- 演员并发默认 1，最大 2；一个演员的多个来源依次请求。不能让提高演员并发绕过上游队列。
- 上游响应默认限制为 16 MiB；头像原图不超过 10 MiB/1600 万像素，输出不超过 4 MiB。sharp 缓存限制 32 MiB、内部并发 1；调整这些限制时同步维护中文文档。
- Compose 容器内存 cgroup 上限为 768 MiB；调高需结合 NAS `docker stats` 的实际峰值。
- 当前 Compose 使用 host 网络，FnOS API 优先通过 NAS loopback `http://127.0.0.1:5666` 连接；不得全局关闭 TLS 校验。
- 登录口令、令牌、Authorization header 不得写入日志或提交。

## 开发与交付规范

- JavaScript / TypeScript 使用 Bun 管理和运行：`bun install --frozen-lockfile`、`bunx tsc --noEmit`、`bun run build`、`bun run fnactor -- --help`。
- 当前没有自动化测试套件。除非用户要求测试，不要新增或运行测试；类型检查、构建与 CLI 帮助可用于验证。
- Compose 默认 `manual` profile，程序不是常驻服务。镜像为 `ghcr.io/biaobiaobiao108/fnos-actor-scraper:latest`。
- `.env` 包含飞牛登录凭据，必须保持 Git 忽略；只提交 `.env.example`。
- 每次完成一轮代码修改后，创建一条中文 Git 提交；提交信息用中文准确概括改动。只有任务要求发布远端或沿用已授权发布流程时才推送。
- 默认使用中文沟通，并确保所有面向用户的文档使用中文。
