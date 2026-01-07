package scaling_vm

import (
	"context"
	"log/slog"
	"testing"
)

// TestCreateVM_Basic 仅测试函数流程，不实际调用 GCP
func TestCreateVM_Basic(t *testing.T) {
	// 1. 创建 logger
	logger := slog.New(slog.NewTextHandler(nil, nil))

	// 2. 创建上下文
	ctx := context.Background()

	// 3. 测试参数
	projectID := "test-project"
	zone := "us-central1-a"
	vmName := "test-vm-001"

	// 4. 调用 CreateVM（注意：真实环境会尝试创建 VM，如果想完全 mock，需要替换 instancesClient）
	err := CreateVM(ctx, logger, projectID, zone, vmName)
	if err != nil {
		// 因为没有真实权限，预期会报错
		t.Logf("CreateVM 返回错误（预期，因为没有真实 GCP 权限）: %v", err)
	} else {
		t.Log("CreateVM 函数执行完成（注意：如果有真实权限，会创建 VM）")
	}
}
