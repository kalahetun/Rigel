#!/bin/bash
set -euo pipefail

# --------------------------
# 0. 前置检查
# --------------------------
#if [ "$USER" != "matth" ]; then
#    echo "❌ 必须以 matth 用户运行"
#    exit 1
#fi

# --------------------------
# 1. 常量定义
# --------------------------
ENVOY_VERSION="1.28.0"
ENVOY_HOME="/home/matth"
ENVOY_BIN="${ENVOY_HOME}/envoy"
ENVOY_CONFIG="${ENVOY_HOME}/envoy-mini.yaml"
DOWNLOAD_URL=""
LUA_SCRIPT_PATH="${ENVOY_HOME}/hop_router.lua"  # Lua script in same directory as config
PROFILE_DIR="$(dirname ${ENVOY_CONFIG})/profile"
OWNER="matth:matth"

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
    echo "ℹ️  发现已存在 Envoy 二进制，备份为 ${ENVOY_BIN}.bak"
    mv "${ENVOY_BIN}" "${ENVOY_BIN}.bak"
fi

echo "📥 下载 Envoy ${ENVOY_VERSION} (${ARCH})..."
curl -L "${DOWNLOAD_URL}" -o "${ENVOY_BIN}"
chmod +x "${ENVOY_BIN}"
chown 640 "${ENVOY_BIN}"

echo "✅ Envoy 版本验证："
"${ENVOY_BIN}" --version

# --------------------------
# 5. 创建 profile 目录（避免Admin报错）
# --------------------------
mkdir -p "${PROFILE_DIR}"
chmod 755 "${PROFILE_DIR}"

# --------------------------
# 6. 生成最小配置
# --------------------------
echo "📝 生成 Envoy 配置文件 ${ENVOY_CONFIG}..."
cat > "${ENVOY_CONFIG}" << EOF
# Envoy 1.28.0 最小启动配置：强制保留Lua脚本加载（必选）
admin:
  address:
    socket_address:
      address: 127.0.0.1
      port_value: 9901

static_resources:
  listeners:
    - name: listener_8095
      address:
        socket_address:
          address: 0.0.0.0
          port_value: 8095
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                codec_type: HTTP1
                stat_prefix: ingress_http_8095
                route_config:
                  name: local_route
                  virtual_hosts:
                    - name: local_service
                      domains: ["*"]
                      routes:
                        - match:
                            prefix: "/"
                          route:
                            cluster: dummy_cluster
                http_filters:
                  # 强制加载外部Lua脚本（必选，不可删除）
                  - name: envoy.filters.http.lua
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua
                      source_codes:
                        hop_router.lua:
                          filename: "/home/matth/hop_router.lua"  # 固定脚本路径，必须存在
                  # 路由转发（依赖Lua后执行）
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  # 核心集群配置
  clusters:
    - name: dummy_cluster
      connect_timeout: 0.25s
      type: STRICT_DNS
      lb_policy: ROUND_ROBIN
      load_assignment:
        cluster_name: dummy_cluster
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address:
                      address: 127.0.0.1
                      port_value: 8080
EOF

#场景 1：单跳代理（仅 B → S3）
#Client 发起请求时携带的 Headers：
# 核心 Headers（替换为实际地址）
#x-hops: 192.168.1.100:8080,s3.example.com:80    # 最终目标：S3 的 IP/域名+Port
#x-index: 1                   # 固定值 1
#
## 关键：Host 指向 B 节点的实际地址（TCP 自动转发）
#Host: 192.168.1.100:8080
#
## 通用 Headers
#Content-Type: application/json
#Accept: application/json

#场景 2：2 跳代理（A → B → S3）
#Client 发起请求时携带的 Headers：
## 核心 Headers（代理链+S3 均为 IP:Port）
#x-hops: 192.168.1.90:8080,192.168.1.100:8080,s3.example.com:80
#x-index: 1                   # 固定值 2（指向 B 节点）
#
## 关键：Host 指向 A 节点的实际地址
#Host: 192.168.1.90:8080
#
## 通用 Headers
#Content-Type: application/json
#Accept: application/json

#还要带上Client header 排查的时候知道从哪里来的

# --------------------------
# 7. 生成 Lua 脚本
# --------------------------
echo "📝 生成 Lua 脚本 ${LUA_SCRIPT_PATH}..."
cat > "${LUA_SCRIPT_PATH}" << EOF
-- Envoy Lua Filter: 极简hops动态路由（仅请求转发+响应透传）
-- 核心：存入current_index到Metadata，精准追溯本次转发的索引
-- ==============================================
-- 通用常量定义（仅保留必需字段）
-- ==============================================
local HEADER_CONST = {
    HOPS = "x-hops",          -- 转发链：A1,A2,...An,S3
    INDEX = "x-index",        -- 游标索引（初始=1）
    HOST = "Host",            -- 转发核心Header
    STATUS = ":status",       -- 响应状态码
    CLIENT = "x-client"       -- 客户端标识（仅日志用）
}

local BUSINESS_RULE = {
    EMPTY_VALUE = "",               -- 空值兜底
    SEPARATOR = ",",                -- hops分隔符
    INIT_INDEX = "1"                -- 初始index=1
}

-- Metadata 命名空间（仅持久化请求阶段关键信息）
local METADATA_NS = "hop_router"

-- ==============================================
-- 通用工具函数（仅保留必需的字符串拆分）
-- ==============================================
-- 拆分字符串为数组（解析hops）
local function split_str(str, sep)
    local arr = {}
    if str == nil or str == BUSINESS_RULE.EMPTY_VALUE then
        return arr
    end
    for val in string.gmatch(str, "[^" .. sep .. "]+") do
        table.insert(arr, val)
    end
    return arr
