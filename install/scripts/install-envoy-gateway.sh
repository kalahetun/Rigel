#!/bin/bash
# Envoy(gateway) 一键安装脚本（matth 用户专属）
# ✅ 修复：支持 Envoy Hot Restart（systemd 只拉起 epoch=0）
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
ENVOY_SERVICE="/etc/systemd/system/envoy.service"
ENVOY_LOG="${ENVOY_HOME}/envoy-service.log"
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

# --------------------------
# 6. systemd（🔥关键修复）
# --------------------------
sudo tee "${ENVOY_SERVICE}" > /dev/null << EOF
[Unit]
Description=Envoy Proxy (hot restart enabled)
After=network.target

[Service]
User=matth
Group=matth

# ⚠️ systemd 只允许启动 epoch=0
ExecStart=${ENVOY_BIN} \\
  -c ${ENVOY_CONFIG} \\
  --restart-epoch 0 \\
  --base-id 1000 \\
  --log-level info

# ❌ 禁止 systemd 自动重启（热重启由 Envoy 自己做）
Restart=no

WorkingDirectory=${ENVOY_HOME}
StandardOutput=append:${ENVOY_LOG}
StandardError=append:${ENVOY_LOG}

Type=simple

# ✅ 允许 fork/exec 子进程
KillMode=mixed

[Install]
WantedBy=multi-user.target
EOF

# --------------------------
# 7. 启动（❌ 不再 pkill -9）
# --------------------------
sudo systemctl daemon-reload
sudo systemctl start envoy
sudo systemctl enable envoy

# --------------------------
# 8. 验证
# --------------------------
sleep 2
systemctl status envoy --no-pager
ps -ef | grep envoy | grep -v grep
