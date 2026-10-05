# 实现与架构

## 目标

`fnactor` 在飞牛 OS 上以 Docker 命令行任务运行，批量为飞牛影视中的本地演员中央档案补充头像和简介。程序按 person 记录工作，因此一个演员被多部影片引用时只维护一份档案。

飞牛影视没有每个演员单独的 NFO 文件夹。用户 NAS 上已只读确认：演员主记录在 `/usr/local/apps/@appdata/trim.media/database/trimmedia.db` 的 `person` 表；电影/演员关联在 `item_person` 表；头像资源由飞牛放入 `/vol1/@appmeta/trim.media/img` 下的哈希目录。程序不需要媒体目录作为默认输入。

## 只读边界和写入边界

- 通过 `src/fnos-db.ts` 以 SQLite `readonly` 模式列举本地演员。Docker 只读挂载整个数据库目录，以便 SQLite 读取同目录中的 WAL/SHM。
- 数据库只负责发现候选人；不执行 SQL 写入，不改动 DB、WAL、SHM、应用图片目录。
- 候选演员的在线资料经公开来源查询；只有通过 `--apply` 后才经 FnOS Web API 保存。
- `--actor` 直接处理指定姓名，不需要数据库挂载。
- `--root` 是可选的 NFO 筛选模式，会只读扫描指定目录收集演员名；不是演员档案存储目录。

## 保护和跳过规则

1. 数据库筛选 `trim_id` 以 `LOCAL_PERSON_` 开头的候选，并排除带 TMDb/IMDb ID 的档案。
2. 每个候选通过 `/person/getEditDetail` 重新读取官方标记和字段锁定状态；只有仍然是本地 person 的记录可继续。
3. 默认只补缺失简介/头像；已有两项都跳过。重复运行自然跳过完成的档案。
4. `--overwrite --apply` 仅覆盖本地且未锁定字段，官方/在线档案始终跳过。
5. 不创建人物、不处理重名猜测。单人或 NFO 模式需精确规范化名称匹配。
6. 缺省是预览；没有 `--apply` 时不会调用上传和保存接口。

## 批量流程

1. 读取数据库 `person` 表，检查必要列存在，筛选本地候选并按姓名排序；`--limit` 限制任务数。
2. 或者通过 `--actor` 指定一个姓名，或通过 `--root` 读取 NFO 姓名作为筛选条件。
3. 登录 FnOS，逐个读取候选编辑详情。演员工作并发默认为 1，最高 3。
4. 对缺少的头像/简介查询 Gfriends、Minnano-AV、Wikipedia；记录缓存到 `/config/actors`。
5. 图片下载后限制大小并裁剪缩放到 640×960（2:3）JPEG。
6. 预览时显示来源和待更新字段；应用模式先调用飞牛头像上传，再通过演员保存接口提交资料。

## 模块职责

- `src/index.ts`：CLI、输入模式选择、演员筛选、保护规则、缓存和并发调度。
- `src/fnos-db.ts`：只读加载本地 person 记录；严格检查当前已知 schema，不匹配就报错。
- `src/fnos.ts`：登录、按名搜索、编辑详情、头像上传和中央演员资料保存。
- `src/nfo.ts`：可选的 XML 演员姓名提取，没有序列化/写文件功能。
- `src/providers/*`：封装在线资料来源。
- `src/image.ts`：校验公网 HTTPS 地址、响应大小及超时，并调用 sharp 处理头像。
- `src/util.ts`：名称归一化、合并辅助函数、所有上游 HTTP 请求的排队/间隔/退避。

所有公开资料和头像下载都必须走共享 `fetchUpstream` 队列。默认间隔 2000 毫秒、请求串行；`UPSTREAM_DELAY_MS` 可调至 500–60000 毫秒。限流/服务端错误使用有限退避；提高演员并发不会绕过上游队列。

## FnOS 兼容性

演员保存、图片上传和登录依赖飞牛影视 Web 前端当前使用的 `/v/api/v1` 内部接口；演员枚举依赖当前 `trimmedia.db` 的 `person` schema。它们不是稳定公开接口。升级 FnOS 后如结构变化，应先查验新前端接口/schema并更新只读适配；不得用猜测字段继续运行，也不得改用 SQL 写入作为后备。

用户名密码只用于登录，密码按飞牛 Web 客户端方式计算 SHA-256；日志中禁止输出口令、token 和授权头。`.env` 不得进入版本库。

## 镜像和发布

构建与运行阶段均使用 `oven/bun:alpine`。构建阶段打包 TypeScript；运行阶段安装生产依赖（包括原生 sharp）。`bin/fnactor` 是容器入口。GitHub Actions 构建 `linux/amd64` 与 `linux/arm64` 镜像并发布到 GHCR。
