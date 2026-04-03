package vultr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	VultrAPIBase = "https://api.vultr.com/v2"
	OsID         = 535 // Debian 12
	Plan         = "voc-g-8c-32gb-160s"
	//Timeout      = 1 * time.Minute
)

type Config struct {
	APIKey string `json:"apiKey"` // Vultr API密钥
	Region string `json:"region"` // 机房区域（如 "ewr"）
	//Plan   string `json:"plan"`   // 实例规格（如 "vc2-1c-2gb"）
	//SSHKeys string `json:"sshKeys"` // SSH密钥ID列表（逗号分隔）
}

type ScalingOperate struct {
	apiKey  string   // Vultr API密钥
	region  string   // 区域
	plan    string   // 实例规格
	sshKeys []string // SSH密钥ID列表
	osID    int      // 操作系统ID
	timeout time.Duration
}

func (vc *ScalingOperate) CreateVM(ctx context.Context, vmName string, pre string, logger *slog.Logger) (string, error) {

	reqBody := createInstanceReq{
		Region:   vc.region,
		Plan:     vc.plan,
		OsID:     vc.osID,
		Label:    vmName,
		Hostname: vmName,
		SSHKeys:  vc.sshKeys,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		logger.Error("序列化创建VM请求体失败", slog.String("pre", pre), slog.String("vmName", vmName), "error", err)
		return "", err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		VultrAPIBase+"/instances",
		bytes.NewReader(data),
	)
	if err != nil {
		logger.Error("创建Vultr VM请求失败", slog.String("pre", pre), slog.String("vmName", vmName), "error", err)
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+vc.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: vc.timeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("发送Vultr VM创建请求失败", slog.String("pre", pre), slog.String("vmName", vmName), "error", err)
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("创建Vultr VM失败: %s", body)
		logger.Error("Vultr VM创建失败", slog.String("pre", pre), slog.String("vmName", vmName), "error", err)
		return "", err
	}

	var result createInstanceResp
	if err := json.Unmarshal(body, &result); err != nil {
		logger.Error("解析Vultr创建VM响应失败", slog.String("pre", pre), slog.String("vmName", vmName), "error", err)
		return "", err
	}

	logger.Info("Vultr VM创建成功", slog.String("pre", pre),
		slog.String("vmName", vmName), slog.String("instanceID", result.Instance.ID))

	return result.Instance.ID, nil
}

func (vc *ScalingOperate) GetVMPublicIP(
	ctx context.Context,
	vmName string,
	pre string,
	logger *slog.Logger,
) (string, error) {

	// Vultr API通过instanceID查询，因此vmName参数实际传入instanceID
	instanceID := vmName

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		VultrAPIBase+"/instances/"+instanceID,
		nil,
	)
	if err != nil {
		logger.Error("创建Vultr查询IP请求失败", slog.String("pre", pre), slog.String("instanceID", instanceID), "error", err)
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+vc.apiKey)

	client := &http.Client{Timeout: vc.timeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("发送Vultr查询IP请求失败", slog.String("pre", pre), slog.String("instanceID", instanceID), "error", err)
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("获取Vultr VM IP失败: %s", body)
		logger.Error("Vultr查询IP失败", slog.String("pre", pre), slog.String("instanceID", instanceID), "error", err)
		return "", err
	}

	var result getInstanceResp
	if err := json.Unmarshal(body, &result); err != nil {
		logger.Error("解析Vultr查询IP响应失败", slog.String("pre", pre), slog.String("instanceID", instanceID), "error", err)
		return "", err
	}

	logger.Info("获取Vultr VM公网IP", slog.String("pre", pre), slog.String("instanceID", instanceID),
		slog.String("ip", result.Instance.MainIP), slog.String("status", result.Instance.Status))

	return result.Instance.MainIP, nil
}

func (vc *ScalingOperate) DeleteVM(
	ctx context.Context,
	vmName string,
	pre string,
	logger *slog.Logger,
) error {

	// Vultr API通过instanceID删除，因此vmName参数实际传入instanceID
	instanceID := vmName

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		VultrAPIBase+"/instances/"+instanceID,
		nil,
	)
	if err != nil {
		logger.Error("创建Vultr删除VM请求失败", slog.String("pre", pre), slog.String("instanceID", instanceID), "error", err)
		return err
	}

	req.Header.Set("Authorization", "Bearer "+vc.apiKey)

	client := &http.Client{Timeout: vc.timeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("发送Vultr删除VM请求失败", slog.String("pre", pre), slog.String("instanceID", instanceID), "error", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("删除Vultr VM失败: %s", body)
		logger.Error("Vultr删除VM失败", slog.String("pre", pre), slog.String("instanceID", instanceID), "error", err)
		return err
	}

	logger.Info("Vultr VM删除成功", slog.String("pre", pre), slog.String("instanceID", instanceID))

	return nil
}

func NewScalingOperate(
	cfg *Config,
	sshKey string,
	pre string,
	logger *slog.Logger,
) *ScalingOperate {

	var sshKeys []string
	sshKeys = append(sshKeys, sshKey)

	so := &ScalingOperate{
		apiKey:  cfg.APIKey,
		region:  cfg.Region,
		plan:    Plan,
		sshKeys: sshKeys,
		osID:    OsID,
		timeout: 1 * time.Minute,
	}
	logger.Info("NewScalingOperate", slog.String("pre", pre), slog.Any("ScalingOperate", *so))

	return so
}

// ExtractVultrFromInterface 解析JSON字符串为*VultrConfig（对齐GCP的ExtractGCPFromInterface）
func ExtractVultrFromInterface(data string) (*Config, error) {
	if data == "" {
		return nil, errors.New("输入的JSON字符串为空")
	}

	config := &Config{}
	if err := json.Unmarshal([]byte(data), config); err != nil {
		return nil, fmt.Errorf("解析Vultr配置失败: %w", err)
	}

	// 基础校验
	if config.APIKey == "" {
		return nil, errors.New("Vultr APIKey不能为空")
	}
	if config.Region == "" {
		return nil, errors.New("Vultr Region不能为空")
	}

	return config, nil
}

type createInstanceReq struct {
	Region   string   `json:"region"`
	Plan     string   `json:"plan"`
	OsID     int      `json:"os_id"`
	Label    string   `json:"label,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	SSHKeys  []string `json:"sshkey_id,omitempty"`
}

type createInstanceResp struct {
	Instance struct {
		ID string `json:"id"`
	} `json:"instance"`
}

type getInstanceResp struct {
	Instance struct {
		ID     string `json:"id"`
		MainIP string `json:"main_ip"`
		Status string `json:"status"`
	} `json:"instance"`
}
