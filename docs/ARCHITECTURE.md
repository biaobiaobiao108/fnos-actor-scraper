# 实现与架构

## 处理流程

1. 从 CLI 参数或环境变量确定媒体根目录与缓存目录。
2. 递归查找 `.nfo` 文件；跳过符号链接。
3. 使用 XML parser 解析每个 NFO 的 `<actor>` 节点，读取首个 `<name>`。
4. 通过 Unicode NFKC、转小写和去空格/标点归一化名称，并聚合演员出现次数及对应 NFO 路径。
5. 为每个选中演员读取本地资料缓存。无缓存或 `--refresh` 时并行执行 Gfriends、Minnano-AV、Wikipedia provider。
6. 合并成功来源中的别名、图片、简介及来源信息；当前字段优先顺序由 provider 调用顺序决定：Gfriends 的图片优先，其次 Minnano-AV、Wikipedia；有简介时优先 Minnano-AV，其次 Wikipedia。
7. 默认仅打印预览。`--apply` 时逐个 NFO 补充缺少的 `<thumb>` 和 `<profile>`，写入前复制旁路备份。

## 模块职责

### `src/index.ts`

CLI 入口和流程编排。负责参数读取、目录遍历、actor 去重、缓存读写、并行 provider 调度、字段合并、预览输出以及 NFO 更新。外部网络和文件系统错误按 actor/provider 级别处理；损坏 NFO 会被跳过。

### `src/nfo.ts`

基于 `@xmldom/xmldom` 解析和序列化 XML。`parseActors` 抽取演员元素和现有标签状态；`updateActor` 只新增缺失标签；`serializeNfo` 生成 XML 文本。

### `src/providers/*`

provider 将在线服务封装为 `Promise<ActorProfile | undefined>`。没有符合匹配阈值的候选时返回 `undefined`；请求失败时 reject，由调度器单独记录，不影响其他 provider。

### `src/util.ts`

提供名称归一化、字符相似度、字符串去重、文本清理，以及统一的带 10 秒请求超时的 HTTP helper。

## Provider 行为

### Gfriends

从 jsDelivr 获取 Gfriends `Filetree.json`。成功下载后写入 `CACHE_DIR/gfriends-filetree.json`；有效期 24 小时。遍历索引，跳过 `Information` 元数据分支，以归一化文件名精确匹配演员名。图片 URL 使用 `raw.githubusercontent.com`。目前不模糊匹配，不读取 Gfriends 的其他演员资料字段。

### Minnano-AV

构造女优搜索请求，解析 HTML 中的 `actressNNNN.html` 候选链接。候选标签/上下文与输入名通过 `nameSimilarity` 比较；长度不少于 4 个字符时阈值为 0.7，否则要求完全匹配。通过后打开资料页，提取头像、链接别名以及 profile 表格中的公开资料字段，合并成简介字符串。

### Wikipedia

依次请求中文和日文 MediaWiki API `action=query&generator=search`，每种语言最多取 5 个页面并获取 intro extract 与 thumbnail。使用相同名称相似度逻辑选最高分页面；得到简介或图片之一即视为命中。中文命中后不再继续查询日文版。

## 缓存设计

- Gfriends 文件树：固定文件名，依据文件修改时间判断 24 小时过期。
- 演员资料：以原始演员名称 UTF-8 内容的 SHA-256 作为文件名，存于 `CACHE_DIR/actors/<hash>.json`。
- 演员资料缓存没有自动过期策略；`--refresh` 忽略演员缓存并重查，同时强制刷新 Gfriends 索引。
- 缓存是可重建数据，可以安全删除；删除不会影响媒体 NFO。

## Docker 镜像

Dockerfile 使用两个 `oven/bun:alpine` 阶段：

1. Build 阶段通过锁文件安装依赖，`bun build --target=bun` 将源码及运行依赖打包为 `dist/index.js`。
2. Runtime 阶段只复制 bundle 并使用 Bun 执行入口。源码、node_modules、编译工具链不进入最终镜像。

workflow 使用 Buildx 输出 `linux/amd64` 和 `linux/arm64` manifest 到 GHCR。iStoreOS 当前运行时使用 Docker host 网络，以沿用宿主机的外网通路。

## 写入边界

- 只处理解析成功的 `.nfo` 文件。
- 不处理文件名以外的媒体内容、不读取或上传媒体文件。
- 仅为已命中的演员节点补空缺标签；已有 `<thumb>`、`<profile>` 不覆盖。
- 备份只在不存在时创建，避免覆盖首次备份。
- XML serializer 可能规范化格式；写入前应先预览，重要资料建议另行备份媒体目录。
