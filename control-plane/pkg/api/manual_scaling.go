package api

import (
	"control-plane/scaling_vm"
	"control-plane/util"
	model "control-plane/vm_info"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ManualScalingRequest HTTP请求体
type ManualScalingRequest struct {
	Action string `json:"action"` // scale_up, start, sleep, release, attach_envoy
	//Count  int    `json:"count,omitempty"` // 扩容数量，仅 scale_up 使用
}

// ManualScalingHandler 封装manual scaling API逻辑
type ManualScalingAPIHandler struct {
	Scaler *scaling_vm.Scaler
	Logger *slog.Logger
}

// NewManualScalingAPIHandler 初始化Handler
func NewManualScalingAPIHandler(s *scaling_vm.Scaler, logger *slog.Logger) *ManualScalingAPIHandler {
	return &ManualScalingAPIHandler{Scaler: s, Logger: logger}
}

// PostManualScaling 处理 POST /api/v1/scaling/manual
func (h *ManualScalingAPIHandler) PostManualScaling(c *gin.Context) {
	resp := model.ApiResponse{
		Code: 200,
		Msg:  "OK",
		Data: nil,
	}
	pre := util.GenerateRandomLetters(5)
	// 解析请求体
	var req ManualScalingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = 400
		resp.Msg = "请求格式错误：不是合法ManualScalingRequest结构 - " + err.Error()
		c.JSON(http.StatusOK, resp)
		h.Logger.Error("解析ManualScalingRequest错误", slog.String("pre", pre), resp.Msg)
		return
	}

	b, _ := json.Marshal(req)
	h.Logger.Info("ManualScalingRequest", slog.String("pre", pre), slog.String("data", string(b)))

	// 执行手动操作
	h.Scaler.ManualScaling(pre, req.Action)

	// 返回结果
	c.JSON(http.StatusOK, resp)
}

// InitManualScalingRouter 注册manual scaling路由
func InitManualScalingRouter(router *gin.Engine, s *scaling_vm.Scaler, logger *slog.Logger) *gin.Engine {
	r := router
	apiV1 := r.Group("/api/v1")
	{
		scalingGroup := apiV1.Group("/scaling")
		{
			handler := NewManualScalingAPIHandler(s, logger)
			scalingGroup.POST("", handler.PostManualScaling)
		}
	}
	return r
}
