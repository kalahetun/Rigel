package probing

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestStartProbePeriodically(t *testing.T) {
	// 测试目标（可以用本机端口测试，也可以用公网常用端口）
	targets := []string{
		"google.com:80",
		"example.com:80",
	}

	cfg := Config{
		Concurrency: 2,
		Timeout:     1 * time.Second,
		Interval:    2 * time.Second, // 周期短一点方便测试
		Attempts:    3,
		BufferSize:  10,
	}

	// 用 context 控制测试运行时间
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resultsCh := StartProbePeriodically(ctx, targets, cfg)

	// 收集结果
	for res := range resultsCh {
		fmt.Printf("Target: %s, Attempts: %d, Failures: %d, LossRate: %.2f, AvgRTT: %v\n",
			res.Target, res.Attempts, res.Failures, res.LossRate, res.AvgRTT)
	}
}
