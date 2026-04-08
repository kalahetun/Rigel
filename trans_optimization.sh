#!/bin/bash

#==============================
# Debian 12 跨洋大文件 BBR 优化脚本
# 适用于 GCP / AWS 日 <-> 美大带宽传输
#==============================

# 1. 写入 sysctl 内核优化（BBR + TCP + 缓冲区 + 队列）
cat > /etc/sysctl.d/rigel-bbr-tuning.conf <<EOF
# BBR 拥塞控制
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr

# 文件句柄
fs.file-max=1048576
fs.nr_open=1048576

# TCP 缓冲区最大值
net.core.rmem_max=67108864
net.core.wmem_max=67108864
net.core.rmem_default=67108864
net.core.wmem_default=67108864

# 自动调整缓冲区
net.ipv4.tcp_rmem=4096 87380 67108864
net.ipv4.tcp_wmem=4096 87380 67108864

# 端口范围
net.ipv4.ip_local_port_range=1024 65535

# TIME-WAIT 复用
net.ipv4.tcp_tw_reuse=1

# SYN 防御（必须开启）
net.ipv4.tcp_syncookies=1

# 连接队列调大
net.core.somaxconn=4096
net.core.netdev_max_backlog=16384
net.ipv4.tcp_max_syn_backlog=8192

# 长连接优化：空闲后不慢启动
net.ipv4.tcp_slow_start_after_idle=0

# 长链路必备
net.ipv4.tcp_window_scaling=1
net.ipv4.tcp_timestamps=1
net.ipv4.tcp_sack=1
EOF

# 2. 立即生效 sysctl
sysctl --system

# 3. 写入文件句柄最大限制
cat >> /etc/security/limits.conf <<EOF
* soft nofile 1048576
* hard nofile 1048576
root soft nofile 1048576
root hard nofile 1048576
EOF

# 4. 当前终端立即生效句柄
ulimit -n 1048576

# 5. 输出结果
echo "=================================="
echo " BBR + TCP 优化 已全部生效！"
echo "=================================="
echo "拥塞控制: $(sysctl -n net.ipv4.tcp_congestion_control)"
echo "队列规则: $(sysctl -n net.core.default_qdisc)"
echo "文件句柄: $(ulimit -n)"
echo "=================================="