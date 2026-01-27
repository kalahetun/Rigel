package main

import (
	"control-plane/etcd_client"
	"control-plane/etcd_server"
	"control-plane/pkg/api"
	envoymanager2 "control-plane/pkg/envoy_manager"
	"control-plane/routing"
	"control-plane/scaling_vm"
	"control-plane/storage"
	"control-plane/util"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Envoy配置文件路径常量
	envoyConfigPath = "/home/matth/envoy-mini.yaml"
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

	// 读取配置文件
	util.Config_, _ = util.ReadYamlConfig(logger)
	uu := util.Config_
	b, _ := json.Marshal(uu)
	logger.Info("读取配置文件成功", "config", b)

	//初始化 bandwidth cost信息
	err = util.LoadBandwidthCost(logger)
	if err != nil {
		logger.Error("Failed to load bandwidth cost: ", err.Error())
		return
	}

	// 启动 etcd server端
	if uu.ServerIP != "" {
		// 节点名用 IP 做后缀，保证唯一
		nodeName := "etcd-" + strings.ReplaceAll(uu.ServerIP, ".", "-")
		// 清理残留数据
		_ = os.RemoveAll(uu.DataDir)
		// 启动 etcd server
		etcdServer, err := etcd_server.StartEmbeddedEtcd(uu.ServerList, uu.ServerIP, uu.DataDir, nodeName, logger)
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
		return
	} else {
		logger.Info("Etcd Client connected", "serverIps", serverIps)
	}
	defer cli.Close()

	//获取全量前缀信息 然后初始化 routing map
	r := routing.NewGraphManager(logger)
	nodeMap, _ := etcd_client.GetPrefixAll(cli, "/routing/", logger)
	logger.Info("获取全量前缀信息成功", "nodeMap", nodeMap)
	for k, nodeJson := range nodeMap {
		var tel storage.NetworkTelemetry
		if err := json.Unmarshal([]byte(nodeJson), &tel); err != nil {
			logger.Warn("解析节点JSON失败，跳过", slog.String("ip", k), slog.Any("error", err))
			continue
		}
		r.AddNode(&tel)
	}

	// 监听 /routing/ 前缀 更新routing map
	etcd_client.WatchPrefix(
		cli, "/routing/",
		func(eventType, key, val string, logger *slog.Logger) {
			logger.Info("[WATCH] %s %s = %s", eventType, key, val)
			var tel storage.NetworkTelemetry
			if err := json.Unmarshal([]byte(val), &tel); err != nil {
				logger.Warn("解析节点JSON失败，跳过", slog.String("ip", key), slog.Any("error", err))
			} else {
				switch eventType {
				case "CREATE":
					r.AddNode(&tel)
				case "UPDATE":
					r.AddNode(&tel)
				case "DELETE":
					r.RemoveNode(tel.PublicIP)
				default:
					logger.Warn("[WATCH] UNKNOWN eventType %s for %s", eventType, key)
				}
			}
		}, logger,
	)

	//启动virtual queue逻辑
	exe, _ := os.Executable()
	storageDir := filepath.Join(filepath.Dir(exe), "vm_local_info_storage")
	//storageDir := filepath.Join(".", "vm_local_info_storage")
	s, _ := storage.NewFileStorage(storageDir, 0, logger)
	queue := util.NewFixedQueue(20)
	storage.CalcClusterWeightedAvg(s, 30*time.Second, cli, queue, logger)

	//启动envoy
	// 1. 固定配置文件路径（matth目录）
	configPath := envoyConfigPath
	// 2. 创建Envoy操作器（固定管理地址+matth配置路径）
	operator := envoymanager2.NewEnvoyOperator("http://127.0.0.1:9901", configPath)
	// 初始化全局配置（管理端口9901）
	_ = operator.InitEnvoyGlobalConfig(uu, 9901)
	err = operator.StartFirstEnvoy(logger, logger1)
	if err != nil {
		logger.Error("启动第一个Envoy失败", "error", err)
		return
	}

	//elastic scaling
	es := scaling_vm.NewScaler("", nil, queue, logger)
	es.StartTicker()

	// 初始化Gin路由
	router := gin.Default()
	router.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, "success") })
	//收集上报的代理节点信息
	api.InitVmReportAPIRouter(router, s, logger)
	//更改envoy的配置信息
	api.InitEnvoyAPIRouter(router, operator, logger, logger1)
	//下发探测任务 & 探测结果从 vm上报接口走
	api.InitNodeProbeRouter(router, cli, logger)
	//client获取 bulk transfer path信息的接口
	api.InitUserRoutingRouter(router, r, logger)
	logger.Info("Envoy端口管理API启动", "addr", ":8081") // 启动API服务
	if err := router.Run(":8081"); err != nil {
		logger.Error("API服务启动失败", "error", err)
		//os.Exit(1)
		return
	}

	return
}
