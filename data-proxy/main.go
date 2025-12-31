package main

import (
	"crypto/tls"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
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

// 全局 Transport（复用连接和缓冲），避免每次请求新建
var globalTransport = &http.Transport{
	MaxIdleConns:        50,
	MaxIdleConnsPerHost: 50,
	IdleConnTimeout:     10 * time.Second,
	ReadBufferSize:      64 * 1024, // 64KB
	WriteBufferSize:     64 * 1024, // 64KB
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
		// 如果需要 https，可以根据实际逻辑解开
		// if newIndex == hopsLen {
		// 	scheme = "https"
		// }

		targetURL := scheme + "://" + targetIP + ":" + targetPort + r.URL.RequestURI()
		logger.Info("Forwarding to target", "target_url", targetURL)

		// 仅在 https 下设置 TLS
		transport := globalTransport
		if scheme == "https" {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}

		client := &http.Client{Transport: transport}

		req, err := http.NewRequest(r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, "Failed to create request", http.StatusInternalServerError)
			logger.Error("Failed to create request", "error", err)
			return
		}
		req.Header = r.Header.Clone()
		req.Header.Set(HeaderIndex, strconv.Itoa(newIndex))
		req.Header.Set(HeaderHost, targetHop)
		req.Header.Set(HeaderHops, hopsStr)

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
		_, err = io.Copy(w, resp.Body)
		if err != nil {
			logger.Error("Error copying response body", "error", err)
		}

		logger.Info("Request completed",
			"target_hop", targetHop,
			"status", resp.StatusCode,
			"protocol", scheme,
		)
	}
}

func main() {
	logDir := "log"
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(logDir+"/proxy.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer logFile.Close()

	// 使用 slog 输出到文件
	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	Config_, _ = ReadYamlConfig(logger)

	// 使用 Gin
	router := gin.Default()

	// 健康检查路由
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, "success")
	})

	// 主代理路由（匹配所有路径）
	router.Any("/*proxyPath", func(c *gin.Context) {
		handler(logger)(c.Writer, c.Request)
	})

	port := "8095" // default
	port = Config_.Port

	logger.Info("Listening", "port", port)
	if err := router.Run(":" + port); err != nil {
		logger.Error("Gin Run failed", "error", err)
	}
}
