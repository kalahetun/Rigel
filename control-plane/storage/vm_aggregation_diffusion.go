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
type VMWeightedAvgResult struct {
	WeightedAvg        float64   `json:"weighted_avg"`         // 最终加权平均值
	TotalWeightedCache float64   `json:"total_weighted_cache"` // 总加权缓存 Σ(ActiveConnections*AvgCachePerConn)
	TotalActiveConns   float64   `json:"total_active_conns"`   // 总活跃连接数 Σ(ActiveConnections)
	VMCount            int       `json:"vm_count"`             // 参与计算的VM数量
	CalculateTime      time.Time `json:"calculate_time"`       // 本次计算时间（便于追踪）
	//StorageDir         string    `json:"storage_dir"`          // 存储目录（便于溯源）
}

//1、定时器 读storage文件 汇聚group信息 到etcd 并且 加入一个全局的 queue供 elastic scaling使用

func CalcWeightedAvgWithTimer(fs *FileStorage, interval time.Duration,
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
			totalActiveConns   float64 // 总活跃连接数：Σ(ActiveConnections)
		)

		// 6. 遍历GetAll()结果，累加统计值
		for _, report := range allReports {
			activeConns := float64(report.Congestion.ActiveConnections)
			avgCache := report.Congestion.AvgCachePerConn
			totalWeightedCache += activeConns * avgCache
			totalActiveConns += activeConns
		}

		// 7. 避免除以0，输出计算结果
		if totalActiveConns <= 0 {
			logger.Info("本次计算：总活跃连接数为0，无需计算平均值")
			continue
		}

		// 填充结果结构体
		result := VMWeightedAvgResult{
			WeightedAvg:        totalWeightedCache / totalActiveConns,
			TotalWeightedCache: totalWeightedCache,
			TotalActiveConns:   totalActiveConns,
			VMCount:            len(allReports),
			CalculateTime:      time.Now(),
			//StorageDir:         fs.storageDir,
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
		//放入queue 为自动化扩缩容做准备
		queue.Push(result)

		weightedAvg := totalWeightedCache / totalActiveConns
		logger.Info("定时计算完成",
			slog.Float64("加权平均值", weightedAvg),
			slog.Float64("总加权缓存", totalWeightedCache),
			slog.Float64("总活跃连接数", totalActiveConns),
			slog.Int("参与计算的VM数量", len(allReports)),
		)
	}
}
