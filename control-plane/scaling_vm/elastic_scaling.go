package scaling_vm

import "time"

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

	// 扩容冷却期
	ScaleCooldown time.Duration // 节点扩容后必须等待的最短时间，避免频繁扩容

	// 定时任务
	TickerInterval time.Duration // 定时检查间隔
}

// NodeState 表示每个节点的弹性伸缩状态
type NodeState struct {
	ID string

	// 波动队列（累积短期扰动）
	VolatilityQueue []interface{} // 可用 Snapshot() 获取历史扰动
	Z               float64       // 当前 Volatility Queue 值，即 \widetilde Z_i(t)

	// 节点资源状态（UNSCALED / SCALED + 子状态）
	State string // e.g., "Inactive", "Dormant", "Triggered", "Permanent"

	// 最近触发时间，用于保留机制计算
	LastTrigger time.Time //类似于 i 时间

	// 其他统计/控制量，可选
	P float64 // 当前扰动 \widetilde P_i(t)
}

// Scaler 弹性伸缩控制器
type Scaler struct {
	config *ScaleConfig

	// 节点集合
	nodes map[string]*NodeState

	// 定时任务停止通道
	stopChan chan struct{}
}
