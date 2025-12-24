package collector

import (
	model "data-plane/pkg/local_info_report"
	"log/slog"
	"os"
	"time"
)

// VMCollector 总采集器（整合所有子采集器）
type VMCollector struct{}

// NewVMCollector 初始化总采集器
func NewVMCollector() *VMCollector {
	return &VMCollector{}
}

// Collect 采集所有VM信息并组装为VMReport
func (c *VMCollector) Collect(logger *slog.Logger) (*model.VMReport, error) {
	// 1. 采集各维度信息
	cpuInfo, err := collectCPU()
	if err != nil {
		return nil, err
	}

	memoryInfo, err := collectMemory()
	if err != nil {
		return nil, err
	}

	diskInfo, err := collectDisk()
	if err != nil {
		return nil, err
	}

	networkInfo, err := collectNetwork()
	if err != nil {
		return nil, err
	}

	osInfo, err := collectOS()
	if err != nil {
		return nil, err
	}

	processInfo, err := collectProcess()
	if err != nil {
		return nil, err
	}

	hostname, _ := os.Hostname()

	// 一站式获取缓冲统计
	envoyMemInfo := GetEnvoyFullBufferStats(logger)

	// 2. 组装VMReport（ReportID由上报器生成，此处留空）
	return &model.VMReport{
		VMID:        "vm-" + hostname + "-001", // 固定VMID（可根据实际场景替换）
		CollectTime: time.Now().UTC(),
		ReportID:    "", // 上报时由服务端/上报器填充
		CPU:         cpuInfo,
		Memory:      memoryInfo,
		Disk:        diskInfo,
		Network:     networkInfo,
		OS:          osInfo,
		Process:     processInfo,
		EnvoyMem:    envoyMemInfo,
	}, nil
}