end

-- ==============================================
-- 请求阶段（核心：解析x-hops转发请求，存入current_index到Metadata）
-- ==============================================
function envoy_on_request(request_handle)
    -- 1. 读取请求Header
    local hops_str = request_handle:headers():get(HEADER_CONST.HOPS) or BUSINESS_RULE.EMPTY_VALUE
    local index_str = request_handle:headers():get(HEADER_CONST.INDEX) or BUSINESS_RULE.INIT_INDEX
    local client_str = request_handle:headers():get(HEADER_CONST.CLIENT) or BUSINESS_RULE.EMPTY_VALUE

    -- 2. 格式转换（current_index是本次转发的核心标识）
    local hops_arr = split_str(hops_str, BUSINESS_RULE.SEPARATOR)
    local current_index = tonumber(index_str) or tonumber(BUSINESS_RULE.INIT_INDEX)
    local hops_len = #hops_arr

    -- 3. 空hops拒绝转发
    if hops_len == 0 then
       request_handle:logErr(string.format(
           "Missing x-hops header, reject forwarding | client=%s",
           client_str
       ))
        request_handle:respond({[HEADER_CONST.STATUS] = "400"}, "Missing x-hops header")
        return
    end

    -- 4. 计算转发目标（基于current_index）
    local target_hop = BUSINESS_RULE.EMPTY_VALUE
    local new_index = current_index + 1

    -- 正常转发：index < hops长度 → 取对应节点
    if new_index <= hops_len then
        target_hop = hops_arr[new_index]
        request_handle:logInfo(string.format(
            "Normal forward | current_index=%d → target=%s | client=%s | hops=%s",
            current_index, target_hop, client_str, hops_str
        ))
    end

    -- 5. 执行转发（修改Host头）
    if target_hop ~= BUSINESS_RULE.EMPTY_VALUE then
        request_handle:headers():set(HEADER_CONST.HOST, target_hop)
    else
        request_handle:logErr(string.format(
            "No valid target hop | client=%s | hops=%s | current_index=%d",
            client_str, hops_str, current_index
        ))
        request_handle:respond({[HEADER_CONST.STATUS] = "400"}, "No valid target hop")
        return
    end

    -- 6. 更新Index Header（传给下一跳）
    request_handle:headers():set(HEADER_CONST.INDEX, tostring(new_index))

    -- 7. 持久化关键信息到Metadata（核心：存入current_index，而非new_index）
    request_handle:streamInfo():setMetadata(METADATA_NS, "hops", hops_str)
    request_handle:streamInfo():setMetadata(METADATA_NS, "client", client_str)
    request_handle:streamInfo():setMetadata(METADATA_NS, "current_index", tostring(current_index)) -- 精准记录本次索引
    request_handle:logInfo(string.format(
        "Request processed | client=%s | hops=%s | current_index=%d | new_index=%d",
        client_str, hops_str, current_index, new_index
    ))
end

-- ==============================================
-- 响应阶段（核心：纯透传，日志带上current_index追溯链路）
-- ==============================================
function envoy_on_response(response_handle)
    -- 1. 从Metadata读取请求阶段的关键信息（含current_index）
    local hops_str = response_handle:streamInfo():metadata():get(METADATA_NS, "hops") or BUSINESS_RULE.EMPTY_VALUE
    local client_str = response_handle:streamInfo():metadata():get(METADATA_NS, "client") or BUSINESS_RULE.EMPTY_VALUE
    local current_index = response_handle:streamInfo():metadata():get(METADATA_NS, "current_index") or BUSINESS_RULE.INIT_INDEX -- 新增

    -- 2. 读取响应状态码和S3排查字段（仅日志用）
    local status_code = tostring(response_handle:responseCode() or "")
    local s3_request_id = response_handle:headers():get("x-amz-request-id") or "unknown"
    local s3_host = response_handle:headers():get("Host") or "unknown"

    -- 3. 分级日志记录（补充current_index，精准追溯）
    local log_msg = string.format(
        "Response pass-through | status=%s | s3_request_id=%s | s3_host=%s | client=%s | hops=%s | current_index=%s",
        status_code, s3_request_id, s3_host, client_str, hops_str, current_index
    )

    -- 按状态码分级日志（便于告警，不影响透传）
    if status_code == "" then
        response_handle:logWarn(log_msg .. " (unknown status code)")
    elseif string.sub(status_code, 1, 1) == "4" then
        response_handle:logWarn(log_msg)
    elseif string.sub(status_code, 1, 1) == "5" then
        response_handle:logErr(log_msg)
    else
        response_handle:logInfo(log_msg)
    end

    -- 核心：无任何修改逻辑，响应原封不动透传
end
EOF

# --------------------------
# 8. 设置文件权限
# --------------------------
chown "${OWNER}" "${ENVOY_CONFIG}"
chown "${OWNER}" "${LUA_SCRIPT_PATH}"
chmod 644 "${ENVOY_CONFIG}"
chmod 644 "${LUA_SCRIPT_PATH}"

# --------------------------
# 9. 完成提示
# --------------------------
echo -e "\n✅ Envoy 安装配置全部完成！"
echo -e "📌 关键文件路径："
echo -e "  - Envoy 二进制：${ENVOY_BIN}"
echo -e "  - 配置文件：${ENVOY_CONFIG}"
echo -e "  - Lua 脚本：${LUA_SCRIPT_PATH}"
echo -e "  - Admin 日志：$(dirname ${ENVOY_CONFIG})/admin_access.log"
echo -e "  - 性能分析目录：${PROFILE_DIR}"
echo -e "⚠️  请通过 Go 程序启动 Envoy（启动命令参考：${ENVOY_BIN} -c ${ENVOY_CONFIG}）"