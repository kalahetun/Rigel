#!/bin/bash
# Envoy(gateway) 一键安装脚本（matth 用户专属）
# 功能：下载Envoy + 配置systemd服务 + 启动服务 + 开机自启
set -euo pipefail

# --------------------------
# 0. 前置检查（必须是matth用户）
# --------------------------
if [ "$USER" != "matth" ]; then
    echo -e "\033[31m❌ 错误：必须以 matth 用户运行此脚本！\033[0m"
    exit 1
fi

# --------------------------
# 1. 定义常量（无需修改）
# --------------------------
ENVOY_VERSION="1.28.0"
ENVOY_HOME="/home/matth"
ENVOY_BIN="${ENVOY_HOME}/envoy"
ENVOY_CONFIG="${ENVOY_HOME}/envoy-mini.yaml"
ENVOY_SERVICE="/etc/systemd/system/envoy.service"
ENVOY_LOG="${ENVOY_HOME}/envoy-service.log"
DOWNLOAD_URL=""

# --------------------------
# 2. 检测系统架构
# --------------------------
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    DOWNLOAD_URL="https://github.com/envoyproxy/envoy/releases/download/v${ENVOY_VERSION}/envoy-${ENVOY_VERSION}-linux-x86_64"
elif [ "$ARCH" = "aarch64" ]; then
    DOWNLOAD_URL="https://github.com/envoyproxy/envoy/releases/download/v${ENVOY_VERSION}/envoy-${ENVOY_VERSION}-linux-aarch64"
else
    echo -e "\033[31m❌ 错误：不支持的架构 ${ARCH}（仅支持x86_64/aarch64）\033[0m"
    exit 1
fi

# --------------------------
# 3. 安装系统依赖（sudo）
# --------------------------
echo -e "\033[32m🔧 步骤1：安装系统依赖...\033[0m"
sudo apt update && sudo apt install -y \
    curl ca-certificates libc6 libgcc-s1 libstdc++6 libssl3 \
    --no-install-recommends && sudo apt clean

# --------------------------
# 4. 下载并安装Envoy二进制
# --------------------------
echo -e "\033[32m📥 步骤2：下载 Envoy ${ENVOY_VERSION}（${ARCH}）...\033[0m"
if [ -f "${ENVOY_BIN}" ]; then
    echo -e "\033[33m⚠️  检测到已存在Envoy二进制，先备份...\033[0m"
    mv "${ENVOY_BIN}" "${ENVOY_BIN}.bak"
fi

curl -L --progress-bar "${DOWNLOAD_URL}" -o "${ENVOY_BIN}"
chmod +x "${ENVOY_BIN}"
chown matth:matth "${ENVOY_BIN}"

# 验证二进制
echo -e "\033[32m✅ 验证Envoy安装...\033[0m"
if ! "${ENVOY_BIN}" --version >/dev/null 2>&1; then
    echo -e "\033[31m❌ Envoy二进制损坏或不兼容！\033[0m"
    rm -f "${ENVOY_BIN}"
    exit 1
fi

# --------------------------
# 5. 生成Envoy配置文件（带带宽限流）
# --------------------------
echo -e "\033[32m📝 步骤3：生成Envoy配置文件...\033[0m"
cat > "${ENVOY_CONFIG}" << EOF
admin:
  address:
    socket_address:
      address: 0.0.0.0
      port_value: 9901
static_resources:
    listeners: []
    clusters: []
EOF
chown matth:matth "${ENVOY_CONFIG}"

# --------------------------
# 6. 配置systemd服务文件（sudo）
# --------------------------
echo -e "\033[32m⚙️  步骤4：配置systemd服务...\033[0m"
sudo tee "${ENVOY_SERVICE}" > /dev/null << EOF
[Unit]
Description=Envoy Proxy (matth user)
After=network.target
Documentation=https://www.envoyproxy.io/

[Service]
User=matth
Group=matth
ExecStart=${ENVOY_BIN} -c ${ENVOY_CONFIG} --log-level info --base-id 1000
Restart=always
RestartSec=3
StandardOutput=append:${ENVOY_LOG}
StandardError=append:${ENVOY_LOG}
WorkingDirectory=${ENVOY_HOME}
Type=simple
KillMode=process
IgnoreSIGHUP=true

[Install]
WantedBy=multi-user.target
EOF

# --------------------------
# 7. 启动并设置开机自启（sudo）
# --------------------------
echo -e "\033[32m🚀 步骤5：启动Envoy服务...\033[0m"
sudo systemctl daemon-reload
sudo pkill -9 envoy || true  # 杀死残留进程
sudo systemctl start envoy
sudo systemctl enable envoy

# --------------------------
# 8. 验证安装结果
# --------------------------
echo -e "\033[32m✅ 步骤6：验证安装...\033[0m"
sleep 3  # 等待服务启动
if sudo systemctl is-active --quiet envoy; then
    echo -e "\033[32m🎉 Envoy安装并启动成功！\033[0m"
    echo -e "\033[36m📌 常用命令（matth用户）：\033[0m"
    echo -e "  启动：sudo systemctl start envoy"
    echo -e "  停止：sudo systemctl stop envoy"
    echo -e "  状态：sudo systemctl status envoy"
    echo -e "  日志：tail -f ${ENVOY_LOG}"
    echo -e "\033[36m📌 验证进程：\033[0m"
    ps -ef | grep envoy | grep -v grep
else
    echo -e "\033[31m❌ Envoy启动失败！查看日志：tail -f ${ENVOY_LOG}\033[0m"
    exit 1
fi