package scaling_vm

import (
	"context"
	"control-plane/util"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

//维护vm信息;扩容&缩容&安装环境以及二进制;开启&关闭健康检查; 这些都是elastic scaling的approach

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
	Z               float64          // 当前 Volatility Queue 值，即 \widetilde Z_i(t)

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
}

// Scaler 弹性伸缩控制器
type Scaler struct {
	config *ScaleConfig

	// 单节点状态
	node *NodeState

	// 定时任务停止通道
	stopChan chan struct{}

	logger *slog.Logger

	// 锁，保护 node 状态并发访问
	mu sync.Mutex
}

func (n *NodeState) LogStateSlog(logger *slog.Logger) {
	history := make([]string, len(n.ScaleHistory))
	for i, evt := range n.ScaleHistory {
		history[i] = fmt.Sprintf("{Time: %s, Amount: %d}", evt.Time.Format(time.RFC3339), evt.Amount)
	}

	logger.Info("NodeState",
		"ID", n.ID,
		"State", n.State,
		"Z", n.Z,
		"P", n.P,
		"RetainTime", n.RetainTime.Format(time.RFC3339),
		"ScaleHistory", history,
		"VolatilityQueue", n.VolatilityQueue.SnapshotLatestFirst(),
	)
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
		ScaleHistory:    []ScaleEvent{},
		ScaledVMs:       []VM{},
		RetainTime:      time.Time{}, // 保持时间
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
	queue = s.node.VolatilityQueue.SnapshotLatestFirst()

	//还没有足够数据
	if len(queue) <= 0 {
		s.logger.Warn("queue is empty")
		return 0
	}

	//如果最新的波动小于阈值 ，则直接返回 0
	if queue[0].(float64) <= s.config.VolatilityThreshold {
		s.logger.Info("latest volatility is too small", "volatility", queue[0].(float64))
		return 0
	}

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

func (s *Scaler) calculateDelta(node *NodeState) float64 {
	// 1️⃣ 当前扰动量
	P := node.P
	Z := node.Z

	// 2️⃣ 成本，根据节点当前状态
	cost := s.calculateCost(node)

	// 3️⃣ 公式
	delta := -s.config.DecayFactor*s.config.VolatilityWeight*s.config.QueueWeight*Z*P +
		s.config.CostWeight*cost

	return delta
}

// calculateCost 按论文公式计算成本
func (s *Scaler) calculateCost(node *NodeState) float64 {
	switch node.State {
	case Inactive:
		return s.config.ScalingCostFixed + s.config.ScalingCostVariable*node.P
	case Dormant:
		return s.config.ScalingCostVariable * node.P
	default:
		return 0
	}
}

// evaluateScaling 核心扩容判断逻辑
// evaluateScaling 核心扩容判断与状态管理逻辑
func (s *Scaler) evaluateScaling() {

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	node := s.node
	node.LogStateSlog(s.logger) //打印 node

	s.logger.Info("evaluating scaling", "node", node.ID, "state", node.State)

	switch node.State {
	case ScalingUp:
		s.logger.Info("node is scaling up")
		return
	case Releasing:
		s.logger.Info("node is releasing")
		return
	case Triggered:
		if now.Sub(s.node.RetainTime) < 0 {
			s.logger.Info("node is triggered, but retention time not reached")
			return
		}
		//往下走就是已经超时
	case Dormant, Permanent:
		if now.Sub(s.node.RetainTime) < 0 {
			s.logger.Info("node is dormant or permanent, but retention time not reached")
			// 后面检验一下是不是需要扩容 如果扩容这个状态就会被change
		} else {
			s.logger.Info("node is dormant or permanent, and retention time reached")
			//如果后面不触发 Triggered 走到最后就会被删除
			node.State = Releasing
		}
	}

	// 1️⃣ 计算当前扰动量 P 和波动值 Z
	node.P = s.calculateP()
	node.Z = s.calculateDelta(s.node)

	// 2️⃣ 判断是否需要触发扩容
	if node.Z < 0 {
		switch node.State {
		case Inactive:
			node.State = ScalingUp
			if ok, vm := s.triggerScaling1(1, s.logger); ok {
				node.State = Triggered
				node.ScaleHistory = append(node.ScaleHistory, ScaleEvent{Time: time.Now(),
					Amount: 1, ScaledVM: vm})
				node.ScaledVMs = append(node.ScaledVMs, vm)
				retain, state := s.calculateRetention()
				node.RetainTime = retain
				if state == Permanent {
					node.State = Permanent
				}
			} else {
				s.logger.Error("triggerScaling 1 failed")
			}
		case Dormant:
			node.State = Triggered
			if s.triggerScaling2() {
				node.State = Triggered
				node.ScaleHistory = append(node.ScaleHistory, ScaleEvent{Time: now, Amount: 1})
				retain, state := s.calculateRetention()
				node.RetainTime = retain
				if state == Permanent {
					node.State = Permanent
				}
			} else {
				s.logger.Error("triggerScaling 2 failed")
			}
		}
	}
	node.LogStateSlog(s.logger) //打印 node
	if node.State == Triggered || node.State == Permanent {
		return
	}

	// 3️⃣ 如果没有触发扩容，根据当前状态处理
	switch node.State {
	case Dormant, Permanent:
		s.logger.Info("node is dormant or permanent, and retention time reached")
		node.State = Releasing
		s.triggerRelease()
		node.State = Inactive
	case ScalingUp:
		retain, _ := s.calculateRetention()
		node.RetainTime = retain
		node.State = Dormant
		s.logger.Info("the state of node is chagned to dormant from scalingup")
	}
	node.LogStateSlog(s.logger) //打印 node
	return
}

// triggerScaling 模拟扩容动作
func (s *Scaler) triggerScaling1(n int, logger *slog.Logger) (bool, VM) {

	logger.Info("triggerScaling1", "n", n)

	//获取本节点配置信息
	//扩容
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel() // 确保上下文最终被释放

	gcp := util.Config_.GCP
	vmName := gcp.VMPrefix + util.GenerateRandomLetters(4)
	err := CreateVM(ctx, logger, gcp.ProjectID, gcp.Zone, vmName, gcp.CredFile)

	if err != nil {
		logger.Error("创建 VM 失败", "error", err)
		return false, VM{}
	}

	// 在创建虚拟机后等待一定时间，确保 VM 启动完成
	logger.Info("Waiting for VM to start...", "vmName", vmName)
	time.Sleep(10 * time.Minute) // 等待 10 分钟

	//获取ip等信息用于管理
	ip, err := GetVMExternalIP(ctx, logger, gcp.ProjectID, gcp.Zone, vmName, gcp.CredFile)
	if err != nil {
		logger.Error("获取 VM 外部 IP 失败", "error", err)
		return false, VM{}
	}

	logger.Info("Scaling node", gcp.Zone, vmName, ip)
	return true, VM{ip, vmName, time.Now()}
}

func (s *Scaler) triggerScaling2() bool {
	// TODO: 调用实际扩容 API 或更新内部状态
	// 这里打印日志模拟
	println("Scaling node", s.node.ID, "by", "VM(s)")
	return true
}

// triggerRelease 模拟释放动作
func (s *Scaler) triggerRelease() {
	// TODO: 调用实际释放 API
	println("Releasing node", s.node.ID)
}

// calculateRetention 计算节点的 Retain Time，返回绝对时间点
func (s *Scaler) calculateRetention() (time.Time, NodeStatus) {
	now := time.Now()
	var activationPotential float64

	for _, evt := range s.node.ScaleHistory {
		// 只考虑 tau 内的触发事件
		delta := now.Sub(evt.Time)
		if delta > s.config.RetentionDecay {
			continue
		}
		activationPotential += float64(evt.Amount) * expDecay(delta, s.config.RetentionDecay)
	}

	// 计算 Retention 时间长度
	retentionDuration := s.config.BaseRetentionTime + time.Duration(s.config.RetentionAmplifier*activationPotential)

	// 如果超过永久阈值，直接返回永久时间
	if retentionDuration >= s.config.PermanentThreshold {
		return now.Add(s.config.PermanentDuration), Permanent
	}

	// 返回节点保持活跃的绝对时间点
	return now.Add(retentionDuration), End
}

// 指数衰减函数
func expDecay(delta time.Duration, tau time.Duration) float64 {
	return math.Exp(-float64(delta) / float64(tau))
}
