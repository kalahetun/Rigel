package envoymanager

import (
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
)

// EnvoyYamlTemplate Envoy配置模板（适配1.28.0，兼容matth目录）
const EnvoyYamlTemplate = `
admin:
  address:
    socket_address:
      address: 0.0.0.0
      port_value: {{.AdminPort}}
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
                  cluster: target_cluster_{{.Port}}
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
{{end}}{{end}}
  clusters:
{{range .Ports}}{{if .Enabled}}
  - name: target_cluster_{{.Port}}
    connect_timeout: 0.25s
    type: STATIC
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: target_cluster_{{.Port}}
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: 127.0.0.1
                port_value: {{.TargetPort}}
{{end}}{{end}}
`

// RenderEnvoyYamlConfig 渲染Envoy YAML配置文件到matth目录
func RenderEnvoyYamlConfig(cfg EnvoyGlobalConfig, outputPath string) error {
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
