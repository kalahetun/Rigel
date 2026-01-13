package virtual_queue

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	WarningLevelforBuffer  = 0.6
	CriticalLevelforBuffer = 0.8
)

// 当前正在“真实转发数据”的请求数
var ActiveTransfers int64

func getTotalMem(logger *slog.Logger) (int64, error) {
	// Linux: 从 /proc/meminfo 获取总内存
	out, err := exec.Command("grep", "MemTotal", "/proc/meminfo").Output()
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected meminfo format")
	}
	kb, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return kb * 1024, nil // 转为 bytes
}

type ProxyStatus struct {
	ActiveConnections int64   `json:"active_connections"` // 当前活跃连接数
	TotalMem          int64   `json:"total_mem"`          // 机器总内存（字节）
	ProcessMem        int64   `json:"process_mem"`        // 当前进程使用内存（字节）
	AvgCachePerConn   float64 `json:"avg_cache_per_conn"` // 平均每连接缓存大小（字节）
	CacheUsageRatio   float64 `json:"cache_usage_ratio"`  // 缓存使用比例 [0,1]
}

// checkCongestion 检查系统是否处于拥堵状态，并返回平均每连接的内存使用量
// 参数:
//   - logger: 用于记录日志的对象
//
// 返回值:
//   - float64: 平均每连接的内存使用量，如果未检测到拥堵则返回0
func CheckCongestion(allBufferSize int, logger *slog.Logger) ProxyStatus {

	s := ProxyStatus{}
	// 获取系统总内存大小
	totalMem, err := getTotalMem(logger)
	if err != nil {
		logger.Error("Failed to get total memory:", err)
		return s
	}
	s.TotalMem = totalMem

	// 获取 proxy 进程内存，Linux 下用 ps
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		logger.Error("Failed to get proxy memory:", err)
		return s
	}
	rssKb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		logger.Error("Failed to parse proxy memory:", err)
		return s
	}
	proxyMem := rssKb * 1024
	s.ProcessMem = proxyMem

	usageRatio := float64(proxyMem) / float64(totalMem)
	logger.Info("Proxy memory: %v MiB, Total memory: %v MiB, Ratio: %.2f%%\n",
		proxyMem/1024/1024, totalMem/1024/1024, usageRatio*100)

	if usageRatio > WarningLevelforBuffer {
		perConnCache := allBufferSize * 1024 // 每连接 128 KB
		active := atomic.LoadInt64(&ActiveTransfers)
		if active <= 0 {
			return s
		}
		s.ActiveConnections = active

		avgCache := float64(proxyMem) / float64(active)
		logger.Info("Active connections: %d, Average per-connection memory: %.2f KB\n",
			active, avgCache/1024)
		s.AvgCachePerConn = avgCache
		s.CacheUsageRatio = float64(avgCache) / float64(perConnCache)

		if s.CacheUsageRatio > CriticalLevelforBuffer {
			logger.Warn("Potential congestion: average per-connection buffer near 128KB")
		}

		logger.Info("Proxy status: %+v", s)

		return s
	}
	return s
}
