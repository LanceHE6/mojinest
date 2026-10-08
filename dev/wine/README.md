# 在 Linux 上用 wine 验证 Windows 客户端

没有 Windows 机器时，用容器里的 wine64 真实运行 `mojinest-desktop.exe`。

## 构建镜像

```bash
docker build -t mojinest-wine dev/wine
```

镜像基于 ubuntu:24.04，装了 `wine64 + xvfb + xdotool + 中文字体`。
刻意不装 `wine` 元包（会拖进 32 位依赖），改用包装脚本调用
`/usr/lib/wine/wine64`。

## 能验证什么

```bash
cd /path/to/mojinest
make release
docker run --rm -v $PWD/dist:/dist:ro mojinest-wine bash -lc '
  wine /dist/mojinest-desktop-windows-amd64.exe -version
  wine /dist/mojinest-desktop-windows-amd64.exe -config /work/cfg.json check
'
```

已验证通过的部分：

- **二进制可运行**：console 子系统、PE32+ x64，wine 下正常启动
- **参数解析**：`-version / help / config / check` 全通
- **配置读取**：`C:\` 风格路径、环境变量覆盖都正常
- **中文字符串**：UTF-8 参数与输出在 `LANG=C.UTF-8` 下完好
- **HTTP 客户端**：连真实服务端拉图库、下载原图都正常
  （`--network host` + `server_url=http://127.0.0.1:PORT`）
- **错误路径**：关键词不存在 → 退出码 1 + 明确报错
- **剪贴板确认逻辑**：wine 的 `powershell` 是空存根，客户端正确识别为
  "没有确认剪贴板写入" 并落盘兜底，而不是谎报成功

## 验证不了什么（wine 的硬限制）

- **真实剪贴板写入**：wine 没有 PowerShell/.NET 实现，`Clipboard::SetImage`
  无从执行。真实 Windows 上才有意义。
- **全局快捷键触发**：`RegisterHotKey` 在 wine 下**注册成功**
  （见日志"全局快捷键已注册"），但 X11 侧不会真的送来按键。
  试过 `xdotool key` / `keydown+keyup` / `--window` 定向投递 /
  `explorer /desktop` 虚拟桌面，均无法送达 —— wine 的热键窗口依赖真实的
  窗口管理器与焦点，Xvfb 下 `XGetInputFocus` 返回 1，没有焦点可言。
  → 这段逻辑改用 `listenHotkey` 接缝在单测里驱动（`daemon_test.go`）。

## 有用的命令

```bash
# 进容器交互调试
docker run --rm -it -v $PWD/dist:/dist:ro mojinest-wine bash

# 容器里看 wine 的窗口
xdotool search --name "." getwindowname %@

# 容器内记录 PowerShell 调用（用桩替换）
# 见 entrypoint.sh 的 MOJINEST_WINE_STUB=1 分支
```

## 结论

wine 适合验证「启动 → 解析参数 → 读配置 → 发请求 → 报错语义」这一整条链，
以及**发现真正的可用性缺陷**（就是靠它抓到了"模态弹窗把脚本卡死"）。
真正需要在 Windows 上确认的只剩两件事：剪贴板写入、全局快捷键触发。
