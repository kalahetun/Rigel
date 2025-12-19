package envoymanager

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
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
	// 初始化时检查当前运行用户是否为matth
	checkCurrentUserIsMatth()
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

	// 5. 先检查是否有运行的Envoy，没有则首次启动，有则热重启
	if !o.IsEnvoyRunning() {
		if err := o.StartFirstEnvoy(); err != nil {
			return EnvoyPortConfig{}, fmt.Errorf("首次启动Envoy失败: %w", err)
		}
	} else {
		if err := o.HotReloadEnvoyConfig(); err != nil {
			return EnvoyPortConfig{}, fmt.Errorf("热加载配置失败: %w", err)
		}
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

// StartFirstEnvoy 首次启动Envoy（epoch=0）
func (o *EnvoyOperator) StartFirstEnvoy() error {
	// 检查配置文件是否存在
	if _, err := os.Stat(o.ConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("配置文件不存在: %s", o.ConfigPath)
	}

	// 构造首次启动命令（epoch=0）
	cmd := exec.Command(
		EnvoyPath,
		"-c", o.ConfigPath,
		"--restart-epoch", "0",
		"--base-id", "1000",
		"--log-level", "info",
		"--enable-shared-memory",
	)

	// 日志输出
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 启动进程
	log.Println("🚀 首次启动Envoy（epoch=0）")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}

	// 验证进程是否存活
	time.Sleep(1 * time.Second)
	if !isProcessAlive(cmd.Process.Pid) {
		return errors.New("Envoy启动后立即退出")
	}

	// 初始化epoch文件
	if err := os.WriteFile("/tmp/envoy_epoch", []byte("0"), 0644); err != nil {
		log.Printf("⚠️ 写入epoch文件警告: %v", err)
	}

	// 后台等待进程（防止僵尸）
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("Envoy进程退出: %v", err)
		}
	}()

	log.Printf("✅ Envoy首次启动成功，PID: %d", cmd.Process.Pid)
	return nil
}

// HotReloadEnvoyConfig 修复后的热重启函数
func (o *EnvoyOperator) HotReloadEnvoyConfig() error {
	// 前置检查：确保Envoy正在运行
	if !o.IsEnvoyRunning() {
		return errors.New("Envoy未运行，无法热重启")
	}

	// ===== 1. 读取上一次 epoch =====
	epoch := 0
	if data, err := os.ReadFile("/tmp/envoy_epoch"); err == nil {
		s := strings.TrimSpace(string(data))
		if n, err := strconv.Atoi(s); err == nil {
			epoch = n
		}
	}
	newEpoch := epoch + 1

	// ===== 2. 启动新 Envoy =====
	cmd := exec.Command(
		EnvoyPath,
		"-c", o.ConfigPath,
		"--restart-epoch", strconv.Itoa(newEpoch),
		"--base-id", "1000",
		"--log-level", "info",
		"--enable-shared-memory",
	)

	// 日志输出
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 启动新进程
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动新Envoy失败: %w", err)
	}

	// 验证新进程存活
	time.Sleep(2 * time.Second)
	if !isProcessAlive(cmd.Process.Pid) {
		return fmt.Errorf("新Envoy进程启动后立即退出（PID: %d）", cmd.Process.Pid)
	}

	// 后台等待新进程（防止僵尸）
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("新Envoy进程退出: %v", err)
		}
	}()

	// ===== 3. 更新 epoch 文件 =====
	if err := os.WriteFile(
		"/tmp/envoy_epoch",
		[]byte(strconv.Itoa(newEpoch)),
		0644,
	); err != nil {
		return fmt.Errorf("写入epoch文件失败: %w", err)
	}

	log.Printf("✅ Envoy热重启成功，旧epoch: %d → 新epoch: %d", epoch, newEpoch)
	return nil
}

// IsEnvoyRunning 检查Envoy是否正在运行
func (o *EnvoyOperator) IsEnvoyRunning() bool {
	cmd := exec.Command("pgrep", "-u", "matth", "envoy")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != ""
}

// -------------------------- 私有辅助函数 --------------------------
// checkCurrentUserIsMatth 检查当前运行用户是否为matth
func checkCurrentUserIsMatth() {
	currentUser := os.Getenv("USER")
	if currentUser != "matth" {
		log.Fatalf("❌ 必须以matth用户运行此程序（当前用户：%s）", currentUser)
	}
}

// isProcessAlive 检查进程是否存活
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// 发送空信号检查进程是否存在
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}
