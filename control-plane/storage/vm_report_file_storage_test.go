package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	model "control-plane/pkg/local_info_manager"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// 测试用临时目录
const testStorageDir = "./test_vm_storage"

// 清理测试目录
func cleanTestDir(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(testStorageDir); err != nil {
		t.Fatalf("清理测试目录失败：%v", err)
	}
}

// 构建测试用VMReport
func buildTestVMReport(vmID string) *model.VMReport {
	return &model.VMReport{
		VMID:        vmID,
		CollectTime: time.Now().UTC(),
		ReportID:    uuid.NewString(),
		CPU: model.CPUInfo{
			PhysicalCore: 2,
			LogicalCore:  4,
			Usage:        25.3,
		},
		Memory: model.MemoryInfo{
			Total: 17179869184,
			Used:  8589934592,
			Free:  8589934592,
			Usage: 50.0,
		},
		Network: model.NetworkInfo{
			PublicIP:  "114.114.114.114",
			PrivateIP: "192.168.1.100",
			PortCount: 20,
		},
	}
}

// TestNewFileStorage 测试初始化文件存储
func TestNewFileStorage(t *testing.T) {
	// 前置清理
	cleanTestDir(t)
	defer cleanTestDir(t)

	// 正常场景：目录不存在，自动创建
	fs, err := NewFileStorage(testStorageDir)
	assert.NoError(t, err)
	assert.NotNil(t, fs)
	assert.Equal(t, testStorageDir, fs.storageDir)

	// 验证目录已创建
	_, err = os.Stat(testStorageDir)
	assert.NoError(t, err)

	// 异常场景：目录路径非法（如系统禁止的路径，这里用权限测试）
	// 注：Windows需替换为权限不足的路径，Linux/Mac可使用 /root/test（普通用户无权限）
	/*
		invalidDir := "/root/invalid_test_dir"
		fs2, err2 := NewFileStorage(invalidDir)
		assert.Error(t, err2)
		assert.Nil(t, fs2)
	*/
}

// TestPut 测试存储/更新VM数据
func TestPut(t *testing.T) {
	cleanTestDir(t)
	defer cleanTestDir(t)

	// 初始化存储
	fs, err := NewFileStorage(testStorageDir)
	assert.NoError(t, err)

	// 测试用例1：空VMReport
	_, err = fs.Put(nil)
	assert.Error(t, err)
	assert.Equal(t, "VMReport不能为空且VMID必须非空", err.Error())

	// 测试用例2：VMID为空
	emptyVMIDReport := buildTestVMReport("")
	_, err = fs.Put(emptyVMIDReport)
	assert.Error(t, err)
	assert.Equal(t, "VMReport不能为空且VMID必须非空", err.Error())

	// 测试用例3：正常存储
	testVMID := "vm-test-001"
	testReport := buildTestVMReport(testVMID)
	savedReportID, err := fs.Put(testReport)
	assert.NoError(t, err)
	assert.Equal(t, testReport.ReportID, savedReportID)

	// 验证文件已创建
	filePath := filepath.Join(testStorageDir, fmt.Sprintf("%s.json", testVMID))
	_, err = os.Stat(filePath)
	assert.NoError(t, err)

	// 验证文件内容正确
	fileContent, err := os.ReadFile(filePath)
	assert.NoError(t, err)
	var savedReport model.VMReport
	err = json.Unmarshal(fileContent, &savedReport)
	assert.NoError(t, err)
	assert.Equal(t, testReport.VMID, savedReport.VMID)
	assert.Equal(t, testReport.CPU.PhysicalCore, savedReport.CPU.PhysicalCore)
	assert.Equal(t, testReport.Network.PublicIP, savedReport.Network.PublicIP)

	// 测试用例4：更新数据（覆盖写入）
	testReport.CPU.Usage = 30.5 // 修改CPU使用率
	updatedReportID, err := fs.Put(testReport)
	assert.NoError(t, err)
	assert.Equal(t, testReport.ReportID, updatedReportID)

	// 验证数据已更新
	updatedContent, err := os.ReadFile(filePath)
	assert.NoError(t, err)
	var updatedReport model.VMReport
	err = json.Unmarshal(updatedContent, &updatedReport)
	assert.NoError(t, err)
	assert.Equal(t, 30.5, updatedReport.CPU.Usage)
}

// TestGet 测试读取VM数据
func TestGet(t *testing.T) {
	cleanTestDir(t)
	defer cleanTestDir(t)

	// 初始化存储
	fs, err := NewFileStorage(testStorageDir)
	assert.NoError(t, err)

	// 测试用例1：VMID为空
	_, err = fs.Get("")
	assert.Error(t, err)
	assert.Equal(t, "VMID不能为空", err.Error())

	// 测试用例2：VMID不存在
	_, err = fs.Get("vm-not-exist")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "VM[vm-not-exist]的上报文件不存在")

	// 测试用例3：正常读取
	testVMID := "vm-test-002"
	testReport := buildTestVMReport(testVMID)
	_, err = fs.Put(testReport)
	assert.NoError(t, err)

	// 读取数据
	gotReport, err := fs.Get(testVMID)
	assert.NoError(t, err)
	assert.NotNil(t, gotReport)
	assert.Equal(t, testVMID, gotReport.VMID)
	assert.Equal(t, testReport.ReportID, gotReport.ReportID)
	assert.Equal(t, testReport.Memory.Total, gotReport.Memory.Total)
	assert.Equal(t, testReport.Network.PortCount, gotReport.Network.PortCount)
}

// TestSave 测试兼容API层的Save方法（内部调用Put）
func TestSave(t *testing.T) {
	cleanTestDir(t)
	defer cleanTestDir(t)

	// 初始化存储
	fs, err := NewFileStorage(testStorageDir)
	assert.NoError(t, err)

	// 测试Save方法等价于Put
	testVMID := "vm-test-003"
	testReport := buildTestVMReport(testVMID)
	savedID, err := fs.Save(testReport)
	assert.NoError(t, err)
	assert.Equal(t, testReport.ReportID, savedID)

	// 验证数据已存储
	gotReport, err := fs.Get(testVMID)
	assert.NoError(t, err)
	assert.Equal(t, testReport.ReportID, gotReport.ReportID)
}

// TestConcurrentPut 测试并发写入（保证读写锁生效）
func TestConcurrentPut(t *testing.T) {
	cleanTestDir(t)
	defer cleanTestDir(t)

	// 初始化存储
	fs, err := NewFileStorage(testStorageDir)
	assert.NoError(t, err)

	testVMID := "vm-concurrent-001"
	var wg sync.WaitGroup
	concurrentCount := 100 // 并发100次写入

	// 并发写入相同VMID的数据
	for i := 0; i < concurrentCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			report := buildTestVMReport(testVMID)
			report.CPU.Usage = float64(idx) // 不同的CPU使用率
			_, err := fs.Put(report)
			assert.NoError(t, err)
		}(i)
	}

	// 等待所有协程完成
	wg.Wait()

	// 验证文件最终存在且可正常读取
	gotReport, err := fs.Get(testVMID)
	assert.NoError(t, err)
	assert.NotNil(t, gotReport)
	assert.Equal(t, testVMID, gotReport.VMID)

	// 验证文件未损坏（JSON可正常解析）
	filePath := filepath.Join(testStorageDir, fmt.Sprintf("%s.json", testVMID))
	content, err := os.ReadFile(filePath)
	assert.NoError(t, err)
	var report model.VMReport
	err = json.Unmarshal(content, &report)
	assert.NoError(t, err)
}
