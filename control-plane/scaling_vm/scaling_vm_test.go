package scaling_vm

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestVMLifecycle(t *testing.T) {
	// ==== 配置 ====
	projectID := "civil-honor-480405-e0"                              // 替换为你的 GCP 项目
	zone := "us-central1-a"                                           // 机房
	vmName := "test-vm-001"                                           // 测试 VM 名称
	credFile := "/home/matth/civil-honor-480405-e0-bdec4345bdd7.json" // 服务账号 JSON 文件路径

	// ==== 初始化日志 ====
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// ==== 上下文 ====
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// ==== 创建 VM ====
	t.Log("==== 创建 VM ====")
	err := CreateVM(ctx, logger, projectID, zone, vmName, credFile)
	if err != nil {
		t.Fatalf("创建 VM 失败: %v", err)
	}
	return

	// ==== 等待 VM 启动完成 ====
	t.Log("==== 等待 30 秒让 VM 启动 ====")
	time.Sleep(30 * time.Second)

	// ==== 获取 VM 公网 IP ====
	t.Log("==== 获取 VM 公网 IP ====")
	ip, err := GetVMExternalIP(ctx, logger, projectID, zone, vmName, credFile)
	if err != nil {
		t.Errorf("获取 VM 公网 IP 失败: %v", err)
	} else {
		t.Logf("VM 公网 IP: %s", ip)
	}

	// ==== 删除 VM ====
	t.Log("==== 删除 VM ====")
	err = DeleteVM(ctx, logger, projectID, zone, vmName, credFile)
	if err != nil {
		t.Errorf("删除 VM 失败: %v", err)
	} else {
		t.Log("VM 删除操作已提交")
	}
}
