package storage

import (
	"control-plane/etcd_client"
	"control-plane/util"
	"encoding/json"
	"fmt"
	clientv3 "go.etcd.io/etcd/client/v3"
	"log/slog"
	"time"
)

// VMWeightedAvgResult 封装加权缓存平均值计算结果（用于发送给Etcd的结构体）
//type NodeWeightedAvgResult struct {
//	WeightedAvg        float64   `json:"weighted_avg"`
//	TotalWeightedCache float64   `json:"total_weighted_cache"`
//	TotalActiveConns   float64   `json:"total_active_conns"`
//	VMCount            int       `json:"vm_count"`
//	CalculateTime      time.Time `json:"calculate_time"`
//
//	// 节点信息
//	PublicIP  string `json:"public_ip"`
//	Provider  string `json:"provider"`
//	Continent string `json:"continent"`
//}

// 节点拥塞指标
type NodeCongestionInfo struct {
	AvgWeightedCache   float64   `json:"avg_weighted_cache"`   // 节点加权平均拥塞指标
	TotalWeightedCache float64   `json:"total_weighted_cache"` // 节点总加权缓存
	TotalActiveConn    float64   `json:"total_active_conn"`    // 节点总活跃连接数
	VMCount            int       `json:"vm_count"`             // 节点 VM 数量
	CalculateTime      time.Time `json:"calculate_time"`       // 计算时间
}

//type ProbeTask struct {
//	TargetType string // "node" | "cloud_storage"
//	Provider   string // node 可为空，cloud storage 用 google/aws/azure
//	IP         string
//	Port       int
//	Region     string // cloud storage 用，node 可为空
//	City       string // cloud storage 用，node 可为空
//}

// 链路拥塞信息
type LinkCongestionInfo struct {
	TargetIP       string         `json:"target_ip"` // 目标节点 IP
	Target         util.ProbeTask `json:"target"`
	PacketLoss     float64        `json:"packet_loss"`     // 丢包率，百分比
	WeightedCache  float64        `json:"weighted_cache"`  // 链路缓存情况（可选）
	AverageLatency float64        `json:"average_latency"` // 平均延迟（毫秒）
	BandwidthUsage float64        `json:"bandwidth_usage"` // 带宽利用率（可选百分比）
}

// 节点遥测数据
type NetworkTelemetry struct {
	NodeCongestion  NodeCongestionInfo            `json:"node_congestion"`  // 节点拥塞指标
	PublicIP        string                        `json:"public_ip"`        // 节点公网 IP
	Provider        string                        `json:"provider"`         // 云厂商
	Continent       string                        `json:"continent"`        // 所属大洲
	LinksCongestion map[string]LinkCongestionInfo `json:"links_congestion"` // 节点到其他节点的链路拥塞信息
}

//1、定时器 读storage文件 汇聚group信息 到etcd 并且 加入一个全局的 queue供 elastic scaling使用

func CalcClusterWeightedAvg(fs *FileStorage, interval time.Duration,
	etcdClient *clientv3.Client, queue *util.FixedQueue, logger *slog.Logger) {
	// 1. 内嵌定时器，直接创建
	ticker := time.NewTicker(interval)
	defer ticker.Stop() // 程序退出时回收定时器资源

	// 2. 日志输出启动信息
	logger.Info("定时加权计算启动成功", slog.Duration("间隔", interval), slog.String("存储目录", fs.storageDir))

	// 3. 无限循环，定时触发核心逻辑（复用GetAll()）
	for {
		// 监听定时器信号，到达间隔执行计算
		<-ticker.C

		// 4. 复用GetAll()获取所有VMReport数据
		allReports, err := fs.GetAll()
		if err != nil {
			logger.Warn("调用GetAll()失败，跳过本次计算", slog.Any("错误", err))
			continue
		}

		// 5. 初始化统计变量，执行核心计算
		var (
			totalWeightedCache float64 // 总加权缓存：Σ(ActiveConnections*AvgCachePerConn)
			totalActiveConn    float64 // 总活跃连接数：Σ(ActiveConnections)
			totalLinksCong     map[string][]float64
			totalLinksCong_    map[string]util.ProbeTask
		)
		totalLinksCong = make(map[string][]float64)
		totalLinksCong_ = make(map[string]util.ProbeTask)

		// 6. 遍历GetAll()结果，累加统计值
		for _, report := range allReports {
			activeConn := float64(report.Congestion.ActiveConnections)
			avgCache := report.Congestion.AvgCachePerConn
			totalWeightedCache += activeConn * avgCache
			totalActiveConn += activeConn

			//探测任务copy
			for _, v := range report.LinksCongestion {
				totalLinksCong_[v.TargetIP] = v.Target
				break
			}

			//处理链路
			for _, v := range report.LinksCongestion {
				totalLinksCong[v.TargetIP] = append(totalLinksCong[v.TargetIP], v.PacketLoss)
			}
		}

		// 7. 避免除以0，输出计算结果
		var avgWeightedCache float64 = 0
		if totalActiveConn <= 0 {
			logger.Info("本次计算：总活跃连接数为0，无需计算平均值")
			totalWeightedCache = 0
		} else {
			avgWeightedCache = totalWeightedCache / totalActiveConn
		}

		//简单求均值
		linkMap := make(map[string]LinkCongestionInfo)
		for k, vs := range totalLinksCong {
			var avg float64 = 0
			for _, v := range vs {
				avg += v
			}
			if avg != 0 && len(vs) > 0 {
				avg = avg / float64(len(vs))
			}
			linkMap[k] = LinkCongestionInfo{TargetIP: k, PacketLoss: avg}
		}

		// 填充结果结构体
		result := NetworkTelemetry{
			NodeCongestion: NodeCongestionInfo{
				AvgWeightedCache:   avgWeightedCache,
				TotalWeightedCache: totalWeightedCache,
				TotalActiveConn:    totalActiveConn,
				VMCount:            len(allReports),
				CalculateTime:      time.Now(),
			},
			LinksCongestion: linkMap,
			PublicIP:        util.Config_.Node.IP.Public,
			Provider:        util.Config_.Node.Provider,
			Continent:       util.Config_.Node.Continent,
		}

		// 4. 结构体序列化为JSON（Etcd存储二进制数据，JSON格式易解析）
		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			logger.Warn("结构体JSON序列化失败，跳过本次发送", slog.Any("错误", err))
			continue
		}

		//// 5. 发送（写入）数据到Etcd（*clientv3.Client核心操作）
		ip, _ := util.GetPublicIP()
		key := fmt.Sprintf("/routing/%s", ip)
		etcd_client.PutKey(etcdClient, key, string(jsonData), logger)
		_ = etcd_client.PutKeyWithLease(etcdClient, key, string(jsonData), int64(60*expireTime), logger)

		//放入queue 为自动化扩缩容做准备
		queue.Push(result)

		logger.Info("定时计算完成", string(jsonData))
	}
}
