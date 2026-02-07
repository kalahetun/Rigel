package scaling_vm

import (
	"control-plane/util"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

type NodeStatus int

const (
	Inactive NodeStatus = iota
	Dormant
	Triggered
	ScalingUp
	Releasing
	Permanent
	End
)

// ScaleConfig 定义弹性伸缩相关参数
type ScaleConfig struct {
	// 队列与波动相关
	VolatilityWeight    float64 `json:"volatility_weight"`
	QueueWeight         float64 `json:"queue_weight"`
	DecayFactor         float64 `json:"decay_factor"`
	VolatilityThreshold float64 `json:"volatility_threshold"`

	// DPP 控制参数
	CostWeight float64 `json:"cost_weight"`

	// 成本相关
	ScalingCostFixed    float64 `json:"scaling_cost_fixed"`
	ScalingCostVariable float64 `json:"scaling_cost_variable"`
	ScalingRatio        float64 `json:"scaling_ratio"`

	// 保留机制
	BaseRetentionTime  time.Duration `json:"base_retention_time"`
	RetentionAmplifier float64       `json:"retention_amplifier"`
	RetentionDecay     time.Duration `json:"retention_decay"`
	PermanentThreshold time.Duration `json:"permanent_threshold"`
	PermanentDuration  time.Duration `json:"permanent_duration"`

	// 定时任务
	TickerInterval time.Duration `json:"ticker_interval"`
}

// NodeState 表示每个节点的弹性伸缩状态
type NodeState struct {
	ID string `json:"id"`

	// 波动队列（只暴露 snapshot）
	VolatilityQueue *util.FixedQueue `json:"volatility_queue,omitempty"`

	Z float64 `json:"z"`

	State NodeStatus `json:"state"`

	ScaleHistory []ScaleEvent `json:"scale_history,omitempty"`

	ScaledVMs []VM `json:"scaled_vms,omitempty"` //目前只能单个轮次转动这个状态机

	RetainTime time.Time `json:"retain_time"`

	P float64 `json:"p"`
}

type ScaleEvent struct {
	Time     time.Time `json:"time"`
	Amount   int       `json:"amount"`
	ScaledVM VM        `json:"scaled_vm,omitempty"`
}

type VM struct {
	PublicIP  string    `json:"public_ip"`
	VMName    string    `json:"vm_name"`
	StartTime time.Time `json:"start_time"`
	//Status    NodeStatus `json:"status"`
}

// mock interface
type ScalerOverride struct {
	Now        *time.Time  `json:"now,omitempty"`
	Delta      *float64    `json:"delta,omitempty"`
	State      *NodeStatus `json:"state,omitempty"`
	RetainTime *time.Time  `json:"retain_time,omitempty"`
}

// Scaler 弹性伸缩控制器
type Scaler struct {

	// 配置
	Config *ScaleConfig `json:"config"`

	// 单节点状态
	Node *NodeState `json:"node"`

	// 定时任务停止通道
	stopChan chan struct{}

	//日志
	logger *slog.Logger

	// 读写锁，保护 node 状态并发访问
	mu sync.Mutex

	// ====== 测试 / 模拟用 ======
	Override *ScalerOverride `json:"override,omitempty"`

	ManualAction string
}

// NewDefaultScaleConfig 返回带默认值的 ScaleConfig
func NewDefaultScaleConfig() *ScaleConfig {
	return &ScaleConfig{
		// 队列与波动相关
		VolatilityWeight:    1.0,
		QueueWeight:         1.0,
		DecayFactor:         0.8,
		VolatilityThreshold: 0.3, // 小波动忽略

		// DPP 控制参数
		CostWeight: 1.0, // 默认成本敏感度

		// 成本相关
		ScalingCostFixed:    10.0, // 举例
		ScalingCostVariable: 1.0,
		ScalingRatio:        0.1, // 默认扩容 1 台

		// 保留机制
		BaseRetentionTime:  5 * time.Minute,
		RetentionAmplifier: 1.0,
		RetentionDecay:     10 * time.Minute,
		PermanentThreshold: 1 * time.Hour,
		PermanentDuration:  1 * time.Hour,

		// 定时任务
		TickerInterval: 30 * time.Second,
	}
}

// NewNodeState 初始化单个节点状态
func NewNodeState(id string, queue *util.FixedQueue) *NodeState {
	return &NodeState{
		ID:              id,
		VolatilityQueue: queue,
		Z:               0, // volatility accumulation
		State:           Inactive,
		ScaleHistory:    []ScaleEvent{},
		ScaledVMs:       []VM{},      //目前只能单个轮次转动这个状态机
		RetainTime:      time.Time{}, // 保持时间
		P:               0,           // 当前扰动 perturbation
	}
}

// NewScaler 初始化 Scaler 控制器
func NewScaler(nodeID string, config *ScaleConfig, queue *util.FixedQueue,
	pre string, logger *slog.Logger) *Scaler {

	configJSON, _ := json.Marshal(config)
	//queueJSON, _ := json.Marshal(queue)
	logger.Info("NewScaler", slog.String("pre", pre),
		"nodeID", nodeID, "config", configJSON)

	if config == nil {
		config = NewDefaultScaleConfig()
	}

	return &Scaler{
		Config:       config,
		Node:         NewNodeState(nodeID, queue),
		stopChan:     make(chan struct{}),
		logger:       logger,
		Override:     nil,
		ManualAction: "init",
	}
}

// 尝试获取锁，如果获取不到则返回 false
func (s *Scaler) tryLock(timeout time.Duration) bool {
	// 设置一个通道，用于接收锁的获取结果
	done := make(chan bool, 1)

	// 启动一个 goroutine 来尝试获取锁
	go func() {
		s.mu.Lock()
		done <- true
	}()

	// 等待锁或超时
	select {
	case <-done: // 如果成功获取到锁
		return true
	case <-time.After(timeout): // 如果超时
		return false
	}
}

func (s *Scaler) ScalerDump(pre string, logger *slog.Logger) {

	configJSON, _ := json.Marshal(s.Config)
	nodeJSON, _ := json.Marshal(s.Node)
	overrideJSON, _ := json.Marshal(s.Override)

	s.logger.Info("scalar dump", slog.String("pre", pre),
		slog.String("config", string(configJSON)),
		slog.String("node", string(nodeJSON)),
		slog.String("override", string(overrideJSON)))
}

func (s *Scaler) now() time.Time {
	now := time.Now()
	if s.Override != nil && s.Override.Now != nil {
		now = *s.Override.Now
	}
	return now
}

func (s *Scaler) getRetainTime() time.Time {
	if s.Override != nil && s.Override.RetainTime != nil {
		return *s.Override.RetainTime
	}
	return s.Node.RetainTime
}

func (s *Scaler) getState() NodeStatus {
	if s.Override != nil && s.Override.State != nil {
		return *s.Override.State
	}
	return s.Node.State
}
