# Windows 客户端的 Linux 上验证环境：wine64 + 虚拟显示。
#
# 用途：在没有 Windows 机器时验证 mojinest-desktop.exe 的真实行为 ——
# 控制台输出、MessageBoxW 弹窗、RegisterHotKey 全局快捷键（wine 把它实现在
# X11 之上，所以用 Xvfb + xdotool 能真的触发一次快捷键）。
#
# 构建：
#   docker build -t mojinest-wine dev/wine
# 运行（交互）：
#   docker run --rm -it -v $PWD/dist:/dist mojinest-wine bash
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV WINEDEBUG=-all
ENV WINEPREFIX=/root/.wine
ENV DISPLAY=:99

RUN apt-get update && apt-get install -y --no-install-recommends \
        wine64 \
        xvfb \
        x11-utils \
        xdotool \
        fonts-wqy-zenhei \
        fonts-wqy-microhei \
        ca-certificates \
        curl \
        file \
    && rm -rf /var/lib/apt/lists/*

# wine64 只提供 /usr/lib/wine/wine64；装 `wine` 元包会拖进 32 位依赖，这里
# 直接做包装脚本（wine 只在容器里当测试工具用，不需要 wow64）。
RUN printf '#!/bin/sh\nexec /usr/lib/wine/wine64 "$@"\n' > /usr/local/bin/wine \
    && chmod +x /usr/local/bin/wine \
    && printf '#!/bin/sh\nexec /usr/lib/wine/wine64 "$@"\n' > /usr/local/bin/wine64 \
    && chmod +x /usr/local/bin/wine64

# wineboot 先把 prefix 建起来，避免每次运行都要等
RUN xvfb-run -a wineboot --init >/dev/null 2>&1 || true

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

WORKDIR /work
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["bash"]
