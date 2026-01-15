package main

import (
	"context"
	"data-plane/pkg/envoy_manager"
	model "data-plane/pkg/local_info_report"
	"data-plane/pkg/local_info_report/reporter"
	"data-plane/probing"
	"data-plane/util"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func InitEnvoy(logger, logger1 *slog.Logger) {
	// 创建启动器
	starter := envoy_manager.NewEnvoyStarter()

	// 启动Envoy
	pid, err := starter.StartEnvoy(logger, logger1)
	if err != nil {
		logger.Error("Envoy启动失败: %v", err)
	}
	logger.Info("Envoy启动成功，PID: %d", pid)
}

func main() {

	// 创建 log 目录（与 pkg 同级）
	logDir := filepath.Join(".", "log")
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		panic("无法创建日志目录: " + err.Error())
	}
	logFilePath := filepath.Join(logDir, "app.log")
	//logFilePath1 := filepath.Join(logDir, "envoy.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic("无法打开日志文件: " + err.Error())
	}
	//logFile1, err := os.OpenFile(logFilePath1, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	//if err != nil {
	//	panic("无法打开日志文件: " + err.Error())
	//}

	// 初始化日志，输出到 log/app.log
	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	//logger1 := slog.New(slog.NewTextHandler(logFile1, &slog.HandlerOptions{
	//	Level: slog.LevelInfo,
	//}))

	util.Config_, _ = util.ReadYamlConfig(logger)

	// 2. 初始化Gin路由
	router := gin.Default()

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, model.ApiResponse{
			Code: 200,
			Msg:  "success",
			Data: gin.H{"time": time.Now().UTC().Format(time.RFC3339)},
		})
	})

	// 3. 初始化上报器
	go reporter.ReportCycle(util.Config_.ControlHost, logger)

	//启动探测逻辑
	cfg := probing.Config{
		Concurrency: 4,
		Timeout:     2 * time.Second,
		Interval:    5 * time.Second,
		Attempts:    5, // 每轮尝试次数
	}
	ctx := context.Background()
	probing.StartProbePeriodically(ctx, util.Config_.ControlHost, cfg, logger)
	//logger.Info("", "probe result", probingResult)

	//InitEnvoy(logger, logger1)

	// 4. 启动API服务
	logger.Info("API端口启动", "addr", ":8082")
	if err := router.Run(":8082"); err != nil {
		logger.Error("API服务启动失败", "error", err)
		os.Exit(1)
	}
}
