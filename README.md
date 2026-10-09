# Mojinest

自部署的表情包 / 贴图跨平台统一管理系统。散落在 QQ、微信、企业微信、Telegram 里的收藏，
全部收进一个属于你自己的小窝：原生桌面端悬浮呼出、点选即贴、多端同步、AI 视觉打标。

- **后端**：Go（标准库 + pure-Go SQLite，单文件、无 CGO，支持 Docker 部署）
- **桌面客户端**：Tauri v2 + Rust（Windows / Linux 跨平台，内存占用仅 30MB）
- **存储引擎**：支持本地磁盘存储，或无缝切换至 S3 兼容对象存储（Cloudflare R2 / MinIO / OSS），各存储空间完全独立物理隔离
- **前端**：暗黑极简 SPA（内嵌进二进制，全量采用 Ionicons 极简细线矢量图标）
- **AI 标注**：接入任意 OpenAI 兼容视觉模型，上传即自动提取中文标签 + 描述 + 图中文字 OCR

---

## 核心特性

- **Tauri 原生桌面端 (v0.3)**：
  - **全局快捷键**：默认 `Ctrl+Alt+E`（可自由配置），在任意聊天窗口按下即刻唤起/隐藏表情面板；
  - **原生系统托盘**：常驻任务栏通知区，右键菜单支持「显示」、「设置」及「退出」；
  - **悬浮与点击逻辑**：
    - 光标悬停表情卡片下 1/3 区域浮现「复制」按钮，点击等比缩放至标准表情包大小（长边 ≤ 240px），完美贴合 QQ/微信聊天气泡；
    - 点击卡片其余部分支持**多选模式**，底部常驻多选计数与「取消多选」胶囊按钮，多选下右键支持**批量删除**（带 5 秒后悔撤回保护）；
    - 右键表情支持「查看大图」、「复制原图（保留原始无损分辨率）」、「重命名」及「删除」；
  - **GIF 动图支持**：卡片悬停自动播放动图预览，Windows 底层采用 `DataObject` 文件 + 位图双通道直写，确保在微信/QQ 粘贴时 100% 作为原生动态表情发送；
  - **多通道导入**：
    - 支持直接将电脑上的图片文件**拖拽进主窗口直接入库**（附带全屏虚线遮罩与实时百分比进度条）；
    - 支持打开原生文件选择器批量多选导入；
    - 支持自动检测本地 QQNT（收藏表情/聊天表情缓存）、微信、企业微信表情库一键批量入库。
- **存储引擎独立隔离**：
  - 支持在 UI 设置弹窗中一键热切换 Local 与 Cloudflare R2，配置立即生效；
  - Local 与 R2 分别绑定独立的 SQLite 数据库（`mojinest_local.db` 与 `mojinest_r3.db`），存储空间彼此隔离独立。
- **AI 智能检索**：
  - 上传即自动打标（情绪/动作/场景/角色），支持提取表情中的梗图文字；
  - 全文检索覆盖文件名、标签、AI 描述及 OCR 文字。

---

## 快速开始

### 1. 编译并启动服务端

```bash
# 编译纯 Go 服务端
make build

# 启动服务（指定配置）
./mojinest server -config ~/.config/mojinest/config.json
```

默认监听端口：`http://0.0.0.0:18642`。

### 2. 编译 Tauri 桌面客户端

Linux 宿主机支持通过 `cargo-xwin` 直接交叉编译出 Windows 原生 64 位可执行文件：

```bash
cd client-desktop/src-tauri

# 交叉编译 Windows Release 版本
cargo xwin build --target x86_64-pc-windows-msvc --release

# 产物位置：
# target/x86_64-pc-windows-msvc/release/mojinest-desktop.exe
```

---

## 存储配置（Cloudflare R2）

可在客户端 UI 设置弹窗中直接可视化填写，或在服务端 `config.json` 中配置：

```json
{
  "storage_backend": "s3",
  "s3": {
    "endpoint": "https://<account_id>.r2.cloudflarestorage.com",
    "region": "auto",
    "bucket": "mojinest",
    "access_key": "<R2_ACCESS_KEY_ID>",
    "secret_key": "<R2_SECRET_ACCESS_KEY>",
    "path_style": false,
    "public_base": ""
  }
}
```

---

## 项目结构

```
cmd/mojinest/          服务端入口 (CLI / Server)
client-desktop/        Tauri v2 原生桌面端工程
  ├── src/             桌面端内嵌暗黑极简 UI (Ionicons 矢量图标)
  └── src-tauri/       Rust 原生后端 (托盘、全局快捷键、剪贴板双通道、文件拖放、本地探测)
internal/api/          REST API 路由、CORS、热切换存储引擎
internal/config/       系统配置与存储引擎绑定
internal/db/           SQLite pure-Go 数据库抽象
internal/importer/     本地应用表情缓存发现与导入流水线
internal/repo/         数据持久化与全文检索
internal/store/        存储后端抽象 (Local 磁盘存储 / S3 Cloudflare R2)
internal/tagger/       OpenAI 兼容视觉大模型打标客户端
internal/worker/       后台异步打标工作队列
```

---

## License

MIT
