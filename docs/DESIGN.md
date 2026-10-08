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
- 语义检索（向量）排在 v0.6，v0.1 的搜索是"标签 + 全文关键词"。

## 桌面客户端设计（v0.2）

目标：让"叫出库 → 找到图 → 进剪贴板"这件事只要一次按键。

```go
internal/desktop/
  config.go      独立配置（server_url / token / hotkey / browser / window_mode）
  client.go      API 客户端（Search / Download / Stats / Upload / MarkUsed）
  clipboard.go   CopyImage(mime, bytes) + Paste(delay)
  picker.go      打开选图窗口（应用式窗口，自动带 token）
  hotkey.go      快捷键解析 + 平台无关入口
  hotkey_windows.go  RegisterHotKey + 自建消息循环（纯 syscall，无 cgo）
  hotkey_other.go    Linux/macOS 返回“请用系统设置绑键”
```

三个刻意的选择：

1. **选图窗口复用 Web UI，不自己画界面**。库的界面只有一份（`internal/webui`），
   客户端只负责"开窗 + 剪贴板 + 快捷键"，改 UI 不用改两处。
   窗口用 Chromium 的 `--app=` 模式，看起来就是独立程序。
2. **按键触发后由客户端自己下载并写系统剪贴板**，不依赖浏览器的
   `navigator.clipboard`（局域网 http 下浏览器会直接拒绝写入）。
   这就是 `copy` 子命令存在的理由：`mojinest-desktop copy 猫` 一行就能绑到
   任何桌面环境的快捷键上，不挑 X11/Wayland。
3. **全局键只在 Windows 自己抢**。macOS 也能抢但要用户授权辅助功能；
   Linux/Wayland 抢键既不靠谱也和用户自己的 WM 快捷键打架，所以让用户
   在系统设置里绑 `pick` —— 行为可预期，出问题也好排查。

剪贴板的平台实现：

| 平台 | 复制 | 自动粘贴 |
| --- | --- | --- |
| Windows | PowerShell + `System.Windows.Forms.Clipboard` | `SendKeys ^v` |
| Linux X11 | `xclip -selection clipboard -t <mime>` | `xdotool key ctrl+v` |
| Linux Wayland | `wl-copy --type <mime>` | `ydotool`（可选） |
| macOS | `osascript` + `«class PNGf»` | `System Events keystroke v` |

格式策略：GIF 原样进剪贴板（保留动画，微信/浏览器都认），WebP 转 PNG
（多数 IM 不认 WebP），PNG/JPEG 直接透传。

两个踩过的坑，代码里都有注释：

- **`exec.CommandContext` 会杀掉浏览器**：父进程退出时 context 被取消，
  浏览器跟着死。所以用 `exec.Command` + `Setsid`（Windows 是
  `CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS`）让子进程脱离会话。
- **Go 的跨主机重定向会带 Authorization**：只有端口不同（同主机名）时 Go
  认为"同源"，`Authorization`/`Cookie` 会跟着跳到别的服务上。
  `CheckRedirect` 里显式对比 `host:port`，不同就删掉凭据；
  S3 预签名 302 不受影响（它自带签名）。
