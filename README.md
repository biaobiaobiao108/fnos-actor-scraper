# 飞牛影视演员资料刮削器

使用 TypeScript + Bun 编写的命令行工具。它扫描飞牛影视媒体目录中的 NFO 文件，提取演员名称，在线查找演员头像和简介，先输出预览；用户显式传入 `--apply` 后，才会把结果补入 NFO。

工具目前针对飞牛“其他视频”媒体库读取的本地 NFO sidecar。它不连接飞牛数据库、不提供 Web 服务，也不下载或修改影片文件。飞牛官方文档说明“其他视频”媒体库读取本地 NFO；不同版本是否导入演员节点中的 `<thumb>` 和 `<profile>`，应在目标版本上重新扫描后确认。

## 功能

- 递归扫描目录中的 `.nfo` 文件，读取 `<actor><name>…</name></actor>` 演员节点。
- 对演员名进行 Unicode NFKC、大小写、空格及标点归一化，跨影片合并同名演员。
- 并行查询 Gfriends、Minnano-AV、中文 Wikipedia 和日文 Wikipedia；单个来源失败不会阻断其他来源。
- 合并可用头像、简介、别名和来源链接，并把结果缓存到配置目录。
- 默认只打印结果，不写媒体目录。
- `--apply` 模式只补充缺少的演员 `<thumb>` 和 `<profile>` 标签，不覆盖已存在标签；写入前为每个 NFO 建立 `.actor-scraper.bak` 备份。
- 支持只处理指定演员、限制演员数量、刷新在线索引缓存。
- 可作为一次性 Docker 容器运行；Compose 服务配置在 `manual` profile 下，不会随 Compose 默认启动。

## 在线数据来源

| 来源 | 查询内容 | 实现方式 |
| --- | --- | --- |
| Gfriends | 演员头像 | 下载并缓存 Gfriends `Filetree.json`，以演员名精确匹配头像文件；图片通过 raw GitHub URL 引用 |
| Minnano-AV | 日文艺名、别名、公开资料字段、头像 | 请求女优搜索页，按候选名称相似度筛选后解析资料页 |
| 中文 Wikipedia | 简介、页面缩略图 | 调用 MediaWiki `w/api.php` 搜索接口 |
| 日文 Wikipedia | 简介、页面缩略图 | 中文 Wikipedia 未命中时调用日文 MediaWiki 搜索接口 |

来源查询失败或没有可靠匹配时，该来源不参与合并。程序不会凭空生成资料；结果仍需使用者检查，尤其注意同名或艺名变体。

## 运行要求

- Bun（本机开发）或 Docker（软路由部署）。
- 可访问上述数据来源的 HTTPS 网络。当前 iStoreOS 环境下容器需要使用 host 网络：宿主机可以访问在线站点，而默认 Docker bridge 网络请求会超时。
- 媒体目录至少可读；应用 NFO 修改时需要可写。
- 建议先在小目录或单个演员上预览，再使用 `--apply`。

## 本机开发

```sh
bun install --frozen-lockfile
bun run start -- --root "媒体目录" --limit 5
```

单独查询演员：

```sh
bun run start -- --root "媒体目录" --actor "三上悠亚"
```

确认预览后应用到 NFO：

```sh
bun run start -- --root "媒体目录" --actor "三上悠亚" --apply
```

生成可直接运行的单文件 bundle：

```sh
bun run build
```

## 命令行接口

```text
bun run start -- [选项]
docker run --rm [挂载选项] ghcr.io/<所有者>/fnos-actor-scraper:latest [选项]
```

| 选项 | 默认值 | 说明 |
| --- | --- | --- |
| `--root <目录>` | `MEDIA_ROOT`，否则 `/media` | 扫描 NFO 的根目录；递归扫描，不跟随符号链接 |
| `--cache <目录>` | `CACHE_DIR`，否则 `/config` | 在线索引与演员资料的缓存位置 |
| `--actor <名称>` | 无 | 只处理归一化名称完全相同的演员 |
| `--limit <数量>` | `0` | 限制本次处理的演员数；`0` 表示不限制 |
| `--refresh` | 关闭 | 忽略现有演员资料缓存并重新查询；同时强制刷新 Gfriends 索引 |
| `--apply` | 关闭 | 将查到的头像和简介写入匹配的 NFO；不传则为预览模式 |

`--actor` 过滤发生在扫描所有 NFO 并建立演员列表之后；名称会忽略大小写、空格和标点差异。当前没有独立的 HTTP API，也没有 `--help` 接口；本节是 CLI 参数的完整说明。

## Docker 使用

官方 Bun Alpine 镜像在构建和运行阶段均使用 `oven/bun:alpine`。Dockerfile 先在构建阶段安装锁定依赖并生成 bundle，再从同一 Bun Alpine 基础镜像构建精简运行镜像。镜像同时支持 `linux/amd64` 和 `linux/arm64`。

### iStoreOS / 软路由预览

