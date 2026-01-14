package main

import (
	"control-plane/etcd_client"
	"control-plane/etcd_server"
	"control-plane/pkg/api"
	"control-plane/storage"
	"control-plane/util"
	"github.com/gin-gonic/gin"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	// 创建 log 目录（与 pkg 同级）
	logDir := filepath.Join(".", "log")
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		panic("无法创建日志目录: " + err.Error())
	}
	logFilePath := filepath.Join(logDir, "app.log")
	logFilePath1 := filepath.Join(logDir, "envoy.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic("无法打开日志文件: " + err.Error())
	}
	logFile1, err := os.OpenFile(logFilePath1, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic("无法打开日志文件: " + err.Error())
	}

	// 初始化日志，输出到 log/app.log
	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	logger1 := slog.New(slog.NewTextHandler(logFile1, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	util.Config_, _ = util.ReadYamlConfig(logger)
	uu := util.Config_
	logger.Info("读取配置文件成功", "config", uu)

	//初始化 bandwidth cost信息
	_ = util.LoadBandwidthCost(logger)

	// 启动 etcd server端
	if uu.ServerIP != "" {
		nodeName := "etcd-" + strings.ReplaceAll(uu.ServerIP, ".", "-")                                            // 节点名用 IP 做后缀，保证唯一
		_ = os.RemoveAll(uu.DataDir)                                                                               // 清理残留数据
		etcdServer, err := etcd_server.StartEmbeddedEtcd(uu.ServerList, uu.ServerIP, uu.DataDir, nodeName, logger) // 启动嵌入式 etcd
		if err != nil {
			logger.Error("Failed to start embedded etcd: %v", err)
		}
		defer etcdServer.Close()
	}

	// 初始化etcd客户端
	serverIps := []string{uu.ServerIP}
	if serverIps[0] == "" && len(uu.ServerList) > 0 {
		serverIps = uu.ServerList
	} else {
		logger.Error("Failed to get serverIps: %v", serverIps)
		return
	}
	cli, err := etcd_client.NewEtcdClient(serverIps, 5*time.Second)
	if err != nil {
		logger.Error("Failed to connect to etcd:", err)
	}
	defer cli.Close()

	//获取全量前缀信息 然后初始化 routing map

	// 监听 /routing/ 前缀 更新routing map
	etcd_client.WatchPrefix(cli, "/routing/", func(eventType, key, val string, logger *slog.Logger) {
		logger.Info("[WATCH] %s %s = %s", eventType, key, val, logger)
		switch eventType {
		case "CREATE":
			etcd_client.PutKey(cli, key, val, logger)

		case "UPDATE":
			etcd_client.PutKey(cli, key, val, logger)

		case "DELETE":
			etcd_client.DeleteKey(cli, key, logger)

		default:
			logger.Warn("[WATCH] UNKNOWN eventType %s for %s", eventType, key)
		}
	}, logger)

	storDir := filepath.Join(".", "vm_local_info_storage")
	s, _ := storage.NewFileStorage(storDir, 0, logger)
	queue := util.NewFixedQueue(10)

	storage.CalcWeightedAvgWithTimer(s, 30*time.Second, cli, queue, logger)

	// 初始化Gin路由
	router := gin.Default()

	//
	api.InitVmReportAPIRouter(router, s, logger)

	// 注册Envoy端口API（已适配matth目录）
	api.InitEnvoyAPIRouter(router, logger, logger1)

	// 启动API服务
	logger.Info("Envoy端口管理API启动", "addr", ":8081")
	if err := router.Run(":8081"); err != nil {
		logger.Error("API服务启动失败", "error", err)
		os.Exit(1)
	}
}
