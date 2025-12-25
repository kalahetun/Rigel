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
LUA_SCRIPT_PATH="${ENVOY_HOME}/access_router.lua"

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
chown 640 "${ENVOY_BIN}"

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

#curl http://127.0.0.1:8081/config/port_bandwidth
## 正确返回示例（端口可字符串/数字，值必须是数字）
#{"8095":10485760, 8096:5242880}

echo "📝 生成 Lua 脚本 ${LUA_SCRIPT_PATH}..."
cat > "${LUA_SCRIPT_PATH}" << EOF
-- ${ENVOY_HOME}/lua/port_bandwidth_limit.lua
-- 核心配置（调整为合理周期）
local CHECK_INTERVAL = 5                     -- 带宽统计周期：5秒（兼顾精度和性能）
local CONFIG_FETCH_INTERVAL = 10             -- 动态配置拉取周期：10秒（低频更新，降低开销）
local CONFIG_SERVER_URL = "http://127.0.0.1:8081/config/port_bandwidth"
local DEBUG_MODE = true                      -- 调试完成后建议关闭
local DEFAULT_BW_LIMIT = 10 * 1024 * 1024    -- 全局默认限流值：10MB/s（字节/秒）

-- 全局变量
local PORT_BANDWIDTH_LIMITS = {}  -- 存储从接口拉取的动态限流值
local port_in_stats = {}          -- 端口带宽统计

-- 核心1：使用 Envoy 原生 httpClient 拉取动态配置（替代 resty.http）
local function fetch_dynamic_config()
    -- Envoy 原生 HTTP 客户端（同步请求）
    local http_client = envoy.httpClient()
    local headers = {}
    headers[":method"] = "GET"
    headers[":path"] = "/config/port_bandwidth"
    headers[":authority"] = "127.0.0.1:8081"
    headers["Content-Type"] = "application/json"

    if DEBUG_MODE then
        print("[Lua-DEBUG] 尝试拉取动态限流配置：" .. CONFIG_SERVER_URL)
    end

    -- 发起同步 HTTP 请求（Envoy 原生 API）
    local response, err = http_client:send({
        url = CONFIG_SERVER_URL,
        headers = headers,
        timeout = 3000  -- 3秒超时（毫秒）
    })

    -- 校验请求结果
    if err then
        local err_msg = "配置接口访问失败：" .. err
        print("[Lua-ERROR] " .. err_msg)
        return nil, err_msg
    end
    if not response then
        local err_msg = "配置接口无响应"
        print("[Lua-ERROR] " .. err_msg)
        return nil, err_msg
    end
    if response.headers[":status"] ~= "200" then
        local err_msg = string.format("配置接口返回异常：状态码=%s", response.headers[":status"])
        print("[Lua-ERROR] " .. err_msg)
        return nil, err_msg
    end

    -- 读取响应体（Envoy 响应体是 table，需拼接）
    local response_body = ""
    for _, chunk in ipairs(response.body) do
        response_body = response_body .. chunk
    end
    if response_body == "" then
        local err_msg = "配置接口返回空响应体"
        print("[Lua-ERROR] " .. err_msg)
        return nil, err_msg
    end

    -- 解析 JSON（Envoy 内置 cjson）
    local ok, cjson = pcall(require, "cjson")
    if not ok then
        local err_msg = "依赖缺失：cjson库未找到（Envoy 需编译启用 cjson）"
        print("[Lua-ERROR] " .. err_msg)
        return nil, err_msg
    end

    local config, decode_err = cjson.decode(response_body)
    if not config then
        local err_msg = string.format("配置JSON解析失败：%s，原始内容=%s", decode_err, response_body)
        print("[Lua-ERROR] " .. err_msg)
        return nil, err_msg
    end
    if type(config) ~= "table" then
        local err_msg = string.format("配置格式错误：非JSON对象，原始内容=%s", response_body)
        print("[Lua-ERROR] " .. err_msg)
        return nil, err_msg
    end

    -- 格式化配置（数字端口:数字阈值）
    local formatted_config = {}
    for port_key, limit_val in pairs(config) do
        local port = tonumber(port_key)
        local limit = tonumber(limit_val)
        if port and limit and limit > 0 then
            formatted_config[port] = limit
            if DEBUG_MODE then
                print(string.format("[Lua-DEBUG] 加载端口%d自定义限流值：%d字节/秒（%.2fMB/s）",
                port, limit, limit/1024/1024))
            end
        else
            print(string.format("[Lua-WARN] 动态配置项无效：端口=%s，阈值=%s（需均为数字且阈值>0）", port_key, limit_val))
        end
    end

    -- 校验是否拉取到有效配置
    if next(formatted_config) == nil then
        local err_msg = string.format("配置接口返回无有效限流规则：%s", response_body)
        print("[Lua-ERROR] " .. err_msg)
        return nil, err_msg
    end

    return formatted_config, nil
