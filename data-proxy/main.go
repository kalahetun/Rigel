package main

import (
	"crypto/tls"
	"data-proxy/config"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"
)

const (
	HeaderHops      = "x-hops"
	HeaderIndex     = "x-index"
	HeaderHost      = "Host"
	DefaultIndex    = "1"
	ServerErrorCode = 503
)

/*
 * =========================
 * 方案 B：数据路径级统计
 * =========================
 */

// 当前正在“真实转发数据”的请求数
var activeTransfers int64

// 统计 reader：包在 io.Copy 的数据路径上
type countingReader struct {
	r io.Reader
}

func (c *countingReader) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

//func (c *countingReader) Read(p []byte) (int, error) {
//	n, err := c.r.Read(p)
//	atomic.AddInt64(&bytesTransferred, int64(n))
//	return n, err
//}

// 拆分 x-hops 字符串
func splitHops(hopsStr string) []string {
	if hopsStr == "" {
		return []string{}
	}
	parts := strings.Split(hopsStr, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// handler 返回 http.HandlerFunc
func handler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hopsStr := r.Header.Get(HeaderHops)
		indexStr := r.Header.Get(HeaderIndex)
		if indexStr == "" {
			indexStr = DefaultIndex
		}

		hops := splitHops(hopsStr)
		currentIndex := 1
		if idx, err := strconv.Atoi(indexStr); err == nil {
			currentIndex = idx
		}
		hopsLen := len(hops)

		logger.Info("Received request",
			"hops", hops,
			"current_index", currentIndex,
			"method", r.Method,
			"path", r.URL.Path,
			"active_transfers", atomic.LoadInt64(&activeTransfers),
		)

		if hopsLen == 0 {
			http.Error(w, "Missing x-hops header", http.StatusBadRequest)
			logger.Warn("Missing x-hops header")
			return
		}

		newIndex := currentIndex + 1
		if newIndex > hopsLen {
			http.Error(w, "Forward index out of range", ServerErrorCode)
			logger.Warn("Forward index out of range",
				"new_index", newIndex,
				"hops_len", hopsLen,
			)
			return
		}

		targetHop := hops[newIndex-1]
		parts := strings.Split(targetHop, ":")
		if len(parts) != 2 {
			http.Error(w, "Invalid target hop format", http.StatusBadRequest)
			logger.Warn("Invalid target hop format", "target_hop", targetHop)
			return
		}
		targetIP := parts[0]
		targetPort := parts[1]

		scheme := "http"

		targetURL := scheme + "://" + targetIP + ":" + targetPort + r.URL.RequestURI()
		logger.Info("Forwarding to target", "target_url", targetURL)

		target := targetIP + ":" + targetPort
		client := getClient(target, scheme)

		req, err := http.NewRequest(r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, "Failed to create request", http.StatusInternalServerError)
			logger.Error("Failed to create request", "error", err)
			return
		}
		req.Header = r.Header.Clone()
		req.Header.Set(HeaderIndex, strconv.Itoa(newIndex))
		//req.Header.Set(HeaderHost, targetHop)
		//req.Header.Set(HeaderHops, hopsStr)
		req.Host = targetHop

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "Failed to forward request", ServerErrorCode)
			logger.Error("Failed to forward request", "error", err)
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// =========================
		// 方案 B 核心：只在真正转发数据时计数
		// =========================
		atomic.AddInt64(&activeTransfers, 1)
		_, err = io.Copy(w, &countingReader{r: resp.Body})
		atomic.AddInt64(&activeTransfers, -1)

		if err != nil {
			logger.Error("Error copying response body", "error", err)
		}

		logger.Info("Request completed",
			"target_hop", targetHop,
			"status", resp.StatusCode,
			"protocol", scheme,
			"active_transfers", atomic.LoadInt64(&activeTransfers),
		)
	}
}

func main() {
	logDir := "log"
	_ = os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(logDir+"/proxy.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer logFile.Close()

	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	config.Config_, _ = config.ReadYamlConfig(logger)

	router := gin.Default()

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, "success")
	})

	router.GET("/getCongestionInfo", func(c *gin.Context) {
		c.JSON(http.StatusOK, checkCongestion(logger))
	})

	//router.Any("/*proxyPath", func(c *gin.Context) {
	//	handler(logger)(c.Writer, c.Request)
	//})
	router.NoRoute(func(c *gin.Context) {
		handler(logger)(c.Writer, c.Request)
	})

	port := "8095"
	port = config.Config_.Port

	logger.Info("Listening", "port", port)
	if err := router.Run(":" + port); err != nil {
		logger.Error("Gin Run failed", "error", err)
	}
}

// ==================== client 池（保持你原来的实现） ====================
var (
	clientMap = make(map[string]*http.Client)
	clientMu  = &sync.RWMutex{}
)

func getClient(target string, scheme string) *http.Client {
	clientMu.RLock()
	c, ok := clientMap[target]
	clientMu.RUnlock()
	if ok {
		return c
	}

	transport := &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     10 * time.Second,
		ReadBufferSize:      64 * 1024,
		WriteBufferSize:     64 * 1024,
	}

	if scheme == "https" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	c = &http.Client{Transport: transport}

	clientMu.Lock()
	clientMap[target] = c
	clientMu.Unlock()
	return c
}

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
}

// checkCongestion 检查系统是否处于拥堵状态，并返回平均每连接的内存使用量
// 参数:
//   - logger: 用于记录日志的对象
//
// 返回值:
//   - float64: 平均每连接的内存使用量，如果未检测到拥堵则返回0
func checkCongestion(logger *slog.Logger) ProxyStatus {

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

	if usageRatio > 0.6 {
		perConnCache := 2 * 64 * 1024 // 每连接 128 KB
		active := atomic.LoadInt64(&activeTransfers)
		if active <= 0 {
			return s
		}
		s.ActiveConnections = active

		avgCache := float64(proxyMem) / float64(active)
		logger.Info("Active connections: %d, Average per-connection memory: %.2f KB\n",
			active, avgCache/1024)
		s.AvgCachePerConn = avgCache

		if avgCache >= float64(perConnCache) {
			logger.Warn("Potential congestion: average per-connection buffer near 128KB")
		}
		return s
	}
	return s
}
