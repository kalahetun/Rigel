package main

import (
	"context"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
	"os"
	"rigel-client/config"
	"rigel-client/upload"
)

const (
	HeaderFileName = "X-File-Name" // 通过 Header 传文件名
	bucketName     = "rigel-data"
	credFile       = "/home/matth/civil-honor-480405-e0-bdec4345bdd7.json"
	localBaseDir   = "/home/matth/upload/" // 本地文件目录前缀
)

func main() {

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
	router.POST("/gcp/upload/client", func(c *gin.Context) {
		// 从 Header 获取文件名
		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		localFilePath := localBaseDir + fileName
		ctx := context.Background()

		if err := upload.UploadToGCSbyClient(ctx, localFilePath, bucketName, fileName, credFile, logger); err != nil {
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
	router.POST("/gcp/upload/direct", func(c *gin.Context) {
		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		localFilePath := localBaseDir + fileName

		if err := upload.UploadToGCSbyDirectHttps(localFilePath, bucketName, fileName, credFile, logger); err != nil {
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

	router.POST("/gcp/upload/redirect/v1", func(c *gin.Context) {
		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		hops := c.GetHeader("X-Hops") // "34.69.185.247:8090,136.116.114.219:8080"
		if len(hops) <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-Hops header"})
			return
		}

		localFilePath := localBaseDir + fileName

		if err := upload.UploadToGCSbyReDirectHttpsV1(localFilePath, bucketName, fileName, credFile,
			hops, c.Request.Header, logger); err != nil {
			logger.Error("ReDirect HTTPS upload failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":   "redirect upload success",
			"file_name": fileName,
		})
	})

	router.POST("/gcp/upload/redirect/v2", func(c *gin.Context) {
		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		hops := c.GetHeader("X-Hops") // "34.69.185.247:8090,136.116.114.219:8080"
		if len(hops) <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-Hops header"})
			return
		}

		localFilePath := localBaseDir + fileName

		if err := upload.UploadToGCSbyReDirectHttpsV2(localFilePath, bucketName, fileName, credFile,
			hops, c.Request.Header, logger); err != nil {
			logger.Error("ReDirect v2 HTTPS upload failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":   "redirect v2 upload success",
			"file_name": fileName,
		})
	})

	port := "8080"
	logger.Info("Starting server on port %s...", port)
	router.Run(":" + port)
}
