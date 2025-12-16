package envoymanager

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"
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

// HotReloadEnvoyConfig 正确的Envoy静态配置热加载（先写配置文件，再触发重载）
func (o *EnvoyOperator) HotReloadEnvoyConfig() error {
	// 外面已经做了
	// 步骤1：先将最新的GlobalCfg渲染为配置文件（核心！确保磁盘配置是最新的）
	//if err := RenderEnvoyYamlConfig(o.GlobalCfg, o.ConfigPath); err != nil {
	//	return fmt.Errorf("热加载前渲染配置文件失败: %w", err)
	//}

	// 步骤2：构造热加载请求（静态配置热加载无需请求体）
	hotReloadURL := fmt.Sprintf("%s/admin/v3/config_reload", o.AdminAddr)
	req, err := http.NewRequest("POST", hotReloadURL, nil) // 请求体为nil（关键修正）
	if err != nil {
		return fmt.Errorf("构造热加载请求失败: %w", err)
	}
	// 无需设置Content-Type（无请求体）

	// 步骤3：发送热加载请求
	client := &http.Client{
		Timeout: 10 * time.Second, // 增加超时，避免卡死
	}
	resp, err := client.Do(req)
	if err != nil {
		// 降级方案：执行curl命令热加载（matth用户可执行）
		cmd := exec.Command("curl", "-X", "POST", "-s", "-o", "/dev/null", hotReloadURL)
		output, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			return fmt.Errorf("API热加载失败，curl降级也失败: %w, curl输出: %s", err, string(output))
		}
		return nil
	}
	defer resp.Body.Close()

	// 步骤4：检查响应状态（200/201都算成功）
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// 读取响应体，便于排查错误
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("热加载响应异常: %s, 响应体: %s", resp.Status, string(body))
	}

	return nil
}
