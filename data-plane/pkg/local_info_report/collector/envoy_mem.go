package collector

import (
	"bufio"
	model "data-plane/pkg/local_info_report"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

func GetEnvoyFullBufferStats(logger *slog.Logger) model.EnvoyBufferStats {
	// Envoy stats地址（固定9901端口）
	statsURL := "http://127.0.0.1:9901/stats"

	// 1. 获取stats数据
	stats, err := GetEnvoyStats(statsURL)
	if err != nil {
		logger.Error("\033[31m获取Envoy统计信息失败：%v\033[0m\n", err)
		return model.EnvoyBufferStats{}
	}

	// 2. 解析统计指标
	ebs := ParseEnvoyBufferStats(stats)
	PrintEnvoyBufferReport(ebs, logger)
	return ebs
}

// GetEnvoyStats 从Envoy的9901端口获取统计指标
func GetEnvoyStats(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("请求Envoy stats失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Envoy stats返回状态码异常: %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var statsBuilder strings.Builder
	for scanner.Scan() {
		statsBuilder.WriteString(scanner.Text() + "\n")
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取Envoy stats响应失败: %v", err)
	}

	return statsBuilder.String(), nil
}

// ParseEnvoyBufferStats 解析Envoy缓冲统计指标（仅处理字节维度）
func ParseEnvoyBufferStats(stats string) model.EnvoyBufferStats {
	ebs := model.EnvoyBufferStats{}

	// 1. 解析全局缓冲总和（匹配所有bytes_buffered指标，仅累加字节数）
	bufferRegex := regexp.MustCompile(`(\d+)\s+bytes_buffered`)
	matches := bufferRegex.FindAllStringSubmatch(stats, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		num, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil {
			ebs.TotalBuffer += num
		}
	}

	// 2. 解析8095端口活跃连接数（保留原始值+强制转数字）
	connRegex := regexp.MustCompile(`ingress_http_8095\.connections\.active\s+(\d+)`)
	connMatch := connRegex.FindStringSubmatch(stats)
	if len(connMatch) >= 2 {
		ebs.ActiveConnRaw = connMatch[1]
		num, err := strconv.ParseInt(connMatch[1], 10, 64)
		if err == nil {
			ebs.ActiveConn = num
		}
	} else {
		ebs.ActiveConnRaw = "空"
		ebs.ActiveConn = 0 // 空值强制转0
	}

	// 3. 计算单连接缓冲均值（仅字节，避免除以0）
	if ebs.ActiveConn > 0 {
		ebs.PerConnBuffer = ebs.TotalBuffer / ebs.ActiveConn
	}

	return ebs
}

// PrintEnvoyBufferReport 打印缓冲统计报告（仅字节维度）
func PrintEnvoyBufferReport(ebs model.EnvoyBufferStats, logger *slog.Logger) {
	logger.Info("\033[32m===== 单连接缓冲均值计算 =====\033[0m")
	logger.Info("全局缓冲总和：%d 字节\n", ebs.TotalBuffer)
	logger.Info("8095端口活跃连接数：%d 个（原始值：%s）\n", ebs.ActiveConn, ebs.ActiveConnRaw)

	if ebs.ActiveConn == 0 {
		logger.Info("\033[33mℹ️  当前无活跃连接，单连接缓冲均值：0 字节\033[0m")
		return
	}

	logger.Info("单连接缓冲均值：%d 字节\n", ebs.PerConnBuffer)
	// 阻塞判断（64KB=65536字节阈值）
	if ebs.PerConnBuffer > 65536 {
		logger.Info("\033[31m⚠️  单连接缓冲超标：均值＞64KB（65536字节），大概率有连接堆积阻塞\033[0m")
	} else {
		logger.Info("\033[32m✅  单连接缓冲正常：均值≤64KB（65536字节）\033[0m")
	}

	// 额外：打印JSON格式结果（便于集成）
	jsonData, err := json.MarshalIndent(ebs, "", "  ")
	if err == nil {
		logger.Info("\n\033[36m===== JSON格式统计结果 =====\033[0m")
		logger.Info(string(jsonData))
	}
}
