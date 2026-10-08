# Mojinest（贴图小窝）

自部署的表情包 / 贴图统一库。散落在 QQ、微信、企业微信、Telegram 里的收藏，
全部收进一个属于你自己的小窝：网页管理、桌面快捷键呼出、手机随时能看能存。

- **后端**：Go（标准库 + pure-Go SQLite，单文件、无 CGO）
- **存储**：本地磁盘，或任意 S3 兼容对象存储（Cloudflare R2 / MinIO / B2 / OSS）
- **前端**：单页 Web（内嵌进二进制，手机浏览器直接可用）
- **AI 标注**：接入任意 OpenAI 兼容视觉模型，上传即自动生成中文标签 + 描述 + 图中文字 OCR
- **鉴权**：单用户 Token（预留多用户与共享库）

## 特性

- 上传即自动标注：标签（情绪/角色/动作/场景/风格）、一句话描述、图中文字 OCR，全部可搜索
- 标签 + 分组 + 收藏 + 回收站 + 使用频率统计（点击即复制）
- 关键词搜索覆盖：文件名、标签、备注、AI 描述、OCR 文字
- 动图支持：GIF / 动画 WebP 原图保留动画，缩略图自动取首帧
- 内容寻址存储：SHA-256 去重，同一张图重复上传直接跳过
- 存储后端可切换：本地盘 ↔ S3/R2，不搬运数据、只改配置
- 限流 + 同源写保护 + HttpOnly Cookie，公网暴露也有基本防线
- 前台复制失败（非 HTTPS 局域网）时自动降级为下载

## 快速开始

```bash
# 编译
make build            # 产出 ./mojinest

# 生成配置（含随机 Token）
./mojinest -init

# 运行
./mojinest -config ~/.config/mojinest/config.json
# 浏览器打开 http://127.0.0.1:18642
```

常用参数：

| 参数 | 说明 |
| --- | --- |
| `-config` | 配置文件路径（默认 `~/.config/mojinest/config.json`） |
| `-data-dir` | 覆盖数据目录（默认 `data/`，内含 `files/`、`thumbs/`、`mojinest.db`） |
| `-listen` | 覆盖监听地址，例如 `:18642` |
| `-storage` | `local` 或 `s3` |
| `-tagger-base-url` / `-tagger-api-key` / `-tagger-model` | 视觉模型接入（OpenAI 兼容） |
| `-init` | 只写配置文件然后退出 |
| `-debug` | 打开 debug 日志 |

### Docker

```bash
cp .env.example .env      # 填 MOJINEST_TOKEN / MOJINEST_S3_* / MOJINEST_TAGGER_*
docker compose up -d --build
```

### Cloudflare R2

```json
{
  "storage_backend": "s3",
  "s3": {
    "endpoint": "https://<accountid>.r2.cloudflarestorage.com",
    "region": "auto",
    "bucket": "mojinest",
    "access_key": "<r2 access key id>",
    "secret_key": "<r2 secret access key>",
    "path_style": true,
    "public_base": ""
  }
}
```

`public_base` 留空时 API 用 1 小时有效的预签名 URL 转发，桶保持私有；
填了公开域名（R2 自定义域 / CDN）则直接 302 到该地址。

## API 摘要

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/auth/login` | `{"token":"..."}` → HttpOnly Cookie |
| GET | `/api/items` | 列表：`q, tags, collection, favorite, trash, animated, platform, sort, limit, offset` |
| POST | `/api/items` | 上传（multipart，`file` 可多份 + `tags/platform/source_hint/collections`） |
| PATCH | `/api/items/{id}` | 改名 / 备注 / 平台 / 收藏 |
| POST | `/api/items/{id}/use` | 记录一次“复制/使用” |
| POST | `/api/items/{id}/trash` · `/restore` | 软删除 / 恢复 |
| DELETE | `/api/items/{id}` | 彻底删除（顺手清理未被引用的 blob） |
| POST/DELETE | `/api/items/{id}/tags[/{name}]` | 手动打标签 / 取消 |
| PUT | `/api/items/{id}/collections` | 覆盖所属分组 |
| POST | `/api/items/batch` | 批量：`trash/restore/delete/favorite/tag/collection` |
| GET/POST | `/api/tags` · `/api/collections` | 标签与分组 |
| GET | `/api/stats` · `/api/jobs` | 统计 / AI 队列状态 |
| POST | `/api/jobs/retry` | 失败的标注重新排队 |
| POST | `/api/trash/empty` | 清空回收站 |
| GET | `/api/files/{sha}?ext=` · `/api/thumbs/{sha}` | 原图 / 缩略图 |
| GET | `/api/export` | 全库清单（桌面客户端与备份用） |

所有接口都接受 `Authorization: Bearer <token>` 或 `?token=`（桌面客户端友好）。

## 路线图

- [x] v0.1 服务端核心：上传去重、标签/分组/搜索、AI 标注流水线、本地 + S3 双存储、Web 库
- [ ] v0.2 桌面客户端：托盘 + 全局快捷键呼出 + 搜索即复制（Tauri 或 Go+WebView）
- [ ] v0.3 导入器：QQNT（PC）、微信 PC 解密提取、Android QQ 收藏目录
- [ ] v0.4 手机端体验：上传/分享目标、iOS 快捷指令、局域网直连
- [ ] v0.5 多用户与共享库、按用户隔离配额
- [ ] v0.6 语义检索（向量）与以图搜图

## 目录结构

```
cmd/mojinest/        入口
internal/api/        HTTP 接口、鉴权、限流
internal/repo/       所有 SQL（SQLite）
internal/store/      blob 存储抽象：local / s3
internal/imageutil/  格式嗅探、元数据、缩略图（pure Go）
internal/tagger/     OpenAI 兼容视觉模型客户端
internal/worker/     AI 标注任务队列
internal/webui/      内嵌前端（dist/index.html）
```

细节设计见 [docs/DESIGN.md](docs/DESIGN.md)。

## License

MIT
