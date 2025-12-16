package envoymanager

// APICommonResp 通用API返回结构
type APICommonResp struct {
	Code    int         `json:"code"`    // 状态码 0成功/非0失败
	Message string      `json:"message"` // 提示信息
	Data    interface{} `json:"data"`    // 业务数据
}

// PortRateLimitConfig 端口维度限流配置
type PortRateLimitConfig struct {
	Enabled   bool     `json:"enabled"`   // 是否开启限流
	QPS       int64    `json:"qps"`       // 每秒请求数限制
	Burst     int64    `json:"burst"`     // 突发请求数
	Whitelist []string `json:"whitelist"` // 白名单IP
	Bandwidth int64    `json:"bandwidth"` // 带宽限制 单位 Bytes/s, 1 Mbps = 125000 Bytes/s
}

// EnvoyPortConfig Envoy端口配置
type EnvoyPortConfig struct {
	Port       int                 `json:"port"`        // 监听端口
	TargetPort int                 `json:"target_port"` // 转发目标端口
	Enabled    bool                `json:"enabled"`     // 是否启用
	RateLimit  PortRateLimitConfig `json:"rate_limit"`  // 限流配置
}

// EnvoyGlobalConfig Envoy全局配置
type EnvoyGlobalConfig struct {
	AdminPort int               `json:"admin_port"` // 管理端口（如9901）
	Ports     []EnvoyPortConfig `json:"ports"`      // 所有端口配置
}

// EnvoyPortCreateReq 创建Envoy端口请求
type EnvoyPortCreateReq struct {
	Port       int                 `json:"port" binding:"required,min=1,max=65535"`        // 监听端口
	TargetPort int                 `json:"target_port" binding:"required,min=1,max=65535"` // 转发端口
	RateLimit  PortRateLimitConfig `json:"rate_limit"`                                     // 限流配置
}

// EnvoyPortDisableReq 禁用Envoy端口请求
type EnvoyPortDisableReq struct {
	Port int `json:"port" binding:"required,min=1,max=65535"` // 要禁用的端口
}
