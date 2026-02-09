package probing

import (
	"context"
	"data-plane/util"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"
)

// Result 保存探测结果
type Result struct {
	Target   util.ProbeTask `json:"target"`    // 探测目标 IP/host
	Attempts int            `json:"attempts"`  // 探测次数
	Failures int            `json:"failures"`  // 失败次数
	LossRate float64        `json:"loss_rate"` // 丢包率
	AvgRTT   time.Duration  `json:"avg_rtt"`   // 成功连接平均时延
}

// Config 配置
type Config struct {
	Concurrency int           // 并发数
	Timeout     time.Duration // TCP Dial 超时
	Interval    time.Duration // 周期
	Attempts    int           // 每轮探测尝试次数
	BufferSize  int           // 可选：channel缓冲大小（现在不用）
}

// ----------------- 全局存储最新一轮结果 -----------------

var (
	mu            sync.RWMutex
	latestResults = make(map[string]Result)
)

// 更新全局最新结果
func updateLatestResults(results []Result) {
	mu.Lock()
	defer mu.Unlock()
	for _, r := range results {
		latestResults[r.Target.IP] = r
	}
}

// 外部调用：获取最新探测结果
func GetLatestResults() map[string]Result {
	mu.RLock()
	defer mu.RUnlock()

	copied := make(map[string]Result, len(latestResults))
	for k, v := range latestResults {
		copied[k] = v
	}
	return copied
}

// ----------------- 核心周期探测函数 -----------------

// StartProbePeriodically 启动无限周期探测
// ctx 由调用方传入，用于停止
// controlHost: 探测任务来源接口（返回目标节点列表）
// cfg: 配置
// logger: 日志
func StartProbePeriodically(ctx context.Context, controlHost string, cfg Config, pre string, logger *slog.Logger) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.Attempts <= 0 {
		cfg.Attempts = 5
	}

	logger.Info("StartProbePeriodically", slog.String("pre", pre))

	go func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.Info("周期探测已停止")
				return
			default:
			}

			pre_ := util.GenerateRandomLetters(5)

			// 获取探测任务
			targets, err := GetProbeTasks(pre, controlHost)
			if err != nil {
				logger.Error("获取探测任务失败", slog.Any("err", err))
				time.Sleep(time.Second) // 防止死循环快速重试
				continue
			}
			logger.Info("get probing tasks", slog.String("pre", pre_), slog.Any("targets", targets))

			// 执行一轮探测
			doProbeLossRTT(targets, cfg, pre, logger)

			// 等待下一个周期
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// ----------------- 单轮探测函数 -----------------

func doProbeLossRTT(targets []util.ProbeTask, cfg Config, pre string, logger *slog.Logger) {
	jobs := make(chan util.ProbeTask)
	var wg sync.WaitGroup
	roundResults := make([]Result, 0, len(targets))
	var roundMu sync.Mutex

	// worker
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				failures := 0
				var totalRTT time.Duration
				successes := 0

				//for a := 0; a < cfg.Attempts; a++ {
				//	start := time.Now()
				//	conn, err := net.DialTimeout("tcp", target.IP+":"+strconv.Itoa(target.Port), cfg.Timeout)
				//	rtt := time.Since(start)
				//
				//	if err != nil {
				//		failures++
				//	} else {
				//		successes++
				//		totalRTT += rtt
				//		conn.Close()
				//	}
				//}

				dialer := net.Dialer{
					Timeout: cfg.Timeout,
				}

				for a := 0; a < cfg.Attempts; a++ {
					start := time.Now()
					conn, err := dialer.Dial("tcp", target.IP+":"+strconv.Itoa(target.Port))
					rtt := time.Since(start)

					if err != nil {
						// 👇 关键：区分错误类型
						if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
							// 网络不通 / 丢包
							failures++
							continue
						}

						// 非 timeout（通常是 RST）
						// 👉 网络是通的，只是端口没服务
						successes++
						totalRTT += rtt
						continue
					}

					// 正常连上
					successes++
					totalRTT += rtt
					conn.Close()
				}

				avgRTT := time.Duration(0)
				if successes > 0 {
					avgRTT = totalRTT / time.Duration(successes)
				}

				result := Result{
					Target:   target,
					Attempts: cfg.Attempts,
					Failures: failures,
					LossRate: float64(failures) / float64(cfg.Attempts),
					AvgRTT:   avgRTT,
				}

				logger.Info(
					"probe result", slog.String("pre", pre),
					slog.String("ip", result.Target.IP),
					slog.Int("port", result.Target.Port),
					slog.String("provider", result.Target.Provider),
					slog.String("target_type", result.Target.TargetType),
					slog.Any("result", result),
				)

				// 收集到本轮结果
				roundMu.Lock()
				roundResults = append(roundResults, result)
				roundMu.Unlock()
			}
		}()
	}

	// 投递任务
	go func() {
		for _, t := range targets {
			jobs <- t
		}
		close(jobs)
	}()

	wg.Wait()

	// 更新全局最新结果
	updateLatestResults(roundResults)
}
