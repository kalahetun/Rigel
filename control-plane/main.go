package main

import (
	"control-plane/pkg/api"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

func main() {
	// 创建 log 目录（与 pkg 同级）
	logDir := filepath.Join(".", "log")
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		panic("无法创建日志目录: " + err.Error())
	}
	logFilePath := filepath.Join(logDir, "app.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic("无法打开日志文件: " + err.Error())
	}

	// 初始化日志，输出到 log/app.log
	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// 2. 初始化Gin路由
	router := gin.Default()

	// 3. 注册Envoy端口API（已适配matth目录）
	api.InitEnvoyAPIRouter(router, logger)

	//
	api.InitVmReportAPIRouter(router, logger)

	// 4. 启动API服务
	logger.Info("Envoy端口管理API启动", "addr", ":8081")
	if err := router.Run(":8081"); err != nil {
		logger.Error("API服务启动失败", "error", err)
		os.Exit(1)
	}
}
