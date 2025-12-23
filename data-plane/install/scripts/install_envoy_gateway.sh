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
LUA_SCRIPT_PATH="${ENVOY_HOME}/hop_router.lua"  # Lua script in same directory as config
PROFILE_DIR="$(dirname ${ENVOY_CONFIG})/profile"

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
chown matth:matth "${ENVOY_BIN}"

echo "✅ Envoy 版本验证："
"${ENVOY_BIN}" --version

# --------------------------
# 5. 创建 profile 目录（避免Admin报错）
# --------------------------
mkdir -p "${PROFILE_DIR}"
chown matth:matth "${PROFILE_DIR}"
chmod 755 "${PROFILE_DIR}"

# --------------------------
# 6. 生成最小配置
# --------------------------
echo "📝 生成 Envoy 配置文件 ${ENVOY_CONFIG}..."
cat > "${ENVOY_CONFIG}" << EOF
# Envoy configuration: 8M file forwarding + dynamic hops routing + port 8095
admin:
  address:
    socket_address:
      address: 127.0.0.1
      port_value: 9901
  access_log_path: "$(dirname ${ENVOY_CONFIG})/admin_access.log"
  profile_path: "${PROFILE_DIR}"

static_resources:
  listeners:
    # Business listener port: 8095
    - name: listener_8095
      address:
        socket_address:
          address: 0.0.0.0
          port_value: 8095
      filter_chains:
        - filters:
            # HTTP connection manager (HTTP/1.1 core config)
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                codec_type: HTTP1.1
                stat_prefix: ingress_http_8095
                common_http_protocol_options:
                  idle_timeout: 300s
                stream_idle_timeout: 300s
                # 核心新增：限制最大并发连接数（实现全局缓冲≈1GB）
                max_connections: 8192  # 128KB/连接 × 8192连接 = 1GB 全局缓冲上限
                # Route config (bind to dummy cluster for syntax validity)
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
                # Buffer config (adapt to 8M file transfer) - 修正注释+逻辑
                # buffer_pool_limit_bytes: 1073741824        # 1GB per connection buffer (deprecated, for compatibility)
                per_connection_buffer_limit_bytes: 131072  # 128KB per connection buffer (core limit)
                per_stream_buffer_limit_bytes: 65536       # 64KB per stream buffer (per request limit)
                # HTTP filter chain (Lua + Router)
                http_filters:
                  # Lua filter: handle hops routing & ACK reverse
                  - name: envoy.filters.http.lua
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua
                      script_path: "${LUA_SCRIPT_PATH}"  # Lua script in ENVOY_HOME
                      log_level: info
                  # Router filter: final forward
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  # Dummy cluster: config reuse, endpoint overwritten by Lua
  clusters:
    - name: dummy_cluster
      connect_timeout: 0.25s
      type: STRICT_DNS
      lb_policy: ROUND_ROBIN
      # HTTP/1.1 protocol config (core effective for large file)
      http_protocol_options:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http1_protocol_options:
            keep_alive:
              keep_alive_timeout: 300s
              keep_alive_interval: 10s
            max_request_header_kb: 16
      # Connection pool circuit breakers (support high concurrency)
      circuit_breakers:
        thresholds:
          - priority: DEFAULT
            max_connections: 2000
            max_pending_requests: 1000
            max_requests: 4000
      # Upstream idle connection management
      common_http_protocol_options:
        idle_timeout: 300s
      # Dummy endpoint (placeholder, overwritten by Lua Host header)
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
#x-hops: s3.example.com:80    # 最终目标：S3 的 IP/域名+Port
#x-index: 2                   # 固定值 2
#x-next-hop: s3.example.com:80 # 兜底 S3 地址
#x-proxy-type: single         # 单代理标记
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
#x-index: 2                   # 固定值 2（指向 B 节点）
#x-next-hop: 192.168.1.100:8080 # 兜底 B 节点地址
#x-proxy-type: multi          # 多代理标记
#
## 关键：Host 指向 A 节点的实际地址
#Host: 192.168.1.90:8080
#
## 通用 Headers
#Content-Type: application/json
#Accept: application/json

