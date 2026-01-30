package scaling_vm

import (
	"bytes"
	"context"
	"control-plane/pkg/envoy_manager"
	"control-plane/storage"
	"control-plane/util"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"
)

//维护vm信息;扩容&缩容&安装环境以及二进制;开启&关闭健康检查; 这些都是elastic scaling的approach

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
func (s *Scaler) calculatePerturbation() float64 {

	var queue []interface{}
	queue = s.node.VolatilityQueue.SnapshotLatestFirst()

	//还没有足够数据
	if len(queue) <= 1 {
		s.logger.Info("the data of volatility queue is spare")
		return 0
	}

	//如果最新的波动小于阈值 ，则直接返回 0
	avgCache := queue[0].(storage.NetworkTelemetry).NodeCongestion.AvgWeightedCache
	avgCache_ := queue[1].(storage.NetworkTelemetry).NodeCongestion.AvgWeightedCache
	if (avgCache <= s.config.VolatilityThreshold && avgCache_ <= s.config.VolatilityThreshold) ||
		(avgCache >= s.config.VolatilityThreshold && avgCache_ >= s.config.VolatilityThreshold) {
		s.logger.Info("latest volatility is too small", "volatility", queue[0].(float64))
		return 0
	}

	p := math.Abs(avgCache - avgCache_)
	return p

}

