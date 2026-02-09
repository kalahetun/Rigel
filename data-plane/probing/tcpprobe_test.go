package probing

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

// 模拟 GetProbeTasks 返回的目标列表
//func init() {
//	GetProbeTasks := func(controlHost string) ([]string, error) {
//		// 简单模拟：探测本地的 80 和 443 端口
//		return []string{"127.0.0.1:80", "127.0.0.1:443"}, nil
//	}
//}

func TestStartProbePeriodically(t *testing.T) {
	logger := slog.New(nil) // 简单 logger，可替换为真实实现
	cfg := Config{
		Concurrency: 2,
		Timeout:     500 * time.Millisecond,
		Interval:    2 * time.Second,
		Attempts:    3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	// 启动周期探测（无限循环，但有ctx超时）
	StartProbePeriodically(ctx, "http://127.0.0.1:8080", cfg, "", logger)

	// 每秒检查一次最新结果
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("测试结束")
			return
		case <-ticker.C:
			results := GetLatestResults()
			fmt.Println("最新探测结果：")
			for target, r := range results {
				fmt.Printf("Target: %s, Attempts: %d, Failures: %d, LossRate: %.2f%%, AvgRTT: %v\n",
					target, r.Attempts, r.Failures, r.LossRate*100, r.AvgRTT)
			}
			fmt.Println("-----")
		}
	}
}