将 `<所有者>` 换为 GitHub 仓库所有者。媒体挂载为只读，缓存单独持久化：

```sh
docker run --rm --network host \
  -v /mnt/CloudNAS/115open:/media:ro \
  -v /mnt/docker_disk/fnos-actor-scraper:/config \
  ghcr.io/<所有者>/fnos-actor-scraper:latest \
  --root "/media/整理好的学习资料/No.1Style" --actor "三上悠亚"
```

`--network host` 是为了让容器使用 iStoreOS 宿主机可用的外网连接。确认预览结果后，使用读写媒体挂载并显式加 `--apply`：

```sh
docker run --rm --network host \
  -v /mnt/CloudNAS/115open:/media:rw \
  -v /mnt/docker_disk/fnos-actor-scraper:/config \
  ghcr.io/<所有者>/fnos-actor-scraper:latest \
  --root "/media/整理好的学习资料/No.1Style" --actor "三上悠亚" --apply
```

工具修改 NFO 前会在旁边创建 `<文件名>.nfo.actor-scraper.bak`。应用后，在飞牛影视中重新扫描媒体库。若目标 FnOS 版本没有从 NFO 导入演员头像或简介，这些标签仍会留在 sidecar 中，但不会自动改写飞牛数据库。

### Docker Compose

仓库提供 `compose.yaml` 示例，挂载路径按用户设备调整。服务位于 `manual` profile，不会自动启动；`network_mode: host` 与软路由外网访问要求一致。

```sh
docker compose --profile manual run --rm fnos-actor-scraper --actor "三上悠亚" --limit 1
```

默认 compose 媒体卷是只读的，适合预览。执行写入时需将该卷从 `:ro` 改成 `:rw`，并在参数末尾添加 `--apply`。

## 缓存与文件布局

```text
<CACHE_DIR>/
├── gfriends-filetree.json       # Gfriends 索引，默认 24 小时有效
└── actors/
    └── <sha256(演员原始名称)>.json # 单个演员合并后的资料
```

演员资料缓存会持续复用，除非传入 `--refresh`。Gfriends 索引默认缓存 24 小时。源站返回图片 URL，工具不下载图片文件。

## NFO 写入规则

给演员节点新增标准标签：

```xml
<actor>
  <name>三上悠亚</name>
  <type>Actor</type>
  <thumb>https://…</thumb>
  <profile>在线资料简介</profile>
</actor>
```

- 已存在 `<thumb>` 时不替换头像，即使为空标签也视为已存在。
- 已存在 `<profile>` 时不替换简介。
- 资料源没有给出对应字段时不添加空标签。
- 每个 NFO 首次修改前复制一份备份；如果备份文件已存在，不覆盖它。
- NFO 使用 XML 解析和序列化；写回格式可能与原始空格、缩进或声明不同，但 XML 节点和内容会保留。

## GitHub Actions 与镜像发布

`.github/workflows/publish-image.yml` 会在推送到默认分支、推送版本 tag（如 `v0.1.0`）或手动触发时构建并发布多架构镜像到 GitHub Container Registry（GHCR）。镜像标签包括 `latest`（默认分支）、分支名、Git SHA、语义版本 tag。

发布到 `ghcr.io/<所有者>/fnos-actor-scraper`。第一次发布后，如果 GHCR 包可见性为私有，需要在 GitHub 的 Packages 设置中将包改为 Public，或登录 GHCR 后再从设备拉取私有镜像。

## 项目结构

```text
src/
├── index.ts                 # CLI 参数、NFO 遍历、提供方调度、合并、缓存、写入
├── nfo.ts                   # NFO XML 解析、演员节点读取和补写
├── types.ts                 # ActorProfile 与 ScrapeOptions 数据类型
├── util.ts                  # 名称归一化、相似度、HTTP 与文本工具
└── providers/
    ├── gfriends.ts          # 头像索引和图片 URL
    ├── minnano.ts           # Minnano-AV 搜索及资料页
    └── wikipedia.ts         # 中文、日文 MediaWiki 搜索接口
docs/
├── ARCHITECTURE.md          # 模块职责、处理流程、失败边界
└── INTERFACES.md            # CLI、环境变量、内部类型、NFO 与数据源接口
```

## 已知限制

- 在线站点结构、限流策略或网络可达性改变时，provider 可能返回空结果或报错。
- 演员名匹配以 NFO 中名称为入口；不同语言、艺名、别名之间不能保证自动识别。
- Gfriends 使用精确名称查找头像；Wikipedia 使用搜索结果名称相似度筛选。
- 写入的是远程头像链接，不保证飞牛可访问源站或会自动缓存图片。
- 飞牛不同版本对 NFO 演员子标签的兼容性需要实际媒体库重新扫描验证。

## 开发约定

```sh
bun install --frozen-lockfile
bun run build
bunx tsc --noEmit
```

在线数据源及页面结构以各项目/站点当前实现为准。若扩展或替换 provider，请同步更新 `docs/INTERFACES.md` 和本 README 的数据来源表。
