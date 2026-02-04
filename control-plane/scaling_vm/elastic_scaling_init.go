package scaling_vm

import (
	"control-plane/util"
	"encoding/json"
	"fmt"
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
	VolatilityWeight    float64 // w^V_i 默认为 1
	QueueWeight         float64 // w^S_i 默认为 1
	DecayFactor         float64 // γ_i
	VolatilityThreshold float64 // C^V，扰动阈值，小波动会被忽略

	// DPP 控制参数
	CostWeight float64 // V_i^S，控制成本敏感度，默认可设为 1

	// 成本相关
	ScalingCostFixed    float64 // C_fc
	ScalingCostVariable float64 // β_i
	ScalingRatio        float64 // α 设定为 0.1 大多数情况下都是扩容 1台机器

	// 保留机制
	BaseRetentionTime  time.Duration // T0
	RetentionAmplifier float64       // λ
	RetentionDecay     time.Duration // τ
	PermanentThreshold time.Duration // T_threshold
	PermanentDuration  time.Duration // T_permanent

	// 定时任务
	TickerInterval time.Duration // 定时检查间隔
}

// NodeState 表示每个节点的弹性伸缩状态
type NodeState struct {
	ID string

	// 波动队列（累积短期扰动）
	VolatilityQueue *util.FixedQueue // 可用 Snapshot() 获取历史扰动

	Z float64 // 当前 Volatility Queue 值，即 \widetilde Z_i(t)

	// 节点资源状态（UNSCALED / SCALED + 子状态）
	State NodeStatus // e.g., "Inactive", "Dormant", "Triggered", "Permanent"

	// 新增：最近扩容事件记录
	ScaleHistory []ScaleEvent

	ScaledVMs []VM // 扩容触发时间，用于保留机制计算

	// 最近触发时间，用于保留机制计算
	RetainTime time.Time //类似于 i 时间

	// 其他统计/控制量，可选
	P float64 // 当前扰动 \widetilde P_i(t)
}

type ScaleEvent struct {
	Time     time.Time // 扩容触发时间
	Amount   int       // 扩容数量
	ScaledVM VM        // 扩容的 VM 信息
}

type VM struct {
	PublicIP  string // VM 的 IP 地址
	VMName    string
	StartTime time.Time // VM 启动时间
	Status    NodeStatus
}

// Scaler 弹性伸缩控制器
type Scaler struct {
	config *ScaleConfig

	// 单节点状态
	node *NodeState

	// 定时任务停止通道
	stopChan chan struct{}

	logger *slog.Logger

	// 读写锁，保护 node 状态并发访问
	mu sync.Mutex
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
func NewScaler(nodeID string, config *ScaleConfig, queue *util.FixedQueue, pre string, logger *slog.Logger) *Scaler {

	configJSON, _ := json.Marshal(config)
	queueJSON, _ := json.Marshal(queue)
	logger.Info("NewScaler", slog.String("pre", pre),
		"nodeID", nodeID, "config", configJSON, "queue", queueJSON)

	if config == nil {
		config = NewDefaultScaleConfig()
	}

	return &Scaler{
		config:   config,
		node:     NewNodeState(nodeID, queue),
		stopChan: make(chan struct{}),
		logger:   logger,
	}
}

func (n *NodeState) LogStateSlog(pre string, logger *slog.Logger) {
	history := make([]string, len(n.ScaleHistory))
	for i, evt := range n.ScaleHistory {
		history[i] = fmt.Sprintf("{Time: %s, Amount: %d}", evt.Time.Format(time.RFC3339), evt.Amount)
	}

	logger.Info("NodeState",
		slog.String("pre", pre),
		"ID", n.ID,
		"State", n.State,
		"Z", n.Z,
		"P", n.P,
		"RetainTime", n.RetainTime.Format(time.RFC3339),
		"ScaleHistory", history,
		"VolatilityQueue", n.VolatilityQueue.SnapshotLatestFirst(),
	)
}
