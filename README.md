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
- [x] v0.2 桌面客户端：全局快捷键呼出选图窗口、按关键词一键复制、原生剪贴板（Win/mac/Linux）
- [ ] v0.3 导入器：QQNT（PC）、微信 PC 解密提取、Android QQ 收藏目录
- [ ] v0.4 手机端体验：上传/分享目标、iOS 快捷指令、局域网直连
- [ ] v0.5 多用户与共享库、按用户隔离配额
- [ ] v0.6 语义检索（向量）与以图搜图

## 桌面客户端（v0.2）

单文件、无运行时依赖（Windows 是 exe，Linux/macOS 直接是二进制）。

```bash
make desktop                 # 产出 ./mojinest-desktop
make release                 # dist/ 下三平台产物 + 服务端
./mojinest-desktop setup     # 首次配置：写配置 + 体检 + 平台指引
./mojinest-desktop check     # 自检：服务端/令牌/快捷键/剪贴板工具/浏览器
./mojinest-desktop run       # 常驻并注册全局快捷键（Windows、macOS）
./mojinest-desktop pick      # 打开选图窗口（给系统快捷键调用）
./mojinest-desktop copy 猫    # 直接按关键词复制第一张进剪贴板
./mojinest-desktop config    # 打印当前配置（令牌打码）
./mojinest-desktop help      # 完整用法
```

命令行的参数可以写在子命令前后任意位置（`pick -config x` 和 `-config x pick` 都行）。

**它到底解决了什么问题**（这是它存在的全部理由）：

浏览器只在「安全上下文」里允许网页写剪贴板。`http://127.0.0.1` 算安全上下文，
但 `http://192.168.1.4`（局域网 IP，也正是你部署服务的地址）**不算** ——
于是网页版在局域网里点图只会提示"复制不可用，已改为下载"。

客户端提供两条真正可用的路子：

1. **`copy 关键词`**：完全绕开浏览器。客户端自己下载原图 → 写系统剪贴板，
   你回聊天窗口 Ctrl+V。这条路径不打开任何窗口，可绑到系统快捷键上。
2. **本地转发（`run`/`pick` 自动开启）**：在 127.0.0.1 上起一个字节转发，
   把流量转到真实的（局域网）服务端。选图窗口打开的是 `http://127.0.0.1:<随机端口>`，
   于是页面变成安全上下文，网页版的"点图即复制"真的能用。
   转发是纯字节中继（不重写请求），所以 Host/Origin 保持一致，服务端的同源校验
   照常通过；转发进程空闲 30 分钟自动退出。

**直接双击 exe 会发生什么**：Windows 上弹一个三选一的小面板
（「是」打开表情库 /「否」看怎么常驻运行 /「取消」什么都不做），
不会擅自弹浏览器窗口。

格式策略：GIF 原样进剪贴板（保留动画），WebP 转 PNG（多数 IM 不认 WebP），
PNG/JPEG 透传。

**弹窗策略**（`-dialogs auto|always|never`，默认 auto）：
有控制台时（终端、脚本、快捷键守护进程）只写 stderr，**绝不弹模态框** ——
否则 `copy` 在脚本里会永远卡住等人点「确定」（这是实测踩到的：
在 wine 里跑 `copy` 直接挂死）。双击启动（没有控制台）才弹窗报错。

**成功必须被确认**：PowerShell 即使什么都不做也可能返回 0，所以客户端要求
脚本输出自己的确认标记；拿不到就判定失败并报错，同时把图片落盘兜底。
这样不会出现"提示已复制、粘出来是空的"。

配置文件 `~/.config/mojinest/desktop.json`：

| 字段 | 说明 |
| --- | --- |
| `server_url` | 服务端地址，如 `http://192.168.1.4:18642` |
| `token` | 服务端访问令牌（客户端自动登入选图窗口） |
| `hotkey` | 快捷键，如 `Ctrl+Alt+M` |
| `window_mode` | `app`（无边框应用窗口，默认）或 `browser`（普通标签页） |
| `browser` | 指定浏览器可执行文件（留空自动探测 Chrome/Chromium/Edge） |
| `auto_paste` | 复制后是否自动发一次 Ctrl+V（默认关） |
| `paste_delay_ms` | 自动粘贴前的等待毫秒数（默认 250） |

各平台怎么用：

- **Windows**：`mojinest-desktop.exe run` 常驻即可，客户端自己注册 `Ctrl+Alt+M`。
  开机自启 = 把 exe 的快捷方式（参数 `run`）放进 `shell:startup`。
  剪贴板与自动粘贴走系统 PowerShell + .NET，无需额外装东西。
- **Linux (X11)**：程序不去抢全局键（不可靠且会被 WM 打断）。在桌面环境的
  键盘设置里加一条自定义快捷键，命令写 `mojinest-desktop pick`。
  剪贴板需要 `xclip`（图片）或 `xsel`；自动粘贴需要 `xdotool`。
  也可以干脆绑 `mojinest-desktop copy 猫` 之类——按下就是一键发常驻表情。
- **macOS**：`run` 需要在「隐私与安全性 → 辅助功能」里授权才能注册全局键。
  剪贴板与自动粘贴用 `osascript`，无需额外安装。
- **Wayland**：同 Linux 思路，绑自定义快捷键到 `pick`；剪贴板用 `wl-clipboard`。

选图窗口是内置网页版（token 走 URL fragment，不落服务端日志）；点图即复制。
网页版在 http（局域网）下浏览器会拒绝写剪贴板，此时要用桌面客户端的
`copy`/`pick`，或点一次后按提示改用下载。

## 目录结构

```
cmd/mojinest/          服务端入口
cmd/mojinest-desktop/  桌面客户端入口（init/pick/copy/run/check）
internal/api/          HTTP 接口、鉴权、限流
internal/desktop/      桌面客户端：配置、API 客户端、剪贴板、选图窗口、全局快捷键
internal/repo/         所有 SQL（SQLite）
internal/store/        blob 存储抽象：local / s3
internal/imageutil/    格式嗅探、元数据、缩略图（pure Go）
internal/tagger/       OpenAI 兼容视觉模型客户端
internal/worker/       AI 标注任务队列
internal/webui/        内嵌前端（dist/index.html）
```

细节设计见 [docs/DESIGN.md](docs/DESIGN.md)。

## License

MIT
