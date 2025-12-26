package envoy_manager

import (
	"bufio"
	"context"
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

// slogWriter 自定义Writer，解析Envoy日志真实级别后写入slog
type slogWriter struct {
	logger *slog.Logger
	stream string // 标记是stdout还是stderr（仅备用）
}

// Write 实现io.Writer接口：解析真实级别，保留原始换行符
func (w *slogWriter) Write(p []byte) (n int, err error) {
	// 1. 保留原始内容（包含换行符）
	content := string(p)
	if strings.TrimSpace(content) == "" { // 过滤空行
		return len(p), nil
	}

	// 2. 核心：解析Envoy日志的真实级别
	level, msg := parseEnvoyLogLevel(content)

	// 3. 按真实级别写入slog，保留原始content
	switch level {
	case slog.LevelDebug:
		w.logger.DebugContext(context.Background(), "envoy_log", "stream", w.stream, "content", msg)
	case slog.LevelInfo:
		w.logger.InfoContext(context.Background(), "envoy_log", "stream", w.stream, "content", msg)
	case slog.LevelWarn:
		w.logger.WarnContext(context.Background(), "envoy_log", "stream", w.stream, "content", msg)
	case slog.LevelError:
		w.logger.ErrorContext(context.Background(), "envoy_log", "stream", w.stream, "content", msg)
	default:
		// 无法识别的级别，按info输出
		w.logger.InfoContext(context.Background(), "envoy_log", "stream", w.stream, "content", msg)
	}

	return len(p), nil
}

// parseEnvoyLogLevel 解析Envoy日志的真实级别
// Envoy日志格式：[2025-12-26 16:33:41.715][267487][info][main] xxxx
func parseEnvoyLogLevel(content string) (slog.Level, string) {
	switch {
	case strings.Contains(content, "[debug]"):
		return slog.LevelDebug, content
	case strings.Contains(content, "[info]"):
		return slog.LevelInfo, content
	case strings.Contains(content, "[warn]"):
		return slog.LevelWarn, content
	case strings.Contains(content, "[error]"):
		return slog.LevelError, content
	case strings.Contains(content, "[critical]"):
		return slog.LevelError, content // critical归为error级别
	default:
		return slog.LevelInfo, content // 未知级别默认info
	}
}

// teeWriter 实现双输出：控制台 + slog（带缓冲但及时刷新）
type teeWriter struct {
	console io.Writer     // 控制台输出（os.Stdout/os.Stderr）
	slog    *bufio.Writer // slog缓冲Writer
}

// Write 实现io.Writer接口，核心：透传所有字节（含\n）+ 刷新缓冲
func (t *teeWriter) Write(p []byte) (n int, err error) {
	// 第一步：写入控制台（保留原始换行符）
	n, err = t.console.Write(p)
	if err != nil {
		return n, err
	}

	// 第二步：写入slog缓冲（包含\n）
	_, err = t.slog.Write(p)
	if err != nil {
		return n, err
	}

	// 关键点：如果包含换行符，立即刷新缓冲（避免\n被吞）
	if len(p) > 0 && p[len(p)-1] == '\n' {
		err = t.slog.Flush()
	}

	return n, err
}

// 辅助函数：创建带双输出的Writer
func NewEnvoyLogWriter(logger *slog.Logger, stream string, console io.Writer) io.Writer {
	// 创建slogWriter
	slogW := &slogWriter{
		logger: logger,
		stream: stream,
	}
	// 包装为缓冲Writer（避免频繁写入）
	bufSlogW := bufio.NewWriter(slogW)
	// 双输出：控制台 + slog
	return &teeWriter{
		console: console,
		slog:    bufSlogW,
	}
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
	//stdoutSlogWriter := &slogWriter{logger: logger1, stream: "stdout"}
	//stderrSlogWriter := &slogWriter{logger: logger1, stream: "stderr"}

	stdoutSlogWriter := NewEnvoyLogWriter(logger1, "stdout", os.Stdout)
	stderrSlogWriter := NewEnvoyLogWriter(logger1, "stderr", os.Stderr)

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
