package main

import (
	"crypto/tls"
	"data-proxy/config"
	"data-proxy/virtual_queue"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"
)

const (
	HeaderHops  = "x-hops"
	HeaderIndex = "x-index"
	//HeaderHost      = "Host"
	DefaultIndex    = "1"
	ServerErrorCode = 503
	BufferSize      = 64
)

/*
 * =========================
 * 方案 B：数据路径级统计
 * =========================
 */

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
		ReadBufferSize:      BufferSize * 1024,
		WriteBufferSize:     BufferSize * 1024,
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
			//"active_transfers", atomic.LoadInt64(&virtual_queue.ActiveTransfers),
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
		method := r.Method
		//最后一跳的逻辑
		if newIndex == len(hops) {
			scheme = "https"
			method = "PUT"
		}

		targetURL := scheme + "://" + targetIP + ":" + targetPort + r.URL.RequestURI()
		logger.Info("Forwarding to target", "target_url", targetURL)

		target := targetIP + ":" + targetPort
		client := getClient(target, scheme)

		req, err := http.NewRequest(method, targetURL, r.Body)
		if err != nil {
			http.Error(w, "Failed to create request", http.StatusInternalServerError)
			logger.Error("Failed to create request", "error", err)
			return
		}
		req.Header = r.Header.Clone()
		req.Header.Set(HeaderIndex, strconv.Itoa(newIndex))

		logger.Info("Forwarded request headers", req.Header)

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "Failed to forward request", ServerErrorCode)
			logger.Error("Failed to forward request", "error", err)
			return
		}
		defer resp.Body.Close()

		logger.Info("Forwarded response headers", resp.Header)

		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// =========================
		// 方案 B 核心：只在真正转发数据时计数
		// =========================
		atomic.AddInt64(&virtual_queue.ActiveTransfers, 1)
		_, err = io.Copy(w, &countingReader{r: resp.Body})
		atomic.AddInt64(&virtual_queue.ActiveTransfers, -1)

		if err != nil {
			logger.Error("Error copying response body", "error", err)
		}

		logger.Info("Request completed",
			"target_hop", targetHop,
			"status", resp.StatusCode,
			"protocol", scheme,
			//"active_transfers", atomic.LoadInt64(&virtual_queue.ActiveTransfers),
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
		c.JSON(http.StatusOK, virtual_queue.CheckCongestion(2*BufferSize, logger))
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
