package probing

import (
	"context"
	"net"
	"sync"
	"time"
)

// Result 保存探测结果
type Result struct {
	Target   string
	Attempts int     // 探测次数
	Failures int     // 失败次数
	LossRate float64 // 失败比例
}

// Config 配置
type Config struct {
	Concurrency int           // 并发数
	Timeout     time.Duration // TCP Dial 超时
	Interval    time.Duration // 周期
	Attempts    int           // 每轮探测尝试次数
}

// StartProbePeriodically 启动周期性丢包探测
func StartProbePeriodically(ctx context.Context, targets []string, cfg Config) <-chan Result {
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

	resultsCh := make(chan Result)

	go func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		defer close(resultsCh)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				doProbeLoss(targets, cfg, resultsCh)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 下一轮探测
			}
		}
	}()

	return resultsCh
}

// doProbeLoss 执行一轮丢包探测
func doProbeLoss(targets []string, cfg Config, resultsCh chan<- Result) {
	jobs := make(chan string)
	var wg sync.WaitGroup

	// worker
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				failures := 0
				for a := 0; a < cfg.Attempts; a++ {
					conn, err := net.DialTimeout("tcp", target, cfg.Timeout)
					if err != nil {
						failures++
					} else {
						conn.Close()
					}
				}
				select {
				case resultsCh <- Result{
					Target:   target,
					Attempts: cfg.Attempts,
					Failures: failures,
					LossRate: float64(failures) / float64(cfg.Attempts),
				}:
				default:
					// 避免阻塞
				}
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
}
