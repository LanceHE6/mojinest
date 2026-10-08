# Mojinest 设计说明

## 目标与取舍

用户的原始痛点：

1. 表情包散落在各平台本地缓存里，找一张图要翻半天；
2. 找到了也要手动上传/粘贴，动作繁琐；
3. 想要一个统一库，任何平台都能一键调出发送。

Mojinest 把这件事拆成三层，后端先行：

| 层 | 职责 | 现状 |
| --- | --- | --- |
| 后端（库） | 存图、去重、标签、搜索、AI 标注、鉴权、限流 | v0.1 已完成 |
| 前端（网页） | 浏览、搜索、上传、点一下进剪贴板（手机浏览器可用） | v0.1 已完成 |
| 客户端（桌面） | 托盘常驻 + 全局快捷键呼出 + 搜索即复制 | v0.2 计划（Tauri / Go+WebView） |
| 导入器 | QQNT / 微信 PC / Android 缓存提取 | v0.3 计划 |

## 数据模型（SQLite）

```
files(sha256 PK, ext, mime, size, width, height, animated, frames, created_at)
users(id, name UNIQUE, pass_hash, created_at)                    -- 单用户起步，预留多用户
items(id, user_id, sha256, name, source_platform, source_hint,
      note, favorite, use_count, last_used_at, created_at, deleted_at)
tags(id, name UNIQUE, created_at)
item_tags(item_id, tag_id, source)                               -- source: manual | ai
collections(id, user_id, name, parent_id, sort, created_at)
item_collections(item_id, collection_id)
item_ai(item_id PK, description, ocr, model, created_at)
jobs(id, item_id, kind, status, attempts, error, created_at, updated_at)
```

设计要点：

- **内容寻址**：`sha256` 是物理文件名，`items` 是其在一个用户库里的条目。
  同一张图重复上传直接跳过；删条目时只有无人引用的 blob 才真正删除。
- **多用户就绪**：`items` / `collections` 都带 `user_id`，v0.1 只写 `1`。
  将来加 `user_id` 过滤即可，不必改表。
- **标签来源**：`item_tags.source` 区分 `manual` / `ai`，AI 重跑不会冲掉手打的标签。
- **软删除**：`deleted_at` 支撑回收站，配合 `/api/trash/empty` 真删。
- **AI 队列独立成表**：上传只入队，标注在后台跑，接口永远不被模型拖慢。

## 存储抽象

```go
type Store interface {
    Put(ctx, key, r, size, contentType) error
    Get(ctx, key) (io.ReadCloser, error)
    Delete(ctx, key) error
    Exists(ctx, key) (bool, error)
    URL(ctx, key) (string, int)   // 直链（可选），"": 由 API 转发
    Kind() string
}
```

- `Local`：`data/files/<sha[:2]>/<sha>.<ext>` 与 `data/thumbs/...`。
  `URL` 返回空串 → API 代理字节，鉴权始终生效。
- `S3`：任意 S3 兼容端点，路径风格可选。默认走 **1 小时预签名 URL**（桶保持私有），
  配了 `public_base` 则直接用公开域名。
- 切换后端只改配置，不动数据库：key 的生成规则两端一致。

## 图片处理（纯 Go，无 CGO）

- `imageutil.Sniff`：按文件头判断 PNG / JPEG / GIF / WebP。
- `imageutil.Inspect`：解出宽高、是否动图、帧数；顺带校验文件可解码。
  - GIF 走 `image/gif`，帧数即帧列表长度；
  - WebP 走 RIFF 分块解析（`VP8X` 取尺寸与动画标志、`ANMF` 数帧）。
- `imageutil.Thumb`：box 滤波降采样到最长边 320，**有透明通道输出 PNG、否则 JPEG**。
  不做 WebP 编码（pure-Go 无编码器），换来零依赖与正确透明。
- 送模型前把首帧转 PNG 并缩到 512，兼顾体积与识别率。

## AI 标注流水线

```
上传 → items + jobs(pending)
worker 轮询 → ClaimJob(running, attempts+1)
  → 读 blob → 首帧 → PNG 512 → POST {base}/chat/completions
  → 解析 JSON（容忍 ``` 围栏与前后废话）
  → item_ai(description, ocr) + tags(source=ai) → FinishJob(done)
失败：attempts<3 回队重试，≥3 标 failed，前端可一键 /api/jobs/retry
未配置模型：任务停在队列，不误标也不丢弃
```

- 走 OpenAI 兼容协议，因此 Ark / DeepSeek / vLLM / Ollama / OpenAI 都能用。
- 提示词内置固定的标签维度（情绪、角色、动作、场景、风格），保证标签词表收敛。
- 标签清洗：去 `#`、去标点、去重、截断长度，最多 16 个。

## 鉴权与安全

- 单用户 Token：登录后写 HttpOnly + SameSite=Lax Cookie（HTTPS 下自动 Secure），
  并支持 `Authorization: Bearer` 与 `?token=`（便于桌面客户端/下载）。
- 常量时间比较 + 失败延迟 300ms，抑制暴力猜测。
- 同源写保护：写接口带外站 `Origin` 直接 403（CSRF 兜底）。
- 令牌桶限流（默认 40 req/s、突发 400，按 IP），防止公网被刷。
- 上传体积上限 32MB/文件，`ParseMultipartForm` 限制总内存。
- 所有响应带 `nosniff`、`Referrer-Policy`；API 响应 `no-store`。

## 前端

单个 `internal/webui/dist/index.html`（无构建步骤，`go:embed` 进二进制）：

- 登录页 → 库页；顶部搜索/排序/收藏/回收站/上传；
- 网格为自适应卡片，懒加载缩略图，动图带角标，悬停显示名称/标签；
- 点击卡片 → 取原图 → 转 PNG（GIF 保持动画）→ 写剪贴板 → `Ctrl+V` 即可发；
- 非安全上下文（局域网 http）或浏览器拒绝时自动降级为下载；
- 加载用递增序号防乱序（快速切换筛选时旧响应直接丢弃）。

## 已知边界

- WebP 只解不编，缩略图统一转 PNG/JPEG；
- 微信 PC 端表情库为加密数据库，提取需要 `WeChat` 进程内存里的密钥（v0.3 处理）；
- 桌面客户端尚未实现（v0.2），当前"一键复制"依赖网页 + 剪贴板；
- 语义检索（向量）排在 v0.6，v0.1 的搜索是"标签 + 全文关键词"。
