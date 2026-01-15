package api

import (
	"control-plane/etcd_client"
	"control-plane/storage"
	"control-plane/util"
	model "control-plane/vm_info"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// NodeProbeAPIHandler 提供获取节点探测任务的接口
type NodeProbeAPIHandler struct {
	etcdClient *clientv3.Client
	logger     *slog.Logger
}

// NewNodeProbeAPIHandler 初始化
func NewNodeProbeAPIHandler(cli *clientv3.Client, logger *slog.Logger) *NodeProbeAPIHandler {
	return &NodeProbeAPIHandler{
		etcdClient: cli,
		logger:     logger,
	}
}

// GetProbeTasks 处理 GET /api/v1/probe/tasks
// 返回当前所有节点的探测任务信息
func (h *NodeProbeAPIHandler) GetProbeTasks(c *gin.Context) {
	resp := model.ApiResponse{
		Code: 500,
		Msg:  "服务端内部错误",
		Data: nil,
	}

	// 1. 从Etcd获取所有节点
	nodeMap, err := etcd_client.GetPrefixAll(h.etcdClient, "/routing/", h.logger)
	if err != nil {
		resp.Code = 500
		resp.Msg = "获取节点信息失败：" + err.Error()
		c.JSON(http.StatusOK, resp)
		h.logger.Error(resp.Msg)
		return
	}

	// 2. 解析每个节点JSON，生成Targets列表
	tasks := []string{}
	ip_, _ := util.GetPublicIP()
	for k, nodeJson := range nodeMap {
		if k == ip_ {
			continue
		}
		var telemetry storage.NetworkTelemetry
		if err := json.Unmarshal([]byte(nodeJson), &telemetry); err != nil {
			h.logger.Warn("解析节点JSON失败，跳过", slog.String("ip", k), slog.Any("error", err))
			continue
		}
		tasks = append(tasks, telemetry.PublicIP)
	}

	// 3. 返回JSON
	resp.Code = 200
	resp.Msg = "成功获取节点探测任务"
	resp.Data = tasks
	c.JSON(http.StatusOK, resp)
}

// InitNodeProbeRouter 初始化路由
func InitNodeProbeRouter(router *gin.Engine, cli *clientv3.Client, logger *slog.Logger) *gin.Engine {
	r := router
	apiV1 := r.Group("/api/v1")
	{
		probeGroup := apiV1.Group("/probe")
		{
			handler := NewNodeProbeAPIHandler(cli, logger)
			probeGroup.GET("/tasks", handler.GetProbeTasks)
		}
	}
	return r
}
