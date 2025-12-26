package envoy_manager

import (
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
)

// EnvoyYamlTemplate Envoy config template (v1.28.0)
const EnvoyYamlTemplate = `
admin:
  address:
    socket_address:
      address: 0.0.0.0
      port_value: {{.AdminPort}}
  access_log_path: "${ENVOY_HOME}/admin_access.log"
  profile_path: "${ENVOY_HOME}/profile"
# 启用Lua扩展支持（必需）
layered_runtime:
  layers:
    - name: static_layer_0
      static_layer:
        envoy:
          lua:
            allow_dynamic_loading: true
            enable_resty: true
static_resources:
  listeners:
{{range .Ports}}{{if .Enabled}}
  - name: listener_{{.Port}}
    address:
      socket_address:
        address: 0.0.0.0
        port_value: {{.Port}}
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: ingress_http_{{.Port}}
          route_config:
            name: local_route_{{.Port}}
            virtual_hosts:
            - name: local_service_{{.Port}}
              domains: ["*"]
              routes:
              - match:
                  prefix: "/"
                route:
                  cluster: target_cluster
          http_filters:
          # 核心修改：引用独立Lua文件（替代内联）
          - name: envoy.filters.http.lua
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua
              source_codes:
                access_router.lua:  # 脚本名称（自定义）
                  filename: "/home/matth/access_router.lua"  # 独立Lua文件路径
          # 保留原有router过滤器
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
{{end}}{{end}}
  clusters:
  - name: target_cluster
    connect_timeout: 0.25s
    type: STATIC
    lb_policy: ROUND_ROBIN
    # ========== 关键修改1：健康检查配置移到集群的 health_checks 节点 ==========
    health_checks:
      - timeout: 1s
        interval: 5s
        unhealthy_threshold: 2
        healthy_threshold: 2
        http_health_check:
          path: /health
          port_value: 8082
          host: "{{index .TargetAddrs 0 .IP}}"  # 取第一个目标IP作为健康检查主机
    # ========== 关键修改2：endpoint 下仅保留地址配置，删除非法的 health_check_config ==========
    load_assignment:
      cluster_name: target_cluster
      endpoints:
      - lb_endpoints:
        {{range .TargetAddrs}}
        - endpoint:
            address:
              socket_address:
                address: {{.IP}}
                port_value: {{.Port}}
        {{end}}
`

// RenderEnvoyYamlConfig 渲染Envoy YAML配置文件到matth目录
func RenderEnvoyYamlConfig(cfg *EnvoyGlobalConfig, outputPath string) error {
	// 解析模板
	tpl, err := template.New("envoy_config").Parse(EnvoyYamlTemplate)
	if err != nil {
		return err
	}

	// 创建/覆盖配置文件（matth目录有读写权限）
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// 渲染模板并写入文件
	if err := tpl.Execute(file, cfg); err != nil {
		return err
	}

	// 验证YAML格式（增强鲁棒性）
	var validate map[string]interface{}
	yamlFile, err := os.ReadFile(outputPath)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(yamlFile, &validate)
}

//- name: envoy.filters.network.bandwidth_limit
//  typed_config:
//    "@type": type.googleapis.com/envoy.extensions.filters.network.bandwidth_limit.v3.BandwidthLimit
//    stat_prefix: bandwidth_limit_{{.Port}}
//    max_download_bandwidth: {{.RateLimit.Bandwidth}}
//    max_upload_bandwidth: {{.RateLimit.Bandwidth}}
