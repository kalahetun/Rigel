package collector

import (
	"data-plane/pkg/local_info_report"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// GetEnvoyBufferConfig 从Envoy 9901端口读取缓冲配置（max_connections/单连接缓冲上限）
func GetEnvoyBufferConfig(adminAddr string) (*local_info_report.EnvoyBufferStats, error) {
	// 1. 构造配置dump请求URL（获取静态配置）
	url := fmt.Sprintf("http://%s/config_dump", adminAddr)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("请求配置dump失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("配置dump返回非200状态码: %d", resp.StatusCode)
	}

	// 2. 解析配置dump响应
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取配置dump响应失败: %w", err)
	}

	var configDump map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &configDump); err != nil {
		return nil, fmt.Errorf("解析配置dump JSON失败: %w", err)
	}

	// 3. 提取http_connection_manager配置（针对8095端口监听器）
	stats := &local_info_report.EnvoyBufferStats{}
	found := false

	// 遍历静态资源配置
	staticResources, ok := configDump["static_resources"].(map[string]interface{})
	if !ok {
		return nil, errors.New("配置dump中未找到static_resources")
	}

	listeners, ok := staticResources["listeners"].([]interface{})
	if !ok {
		return nil, errors.New("配置dump中未找到listeners")
	}

	for _, listener := range listeners {
		listenerMap, ok := listener.(map[string]interface{})
		if !ok {
			continue
		}

		// 匹配8095端口监听器（name: listener_8095）
		if listenerMap["name"] != "listener_8095" {
			continue
		}

		// 遍历filter_chains → http_connection_manager
		filterChains, ok := listenerMap["filter_chains"].([]interface{})
		if !ok {
			continue
		}

		for _, filterChain := range filterChains {
			filters, ok := filterChain.(map[string]interface{})["filters"].([]interface{})
			if !ok {
				continue
			}

			for _, filter := range filters {
				filterMap, ok := filter.(map[string]interface{})
				if !ok || filterMap["name"] != "envoy.filters.network.http_connection_manager" {
					continue
				}

				// 提取typed_config
				typedConfig, ok := filterMap["typed_config"].(map[string]interface{})
				if !ok {
					continue
				}

				// 4. 提取max_connections
				if maxConnVal, ok := typedConfig["max_connections"]; ok {
					stats.MaxConnections, err = strconv.ParseInt(fmt.Sprintf("%v", maxConnVal), 10, 64)
					if err != nil {
						return nil, fmt.Errorf("解析max_connections失败: %w", err)
					}
				} else {
					return nil, errors.New("未找到max_connections配置")
				}

				// 5. 提取per_connection_buffer_limit_bytes
				if perConnBufVal, ok := typedConfig["per_connection_buffer_limit_bytes"]; ok {
					stats.PerConnBufferLimitBytes, err = strconv.ParseInt(fmt.Sprintf("%v", perConnBufVal), 10, 64)
					if err != nil {
						return nil, fmt.Errorf("解析per_connection_buffer_limit_bytes失败: %w", err)
					}
				} else {
					return nil, errors.New("未找到per_connection_buffer_limit_bytes配置")
				}

				// 6. 提取per_stream_buffer_limit_bytes
				if perStreamBufVal, ok := typedConfig["per_stream_buffer_limit_bytes"]; ok {
					stats.PerStreamBufferLimitBytes, err = strconv.ParseInt(fmt.Sprintf("%v", perStreamBufVal), 10, 64)
					if err != nil {
						return nil, fmt.Errorf("解析per_stream_buffer_limit_bytes失败: %w", err)
					}
				} else {
					return nil, errors.New("未找到per_stream_buffer_limit_bytes配置")
				}

				// 计算全局缓冲上限
				stats.GlobalBufferLimitBytes = stats.MaxConnections * stats.PerConnBufferLimitBytes
				found = true
				break
			}
			if found {
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return nil, errors.New("未找到8095端口监听器的http_connection_manager配置")
	}

	return stats, nil
}

// GetEnvoyBufferUsedStats 从Envoy 9901端口读取已用缓冲内存（实时指标）
func GetEnvoyBufferUsedStats(adminAddr string, stats *local_info_report.EnvoyBufferStats) error {
	// 1. 构造stats请求URL（获取实时指标）
	url := fmt.Sprintf("http://%s/stats", adminAddr)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("请求stats失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stats返回非200状态码: %d", resp.StatusCode)
	}

	// 2. 读取并解析stats响应
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取stats响应失败: %w", err)
	}

	statsStr := string(bodyBytes)
	lines := strings.Split(statsStr, "\n")

	// 3. 提取8095端口的全局已用缓冲字节数（ingress_http_8095_buffered_bytes）
	found := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "envoy_http_connection_manager_ingress_http_8095_buffered_bytes:") {
			parts := strings.Split(line, ":")
			if len(parts) != 2 {
				continue
			}
			usedBytes, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
			if err != nil {
				return fmt.Errorf("解析已用缓冲字节数失败: %w", err)
			}
			stats.GlobalBufferUsedBytes = usedBytes
			// 计算使用率
			if stats.GlobalBufferLimitBytes > 0 {
				stats.GlobalBufferUsedPercent = float64(stats.GlobalBufferUsedBytes) / float64(stats.GlobalBufferLimitBytes) * 100
			}
			found = true
			break
		}
	}

	if !found {
		return errors.New("未找到ingress_http_8095_buffered_bytes指标")
	}

	return nil
}

// GetEnvoyFullBufferStats 一站式获取缓冲配置+已用指标
func GetEnvoyFullBufferStats(adminAddr string) (*local_info_report.EnvoyBufferStats, error) {
	// 1. 获取静态配置
	stats, err := GetEnvoyBufferConfig(adminAddr)
	if err != nil {
		return nil, fmt.Errorf("获取配置失败: %w", err)
	}

	// 2. 获取实时已用指标
	if err := GetEnvoyBufferUsedStats(adminAddr, stats); err != nil {
		return nil, fmt.Errorf("获取已用指标失败: %w", err)
	}

	return stats, nil
}
