package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"log/slog"
	"net/http"
	"rigel-client/config"
	"rigel-client/upload"
	"rigel-client/util"
)

const (
	HeaderFileName = "X-File-Name" // 通过 Header 传文件名
	bucketName     = "rigel-data"
	credFile       = "/home/matth/civil-honor-480405-e0-e62b994bbc27.json"
	localBaseDir   = "/home/matth/upload/" // 本地文件目录前缀
	//HOST           = "http://127.0.0.1:8081" //可以通过geoDNS获取
	RoutingURL = "/api/v1/routing"
)

type ApiResponse struct {
	Code int         `json:"code"` // 200=成功，400=参数错误，500=服务端错误
	Msg  string      `json:"msg"`  // 提示信息
	Data interface{} `json:"data"` // 业务数据
}

type UserRouteRequest struct {
	FileName   string `json:"fileName"` // 文件名
	Priority   int    `json:"priority"`
	ClientCont string `json:"clientContinent"`
	ServerIP   string `json:"serverIP"`
	ServerCont string `json:"serverContinent"`
	Username   string `json:"username"`
}

func RedirectV1Handler(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		hops := c.GetHeader("X-Hops") // "34.69.185.247:8090,136.116.114.219:8080"
		if hops == "" {
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
	}
}

func ClientUploadHandler(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
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
	}
}

func DirectUploadHandler(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
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
	}
}

func RedirectV2Handler(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {

		pre := util.GenerateRandomLetters(5)
		logger.Info("RedirectV2Handler", slog.String("pre", pre))

		var routingInfo upload.RoutingInfo
		if err := c.ShouldBindJSON(&routingInfo); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "invalid json body for routing",
				"detail": err.Error(),
			})
			return
		}

		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}
		localFilePath := localBaseDir + fileName

		//没有路径直传
		if len(routingInfo.Routing) == 0 {
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
			return
		}

		uploadInfo := upload.UploadFileInfo{
			LocalFilePath: localFilePath,
			BucketName:    bucketName,
			FileName:      fileName,
			CredFile:      credFile,
		}

		if err := upload.UploadToGCSbyReDirectHttpsV2(uploadInfo, routingInfo, pre, logger); err != nil {
			logger.Error("ReDirect v2 HTTPS upload failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":   "redirect v2 upload success",
			"file_name": fileName,
		})
	}
}

func Upload(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {

		pre := util.GenerateRandomLetters(5)
		logger.Info("Upload", slog.String("pre", pre))

		// 1️⃣ 读取客户端请求 body
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "读取请求失败" + err.Error(),
			})
			return
		}

		// 2️⃣ 解析 body 用于日志
		var req UserRouteRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "请求体解析失败" + err.Error(),
			})
			return
		}

		fileName := c.GetHeader(HeaderFileName)
		if fileName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-File-Name header"})
			return
		}

		clientIP := c.GetHeader("X-Client-IP")
		if clientIP == "" {
			clientIP = c.ClientIP()
		}
		username := c.GetHeader("X-Username")

		logger.Info("Proxy UserRoute request",
			slog.String("pre", pre),
			"clientIP", clientIP,
			"username", username,
			"fileName", fileName,
			"priority", req.Priority,
			"clientContinent", req.ClientCont,
			"serverIP", req.ServerIP,
			"serverContinent", req.ServerCont,
		)

		// 3️⃣ 构建请求转发给B
		bReq, err := http.NewRequest("POST", config.Config_.ControlHost+RoutingURL,
			bytes.NewReader(bodyBytes))
		if err != nil {
			logger.Error("http NewRequest failed", slog.String("pre", pre),
				slog.String("err", err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		bReq.Header.Set("Content-Type", "application/json")
		bReq.Header.Set(HeaderFileName, fileName)
		bReq.Header.Set("X-Client-IP", clientIP)
		bReq.Header.Set("X-User-Name", username)

		client := &http.Client{}
		bResp, err := client.Do(bReq)
		if err != nil {
			logger.Error("http Do failed", slog.String("pre", pre),
				slog.String("err", err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer bResp.Body.Close()

		// 4️⃣ 读取B响应 body
		bRespBody, err := io.ReadAll(bResp.Body)
		if err != nil {
			logger.Error("io ReadAll failed", slog.String("pre", pre),
				slog.String("err", err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 5️⃣ 解析B的 JSON 成 ApiResponse
		var bApiResp ApiResponse
		if err := json.Unmarshal(bRespBody, &bApiResp); err != nil {
			logger.Error("json Unmarshal failed", slog.String("pre", pre),
				slog.String("err", err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		var routingInfo upload.RoutingInfo
		if err := c.ShouldBindJSON(&routingInfo); err != nil {
			logger.Error("c ShouldBindJSON failed", slog.String("pre", pre),
				slog.String("err", err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		logger.Info("Proxy UserRoute response", slog.String("pre", pre),
			slog.Any("routingInfo", routingInfo))

		if len(routingInfo.Routing) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "routing info is empty",
			})
			return
		}

		uploadInfo := upload.UploadFileInfo{
			LocalFilePath: localBaseDir + fileName,
			BucketName:    bucketName,
			FileName:      fileName,
			CredFile:      credFile,
		}

		if err := upload.UploadToGCSbyReDirectHttpsV2(uploadInfo, routingInfo, pre, logger); err != nil {
			logger.Error("ReDirect v2 HTTPS upload failed",
				slog.String("pre", pre), slog.Any("err", err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":   "upload success",
			"file_name": fileName,
		})

	}
}
