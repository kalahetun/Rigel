package main

import (
	"context"
	"data-proxy/config"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2/google"
)

const (
	HeaderFileName = "X-File-Name" // 通过 Header 传文件名
)

// UploadToGCS 上传本地文件到指定 GCS bucket
func UploadToGCSbyClient(ctx context.Context, localFilePath, bucketName, objectName, credFile string, logger *slog.Logger) error {

	logger.Info("Uploading file to GCS bucket using client library", localFilePath, objectName)
	// 使用环境变量配置凭证
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	// 创建客户端
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create storage client: %w", err)
	}
	defer client.Close()

	// 打开本地文件
	f, err := os.Open(localFilePath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer f.Close()

	// 获取 bucket handle
	bucket := client.Bucket(bucketName)

	// 上传文件
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	wc := bucket.Object(objectName).NewWriter(ctx)
	wc.StorageClass = "STANDARD"
	wc.ContentType = "application/octet-stream"

	if _, err := io.Copy(wc, f); err != nil {
		return fmt.Errorf("failed to copy file to bucket: %w", err)
	}

	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to close writer: %w", err)
	}

	logger.Info("Upload success client library", localFilePath, objectName)

	return nil
}

// =========================
// HTTPS Direct Upload using Service Account JSON
// =========================
func UploadToGCSbyDirectHttps(localFilePath, bucketName, objectName, credFile string, logger *slog.Logger) error {

	logger.Info("Uploading file to GCS bucket using direct HTTPS upload", localFilePath, objectName)

	ctx := context.Background()

	// 从 Service Account JSON 获取 Token
	jsonBytes, err := os.ReadFile(credFile)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	creds, err := google.CredentialsFromJSON(ctx, jsonBytes, "https://www.googleapis.com/auth/devstorage.full_control")
	if err != nil {
		return fmt.Errorf("failed to parse credentials: %w", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}
	accessToken := token.AccessToken

	// 打开本地文件
	f, err := os.Open(localFilePath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer f.Close()

	// 构造 PUT 请求
	url := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectName)
	req, err := http.NewRequest("PUT", url, f)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/octet-stream")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to perform HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed, status: %d, body: %s", resp.StatusCode, string(body))
	}
	logger.Info("Upload success direct HTTPS upload", localFilePath, objectName)

	return nil
}

func UploadToGCSbyReDirectHttps(localFilePath, bucketName, fileName, credFile string, reqHeaders http.Header) error {
	// 读取 bucket 和 object
	//bucketName := reqHeaders.Get("X-Bucket-Name")
	objectName := fileName

	// 生成 access token
	ctx := context.Background()
	jsonBytes, err := os.ReadFile(credFile)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}
	creds, err := google.CredentialsFromJSON(ctx, jsonBytes, "https://www.googleapis.com/auth/devstorage.full_control")
	if err != nil {
		return fmt.Errorf("failed to parse credentials: %w", err)
	}
	token, err := creds.TokenSource.Token()
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}
	accessToken := token.AccessToken

	// 打开本地文件
	f, err := os.Open(localFilePath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer f.Close()

	hops := reqHeaders.Get("X-Hops") // "34.69.185.247:8090,136.116.114.219:8080"
	hopList := strings.Split(hops, ",")
	if len(hopList) <= 1 {
		return fmt.Errorf("invalid X-Hops header: %s", hops)
	}
	firstHop := hopList[0] // 第一跳 IP:PORT

	// 拼装最终 URI
	url := fmt.Sprintf("http://%s/%s/%s", firstHop, bucketName, objectName)

	// 构造 PUT 请求
	putReq, err := http.NewRequest("POST", url, f)
	if err != nil {
		return fmt.Errorf("failed to create POST request: %w", err)
	}
	putReq.Header.Set("Authorization", "Bearer "+accessToken)
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.Header.Set("X-Hops", hops)
	putReq.Header.Set("X-Index", "1")
	putReq.Header.Set("X-Rate-Limit-Enable", "false")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(putReq)
	if err != nil {
		return fmt.Errorf("failed to upload to GCS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed, status: %d, body: %s", resp.StatusCode, string(body))
	}

	log.Println("UploadToGCSbyReDirectHttps success:", bucketName, objectName)
	return nil
}

func main() {
	bucketName := "rigel-data"
	credFile := "/home/matth/civil-honor-480405-e0-bdec4345bdd7.json"
	localBaseDir := "/home/matth/" // 本地文件目录前缀

	logDir := "log"
	_ = os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(logDir+"/client.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer logFile.Close()

	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	config.Config_, _ = config.ReadYamlConfig(logger)

	router := gin.Default()

	// 上传接口
	router.POST("/gcp/client/upload", func(c *gin.Context) {
		// 从 Header 获取文件名
		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		localFilePath := localBaseDir + fileName
		ctx := context.Background()

		if err := UploadToGCSbyClient(ctx, localFilePath, bucketName, fileName, credFile, logger); err != nil {
			logger.Error("Upload failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":    "upload success",
			"file_name":  fileName,
			"bucket":     bucketName,
			"objectName": fileName,
		})
	})

	// ========== 新增 HTTPS 直传 ==========
	router.POST("/gcp/direct/upload", func(c *gin.Context) {
		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		localFilePath := localBaseDir + fileName

		if err := UploadToGCSbyDirectHttps(localFilePath, bucketName, fileName, credFile, logger); err != nil {
			logger.Error("Direct HTTPS upload failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":    "direct upload success",
			"file_name":  fileName,
			"bucket":     bucketName,
			"objectName": fileName,
		})
	})

	router.POST("/gcp/redirect/upload", func(c *gin.Context) {
		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		localFilePath := localBaseDir + fileName

		if err := UploadToGCSbyReDirectHttps(localFilePath, bucketName, fileName, credFile, c.Request.Header); err != nil {
			logger.Error("ReDirect HTTPS upload failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":   "redirect upload success",
			"file_name": fileName,
		})
	})

	port := "8080"
	logger.Info("Starting server on port %s...", port)
	router.Run(":" + port)
}
