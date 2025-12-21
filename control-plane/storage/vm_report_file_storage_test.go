package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	model "control-plane/pkg/local_info_manager"
)

// 测试用临时目录（每次测试前清理）
const testStorageDir = "./test_vm_storage"

// TestNewFileStorage 测试存储实例初始化
func TestNewFileStorage(t *testing.T) {
	// 清理旧测试目录
	_ = os.RemoveAll(testStorageDir)
	defer os.RemoveAll(testStorageDir)

	// 测试正常初始化
	storage, err := NewFileStorage(testStorageDir, 5)
	if err != nil {
		t.Fatalf("初始化存储实例失败: %v", err)
	}
	defer storage.Close()

	// 验证目录创建成功
	if _, err := os.Stat(testStorageDir); os.IsNotExist(err) {
		t.Error("存储目录未创建成功")
	}

	// 测试自定义过期时长
	storage2, err := NewFileStorage(testStorageDir, 10)
	if err != nil {
		t.Fatalf("初始化自定义过期时长失败: %v", err)
	}
	defer storage2.Close()
}

// TestFileStorage_Put 测试数据存储（生成新文件）
func TestFileStorage_Put(t *testing.T) {
	// 清理旧测试目录
	_ = os.RemoveAll(testStorageDir)
	defer os.RemoveAll(testStorageDir)

	// 初始化存储
	storage, err := NewFileStorage(testStorageDir, 5)
	if err != nil {
		t.Fatalf("初始化存储失败: %v", err)
	}
	defer storage.Close()

	// 构造测试数据
	testReport := &model.VMReport{
		VMID:     "vm-test-001",
		ReportID: "report-001",
		// 可补充其他必要字段（根据model定义）
		CollectTime: time.Now(),
	}

	// 测试正常存储
	reportID, err := storage.Put(testReport)
	if err != nil {
		t.Fatalf("存储数据失败: %v", err)
	}
	if reportID != testReport.ReportID {
		t.Errorf("返回的ReportID不匹配，期望: %s, 实际: %s", testReport.ReportID, reportID)
	}

	// 验证文件生成（匹配VMID_时间戳.json格式）
	files, err := os.ReadDir(testStorageDir)
	if err != nil {
		t.Fatalf("读取存储目录失败: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("期望生成1个文件，实际生成: %d个", len(files))
	}

	// 验证文件名格式
	fileName := files[0].Name()
	if !filepath.HasPrefix(fileName, testReport.VMID+"_") {
		t.Errorf("文件名格式错误，期望: %s_*.json, 实际: %s", testReport.VMID, fileName)
	}

	// 测试空数据存储（入参校验）
	_, err = storage.Put(nil)
	if err == nil {
		t.Error("存储空数据未返回错误，不符合预期")
	}
	_, err = storage.Put(&model.VMReport{VMID: ""})
	if err == nil {
		t.Error("存储空VMID数据未返回错误，不符合预期")
	}
}

// TestFileStorage_Get 测试读取最新数据
func TestFileStorage_Get(t *testing.T) {
	// 清理旧测试目录
	_ = os.RemoveAll(testStorageDir)
	defer os.RemoveAll(testStorageDir)

	// 初始化存储
	storage, err := NewFileStorage(testStorageDir, 5)
	if err != nil {
		t.Fatalf("初始化存储失败: %v", err)
	}
	defer storage.Close()

	// 测试读取不存在的VMID
	_, err = storage.Get("vm-not-exist")
	if err == nil {
		t.Error("读取不存在的VMID未返回错误，不符合预期")
	}

	// 测试空VMID读取
	_, err = storage.Get("")
	if err == nil {
		t.Error("读取空VMID未返回错误，不符合预期")
	}

	// 存储多条数据（不同时间戳）
	vmID := "vm-test-002"
	report1 := &model.VMReport{
		VMID:        vmID,
		ReportID:    "report-001",
		CollectTime: time.Now().Add(-10 * time.Second), // 旧数据
	}
	report2 := &model.VMReport{
		VMID:        vmID,
		ReportID:    "report-002",
		CollectTime: time.Now(), // 新数据
	}
	_, err = storage.Put(report1)
	if err != nil {
		t.Fatalf("存储report1失败: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // 确保时间戳不同
	_, err = storage.Put(report2)
	if err != nil {
		t.Fatalf("存储report2失败: %v", err)
	}

	// 读取最新数据
	latestReport, err := storage.Get(vmID)
	if err != nil {
		t.Fatalf("读取最新数据失败: %v", err)
	}
	if latestReport.ReportID != report2.ReportID {
		t.Errorf("读取的最新数据错误，期望: %s, 实际: %s", report2.ReportID, latestReport.ReportID)
	}
}

// TestFileStorage_CleanupExpiredFiles 测试过期文件清理
func TestFileStorage_CleanupExpiredFiles(t *testing.T) {
	// 清理旧测试目录
	_ = os.RemoveAll(testStorageDir)
	defer os.RemoveAll(testStorageDir)

	// 初始化存储（过期时长1秒，便于测试）
	storage, err := NewFileStorage(testStorageDir, 1)
	if err != nil {
		t.Fatalf("初始化存储失败: %v", err)
	}
	defer storage.Close()

	// 存储测试数据
	vmID := "vm-test-003"
	report := &model.VMReport{
		VMID:     vmID,
		ReportID: "report-001",
	}
	_, err = storage.Put(report)
	if err != nil {
		t.Fatalf("存储数据失败: %v", err)
	}

	// 验证文件存在
	filesBefore, err := os.ReadDir(testStorageDir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(filesBefore) != 1 {
		t.Error("清理前文件数量错误")
	}

	// 等待过期+清理（清理协程每分钟执行，手动触发一次）
	time.Sleep(2 * time.Second)
	err = storage.cleanupExpiredFiles()
	if err != nil {
		t.Fatalf("手动清理过期文件失败: %v", err)
	}

	// 验证文件被清理
	filesAfter, err := os.ReadDir(testStorageDir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(filesAfter) != 0 {
		t.Error("过期文件未被清理")
	}
}

// TestFileStorage_Concurrent 测试并发读写安全
func TestFileStorage_Concurrent(t *testing.T) {
	// 清理旧测试目录
	_ = os.RemoveAll(testStorageDir)
	defer os.RemoveAll(testStorageDir)

	// 初始化存储
	storage, err := NewFileStorage(testStorageDir, 5)
	if err != nil {
		t.Fatalf("初始化存储失败: %v", err)
	}
	defer storage.Close()

	// 并发写入
	var wg sync.WaitGroup
	concurrentNum := 100
	vmID := "vm-concurrent-001"

	// 启动100个协程并发写入
	for i := 0; i < concurrentNum; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			report := &model.VMReport{
				VMID:        vmID,
				ReportID:    "report-" + string(idx),
				CollectTime: time.Now(),
			}
			_, err := storage.Put(report)
			if err != nil {
				t.Errorf("并发写入失败（idx:%d）: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// 验证写入的文件数量
	files, err := os.ReadDir(testStorageDir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(files) != concurrentNum {
		t.Errorf("并发写入文件数量错误，期望: %d, 实际: %d", concurrentNum, len(files))
	}

	// 并发读取
	for i := 0; i < concurrentNum; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := storage.Get(vmID)
			if err != nil {
				t.Errorf("并发读取失败（idx:%d）: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()
}

// TestFileStorage_Save 测试兼容的Save方法
func TestFileStorage_Save(t *testing.T) {
	// 清理旧测试目录
	_ = os.RemoveAll(testStorageDir)
	defer os.RemoveAll(testStorageDir)

	// 初始化存储
	storage, err := NewFileStorage(testStorageDir, 5)
	if err != nil {
		t.Fatalf("初始化存储失败: %v", err)
	}
	defer storage.Close()

	// 测试Save方法（内部调用Put）
	testReport := &model.VMReport{
		VMID:     "vm-test-004",
		ReportID: "report-001",
	}
	reportID, err := storage.Save(testReport)
	if err != nil {
		t.Fatalf("Save方法失败: %v", err)
	}
	if reportID != testReport.ReportID {
		t.Errorf("Save返回的ReportID不匹配")
	}

	// 验证文件生成
	files, err := os.ReadDir(testStorageDir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(files) != 1 {
		t.Error("Save方法未生成文件")
	}
}
