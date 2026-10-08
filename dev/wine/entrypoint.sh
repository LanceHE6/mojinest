#!/usr/bin/env bash
# 在容器里拉起 Xvfb，然后执行传入的命令。
# 加 MOJINEST_WINE_STUB=1 时，给 wine 的 PATH 里塞一个假的 powershell，
# 用来验证剪贴板调用路径（真实剪贴板仍需要真 Windows）。
set -u

export DISPLAY="${DISPLAY:-:99}"
Xvfb "$DISPLAY" -screen 0 1280x900x24 >/tmp/xvfb.log 2>&1 &
XVFB_PID=$!
for _ in $(seq 1 40); do
  xdpyinfo -display "$DISPLAY" >/dev/null 2>&1 && break
  sleep 0.1
done
trap 'kill $XVFB_PID 2>/dev/null || true' EXIT

if [ "${MOJINEST_WINE_STUB:-0}" = "1" ]; then
  STUB_DIR=/opt/stubs
  mkdir -p "$STUB_DIR"
  cat > "$STUB_DIR/powershell" <<'STUB'
#!/bin/bash
# 假的 Windows PowerShell：记录调用参数与 MOJI_FILE 指向的文件
echo "ARGS: $*" >> /tmp/ps-calls.log
if [ -n "${MOJI_FILE:-}" ]; then
  echo "FILE: $MOJI_FILE size=$(stat -c%s "$MOJI_FILE" 2>/dev/null || echo 0)" >> /tmp/ps-calls.log
  cp "$MOJI_FILE" /tmp/ps-copied.png 2>/dev/null || true
fi
echo ok
STUB
  chmod +x "$STUB_DIR/powershell"
  # wine 里 LookPath 会带上 PATHEXT，脚本名加 .bat 也能被找到
  cp "$STUB_DIR/powershell" "$STUB_DIR/powershell.bat"
  chmod +x "$STUB_DIR/powershell.bat"
  echo "wine 剪贴板桩已就绪: $STUB_DIR" >&2
fi

exec "$@"
