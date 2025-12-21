package api

import (
	envoymanager2 "control-plane/pkg/envoy_manager"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
)

// Envoy配置文件路径常量
const envoyConfigPath = "/home/matth/envoy-mini.yaml"

// EnvoyPortAPIHandler Envoy端口API处理器
type EnvoyPortAPIHandler struct {
	operator *envoymanager2.EnvoyOperator
	logger   *slog.Logger
}

// NewEnvoyPortAPIHandler 创建API处理器实例
func NewEnvoyPortAPIHandler(operator *envoymanager2.EnvoyOperator, logger *slog.Logger) *EnvoyPortAPIHandler {
	return &EnvoyPortAPIHandler{
		operator: operator,
		logger:   logger,
	}
}

// HandleEnvoyPortCreate 处理创建Envoy端口请求（POST /envoy/port/create）
func (h *EnvoyPortAPIHandler) HandleEnvoyPortCreate(c *gin.Context) {
	var req envoymanager2.EnvoyPortCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Error("绑定创建端口请求失败", "error", err)
		c.JSON(http.StatusBadRequest, envoymanager2.APICommonResp{
			Code:    400,
			Message: "参数错误: " + err.Error(),
		})
		return
	}

	// 创建/更新端口（配置文件写入/home/matth/envoy.yaml）
	portCfg, err := h.operator.CreateOrUpdateEnvoyPort(req, h.logger)
	if err != nil {
		h.logger.Error("创建Envoy端口失败", "port", req.Port, "error", err)
		c.JSON(http.StatusInternalServerError, envoymanager2.APICommonResp{
			Code:    500,
			Message: "创建端口失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, envoymanager2.APICommonResp{
		Code:    0,
		Message: "端口创建/更新成功",
		Data:    portCfg,
	})
}

// HandleEnvoyPortDisable 处理禁用Envoy端口请求（POST /envoy/port/disable）
func (h *EnvoyPortAPIHandler) HandleEnvoyPortDisable(c *gin.Context) {
	var req envoymanager2.EnvoyPortDisableReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Error("绑定禁用端口请求失败", "error", err)
		c.JSON(http.StatusBadRequest, envoymanager2.APICommonResp{
			Code:    400,
			Message: "参数错误: " + err.Error(),
		})
		return
	}

	// 禁用端口
	if err := h.operator.DisableEnvoyPort(req.Port, h.logger); err != nil {
		h.logger.Error("禁用Envoy端口失败", "port", req.Port, "error", err)
		c.JSON(http.StatusInternalServerError, envoymanager2.APICommonResp{
			Code:    500,
			Message: "禁用端口失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, envoymanager2.APICommonResp{
		Code:    0,
		Message: "端口禁用成功",
	})
}

// HandleEnvoyPortQuery 处理查询Envoy端口请求（GET /envoy/port/query）
func (h *EnvoyPortAPIHandler) HandleEnvoyCfgQuery(c *gin.Context) {

	// 查询端口配置
	cfg, err := h.operator.GetCurrentConfig()
	if err != nil {
		h.logger.Error("查询 Envoy cfg 失败", "error", err)
		c.JSON(http.StatusNotFound, envoymanager2.APICommonResp{
			Code:    404,
			Message: "Envoy cfg 未找到: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, envoymanager2.APICommonResp{
		Code:    0,
		Message: "查询 Envoy cfg 成功",
		Data:    cfg,
	})
}

func (o *EnvoyPortAPIHandler) UpdateGlobalTargetAddrsHandler(c *gin.Context) {
	// 1. 绑定并校验请求体
	var req []envoymanager2.EnvoyTargetAddr
	if err := c.ShouldBindJSON(&req); err != nil {
		o.logger.Error("Invalid request body", "error", err)
		c.JSON(http.StatusOK, envoymanager2.APICommonResp{
			Code:    400,
			Message: "参数错误：" + err.Error(),
			Data:    nil,
		})
		return
	}

	// 2. 调用核心方法更新配置
	if err := o.operator.UpdateGlobalTargetAddrs(req, o.logger); err != nil {
		o.logger.Error("Failed to update target addrs", "error", err)
		c.JSON(http.StatusInternalServerError, envoymanager2.APICommonResp{
			Code:    500,
			Message: "更新后端地址失败：" + err.Error(),
			Data:    nil,
		})
		return
	}

	// 3. 返回成功响应
	o.logger.Info("Update envoy target addrs success", "target_addrs", req)
	c.JSON(http.StatusOK, envoymanager2.APICommonResp{
		Code:    0,
		Message: "更新后端地址成功",
		Data:    nil,
	})
}

// InitEnvoyAPIRouter 初始化Envoy端口API路由（已固化matth目录路径）
func InitEnvoyAPIRouter(router *gin.Engine, logger *slog.Logger) {
	// 1. 固定配置文件路径（matth目录）
	configPath := envoyConfigPath

	// 2. 创建Envoy操作器（固定管理地址+matth配置路径）
	operator := envoymanager2.NewEnvoyOperator("http://127.0.0.1:9901", configPath)
	// 初始化全局配置（管理端口9901）
	operator.InitEnvoyGlobalConfig(9901)

	operator.StartFirstEnvoy(logger)

	// 3. 创建API处理器
	handler := NewEnvoyPortAPIHandler(operator, logger)

	// 4. 注册路由
	envoyGroup := router.Group("/envoy/port")
	{
		envoyGroup.POST("/create", handler.HandleEnvoyPortCreate)
		envoyGroup.POST("/disable", handler.HandleEnvoyPortDisable)
	}
	envoyGroup1 := router.Group("/envoy/cfg")
	{
		envoyGroup1.GET("/setTargetIps", handler.HandleEnvoyCfgQuery)
		envoyGroup1.GET("/query", handler.HandleEnvoyCfgQuery)
	}
}
