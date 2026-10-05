# fnactor

通过在线数据源补全飞牛影视中的**本地演员中央档案**，包括头像和简洁简介。程序用 TypeScript + Bun 编写，支持飞牛 OS Docker、`amd64` 和 `arm64`。

演员档案是集中维护的：一个演员在多部电影中出现时，只需补全飞牛演员中心中的一条档案，不用逐部修改 NFO。

## 安全与更新规则

- 批量扫描只读影视 NFO 收集演员姓名；不会写 NFO、电影文件或飞牛数据库。
- 更新通过飞牛影视 Web API 保存中央演员档案，不挂载数据库或私有图片目录。
- 飞牛自己刮削的官方/在线档案会跳过。程序只对带 `LOCAL_PERSON_` 标识、没有 TMDb/IMDb ID 且非官方的本地演员档案操作。
- 默认只补空头像和简介；完整资料自动跳过。`--overwrite` 可覆盖本地档案的已有字段，但仍跳过官方档案和已锁定字段。
- 默认仅预览，只有传入 `--apply` 才写入。重名、没有匹配档案的演员会跳过，不会自动创建人物。

## 快速部署

1. 在飞牛 Docker 项目中使用本仓库 Compose，或拉取镜像：

   ```text
   ghcr.io/biaobiaobiao108/fnos-actor-scraper:latest
   ```

2. 复制 `.env.example` 为 `.env`，设置 `FNOS_URL`、`FNOS_USERNAME`、`FNOS_PASSWORD` 和 `MEDIA_HOST_PATH`。密码以明文保存在 `.env`，需限制文件权限，不要提交到 Git。也可使用当前有效的 `FNOS_TOKEN`。

3. 将**飞牛影视库实际存放 NFO 的目录**挂载到 `/media:ro`，并把持久化目录挂载到 `/config`。例如：

   ```yaml
   volumes:
     - /vol1/video/movies:/media:ro
     - /vol1/docker/fnos-actor-scraper:/config
   ```

   不要挂飞牛的数据库或私有图片目录。程序不需要媒体卷写权限。若使用 `--actor NAME` 单人模式，则可以不挂载 `/media`。

4. 执行预览，再小批量写入：

   ```sh
   docker compose run --rm fnactor --limit 5
   docker compose run --rm fnactor --limit 5 --apply
   ```

容器使用 host 网络以访问飞牛 Web API；该工具按需运行，不是常驻服务。Compose 默认放在 `manual` profile，不会开机自动跑。

## 用法

```text
fnactor [--actor NAME | --root DIR] [--cache DIR] [--limit N] [--concurrency N] [--refresh] [--apply] [--overwrite]
```

```sh
# 单人预览
docker compose run --rm fnactor --actor '三上悠亚'

# 预览媒体库内前 5 个演员；NFO 只读
docker compose run --rm fnactor --limit 5

# 确认目标后补全飞牛演员档案
docker compose run --rm fnactor --limit 5 --apply

# 刷新来源，并覆盖本地档案已有头像/简介
docker compose run --rm fnactor --actor '三上悠亚' --refresh --overwrite --apply
```

| 参数 | 说明 |
| --- | --- |
| `--actor NAME` | 只处理指定演员；无需 `/media` |
| `--root DIR` | 批量扫描 NFO 根目录，默认 `/media` |
| `--cache DIR` | 缓存目录，默认 `/config` |
| `--limit N` | 限制处理演员数；0 表示不限 |
| `--concurrency N` | 同时处理演员数，默认 1，允许 1–3 |
| `--refresh` | 忽略资料缓存并重新查询来源 |
| `--apply` | 实际写飞牛演员中央档案；缺省为预览 |
| `--overwrite` | 覆盖本地档案已有字段，必须搭配 `--apply` |

环境变量：`FNOS_URL`、`FNOS_USERNAME`、`FNOS_PASSWORD` 或 `FNOS_TOKEN`、`MEDIA_ROOT`、`CACHE_DIR`。建议 `FNOS_URL` 使用 HTTPS 和有效证书。

### 批量速度与上游限流

当前版本支持批量处理 NFO 中收集到的演员。默认 `--concurrency 1`，按演员逐个处理；来源请求由全局队列串行发送，默认请求间隔至少 2 秒（理论上不超过 30 次/分钟，通常更慢，因为还会等待响应）。头像下载也经过同一限速队列。遇到 HTTP 429 或服务端错误会按 `Retry-After`/退避策略重试，最多 3 次尝试。

通常不需要调高并发。`--concurrency 2` 或 `3` 只增加同时处理的演员数，不会并发轰炸来源，因为所有在线资料和头像请求仍受全局队列限制。`UPSTREAM_DELAY_MS` 可设置请求最小间隔，默认 2000 毫秒，代码将其限制在 500–60000 毫秒。建议保持默认值；若来源出现 429/403，应暂停批量任务并把间隔提高到 5000–10000 毫秒，稍后再继续。

## 来源与处理

来源包括 Gfriends、Minnano-AV 和 Wikipedia 中文/日文。多来源并行查询，头像和简介分别取第一个提供该字段的来源。头像会转换为 640×960 的 JPEG 后上传到飞牛。缓存保存在 `/config/actors`；清理缓存不会删除已写入的飞牛档案。

具体 API、保护规则、挂载说明和故障排查见 [接口与使用说明](docs/INTERFACES.md)；处理流程见 [实现与架构](docs/ARCHITECTURE.md)。

## 本地开发

需要 Bun：

```sh
bun install --frozen-lockfile
bun run fnactor -- --help
bun run build
```

## 镜像发布

`.github/workflows/publish-image.yml` 在 main 分支 push、版本 tag 或手动触发时构建 `linux/amd64` 和 `linux/arm64` 并发布到 GHCR。基础镜像使用 `oven/bun:alpine`。
