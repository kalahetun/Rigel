package probing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	//ControlHost    = "http://34.69.185.247:8081"
	ProbingTaskURL = "/api/v1/probe/tasks"
)

func GetProbeTasks(controlHost string) ([]string, error) {

	url := controlHost + ProbingTaskURL

	// 1. 发起 HTTP GET 请求
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("请求接口失败: %w", err)
	}
	defer resp.Body.Close()

	// 2. 读取响应 body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 3. 定义服务端返回结构
	var serverResp struct {
		Code int      `json:"code"`
		Msg  string   `json:"msg"`
		Data []string `json:"data"`
	}

	// 4. 解析 JSON
	if err := json.Unmarshal(body, &serverResp); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}

	// 5. 检查返回码
	if serverResp.Code != 200 {
		return nil, fmt.Errorf("接口返回错误: %d %s", serverResp.Code, serverResp.Msg)
	}

	// 6. 返回节点列表
	return serverResp.Data, nil
}