end

-- 核心2：定时更新配置（保留你指定的 err 优先校验逻辑）
local function update_config_periodically()
    while true do
        local new_config, err = fetch_dynamic_config()

        -- 第一步：优先校验err（只要err非空，直接判定为失败）
        if err then
            PORT_BANDWIDTH_LIMITS = {}  -- 清空旧配置
            print(string.format("[Lua-WARN] 限流配置拉取失败，全局限流规则已清空，具体原因：%s", err))
        -- 第二步：err为空时，再校验new_config是否有效
        elseif new_config and next(new_config) ~= nil then
            PORT_BANDWIDTH_LIMITS = new_config
            -- 计算有效配置数量
            local config_count = 0
            for _ in pairs(PORT_BANDWIDTH_LIMITS) do
                config_count = config_count + 1
            end
            print(string.format("[Lua-INFO] 限流配置更新成功，共加载%d个端口规则", config_count))
        -- 第三步：err为空但new_config无效（空表）
        else
            PORT_BANDWIDTH_LIMITS = {}
            print("[Lua-WARN] 限流配置拉取成功，但无有效端口规则，全局限流规则已清空")
        end

        -- Envoy Lua 中使用 envoy.sleep 替代 ngx.sleep
        envoy.sleep(CONFIG_FETCH_INTERVAL)
    end
end

-- 核心3：获取端口的最终限流值（优先动态配置，无则默认10MB/s）
local function get_port_bw_limit(port)
    local dynamic_limit = PORT_BANDWIDTH_LIMITS[port]
    if dynamic_limit and dynamic_limit > 0 then
        return dynamic_limit
    end
    if DEBUG_MODE then
        print(string.format("[Lua-DEBUG] 端口%d无动态限流配置，使用默认值：10MB/s", port))
    end
    return DEFAULT_BW_LIMIT
end

-- 核心4：精准获取当前请求的端口
local function get_current_port(request_handle)
    local current_port = nil
    local ok, stream_info = pcall(function()
        return request_handle:streamInfo()
    end)
    if ok and stream_info then
        local ok2, listener_port = pcall(function()
            return stream_info:listenerAddress():getPortValue()
        end)
        if ok2 and listener_port then
            current_port = tonumber(listener_port)
        end
    end

    if DEBUG_MODE then
        print(string.format("[Lua-DEBUG] 当前请求的端口：%s", current_port or "获取失败"))
    end
    return current_port
end

