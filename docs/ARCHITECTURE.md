# 实现与架构

## 目标

`fnactor` 是面向飞牛 OS 的 Go CLI 容器任务，用于为飞牛影视中的本地演员中央档案补充头像和简介。一次命令处理完任务后退出。一个演员被多部影片引用时，对应同一条中央 person 记录，因此只需维护这条档案。

当前用户 NAS 上只读确认：演员主记录位于 `/usr/local/apps/@appdata/trim.media/database/trimmedia.db` 的 `person` 表；影片与演员关系另存于 `item_person`；头像图片由飞牛放在 `/vol1/@appmeta/trim.media/img` 下的内部哈希目录。程序无需直接访问或挂载头像目录。

## 技术组件

- Go CLI 及标准库 `net/http`、`encoding/json`、`encoding/xml`。
- `goquery` 用于解析在线来源 HTML。
- `modernc.org/sqlite` 以只读模式访问 FnOS 数据库，避免依赖 CGO。
- `golang.org/x/image/webp` 与标准 JPEG 图像处理能力用于读取头像；统一转换为 640×960 JPEG。
- FnOS API 客户端经飞牛影视内部 Web API 读取和保存人物档案。
- 来源适配器获取 Gfriends、Minnano-AV、Wikipedia、Wikidata 资料；缓存放入 `/config/actors`。

## 输入和候选人筛选

1. 默认打开 `FNOS_DB_PATH` 指定的 SQLite 文件，缺省为 `/fnos-db/trimmedia.db`，以只读连接读取 `person` 表。
2. 校验必要 schema 后，只选择 `trim_id` 以 `LOCAL_PERSON_` 开头且没有 TMDb/IMDb 标识的本地人物。
3. 用户可用 `--limit` 限制数量；`--actor` 指定单人；`--root` 只读扫描 NFO 中的演员名并筛选任务。
4. `--actor` 单人模式不要求挂载数据库。`--root` 仅是可选名称来源，不是演员档案目录。
5. `--probe --actor NAME` 仅调用公网来源并在内存中处理头像，用于检查代理和上游，不登录飞牛、不写入任何资料。

数据库目录需挂载到 `/fnos-db`。程序使用 SQLite `mode=ro` 连接，并且只执行查询，不会通过 SQL 写入人物资料。不要将 Docker 目录挂载设为只读：飞牛数据库使用 WAL 时，SQLite 需要在目录中处理 `-shm` 共享内存锁文件；Docker 只读挂载会导致 `unable to open database file (14)`。此挂载权限只用于 SQLite WAL/SHM 协调，不代表程序会更新数据库记录。

## 安全写入流程

1. 登录飞牛影视 API，按名称搜索人物。
2. 只接受唯一的规范化精确匹配；找不到或出现重名时跳过。
3. 通过编辑详情接口再次检查官方人物标记、在线来源标识和字段锁定状态。
4. 官方档案、有 TMDb/IMDb 标识、飞牛在线刮削的演员信息一律跳过。
5. 默认仅补缺失头像/简介；两项都齐全则跳过。`--overwrite --apply` 仅覆盖本地且未锁定字段。
6. 缺省只输出预览。显式传入 `--apply` 才上传头像并保存资料；程序不创建人物。

API 读取与保存的校验是第二道边界，不能只依赖本地数据库筛选结果。所有保存均通过飞牛 API 完成，不改数据库、WAL、SHM 或应用图片文件。

## 刮削与请求控制

在线来源按顺序查询。公开来源页面和头像下载统一经过全局请求队列：默认串行，任意两次上游请求至少间隔 2 秒。演员任务并发默认 1、最高 2；多个演员的工作并发不能绕过全局请求限速。

上游响应最大 16 MiB；超限时应边读边计数并中止，不应先完整缓冲再丢弃。遇到临时网络错误和常见 5xx 可有限重试，并遵从 `Retry-After`。遇到 403/429 应停止或降低速率，不得无限重试。

FnOS API 走容器 host 网络访问 NAS 本机 `http://127.0.0.1:5666`。公网来源使用 `HTTP_PROXY`、`HTTPS_PROXY`；配置 `NO_PROXY=localhost,127.0.0.1`，确保回环 API 不经过代理。代理凭据只能存在部署环境的秘密配置中。

## 图片和内存管理

- 头像下载上限 10 MiB；读取时限制流量，设置网络超时，并验证媒体类型和解码结果。
- 解码前限制图像最多 16,000,000 像素，阻止异常尺寸图像占用过多内存。
- 处理后将图像缩放/裁剪为 640×960 JPEG，输出不超过 4 MiB，再交给飞牛 API 上传。
- 图片按单张顺序处理。不要同时保留大量原图、解码后像素数组和上传副本；处理后及时释放引用。
- Gfriends 文件树等较大的来源索引应每个进程只加载和解析一次，并共享结果；对磁盘缓存和内存缓存设定清晰生命周期。
- 演员并发默认 1，最大 2。调高并发前必须核对响应体与图片临时内存，不应引入无界队列。
- 设置 `GOMEMLIMIT=640MiB` 控制 Go GC 的内存目标；Compose 设置 `mem_limit: 768m` 作为进程 cgroup 硬限制。Go 运行时目标不是硬上限，容器仍可能因 cgroup 限制而被终止。
- 使用 `docker stats fnactor` 观察 NAS 实际峰值。新增大缓存、改变图片尺寸或并发限制时，必须同步评估内存并更新部署说明。

## 数据持久化与日志

来源结果缓存放在 `/config/actors`，可删除重建；它与飞牛已保存的人物资料无关。命中缓存时减少上游请求，`--refresh` 仅在用户需要更新来源资料时使用。

日志应给出处理人数、跳过原因和错误类别，但不得输出密码、token、Authorization header 或代理 URL 中的用户名/密码。远端来源应使用 HTTPS；仅允许 NAS 本机 API 使用 HTTP。不能全局关闭 TLS 校验。

## FnOS 兼容性

登录、人物查询、编辑详情、头像上传和保存依赖飞牛影视前端调用的 `/v/api/v1` 内部 API；请求需携带前端使用的 `X-Trim-Client`、`X-Trim-Client-Version` 和 `Authorization` 头。候选读取依赖 `trimmedia.db` 的 `person` schema。它们都不是稳定公开接口。升级飞牛后若接口或 schema 变化，应验证新版实现并更新适配；不兼容时明确报错停止，不允许猜字段继续运行或直接写 SQL 作为后备。

## 部署形态

容器镜像应提供 `fnactor` 命令入口，按需通过 Compose 手动启动，不作为常驻服务。默认挂载只包含 FnOS 数据库目录（只读）与持久缓存目录（可写）；不挂载飞牛图片目录。选用 `--root` 时才额外挂载实际存在的 NFO 媒体目录且保持只读。
