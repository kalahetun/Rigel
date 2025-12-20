package reporter

import (
	"bytes"
	"data-plane/pkg/local_info_report/collector"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"time"

	model "data-plane/pkg/local_info_report"
	"github.com/google/uuid"
)

// 常量配置（写死）
const (
	ReportURL      = "http://localhost:8081/api/v1/vm/report" // 控制平面地址
	ReportInterval = 5 * time.Second                          // 上报周期
	Timeout        = 10 * time.Second                         // HTTP超时
)

// HTTPReporter HTTP上报器
type HTTPReporter struct {
	client *http.Client
}

// NewHTTPReporter 初始化上报器
func NewHTTPReporter() *HTTPReporter {
	return &HTTPReporter{
		client: &http.Client{
			Timeout: Timeout,
		},
	}
}

// Report 上报VM信息（按ApiResponse格式封装）
func (r *HTTPReporter) Report(vmReport *model.VMReport) error {
	// 1. 填充ReportID（若为空）
	if vmReport.ReportID == "" {
		vmReport.ReportID = uuid.NewString()
	}

	// 2. 构造外层ApiResponse请求体
	reqBody := model.ApiResponse{
		Code: 200, // 客户端默认填200
		Msg:  "VM信息上报请求",
		Data: vmReport,
	}

	// 3. 序列化为JSON
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	// 4. 发送POST请求
	resp, err := r.client.Post(ReportURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 5. 解析响应（可选，验证上报结果）
	var respBody model.ApiResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return err
	}

	if respBody.Code != 200 {
		return fmt.Errorf("上报失败：%s", respBody.Msg)
	}

	return nil
}

// 工具函数：格式化UTC时间（复用）
func FormatUTCTime(t time.Time) string {
	return t.Format(time.RFC3339)
}

func ReportCycle(logger *slog.Logger) {
	// 1. 初始化采集器和上报器
	vmCollector := collector.NewVMCollector()
	httpReporter := NewHTTPReporter()

	// 2. 启动定时上报任务
	ticker := time.NewTicker(ReportInterval)
	defer ticker.Stop()

	logger.Info("数据平面启动，开始定时上报（周期：%v），上报地址：%s", ReportInterval, ReportURL)

	// 3. 立即执行一次上报，然后按周期执行
	//reportOnce(vmCollector, httpReporter, logger)

	for range ticker.C {
		reportOnce(vmCollector, httpReporter, logger)
	}
}

// reportOnce 单次上报逻辑
func reportOnce(collector *collector.VMCollector, reporter *HTTPReporter, logger *slog.Logger) {
	// 1. 采集信息
	logger.Info("开始采集VM信息...")
	vmReport, err := collector.Collect()
	if err != nil {
		log.Printf("采集失败：%v", err)
		return
	}

	// 2. 上报信息
	b, _ := json.Marshal(vmReport)
	logger.Info("开始上报VM信息：%v", string(b))
	err = reporter.Report(vmReport)
	if err != nil {
		logger.Error("上报失败：%v", err)
		return
	}

	logger.Info("上报成功（ReportID：%s）", vmReport.ReportID)
}
