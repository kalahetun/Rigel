package scaling_vm

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

const (
	projectID = "civil-honor-480405-e0"                               // 替换为你的 GCP 项目
	zone      = "us-central1-a"                                       // 机房
	vmName    = "test-vm-001"                                         // 测试 VM 名称
	credFile  = "/home/matth/civil-honor-480405-e0-bdec4345bdd7.json" // 服务账号 JSON 文件路径
)

// testCreateVM 拆分：创建VM（单一职责：仅处理VM创建逻辑）
func TestCreateVM(t *testing.T) {

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel() // 确保上下文最终被释放

	t.Log("==== 创建 VM ====")
	err := CreateVM(ctx, logger, projectID, zone, vmName, credFile)
	if err != nil {
		// t.Fatalf 会终止当前测试用例，若想后续步骤继续执行，可改为 t.Errorf
		t.Fatalf("创建 VM 失败: %v", err)
	}
}

// testGetVMExternalIP 拆分：获取VM公网IP（单一职责：仅处理公网IP查询逻辑）
func TestGetVMExternalIP(t *testing.T) {

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel() // 确保上下文最终被释放

	t.Log("==== 获取 VM 公网 IP ====")
	ip, err := GetVMExternalIP(ctx, logger, projectID, zone, vmName, credFile)
	if err != nil {
		t.Errorf("获取 VM 公网 IP 失败: %v", err)
	} else {
		t.Logf("VM 公网 IP: %s", ip)
	}
}

// testDeleteVM 拆分：删除VM（单一职责：仅处理VM删除逻辑）
func TestDeleteVM(t *testing.T) {

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel() // 确保上下文最终被释放

	t.Log("==== 删除 VM ====")
	err := DeleteVM(ctx, logger, projectID, zone, vmName, credFile)
	if err != nil {
		t.Errorf("删除 VM 失败: %v", err)
	} else {
		t.Log("VM 删除操作已提交")
	}
}
