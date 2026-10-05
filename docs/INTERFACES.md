# 接口说明

本项目是一次性 CLI 工具，不启动 HTTP 服务，也没有网络监听端口。外部操作接口是命令行参数、环境变量、NFO sidecar 和在线 provider 请求。

## CLI 接口

```text
fnos-actor-scraper [--root DIR] [--cache DIR] [--actor NAME] [--limit N] [--refresh] [--apply]
```

程序在启动时直接读取 Bun 进程参数。`--root`、`--cache`、`--actor`、`--limit` 后各读取一个参数值；`--apply` 和 `--refresh` 为布尔开关。

| 参数 | 类型 | 默认 | 行为 |
| --- | --- | --- | --- |
| `--root DIR` | 路径 | `MEDIA_ROOT` 或 `/media` | NFO 搜索根目录 |
| `--cache DIR` | 路径 | `CACHE_DIR` 或 `/config` | 缓存根目录 |
| `--actor NAME` | 字符串 | 空 | 过滤唯一规范化名称等于 NAME 的演员 |
| `--limit N` | 非负数 | `0` | 最多查询 N 个演员；`0` 不限 |
| `--refresh` | 开关 | false | 忽略现有演员缓存并强制刷新 Gfriends Filetree |
| `--apply` | 开关 | false | 允许写 NFO；未启用时不会修改媒体目录 |

### 输出

标准输出按演员显示：演员名、命中来源、头像 URL、简介预览和命中的 NFO 数量。没有匹配到资料时显示未找到。错误的 NFO 和 provider 错误会写到标准错误/控制台警告中。

当前未定义稳定的退出码约定或机器可读 JSON 输出；自动化消费请勿依赖格式化的人类可读日志。

## 环境变量

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MEDIA_ROOT` | `/media` | 默认媒体扫描目录；CLI `--root` 优先 |
| `CACHE_DIR` | `/config` | 默认缓存目录；CLI `--cache` 优先 |

Docker 运行需要把 `MEDIA_ROOT` 容器路径映射到媒体卷，把 `CACHE_DIR` 映射到可持久化配置卷。

## 内部 TypeScript 接口

### `ActorProfile`

定义在 `src/types.ts`：

```ts
interface ActorProfile {
  name: string;
  aliases: string[];
  imageUrl?: string;
  biography?: string;
  birthday?: string;
  sourceUrls: string[];
  sourceNames: string[];
}
```

`name` 保留 NFO 输入名称；其他可选字段代表在线来源提供的属性；来源数组记录合并资料的来源。当前 NFO 写入器只消费 `imageUrl` 和 `biography`。

### Provider 函数约定

新增 provider 应返回标准 `ActorProfile` 或无匹配的 `undefined`。请求错误通过 reject 表达，由 `Promise.allSettled` 边界隔离。

```ts
type Provider = (name: string, ...options: unknown[]) => Promise<ActorProfile | undefined>;
```

当前具体导出：

```ts
scrapeGfriends(name: string, cacheDir: string, refresh: boolean)
scrapeMinnano(name: string)
scrapeWikipedia(name: string)
```

如果增加 profile 字段，需检查 `src/index.ts` 的合并逻辑、`ActorProfile` 缓存兼容和 `src/nfo.ts` 写入行为，并更新架构文档。

### NFO 读写格式

识别任意 XML 根节点内的 `<actor>` 子孙节点；每个 actor 的首个 `<name>` 为匹配键。演员通常形如：

```xml
<actor>
  <name>三上悠亚</name>
  <type>Actor</type>
</actor>
```

写入时只在 actor 节点内缺少相应标签时添加：

```xml
<thumb>https://example.invalid/portrait.jpg</thumb>
<profile>人物简介</profile>
```

`<thumb>` 的内容为图片 URL；`<profile>` 的内容为文本简介。程序将文本作为 XML text node 写入并由 serializer 转义。

## 在线 HTTP 接口

| 提供方 | 请求方式/接口 | 解析结果 |
| --- | --- | --- |
| Gfriends | `GET https://cdn.jsdelivr.net/gh/gfriends/gfriends@master/Filetree.json` | 精确查找头像路径；图片引用 `https://raw.githubusercontent.com/gfriends/gfriends/master/<path>` |
| Minnano-AV | `GET https://www.minnano-av.com/search_result.php`，参数 `search_scope=actress`、`search_word=<name>`、`search= Go `；随后 GET 命中 profile URL | 候选艺名、图片、profile 表格字段、profile URL |
| Wikipedia | `GET https://{zh,ja}.wikipedia.org/w/api.php`；`action=query`、`generator=search`、`prop=extracts|pageimages`、`format=json` | 搜索页标题、简介 extract、缩略图 URL |

所有 fetch 请求通过 `src/util.ts` 设置通用 User-Agent 和 10 秒 AbortSignal 超时。HTTP 非 2xx 会作为 provider 错误处理。仅 provider 发起的公开网页/API请求；本项目不使用密钥、账号 Cookie 或用户媒体内容上传。

## 容器接口

- 镜像名：`ghcr.io/<owner>/fnos-actor-scraper:<tag>`。
- 入口：`bun index.js`，Docker `CMD` 参数原样传给 CLI。
- 必需挂载：媒体目录到 `/media`；缓存配置目录到 `/config`。
- 网络：需要在线数据源 HTTPS 可达；iStoreOS 建议 `--network host`。
- 安全默认：媒体目录可挂只读；只有 `--apply` 时需要读写挂载。

## CI 发布接口

`.github/workflows/publish-image.yml` 使用 GitHub Actions `GITHUB_TOKEN` 推送 GHCR，不使用个人访问令牌。发布触发器为默认分支 push、版本 tag push 和手动 workflow dispatch。目标平台为 `linux/amd64,linux/arm64`。
