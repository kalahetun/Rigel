package api

import (
	"control-plane/routing"
	model "control-plane/vm_info"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
)

// UserRoutingAPIHandler 提供获取用户传输文件的路由信息
type UserRoutingAPIHandler struct {
	GM     *routing.GraphManager
	Logger *slog.Logger
}

// NewUserRoutingAPIHandler 初始化
func NewUserRoutingAPIHandler(gm *routing.GraphManager, logger *slog.Logger) *UserRoutingAPIHandler {
	return &UserRoutingAPIHandler{
		GM:     gm,
		Logger: logger,
	}
}

// Post请求Body结构
type UserRouteRequest struct {
	FileName   string `json:"fileName"`        // 文件名
	Priority   int    `json:"priority"`        // 文件优先级
	ClientCont string `json:"clientContinent"` // 客户端大区
	ServerIP   string `json:"serverIP"`        // 目标服务器 IP 或域名
	ServerCont string `json:"serverContinent"` // 目标服务器大区
	Username   string `json:"username"`        // 客户端用户名
}

// RouteInfo 返回给用户的结构
//type PathInfo struct {
//	Hops string `json:"hops"`
//	Rate int64  `json:"rate"`
//	//Weight int64  `json:"weight"`
//}
//
//type RoutingInfo struct {
//	Routing []PathInfo `json:"routing"`
//}

// GetUserRoute 处理 POST /api/v1/routing
func (h *UserRoutingAPIHandler) GetUserRoute(c *gin.Context) {
	resp := model.ApiResponse{
		Code: 500,
		Msg:  "服务端内部错误",
		Data: nil,
	}

	// 1️⃣ 解析 header 信息（如果有）
	clientIP := c.GetHeader("X-Client-IP")
	if clientIP == "" {
		clientIP = c.ClientIP() // fallback
	}

	filename := c.GetHeader("X-File-Name")

	username := c.GetHeader("X-User-Name")

	// 2️⃣ 解析 body JSON
	var req UserRouteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = 400
		resp.Msg = "请求体解析失败：" + err.Error()
		c.JSON(http.StatusOK, resp)
		h.Logger.Warn("PostUserRoute parse body failed", slog.Any("error", err))
		return
	}

	h.Logger.Info("UserRoute POST request",
		"clientIP", clientIP,
		"username", username,
		"fileName", filename,
		"priority", req.Priority,
		"clientContinent", req.ClientCont,
		"serverIP", req.ServerIP,
		"serverContinent", req.ServerCont,
	)

	// 3️⃣ 调用 GraphManager 获取最优路径
	// 可以把 GetBestPath 改成支持从客户端到服务器
	paths := h.GM.Routing(req.ClientCont, req.ServerCont, req.ServerIP, h.Logger)

	resp.Code = 200
	resp.Msg = "成功获取路径"
	resp.Data = paths
	c.JSON(http.StatusOK, resp)
}

// InitUserRoutingRouter 初始化用户路由 API 路由
func InitUserRoutingRouter(router *gin.Engine, gm *routing.GraphManager, logger *slog.Logger) *gin.Engine {
	apiV1 := router.Group("/api/v1")
	{
		routingGroup := apiV1.Group("/routing")
		{
			handler := NewUserRoutingAPIHandler(gm, logger)
			routingGroup.POST("", handler.GetUserRoute) // POST /api/v1/routing
		}
	}
	return router
}
