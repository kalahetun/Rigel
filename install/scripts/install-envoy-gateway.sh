#!/bin/bash
set -euo pipefail

# --------------------------
# 0. 前置检查
# --------------------------
if [ "$USER" != "matth" ]; then
    echo "❌ 必须以 matth 用户运行"
    exit 1
fi

# --------------------------
# 1. 常量定义
# --------------------------
ENVOY_VERSION="1.28.0"
ENVOY_HOME="/home/matth"
ENVOY_BIN="${ENVOY_HOME}/envoy"
ENVOY_CONFIG="${ENVOY_HOME}/envoy-mini.yaml"
DOWNLOAD_URL=""

# --------------------------
# 2. 架构检测
# --------------------------
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    DOWNLOAD_URL="https://github.com/envoyproxy/envoy/releases/download/v${ENVOY_VERSION}/envoy-${ENVOY_VERSION}-linux-x86_64"
elif [ "$ARCH" = "aarch64" ]; then
    DOWNLOAD_URL="https://github.com/envoyproxy/envoy/releases/download/v${ENVOY_VERSION}/envoy-${ENVOY_VERSION}-linux-aarch64"
else
    echo "❌ 不支持架构 ${ARCH}"
    exit 1
fi

# --------------------------
# 3. 系统依赖
# --------------------------
sudo apt update
sudo apt install -y curl ca-certificates libssl3 --no-install-recommends
sudo apt clean

# --------------------------
# 4. 下载 Envoy
# --------------------------
if [ -f "${ENVOY_BIN}" ]; then
    mv "${ENVOY_BIN}" "${ENVOY_BIN}.bak"
fi

curl -L "${DOWNLOAD_URL}" -o "${ENVOY_BIN}"
chmod +x "${ENVOY_BIN}"
chown matth:matth "${ENVOY_BIN}"

"${ENVOY_BIN}" --version

# --------------------------
# 5. 生成最小配置
# --------------------------
cat > "${ENVOY_CONFIG}" << EOF
admin:
  address:
    socket_address:
      address: 127.0.0.1
      port_value: 9901

static_resources:
  listeners: []
  clusters: []
EOF

chown matth:matth "${ENVOY_CONFIG}"

echo "✅ Envoy 安装完成！配置文件：${ENVOY_CONFIG}，二进制：${ENVOY_BIN}"
echo "⚠️ 请通过 Go 程序启动 Envoy"