# --------------------------
# 7. 生成 Lua 脚本
# --------------------------
echo "📝 生成 Lua 脚本 ${LUA_SCRIPT_PATH}..."
cat > "${LUA_SCRIPT_PATH}" << EOF
-- Envoy Lua Filter: hops dynamic routing + S3 ACK reverse (HTTP/1.1)
-- ==============================================
-- 通用常量定义（单/多代理统一，支持N跳）
-- ==============================================
local HEADER_CONST = {
    HOPS = "x-hops",          -- 转发链：N跳=A1,A2,...An,S3；单代理=S3
    NEXT_HOP = "x-next-hop",  -- 兜底转发目标
    INDEX = "x-index",        -- 游标索引（初始=2）
    HOST = "Host",            -- 转发核心Header
    STATUS = ":status",       -- 响应状态码
    PROXY_TYPE = "x-proxy-type" -- 代理类型：multi/single
}

local BUSINESS_RULE = {
    S3_ACK_SUCCESS_STATUS = "200",  -- S3合法ACK状态码
    EMPTY_VALUE = "",               -- 空值兜底
    SEPARATOR = ",",                -- hops分隔符
    INIT_INDEX = "2",               -- 去程/返程统一初始index=2
    MULTI_PROXY_FLAG = "multi",     -- 多代理标记（支持N跳）
    SINGLE_PROXY_FLAG = "single"    -- 单代理标记
}

-- ==============================================
-- 通用工具函数（核心修复：支持N跳翻转）
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

-- 翻转hops（适配任意多跳代理）
-- 核心逻辑：剔除最后一个节点（S3），翻转剩余代理链
-- 示例1：A,B,S3 → B,A；示例2：A,B,C,S3 → C,B,A；示例3：S3 → S3
local function reverse_hops(hops_arr, proxy_type)
    local reversed = {}
    local arr_len = #hops_arr

    -- 多代理场景（N跳）：剔除S3，翻转剩余代理链
    if proxy_type == BUSINESS_RULE.MULTI_PROXY_FLAG and arr_len >= 2 then
        -- 遍历范围：1 ~ arr_len-1（剔除最后一个元素S3）
        for i = arr_len - 1, 1, -1 do
            table.insert(reversed, hops_arr[i])
        end
    -- 单代理场景：保留唯一节点S3
    elseif proxy_type == BUSINESS_RULE.SINGLE_PROXY_FLAG then
        if arr_len > 0 then
            table.insert(reversed, hops_arr[1])
        end
    end

    return reversed
end

-- 数组合并为字符串
local function join_arr(arr, sep)
    if #arr == 0 then
        return BUSINESS_RULE.EMPTY_VALUE
    end
    return table.concat(arr, sep)
end

-- ==============================================
-- 请求阶段（去程转发，支持N跳代理）
-- ==============================================
function envoy_on_request(request_handle)
    -- 1. 读取Header
    local hops_str = request_handle:headers():get(HEADER_CONST.HOPS) or BUSINESS_RULE.EMPTY_VALUE
    local index_str = request_handle:headers():get(HEADER_CONST.INDEX) or BUSINESS_RULE.INIT_INDEX
    local next_hop_str = request_handle:headers():get(HEADER_CONST.NEXT_HOP) or BUSINESS_RULE.EMPTY_VALUE
    local proxy_type = request_handle:headers():get(HEADER_CONST.PROXY_TYPE) or BUSINESS_RULE.EMPTY_VALUE

    -- 2. 格式转换
    local hops_arr = split_str(hops_str, BUSINESS_RULE.SEPARATOR)
    local current_index = tonumber(index_str) or tonumber(BUSINESS_RULE.INIT_INDEX)
    local hops_len = #hops_arr

    -- 3. 空hops拒绝转发
    if hops_len == 0 then
        request_handle:logErr("Missing x-hops header, reject forwarding")
        request_handle:respond({[HEADER_CONST.STATUS] = "400"}, "Missing x-hops header")
        return
    end

    -- 4. 计算转发目标（支持N跳，index=2 兼容）
    local target_hop = BUSINESS_RULE.EMPTY_VALUE
    local new_index = current_index
    local new_next_hop = BUSINESS_RULE.EMPTY_VALUE

    -- 正常转发：index < hops长度 → 取hops[index]
    if current_index < hops_len then
        target_hop = hops_arr[current_index]
        new_index = current_index + 1
        new_next_hop = new_index <= hops_len and hops_arr[new_index] or BUSINESS_RULE.EMPTY_VALUE
        request_handle:logInfo(string.format(
            "Normal forward: proxy_type=%s, index=%d → target=%s, new_index=%d",
            proxy_type, current_index, target_hop, new_index
        ))
    -- 兜底转发：index ≥ hops长度 → 取x-next-hop
    else
        target_hop = next_hop_str
        new_index = current_index + 1
        request_handle:logWarn(string.format(
            "Fallback forward: proxy_type=%s, index=%d ≥ hops_len=%d, use next-hop=%s",
            proxy_type, current_index, hops_len, target_hop
        ))
    end

    -- 5. 执行转发（修改Host头）
    if target_hop ~= BUSINESS_RULE.EMPTY_VALUE then
        request_handle:headers():set(HEADER_CONST.HOST, target_hop)
    else
        request_handle:logErr("No valid target hop, reject forwarding")
        request_handle:respond({[HEADER_CONST.STATUS] = "400"}, "No valid target hop")
        return
    end

    -- 6. 更新Header（传给下一跳）
    request_handle:headers():set(HEADER_CONST.INDEX, tostring(new_index))
    request_handle:headers():set(HEADER_CONST.NEXT_HOP, new_next_hop)

    -- 7. 标记是否为最后一跳（上下文传递）
    local is_last_hop = (new_index > hops_len)
    request_handle:streamInfo():setMetadata("hop_router", "is_last_hop", tostring(is_last_hop))
    request_handle:logInfo(string.format(
        "Request processed: proxy_type=%s, is_last_hop=%s",
        proxy_type, tostring(is_last_hop)
    ))
