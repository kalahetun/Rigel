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
                        route_hops.lua:
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
#x-index: 1                   # 固定值 2
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
#x-index: 1                   # 固定值 2（指向 B 节点）
#x-proxy-type: multi          # 多代理标记
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
-- Envoy Lua Filter: hops dynamic routing + S3 ACK reverse (HTTP/1.1)
-- ==============================================
-- 通用常量定义（单/多代理统一，支持N跳）
-- ==============================================
local HEADER_CONST = {
    HOPS = "x-hops",          -- 转发链：N跳=A1,A2,...An,S3；单代理=S3
    INDEX = "x-index",        -- 游标索引（初始=2）
    HOST = "Host",            -- 转发核心Header
    STATUS = ":status",       -- 响应状态码
    PROXY_TYPE = "x-proxy-type" -- 代理类型：multi/single
}

local BUSINESS_RULE = {
    S3_ACK_SUCCESS_STATUS = "200",  -- S3合法ACK状态码
    EMPTY_VALUE = "",               -- 空值兜底
    SEPARATOR = ",",                -- hops分隔符
    INIT_INDEX = "1",               -- 去程/返程统一初始index=1
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
-- 示例1：A,B,S3 → B,A；示例2：A,B,C,S3 → C,B,A；示例3：A, S3 → A
local function reverse_hops(hops_arr, proxy_type)
    local reversed = {}
    local arr_len = #hops_arr

    -- 多代理场景（N跳）：剔除S3，翻转剩余代理链
    if proxy_type == BUSINESS_RULE.MULTI_PROXY_FLAG and arr_len >= 2 then
        -- 遍历范围：1 ~ arr_len-1（剔除最后一个元素S3）
        for i = arr_len - 1, 1, -1 do
            table.insert(reversed, hops_arr[i])
        end
    -- 单代理场景：保留唯一节点A
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
    local proxy_type = request_handle:headers():get(HEADER_CONST.PROXY_TYPE) or BUSINESS_RULE.EMPTY_VALUE
    local client_str = response_handle:headers():get(HEADER_CONST.CLIENT) or BUSINESS_RULE.EMPTY_VALUE

    -- 2. 格式转换
    local hops_arr = split_str(hops_str, BUSINESS_RULE.SEPARATOR)
    local current_index = tonumber(index_str) or tonumber(BUSINESS_RULE.INIT_INDEX)
    local hops_len = #hops_arr

    -- 3. 空hops拒绝转发
    if hops_len == 0 then
       request_handle:logErr(string.format(
           "Missing x-hops header, reject forwarding, hops=%s, client=%s",
           hops_str,  -- 第一个%s的占位值
           client_str -- 第二个%s的占位值
       ))
        request_handle:respond({[HEADER_CONST.STATUS] = "400"}, "Missing x-hops header")
        return
    end

    -- 4. 计算转发目标（支持N跳，index=2 兼容）
    local target_hop = BUSINESS_RULE.EMPTY_VALUE
    local new_index = current_index + 1

    -- 正常转发：index < hops长度 → 取hops[index]
    if new_index <= hops_len then
        target_hop = hops_arr[new_index]
        request_handle:logInfo(string.format(
            "Normal forward: proxy_type=%s, index=%d → target=%s, new_index=%d",
            proxy_type, new_index, target_hop, new_index
        ))
    end

    -- 5. 执行转发（修改Host头）
    if target_hop ~= BUSINESS_RULE.EMPTY_VALUE then
        request_handle:headers():set(HEADER_CONST.HOST, target_hop)
    else
        request_handle:logErr(string.format(
            "No valid target hop, reject forwarding, hops=%s, client=%s",
            hops_str,  -- 第一个%s的占位值
            client_str -- 第二个%s的占位值
        ))
        request_handle:respond({[HEADER_CONST.STATUS] = "400"}, "No valid target hop")
        return
    end

    -- 6. 更新Header（传给下一跳）
    request_handle:headers():set(HEADER_CONST.INDEX, tostring(new_index))

    -- 7. 标记是否为最后一跳（上下文传递）
    local is_last_hop = (new_index + 1 >= hops_len)
    request_handle:streamInfo():setMetadata("hop_router", "is_last_hop", tostring(is_last_hop))
    request_handle:logInfo(string.format(
        "Request processed: proxy_type=%s, is_last_hop=%s, hops=%s, client=%s",
        proxy_type, tostring(is_last_hop), hops_str, client_str
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

    local status_code_num = response_handle:responseCode()
    local status_code = status_code_num and tostring(status_code_num) or BUSINESS_RULE.EMPTY_VALUE

    local hops_str = response_handle:headers():get(HEADER_CONST.HOPS) or BUSINESS_RULE.EMPTY_VALUE
    local client_str = response_handle:headers():get(HEADER_CONST.CLIENT) or BUSINESS_RULE.EMPTY_VALUE

    -- 非最后一跳/非200 ACK → 直接透传
    -- 场景1：非最后一跳 → 普通INFO日志，直接透传（独立if）
    if not is_last_hop then
        response_handle:logInfo(string.format(
            "Skip reverse: non-last-hop, is_last_hop=%s, status=%s, proxy_type=%s, hops=%s, client=%s",
            tostring(is_last_hop), status_code, proxy_type, hops_str, client_str
        ))
        return
    end

    -- 场景2：【最后一跳 + 非200】→ ERROR/WARN级日志，直接透传（独立if，必须加is_last_hop条件）
    if is_last_hop and status_code ~= BUSINESS_RULE.S3_ACK_SUCCESS_STATUS then
        -- 补充S3核心排查字段
        local s3_request_id = response_handle:headers():get("x-amz-request-id") or "unknown"
        local s3_host = response_handle:headers():get("Host") or "unknown"
        local hops_str = response_handle:headers():get(HEADER_CONST.HOPS) or BUSINESS_RULE.EMPTY_VALUE
        local client_str = response_handle:headers():get(HEADER_CONST.CLIENT) or BUSINESS_RULE.EMPTY_VALUE
        local log_msg = string.format(
            "S3 response error (last hop): is_last_hop=%s, s3_status=%s, s3_request_id=%s, s3_host=%s, proxy_type=%s, hops=%s, client=%s",
            tostring(is_last_hop), status_code, s3_request_id, s3_host, proxy_type, hops_str, client_str
        )

        -- 细分级别：4xx客户端错误打WARN，5xx服务端错误打ERROR
        if string.sub(status_code, 1, 1) == "4" then
            response_handle:logWarn(log_msg)
        else
            response_handle:logErr(log_msg)
        end
        return
    end

    -- 能走到这里的条件：是最后一跳 + status_code == 200 → 执行返程逻辑
    response_handle:logInfo(string.format(
        "Start reverse routing: last hop confirmed, S3 ACK 200, proxy_type=%s, hops=%s, client=%s",
        proxy_type, hops_str, client_str
    ))
    -- 后续写返程逻辑（翻转hops、重置index、修改Host等）

    -- 2. 解析并翻转hops（支持N跳）
    local hops_str = response_handle:headers():get(HEADER_CONST.HOPS) or BUSINESS_RULE.EMPTY_VALUE
    local hops_arr = split_str(hops_str, BUSINESS_RULE.SEPARATOR)
    local reversed_hops_arr = reverse_hops(hops_arr, proxy_type)
    local reversed_hops_str = join_arr(reversed_hops_arr, BUSINESS_RULE.SEPARATOR)

    response_handle:logInfo(string.format(
        "reversed_hops_str, reversed_hops=%s, hops=%s, client=%s",
        reversed_hops_str, hops_str, client_str
    ))

    response_handle:headers():set(HEADER_CONST.HOPS, reversed_hops_str)
    local target_hop = BUSINESS_RULE.EMPTY_VALUE
    local new_index =  0
    response_handle:headers():set(HEADER_CONST.HOST, client_str)
    response_handle:headers():set(HEADER_CONST.INDEX, new_index)

    -- 5. 日志记录
    response_handle:logInfo(string.format(
        "Reverse success: proxy_type=%s, original_hops=%s → reversed_hops=%s, index=%s, new_index=%s, target_hop=%s",
        proxy_type, hops_str, reversed_hops_str, BUSINESS_RULE.INIT_INDEX, new_index, target_hop
    ))
end
EOF

# --------------------------
# 8. 设置文件权限
# --------------------------
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