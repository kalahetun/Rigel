package api

import (
	"Rigel/pkg/envoymanager"
	"fmt"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
)

// Envoy配置文件路径常量
const envoyConfigPath = "/home/matth/envoy-mini.yaml"

// EnvoyPortAPIHandler Envoy端口API处理器
type EnvoyPortAPIHandler struct {
	operator *envoymanager.EnvoyOperator
	logger   *slog.Logger
}

// NewEnvoyPortAPIHandler 创建API处理器实例
func NewEnvoyPortAPIHandler(operator *envoymanager.EnvoyOperator, logger *slog.Logger) *EnvoyPortAPIHandler {
	return &EnvoyPortAPIHandler{
		operator: operator,
		logger:   logger,
	}
}

// HandleEnvoyPortCreate 处理创建Envoy端口请求（POST /envoy/port/create）
func (h *EnvoyPortAPIHandler) HandleEnvoyPortCreate(c *gin.Context) {
	var req envoymanager.EnvoyPortCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Error("绑定创建端口请求失败", "error", err)
		c.JSON(http.StatusBadRequest, envoymanager.APICommonResp{
			Code:    400,
			Message: "参数错误: " + err.Error(),
		})
		return
	}

	// 创建/更新端口（配置文件写入/home/matth/envoy.yaml）
	portCfg, err := h.operator.CreateOrUpdateEnvoyPort(req)
	if err != nil {
		h.logger.Error("创建Envoy端口失败", "port", req.Port, "error", err)
		c.JSON(http.StatusInternalServerError, envoymanager.APICommonResp{
			Code:    500,
			Message: "创建端口失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, envoymanager.APICommonResp{
		Code:    0,
		Message: "端口创建/更新成功",
		Data:    portCfg,
	})
}

// HandleEnvoyPortDisable 处理禁用Envoy端口请求（POST /envoy/port/disable）
func (h *EnvoyPortAPIHandler) HandleEnvoyPortDisable(c *gin.Context) {
	var req envoymanager.EnvoyPortDisableReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Error("绑定禁用端口请求失败", "error", err)
		c.JSON(http.StatusBadRequest, envoymanager.APICommonResp{
			Code:    400,
			Message: "参数错误: " + err.Error(),
		})
		return
	}

	// 禁用端口
	if err := h.operator.DisableEnvoyPort(req.Port); err != nil {
		h.logger.Error("禁用Envoy端口失败", "port", req.Port, "error", err)
		c.JSON(http.StatusInternalServerError, envoymanager.APICommonResp{
			Code:    500,
			Message: "禁用端口失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, envoymanager.APICommonResp{
		Code:    0,
		Message: "端口禁用成功",
	})
}

// HandleEnvoyPortQuery 处理查询Envoy端口请求（GET /envoy/port/query）
func (h *EnvoyPortAPIHandler) HandleEnvoyPortQuery(c *gin.Context) {
	// 从URL参数获取端口号
	portStr := c.Query("port")
	if portStr == "" {
		c.JSON(http.StatusBadRequest, envoymanager.APICommonResp{
			Code:    400,
			Message: "参数错误: 端口号不能为空",
		})
		return
	}

	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		c.JSON(http.StatusBadRequest, envoymanager.APICommonResp{
			Code:    400,
			Message: "参数错误: 端口号必须是数字",
		})
		return
	}

	// 查询端口配置
	portCfg, err := h.operator.GetEnvoyPortConfig(port)
	if err != nil {
		h.logger.Error("查询Envoy端口失败", "port", port, "error", err)
		c.JSON(http.StatusNotFound, envoymanager.APICommonResp{
			Code:    404,
			Message: "端口未找到: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, envoymanager.APICommonResp{
		Code:    0,
		Message: "查询端口成功",
		Data:    portCfg,
	})
}

// InitEnvoyAPIRouter 初始化Envoy端口API路由（已固化matth目录路径）
func InitEnvoyAPIRouter(router *gin.Engine, logger *slog.Logger) {
	// 1. 固定配置文件路径（matth目录）
	configPath := envoyConfigPath

	// 2. 创建Envoy操作器（固定管理地址+matth配置路径）
	operator := envoymanager.NewEnvoyOperator("http://127.0.0.1:9901", configPath)
	// 初始化全局配置（管理端口9901）
	operator.InitEnvoyGlobalConfig(9901)

	operator.StartFirstEnvoy()

	// 3. 创建API处理器
	handler := NewEnvoyPortAPIHandler(operator, logger)

	// 4. 注册路由
	envoyGroup := router.Group("/envoy/port")
	{
		envoyGroup.POST("/create", handler.HandleEnvoyPortCreate)
		envoyGroup.POST("/disable", handler.HandleEnvoyPortDisable)
		envoyGroup.GET("/query", handler.HandleEnvoyPortQuery)
	}
}
