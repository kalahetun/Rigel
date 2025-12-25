package envoy_manager

import (
	"bufio"
	"data-plane/util"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// 核心常量（适配你的环境）
const (
	EnvoyPath     = "/home/matth/envoy"           // Envoy二进制路径
	DefaultConfig = "/home/matth/envoy-mini.yaml" // Envoy配置文件路径
	EpochFile     = "/tmp/envoy_epoch"            // epoch记录文件
)

// EnvoyStarter 仅负责Envoy启动的极简结构体
type EnvoyStarter struct {
	configPath string // 配置文件路径
	baseID     int    // Envoy base-id（避免端口冲突）
}

// NewEnvoyStarter 创建启动器实例（默认使用matth目录配置）
func NewEnvoyStarter() *EnvoyStarter {
	absConfigPath, _ := filepath.Abs(DefaultConfig)
	return &EnvoyStarter{
		configPath: absConfigPath,
		baseID:     1000, // 固定base-id，和你原有逻辑一致
	}
}

// slogWriter 适配slog的Writer，将输出转发到logger1
type slogWriter struct {
	logger *slog.Logger // 你指定的logger1
	stream string       // 区分stdout/stderr
}

// Write 实现io.Writer接口，转发输出到logger1
func (s *slogWriter) Write(p []byte) (n int, err error) {
	content := string(p)
	if content == "" {
		return len(p), nil
	}
	// 根据流类型输出到logger1（stdout=INFO，stderr=ERROR）
	if s.stream == "stderr" {
		s.logger.Error(content, "stream", s.stream)
	} else {
		s.logger.Info(content, "stream", s.stream)
	}
	return len(p), nil
}

// teeWriter 实现"控制台+logger1"双输出
type teeWriter struct {
	console io.Writer // 原有控制台输出（os.Stdout/os.Stderr）
	slog    io.Writer // 转发到logger1的Writer
}

func (t *teeWriter) Write(p []byte) (n int, err error) {
	// 1. 先输出到控制台（保留原有逻辑）
	n1, err1 := t.console.Write(p)
	// 2. 再转发到logger1（额外输出）
	_, _ = t.slog.Write(p) // 忽略logger1写入错误，优先保证控制台输出
	return n1, err1
}

// StartEnvoy 启动Envoy（首次启动，epoch=0）
func (s *EnvoyStarter) StartEnvoy(logger, logger1 *slog.Logger) (int, error) {
	// 1. 检查配置文件是否存在
	if _, err := os.Stat(s.configPath); os.IsNotExist(err) {
		return 0, fmt.Errorf("配置文件不存在: %s", s.configPath)
	}

	// 2. 检查是否已运行（避免重复启动）
	if s.IsEnvoyRunning() {
		return 0, errors.New("Envoy已在运行，无需重复启动")
	}

	// 3. 构造启动命令（核心：加载配置，epoch=0）
	cmd := exec.Command(
		EnvoyPath,
		"-c", s.configPath, // 指定配置文件
		"--restart-epoch", "0", // 首次启动epoch=0
		"--base-id", strconv.Itoa(s.baseID), // 基础ID
		"--log-level", "info", // 日志级别
	)

	logger.Info("Test config load", util.Config_.EnvoyPath)

	// 日志输出
	//cmd.Stdout = os.Stdout
	//cmd.Stderr = os.Stderr
	// --------------------------
	// 核心修改：保留控制台输出 + 转发到logger1
	// --------------------------
	// 1. 创建stdout/stderr对应的slogWriter（关联logger1）
	stdoutSlogWriter := &slogWriter{logger: logger1, stream: "stdout"}
	stderrSlogWriter := &slogWriter{logger: logger1, stream: "stderr"}

	// 2. 带缓冲避免阻塞，包装成teeWriter实现双输出
	cmd.Stdout = &teeWriter{
		console: os.Stdout,
		slog:    bufio.NewWriter(stdoutSlogWriter),
	}
	cmd.Stderr = &teeWriter{
		console: os.Stderr,
		slog:    bufio.NewWriter(stderrSlogWriter),
	}

	// 5. 启动进程
	logger.Info("开始启动Envoy，配置文件：%s", s.configPath)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("Envoy启动失败: %w", err)
	}

	// 6. 验证进程是否存活
	time.Sleep(1 * time.Second)
	if !isProcessAlive(cmd.Process.Pid) {
		return 0, errors.New("Envoy启动后立即退出")
	}

	// 7. 记录epoch（为后续热重载预留）
	if err := os.WriteFile(EpochFile, []byte("0"), 0644); err != nil {
		logger.Error("警告：写入epoch文件失败: %v", err)
	}

	// 8. 后台等待进程（防止僵尸进程）
	go func(pid int) {
		if err := cmd.Wait(); err != nil {
			logger.Error("Envoy进程(PID:%d)退出: %v", pid, err)
		}
	}(cmd.Process.Pid)

	logger.Info("Envoy启动成功，PID: %d", cmd.Process.Pid)
	return cmd.Process.Pid, nil
}

// IsEnvoyRunning 检查Envoy是否正在运行（仅检查matth用户的envoy进程）
func (s *EnvoyStarter) IsEnvoyRunning() bool {
	//cmd := exec.Command("pgrep", "-u", "matth", "envoy")
	cmd := exec.Command("pgrep", "envoy")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != ""
}

// ---------------- 私有辅助函数 ----------------
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