-- 核心5：计算端口实时入带宽（调整为5秒统计周期）
local function calculate_port_in_bandwidth(request_handle, port)
    if not port_in_stats[port] then
        port_in_stats[port] = { last_bytes = 0, last_check_time = os.time() }
    end
    local stats = port_in_stats[port]

    -- 获取Envoy内置指标
    local stat_prefix = "ingress_http_" .. port
    local current_bytes = 0
    local ok, counter = pcall(function()
        return request_handle:stats():counter(stat_prefix .. ".downstream_rq_bytes_total")
    end)
    if ok and counter then
        current_bytes = counter:value()
    else
        print(string.format("[Lua-ERROR] 无法获取端口%d的带宽指标：%s", port, counter or "指标不存在"))
        return 0
    end

    -- 计算实时带宽（5秒统计一次）
    local now = os.time()
    local time_diff = now - stats.last_check_time
    local bandwidth = 0
    if time_diff >= CHECK_INTERVAL and time_diff > 0 then
        local byte_diff = current_bytes - stats.last_bytes
        bandwidth = byte_diff / time_diff  -- 字节/秒
        stats.last_bytes = current_bytes
        stats.last_check_time = now
        stats.last_bw = bandwidth
        if DEBUG_MODE then
            print(string.format("[Lua-DEBUG] 端口%d更新带宽统计：时间差=%d秒，累计字节差=%d，实时带宽=%.2fMB/s",
            port, time_diff, byte_diff, bandwidth/1024/1024))
        end
    else
        bandwidth = stats.last_bw or 0
        if DEBUG_MODE then
            print(string.format("[Lua-DEBUG] 端口%d未到统计周期（当前差%d秒），使用上次带宽值：%.2fMB/s",
            port, time_diff, bandwidth/1024/1024))
        end
    end

    return bandwidth
end

-- 核心6：请求限流逻辑
function envoy_on_request(request_handle)
    local current_port = get_current_port(request_handle)
    if not current_port then
        request_handle:logError("[Lua] 限流失败：无法识别当前请求的端口")
        return
    end

    local port_limit = get_port_bw_limit(current_port)
    local port_limit_mb = port_limit / 1024 / 1024

    local current_bw = calculate_port_in_bandwidth(request_handle, current_port)
    if current_bw <= 0 then
        if DEBUG_MODE then
            print(string.format("[Lua-DEBUG] 端口%d带宽计算异常：%d字节/秒", current_port, current_bw))
        end
        return
    end
    local current_bw_mb = current_bw / 1024 / 1024

    if current_bw > port_limit then
        request_handle:respond(
            {
                [":status"] = "503",
                "X-Limit-Type" = "Port In Bandwidth",
                "X-Current-Port" = tostring(current_port),
                "X-Current-BW" = string.format("%.2fMB/s", current_bw_mb),
                "X-Max-BW" = string.format("%.2fMB/s", port_limit_mb)
            },
            string.format("Port %d Bandwidth Limit Exceeded (Max: %.2fMB/s)", current_port, port_limit_mb)
        )
        request_handle:logError(string.format("[Lua] 端口%d限流触发：%.2fMB/s > %.2fMB/s",
        current_port, current_bw_mb, port_limit_mb))
        if DEBUG_MODE then
            print(string.format("[Lua-DEBUG] 端口%d触发限流：%.2fMB/s > %.2fMB/s",
            current_port, current_bw_mb, port_limit_mb))
        end
        return
    end

    if DEBUG_MODE then
        request_handle:logInfo(string.format("[Lua] 端口%d带宽正常：%.2fMB/s（上限：%.2fMB/s）",
        current_port, current_bw_mb, port_limit_mb))
    end
end

-- 响应阶段空实现
function envoy_on_response(response_handle)
end

-- 启动定时配置更新（Envoy Lua 中启动定时器）
local ok, err = pcall(function()
    -- Envoy Lua 中使用 envoy.timer 替代 ngx.timer.at
    envoy.timer.at(0, update_config_periodically)
end)
if not ok then
    print("[Lua-ERROR] 定时更新任务启动失败：" .. err)
    print("[Lua-INFO] 定时任务启动失败，所有端口将使用默认值10MB/s限流")
end
EOF

chown 644 "${ENVOY_CONFIG}"
chmod 644 "${LUA_SCRIPT_PATH}"

echo "✅ Envoy 安装完成！配置文件：${ENVOY_CONFIG}，二进制：${ENVOY_BIN}"
echo "⚠️ 请通过 Go 程序启动 Envoy"