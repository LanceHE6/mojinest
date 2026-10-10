# Mojinest 跨 Session 开发交接文档

## 1. 项目基础与核心路径
- **项目路径**：`/home/hycer/code/mojinest`
- **GitHub 仓库**：`https://github.com/LanceHE6/mojinest.git`（当前在 `main` 分支，工作区完全干净已推送）
- **服务端可执行文件**：`/home/hycer/code/mojinest/mojinest`
  - 启动命令：`/home/hycer/code/mojinest/mojinest server -config ~/.config/mojinest/config.json`
  - 默认监听端口：`18642`
  - 当前状态：**已完全停止释放**（无残留进程占用 `18642` 端口）
- **客户端产物位置 (Win11)**：
  - 机器 IP：`192.168.1.3`（用户 `27655`，Win11 原版）
  - 桌面路径：`C:\Users\27655\Desktop\Mojinest\Mojinest.exe`
  - 客户端配置：`C:\Users\27655\.config\mojinest\desktop.json`

## 2. 工具链配置（严格遵守用户规范）
- **Rust/Cargo 路径**：隔离在 `/home/hycer/code/.env/rust/`（`cargo`, `rustup`, `cargo-xwin`）
  - 环境变量：`CARGO_HOME=/home/hycer/code/.env/rust/cargo`, `RUSTUP_HOME=/home/hycer/code/.env/rust/rustup`
  - 镜像源：`rsproxy.cn`
  - 交叉编译命令：
    ```bash
    cd /home/hycer/code/mojinest/client-desktop/src-tauri
    export RUSTUP_HOME=/home/hycer/code/.env/rust/rustup
    export CARGO_HOME=/home/hycer/code/.env/rust/cargo
    export PATH=$CARGO_HOME/bin:$PATH
    cargo xwin build --target x86_64-pc-windows-msvc --release
    ```
- **Go 环境**：`/home/hycer/code/.env/go/current-go`（版本软链管理，禁止 apt）

## 3. 当前功能实现现状与技术沉淀
1. **架构模式与物理隔离**：
   - 存储引擎：支持 Local 本地文件存储 与 Cloudflare R2 / S3 兼容对象存储。
   - **完全物理隔离**：Local 与 R2 分别绑定独立的 SQLite 数据库文件（`data/mojinest_local.db` 与 `data/mojinest_r3.db`）。
   - UI 设置窗口支持直接在 Local 与 R2 之间热切换并保存。
2. **桌面客户端 (Tauri v2 + Rust)**：
   - 快捷键：默认 `Ctrl+Alt+E`。
   - 托盘：带透明通道专属六边形 Emoji Logo 图标，右键支持「显示」、「设置」、「退出」。
   - 选图与多选复制交互：
     - 光标悬停卡片下 1/3 区域浮现「复制」按钮，点击按聊天标准尺寸（≤ 240px）缩放直写剪贴板；右键「复制」保留原始大图。
     - 点击卡片其余部分进入多选模式，底部常驻 `已多选 N 个表情包 [取消多选]` 胶囊；多选下右键只保留 `删除 (N 个)` 并带 5 秒撤回保护。
     - GIF 动图悬停播放，通过 Windows `DataObject` 双通道直写，确保微信/QQ 粘贴时 100% 作为动态表情发送。
     - 导入：支持直接拖拽图片文件到主窗口（带深蓝虚线全屏遮罩与动态百分比进度条）。
     - 图标风格：全站全量统一为 Ionicons 极简细线矢量 SVG 图标。
3. **微信 4.x 内存自动解密导出**：
   - 核心文件：`client-desktop/src-tauri/src/wx_decrypt.rs`。
   - 算法：`Weixin.exe` 运行时调用 Windows API 扫描提取 Seed，派生 `AES-128-CBC + PKCS7` 密钥（Key = IV）。
   - 全覆盖：同时解密 `Thumb`（标准图片）与 `Persist`（含 wxgf 动图），打上 `wechat,导入` 标签直传服务端。

## 4. 下一 Session 待办事项
1. 启动服务端验证最新包含 Thumb+Persist 的微信解密全量导出效果；
2. 实现企业微信（WXWork）本地表情库导出与导入逻辑；
3. 接入多模态视觉大模型异步打标与 OCR 文字提取队列。
