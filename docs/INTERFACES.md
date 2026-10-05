# 接口与使用说明

## CLI

```text
fnos-actor-scraper [--actor NAME | --root DIR] [--cache DIR] [--limit N] [--refresh] [--apply] [--overwrite]
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--actor NAME` | 无 | 只处理一个演员名，不需要媒体目录挂载 |
| `--root DIR` | `MEDIA_ROOT` 或 `/media` | 批量扫描 NFO 的目录，只读 |
| `--cache DIR` | `CACHE_DIR` 或 `/config` | 在线资料缓存目录，应持久化 |
| `--limit N` | `0` | 最多处理数量；0 表示不限 |
| `--refresh` | 关闭 | 忽略该演员资料缓存并重新抓取在线来源 |
| `--apply` | 关闭 | 实际更新飞牛影视中央演员档案；缺省只预览 |
| `--overwrite` | 关闭 | 配合 `--apply` 覆盖已有头像/简介；官方资料和锁定字段仍受保护 |
| `--help` | — | 显示帮助 |

示例：

```sh
# 单人预览，不需要挂媒体目录
docker compose run --rm fnos-actor-scraper --actor '三上悠亚'

# 首次建议小批量预览
docker compose run --rm fnos-actor-scraper --limit 5

# 确认匹配后写入缺少的字段
docker compose run --rm fnos-actor-scraper --limit 5 --apply

# 强制刷新来源并覆盖本地档案已存在的头像/简介
docker compose run --rm fnos-actor-scraper --actor '三上悠亚' --refresh --overwrite --apply
```

## Docker 部署（飞牛 OS）

### 挂载哪个目录

批量模式需要把**飞牛影视实际媒体库目录**挂载进容器的 `/media`，因为电影/剧集 NFO 中包含演员名称。例如飞牛影视库是 `/vol1/video/movies`，映射为：

```yaml
volumes:
  - /vol1/video/movies:/media:ro
  - /vol1/docker/fnos-actor-scraper:/config
```

若库分散在多个根目录，可以分别挂到 `/media/movies:ro`、`/media/tv:ro`。不要挂载影视应用数据库目录、飞牛私有图片目录或整个系统目录。容器**不需要**媒体目录写权限，也不需要访问 NFO 以外的影片内容。

如果只用 `--actor NAME` 单人处理，完全可以不挂载 `/media`。所有模式都需要把可写持久化目录挂到 `/config`，用于资料缓存。

### 环境配置

复制 `.env.example` 为 `.env`，设置：

```dotenv
FNOS_URL=https://飞牛地址:5667
FNOS_USERNAME=飞牛用户名
FNOS_PASSWORD=飞牛密码
MEDIA_HOST_PATH=/vol1/video/movies
```

密码以明文保存在 `.env`，请限制该文件访问权限并勿提交到 Git。也可配置由飞牛 Web 登录获得的当前 `FNOS_TOKEN`，避免容器保存密码；token 过期后需更新。API 地址建议使用 HTTPS 和有效证书。若飞牛证书为自签名，优先配置受信任证书；程序不会关闭 TLS 校验。

Compose 使用 host 网络，便于容器访问飞牛本机 Web API 和网络。根据实际环境可在 Docker 管理界面配置网络与卷；镜像：

```text
ghcr.io/biaobiaobiao108/fnos-actor-scraper:latest
```

该容器是按需运行的 CLI，不是常驻服务。Compose 配置使用 `manual` profile，避免它随开机自动运行。可使用 `docker compose run --rm fnos-actor-scraper ...` 执行。

### 重要行为

- 程序通过 NFO 收集演员名称，但写入的是飞牛影视的**中央演员档案**。同一个人的头像和简介维护一次后，会显示在引用该演员的多个影片中。
- 飞牛自己在线刮削出的官方/外部资料通过官方标记、TMDb/IMDb ID 及 `trim_id` 保守识别并跳过；不直接修改 SQLite。
- 默认补空字段。资料完整时重复运行会跳过。`--overwrite` 只对本地档案生效，且不会覆盖已锁定字段。
- 程序不会自动新建 person 档案；演员在飞牛影视中不存在或重名时会跳过。请先在飞牛影视生成/确认演员档案，再处理。
- 先用预览确认演员名称与目标档案；再用 `--limit` 小批量 `--apply`。字段锁定时需要先在飞牛影视中解除锁定。

## 环境变量

| 名称 | 必需 | 用途 |
| --- | --- | --- |
| `FNOS_URL` | 是 | FnOS Web 地址，如 `https://192.168.1.10:5667` |
| `FNOS_USERNAME` | 二选一 | 登录用户名 |
| `FNOS_PASSWORD` | 二选一 | 登录密码 |
| `FNOS_TOKEN` | 二选一 | 当前有效 API token；优先于用户名/密码 |
| `MEDIA_ROOT` | 否 | NFO 扫描根目录，默认 `/media` |
| `CACHE_DIR` | 否 | 缓存目录，默认 `/config` |

## 内部 HTTP 接口

当前实现调用飞牛影视 Web API：

| 请求 | 用途 |
| --- | --- |
| `POST /v/api/v1/user/loginByPassword?channel=v2` | SHA-256 密码登录 |
| `POST /v/api/v1/person/search` | 按演员名搜索中央 person 记录 |
| `POST /v/api/v1/person/getEditDetail` | 读取编辑详情及 `is_official`、外部 ID、字段锁定标志 |
| `POST /v/api/v1/image/temp/upload` | 上传处理后的 JPEG 头像，读取 `hash_path` |
| `POST /v/api/v1/person/saveEditDetail` | 保存中央演员档案头像和简介 |

这些是从飞牛影视当前 Web 前端确认的内部接口，非公开稳定 API。兼容性取决于 FnOS 版本；接口异常时请保存报错和版本号。

## 在线来源及缓存

- Gfriends：头像索引，缓存文件树 24 小时。
- Minnano-AV：演员头像与简洁资料字段。
- Wikipedia 中文/日文：头像与摘要简介。
- 合并时优先选第一个提供头像/简介的来源；查询失败会记录警告并继续。
- 演员来源缓存存于 `/config/actors`，包含公开资料 URL 和文本；`--refresh` 重新查询。清除缓存不会删除飞牛头像或资料。

## 故障排查

- `401/未登录`：检查 `FNOS_URL`、账户密码或刷新 `FNOS_TOKEN`；确认用户有飞牛影视编辑权限。
- `找不到同名档案`：检查 NFO `<actor><name>` 与飞牛演员中心中的名称。程序要求精确名称匹配。
- 显示官方/在线资料受保护：这是预期保护行为；官方资料不会由 `--overwrite` 改写。
- 头像上传失败：检查 FnOS API 可达性、账号权限和图片上传限制；程序将图片转换为 2:3 JPEG。
- 字段已锁定：在飞牛影视编辑对应演员并解除头像/简介锁定后再运行。
- 资料来源没有结果：尝试 `--refresh`，检查容器 DNS/HTTPS 出网；未命中的查询结果也会缓存。
