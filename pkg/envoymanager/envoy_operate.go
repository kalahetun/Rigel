package envoymanager

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const EnvoyPath = "/home/matth/envoy"

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

func (o *EnvoyOperator) HotReloadEnvoyConfig() error {
	// 步骤1：渲染最新配置
	//if err := RenderEnvoyYamlConfig(o.GlobalCfg, o.ConfigPath); err != nil {
	//	return fmt.Errorf("渲染配置失败: %w", err)
	//}

	// 步骤2：读取上一次 epoch
	epoch := 0
	if data, err := os.ReadFile("/tmp/envoy_epoch"); err == nil {
		if n, err := strconv.Atoi(string(data)); err == nil {
			epoch = n
		}
	}

	newEpoch := epoch + 1

	// 步骤3：启动新 Envoy 进程
	cmd := exec.Command(EnvoyPath,
		"-c", o.ConfigPath,
		"--restart-epoch", fmt.Sprintf("%d", newEpoch),
		//"--hot-restart-epoch", fmt.Sprintf("%d", newEpoch),
		"--base-id", "1",
		//"--admin-address", "0.0.0.0:9901",
		"--log-level", "info",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动新 Envoy 失败: %w", err)
	}

	// 步骤4：更新 epoch 文件
	if err := os.WriteFile("/tmp/envoy_epoch", []byte(fmt.Sprintf("%d", newEpoch)), 0644); err != nil {
		return fmt.Errorf("写入 epoch 文件失败: %w", err)
	}

	return nil
}
