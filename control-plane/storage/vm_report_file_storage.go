package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	model "control-plane/pkg/local_info_manager"
)

// -------------------------- 存储抽象接口 --------------------------
// Storage 存储核心接口（定义Put/Get/Save方法）
type Storage interface {
	// Save 保存VM上报数据（兼容API层命名）
	Save(report *model.VMReport) (string, error)
	// Put 存储/更新VM上报数据（按VMID分文件存储）
	Put(report *model.VMReport) (string, error)
	// Get 根据VMID读取上报数据
	Get(vmID string) (*model.VMReport, error)
}

// -------------------------- 文件存储实现 --------------------------
// FileStorage 基于文件的存储实现（按VM ID分文件）
type FileStorage struct {
	storageDir string       // 存储根目录（如 "./storage/vm_data"）
	mu         sync.RWMutex // 读写锁，保证并发安全
}

// NewFileStorage 初始化文件存储实例
// 参数：storageDir 存储根目录路径
// 返回：FileStorage实例 / 初始化错误
func NewFileStorage(storageDir string) (*FileStorage, error) {
	// 创建存储目录（不存在则创建，权限0755）
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, fmt.Errorf("创建存储目录失败：%w", err)
	}

	return &FileStorage{
		storageDir: storageDir,
	}, nil
}

// Put 存储/更新VM上报数据（核心写方法）
// 规则：按VMID生成json文件，覆盖式写入（保留最新数据）
func (fs *FileStorage) Put(report *model.VMReport) (string, error) {
	// 入参校验
	if report == nil || report.VMID == "" {
		return "", errors.New("VMReport不能为空且VMID必须非空")
	}

	// 加写锁（排他锁，防止并发写冲突）
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// 拼接文件路径：存储目录/VMID.json
	filePath := filepath.Join(fs.storageDir, fmt.Sprintf("%s.json", report.VMID))
	// 临时文件路径（避免写入失败损坏原文件）
	tmpFilePath := fmt.Sprintf("%s.tmp_%d", filePath, time.Now().UnixNano())

	// 结构体序列化为格式化JSON（易读）
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("JSON序列化失败：%w", err)
	}

	// 写入临时文件（权限0644：当前用户可读写，其他用户只读）
	if err := os.WriteFile(tmpFilePath, data, 0644); err != nil {
		return "", fmt.Errorf("写入临时文件失败：%w", err)
	}

	// 原子重命名临时文件为正式文件（保证数据完整性）
	if err := os.Rename(tmpFilePath, filePath); err != nil {
		_ = os.Remove(tmpFilePath) // 清理临时文件
		return "", fmt.Errorf("重命名文件失败：%w", err)
	}

	return report.ReportID, nil
}

// Get 根据VMID读取上报数据（核心读方法）
func (fs *FileStorage) Get(vmID string) (*model.VMReport, error) {
	// 入参校验
	if vmID == "" {
		return nil, errors.New("VMID不能为空")
	}

	// 加读锁（共享锁，支持多协程并发读）
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	// 拼接文件路径
	filePath := filepath.Join(fs.storageDir, fmt.Sprintf("%s.json", vmID))

	// 检查文件是否存在
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("VM[%s]的上报文件不存在：%w", vmID, err)
	}

	// 读取文件内容
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败：%w", err)
	}

	// JSON反序列化为VMReport结构体
	var report model.VMReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("JSON反序列化失败：%w", err)
	}

	return &report, nil
}

// Save 兼容API层的Save方法（内部调用Put）
func (fs *FileStorage) Save(report *model.VMReport) (string, error) {
	return fs.Put(report)
}