func (s *Scaler) calculateVolatilityAccumulation() float64 {
	z := s.node.P*s.config.VolatilityWeight + s.node.Z*s.config.DecayFactor
	if z < 0 {
		return 0
	}
	return z
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

// evaluateScaling 核心扩容判断逻辑
// evaluateScaling 核心扩容判断与状态管理逻辑
func (s *Scaler) evaluateScaling() {

	// 尝试获取锁，若获取不到则直接返回
	if !s.tryLock(1 * time.Second) {
		fmt.Println("无法获取到锁，定时任务取消")
		return
	}
	defer s.mu.Unlock()

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
		if time.Now().Sub(s.node.RetainTime) < 0 {
			s.logger.Info("node is triggered, but retention time not reached")
			return
		}
		//往下走就是已经超时
	case Dormant, Permanent:
		if time.Now().Sub(s.node.RetainTime) < 0 {
			s.logger.Info("node is dormant or permanent, but retention time not reached")
			// 后面检验一下是不是需要扩容 如果扩容这个状态就会被change
		} else {
			s.logger.Info("node is dormant or permanent, and retention time reached")
			//如果后面不触发 Triggered 走到最后就会被删除
			node.State = Releasing
		}
	default:
		panic("unhandled default case")
	}

	// 1️⃣ 计算当前扰动量 P 和波动值 Z
	node.P = s.calculatePerturbation()
	node.Z = s.calculateVolatilityAccumulation()
	delta := s.calculateDelta(s.node)
	s.logger.Info("calculate delta", node.P, node.Z, delta)

	// 2️⃣ 判断是否需要触发扩容
	if delta < 0 {
		switch node.State {
		case Inactive:
			node.State = ScalingUp
			if ok, vm := s.triggerScaling1(1, s.logger); ok {
				node.State = Triggered
				node.ScaleHistory = append(node.ScaleHistory, ScaleEvent{Time: time.Now(), Amount: 1, ScaledVM: vm})
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
				node.ScaleHistory = append(node.ScaleHistory, ScaleEvent{Time: time.Now(), Amount: 1})
				retain, state := s.calculateRetention()
				node.RetainTime = retain
				if state == Permanent {
					node.State = Permanent
				}
			} else {
				s.logger.Error("triggerScaling 2 failed")
			}
		default:
			panic("unhandled default case")
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
		s.triggerDormant()
		s.logger.Info("the state of node is chagned to dormant from scalingup")
	default:
		panic("unhandled default case")
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

	//安装环境 启动 触发envoy
	err = deployBinaryToServer(username, ip, "22", localPathProxy, remotePathProxy, binaryProxy, logger)
	if err != nil {
		logger.Error("部署二进制文件失败", remotePathProxy, "error", err)
		return false, VM{}
	}
	err = deployBinaryToServer(username, ip, "22", localPathPlane, remotePathPlane, binaryPlane, logger)
	if err != nil {
		logger.Error("部署二进制文件失败", remotePathPlane, "error", err)
		return false, VM{}
	}

	//关联到envoy
	_, err = sendAddTargetIpsRequest([]envoy_manager.EnvoyTargetAddr{envoy_manager.EnvoyTargetAddr{ip, 8095}})
	if err != nil {
		logger.Error("关联到envoy失败", "error", err)
		return false, VM{}
	}

	return true, VM{ip, vmName, time.Now(), Triggered}
}

func (s *Scaler) triggerScaling2() bool {

	var ip, setState string
	//找到睡眠的vm获取 ip
	if len(s.node.ScaledVMs) <= 0 {
		s.logger.Error("No scaled VMs found")
		return false
	}
	vm := s.node.ScaledVMs[0]
	ip = vm.PublicIP
	setState = "on"

	if b := setHealthState(ip, setState, s.logger); b == false {
		return false
	}
	return true
}

func (s *Scaler) triggerDormant() bool {

	var ip, setState string
	//找到睡眠的vm获取 ip
	if len(s.node.ScaledVMs) <= 0 {
		s.logger.Error("No scaled VMs found")
		return false
	}
	vm := s.node.ScaledVMs[0]
	ip = vm.PublicIP
	setState = "off"

	if b := setHealthState(ip, setState, s.logger); b == false {
		return false
	}
	return true
}

// triggerRelease 模拟释放动作
func (s *Scaler) triggerRelease() bool {

	s.logger.Info("triggerRelease")

	//找到睡眠的vm获取 ip
	if len(s.node.ScaledVMs) <= 0 {
		s.logger.Error("No scaled VMs found")
		return false
	}
	vm := s.node.ScaledVMs[0]

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel() // 确保上下文最终被释放

	gcp := util.Config_.GCP
	err := DeleteVM(ctx, logger, gcp.ProjectID, gcp.Zone, vm.VMName, gcp.CredFile)
	if err != nil {
		s.logger.Error("删除 VM 失败", "error", err)
	}

	s.logger.Info("Releasing node", vm.VMName)
	return true
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

// setHealthState 用于向 API 发送请求，设置健康状态
// 参数 apiHost 是主机地址，setState 是健康状态（可以是 "on" 或 "off"）
func setHealthState(apiHost, setState string, logger *slog.Logger) bool {
	// 创建 URL 和查询参数
	apiURL := fmt.Sprintf("http://%s:8095/healthStateChange", apiHost) // 使用传入的 apiHost
	params := url.Values{}
	params.Add("set", setState)

	// 构建完整的请求 URL
	reqURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())

	// 调用 API 并设置健康状态
	resp, err := http.Get(reqURL)
	if err != nil {
		logger.Error("请求失败: %v", err)
		return false
	}
	defer resp.Body.Close()

	// 输出响应状态
	logger.Info("响应状态码: %d\n", resp.StatusCode)

	// 根据响应状态码处理结果
	if resp.StatusCode == http.StatusOK {
		logger.Info("健康状态已成功设置为: %s\n", setState)
	} else {
		logger.Error("健康状态设置失败，状态码: %d", resp.StatusCode)
		return false
	}
	return true
}

// sendRequest 向指定的 API 路由发送请求
func sendAddTargetIpsRequest(targetIps []envoy_manager.EnvoyTargetAddr) (*envoy_manager.APICommonResp, error) {
	// 构建请求体
	url := "http://127.0.0.1:8081/setTargetIps" // API URL
	body, err := json.Marshal(targetIps)        // 将目标地址数据编码为 JSON
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	// 发送 POST 请求
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	// 解析响应体
	var response envoy_manager.APICommonResp
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	// 返回响应
	return &response, nil
}