end

-- ==============================================
-- 响应阶段（返程处理，支持N跳代理）
-- ==============================================
function envoy_on_response(response_handle)
    -- 1. 读取上下文和Header
    local is_last_hop_str = response_handle:streamInfo():metadata():get("hop_router", "is_last_hop") or "false"
    local is_last_hop = (is_last_hop_str == "true")
    local proxy_type = response_handle:headers():get(HEADER_CONST.PROXY_TYPE) or BUSINESS_RULE.EMPTY_VALUE
    local status_code = response_handle:headers():get(HEADER_CONST.STATUS) or BUSINESS_RULE.EMPTY_VALUE

    -- 非最后一跳/非200 ACK → 直接透传
    if not is_last_hop or status_code ~= BUSINESS_RULE.S3_ACK_SUCCESS_STATUS then
        response_handle:logInfo(string.format(
            "Skip reverse: is_last_hop=%s, status=%s, proxy_type=%s",
            tostring(is_last_hop), status_code, proxy_type
        ))
        return
    end

    -- 2. 解析并翻转hops（支持N跳）
    local hops_str = response_handle:headers():get(HEADER_CONST.HOPS) or BUSINESS_RULE.EMPTY_VALUE
    local hops_arr = split_str(hops_str, BUSINESS_RULE.SEPARATOR)
    local reversed_hops_arr = reverse_hops(hops_arr, proxy_type)
    local reversed_hops_str = join_arr(reversed_hops_arr, BUSINESS_RULE.SEPARATOR)

    -- 3. 重置返程Header（统一index=2）
    response_handle:headers():set(HEADER_CONST.HOPS, reversed_hops_str)
    response_handle:headers():set(HEADER_CONST.INDEX, BUSINESS_RULE.INIT_INDEX)  -- 返程index=2
    -- 返程下一跳=翻转后第一个节点（兜底）
    local new_next_hop = #reversed_hops_arr > 0 and reversed_hops_arr[1] or BUSINESS_RULE.EMPTY_VALUE
    response_handle:headers():set(HEADER_CONST.NEXT_HOP, new_next_hop)

    -- 4. 多代理场景：强制设置Host转发到返程第一个节点
    if proxy_type == BUSINESS_RULE.MULTI_PROXY_FLAG and new_next_hop ~= BUSINESS_RULE.EMPTY_VALUE then
        response_handle:headers():set(HEADER_CONST.HOST, new_next_hop)
    end

    -- 5. 日志记录
    response_handle:logInfo(string.format(
        "Reverse success: proxy_type=%s, original_hops=%s → reversed_hops=%s, index=%s, next_hop=%s",
        proxy_type, hops_str, reversed_hops_str, BUSINESS_RULE.INIT_INDEX, new_next_hop
    ))
end
EOF

# --------------------------
# 8. 设置文件权限
# --------------------------
chown matth:matth "${ENVOY_CONFIG}"
chmod 644 "${ENVOY_CONFIG}"
chown matth:matth "${LUA_SCRIPT_PATH}"
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