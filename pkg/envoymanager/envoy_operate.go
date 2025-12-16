package envoymanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
)

// EnvoyOperator Envoy操作器（适配matth目录）
type EnvoyOperator struct {
	AdminAddr  string // 管理地址（固定为http://127.0.0.1:9901）
	ConfigPath string // 配置文件路径（固定为/home/matth/envoy.yaml）
	GlobalCfg  EnvoyGlobalConfig
}

// NewEnvoyOperator 创建Envoy操作器实例
func NewEnvoyOperator(adminAddr, configPath string) *EnvoyOperator {
	// 标准化配置文件路径（确保是绝对路径）
	absPath, _ := filepath.Abs(configPath)
	return &EnvoyOperator{
		AdminAddr:  adminAddr,
		ConfigPath: absPath,
	}
}

// InitEnvoyGlobalConfig 初始化Envoy全局配置
func (o *EnvoyOperator) InitEnvoyGlobalConfig(adminPort int) error {
	o.GlobalCfg = EnvoyGlobalConfig{
		AdminPort: adminPort,
		Ports:     make([]EnvoyPortConfig, 0),
	}
	return nil
}

// CreateOrUpdateEnvoyPort 新增/更新Envoy端口配置
func (o *EnvoyOperator) CreateOrUpdateEnvoyPort(req EnvoyPortCreateReq) (EnvoyPortConfig, error) {
	// 1. 检查端口是否已存在
	portIdx := -1
	for i, p := range o.GlobalCfg.Ports {
		if p.Port == req.Port {
			portIdx = i
			break
		}
	}

	// 2. 构造端口配置
	newPortCfg := EnvoyPortConfig{
		Port:       req.Port,
		TargetPort: req.TargetPort,
		Enabled:    true,
		RateLimit:  req.RateLimit,
	}

	// 3. 更新/新增端口配置
	if portIdx >= 0 {
		o.GlobalCfg.Ports[portIdx] = newPortCfg
	} else {
		o.GlobalCfg.Ports = append(o.GlobalCfg.Ports, newPortCfg)
	}

	// 4. 渲染配置文件到matth目录
	if err := RenderEnvoyYamlConfig(o.GlobalCfg, o.ConfigPath); err != nil {
		return EnvoyPortConfig{}, fmt.Errorf("渲染配置失败: %w", err)
	}

	// 5. 热加载配置
	if err := o.HotReloadEnvoyConfig(); err != nil {
		return EnvoyPortConfig{}, fmt.Errorf("热加载配置失败: %w", err)
	}

	return newPortCfg, nil
}

// DisableEnvoyPort 禁用Envoy端口
func (o *EnvoyOperator) DisableEnvoyPort(port int) error {
	// 1. 查找端口并禁用
	portIdx := -1
	for i, p := range o.GlobalCfg.Ports {
		if p.Port == port {
			portIdx = i
			break
		}
	}
	if portIdx < 0 {
		return errors.New("端口未配置")
	}

	o.GlobalCfg.Ports[portIdx].Enabled = false

	// 2. 重新渲染配置到matth目录
	if err := RenderEnvoyYamlConfig(o.GlobalCfg, o.ConfigPath); err != nil {
		return fmt.Errorf("渲染禁用端口配置失败: %w", err)
	}

	// 3. 热加载配置
	return o.HotReloadEnvoyConfig()
}

// GetEnvoyPortConfig 查询指定端口配置
func (o *EnvoyOperator) GetEnvoyPortConfig(port int) (EnvoyPortConfig, error) {
	for _, p := range o.GlobalCfg.Ports {
		if p.Port == port {
			return p, nil
		}
	}
	return EnvoyPortConfig{}, errors.New("端口未找到")
}

// HotReloadEnvoyConfig 热加载Envoy配置（适配1.28.0）
func (o *EnvoyOperator) HotReloadEnvoyConfig() error {
	// 1. 构造热加载请求（固定调用9901管理端口）
	hotReloadURL := fmt.Sprintf("%s/admin/v3/config_reload", o.AdminAddr)
	reqBody, _ := json.Marshal(map[string]string{"resource": "static"})
	req, err := http.NewRequest("POST", hotReloadURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// 2. 发送热加载请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		// 降级方案：执行curl命令热加载（matth用户可执行）
		cmd := exec.Command("curl", "-X", "POST", hotReloadURL)
		if cmdErr := cmd.Run(); cmdErr != nil {
			return fmt.Errorf("API热加载失败，系统命令也失败: %w, %v", err, cmdErr)
		}
		return nil
	}
	defer resp.Body.Close()

	// 3. 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("热加载响应异常: %s", resp.Status)
	}

	return nil
}
