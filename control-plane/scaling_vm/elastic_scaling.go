package scaling_vm

import (
	"control-plane/util"
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

	// 扩容冷却期
	ScaleCooldown time.Duration // 节点扩容后必须等待的最短时间，避免频繁扩容

	// 定时任务
	TickerInterval time.Duration // 定时检查间隔
}

// NodeState 表示每个节点的弹性伸缩状态
type NodeState struct {
	ID string

	// 波动队列（累积短期扰动）
	VolatilityQueue *util.FixedQueue // 可用 Snapshot() 获取历史扰动
	Z               float64          // 当前 Volatility Queue 值，即 \widetilde Z_i(t)

	// 节点资源状态（UNSCALED / SCALED + 子状态）
	State NodeStatus // e.g., "Inactive", "Dormant", "Triggered", "Permanent"

	// 最近触发时间，用于保留机制计算
	LastTrigger time.Time //类似于 i 时间

	// 其他统计/控制量，可选
	P float64 // 当前扰动 \widetilde P_i(t)
}

// Scaler 弹性伸缩控制器
type Scaler struct {
	config *ScaleConfig

	// 单节点状态
	node *NodeState

	// 定时任务停止通道
	stopChan chan struct{}

	// 锁，保护 node 状态并发访问
	mu sync.Mutex
}

// NewDefaultScaleConfig 返回带默认值的 ScaleConfig
func NewDefaultScaleConfig() *ScaleConfig {
	return &ScaleConfig{
		// 队列与波动相关
		VolatilityWeight:    1.0,
		QueueWeight:         1.0,
		DecayFactor:         0.8,
		VolatilityThreshold: 0.05, // 小波动忽略

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

		// 扩容冷却期
		ScaleCooldown: 5 * time.Minute,

		// 定时任务
		TickerInterval: 30 * time.Second,
	}
}

// NewNodeState 初始化单个节点状态
func NewNodeState(id string, queue *util.FixedQueue) *NodeState {
	return &NodeState{
		ID:              id,
		VolatilityQueue: queue,
		Z:               0,
		State:           Inactive,
		LastTrigger:     time.Time{}, // 零值表示从未触发
		P:               0,
	}
}

// NewScaler 初始化 Scaler 控制器
func NewScaler(nodeID string, config *ScaleConfig, queue *util.FixedQueue) *Scaler {
	if config == nil {
		config = NewDefaultScaleConfig()
	}

	return &Scaler{
		config:   config,
		node:     NewNodeState(nodeID, queue),
		stopChan: make(chan struct{}),
	}
}

// StartTicker 启动定时任务
func (s *Scaler) StartTicker() {
	ticker := time.NewTicker(s.config.TickerInterval)

	go func() {
		for {
			select {
			case <-ticker.C:
				s.mu.Lock()
				s.evaluateScaling()
				s.mu.Unlock()
			case <-s.stopChan:
				ticker.Stop()
				return
			}
		}
	}()
}

// StopTicker 停止定时任务
func (s *Scaler) StopTicker() {
	close(s.stopChan)
}

// 计算当前扰动量 \widetilde P_i(t)
func (s *Scaler) calculateP() float64 {
	var queue []interface{}
	var sum float64
	for _, v := range queue {
		if f, ok := v.(float64); ok {
			sum += f
		}
	}
	if len(queue) == 0 {
		return 0
	}
	return sum / float64(len(queue)) // 简单取平均，可按实际逻辑加权
}

// evaluateScaling 核心扩容判断逻辑
// evaluateScaling 核心扩容判断与状态管理逻辑
func (s *Scaler) evaluateScaling() {
	now := time.Now()
	node := s.node

	// 1️⃣ 计算当前扰动量 P 和波动值 Z
	node.P = s.calculateP()
	node.Z = s.config.DecayFactor*node.Z + s.config.VolatilityWeight*node.P

	// 2️⃣ 判断是否需要触发扩容
	if node.Z >= s.config.VolatilityThreshold {
		switch node.State {
		case Inactive:
			node.State = ScalingUp
			s.triggerScaling(1) // 默认扩容 1 台
			node.LastTrigger = now
		case Dormant:
			node.State = Triggered
			s.triggerScaling(1)
			node.LastTrigger = now
		case Triggered, ScalingUp, Permanent:
			// 已经活跃，无需重复触发
		case Releasing:
			// 取消释放，直接扩容
			node.State = ScalingUp
			s.triggerScaling(1)
			node.LastTrigger = now
		}
		return
	}

	// 3️⃣ 如果没有触发扩容，根据当前状态处理
	switch node.State {
	case ScalingUp:
		// 扩容完成后进入 Dormant，并开始冷却期
		if now.Sub(node.LastTrigger) >= s.config.ScaleCooldown {
			node.State = Dormant
			node.LastTrigger = now
		}
	case Triggered:
		// 冷却期结束后恢复 Dormant
		if now.Sub(node.LastTrigger) >= s.config.ScaleCooldown {
			node.State = Dormant
		}
	case Dormant:
		// 计算动态保留时间
		retention := s.calculateRetention(node)
		if now.Sub(node.LastTrigger) >= retention {
			if now.Sub(node.LastTrigger) >= s.config.PermanentThreshold {
				node.State = Permanent
			} else {
				node.State = Releasing
				s.triggerRelease()
			}
		}
	case Permanent:
		// 永久保留状态，不释放
	case Releasing:
		// 已经在释放，持续监控释放动作
	}
}

// 计算当前节点保留时间
func (s *Scaler) calculateRetention(node *NodeState) time.Duration {
	base := s.config.BaseRetentionTime
	activityScore := node.P // 简化，活动评分可以更复杂
	amplified := time.Duration(float64(base) + float64(base)*s.config.RetentionAmplifier*activityScore)
	return amplified
}

// triggerScaling 模拟扩容动作
func (s *Scaler) triggerScaling(n int) {
	// TODO: 调用实际扩容 API 或更新内部状态
	// 这里打印日志模拟
	println("Scaling node", s.node.ID, "by", n, "VM(s)")
}

// triggerRelease 模拟释放动作
func (s *Scaler) triggerRelease() {
	// TODO: 调用实际释放 API
	println("Releasing node", s.node.ID)
}
