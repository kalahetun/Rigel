package probing

import (
	"context"
	"testing"
	"time"
)

func TestPeriodicProbeLoss(t *testing.T) {
	targets := []string{
		"8.8.8.8:53",
		"1.1.1.1:53",
		"google.com:443",
	}

	cfg := Config{
		Concurrency: 2,
		Timeout:     2 * time.Second,
		Interval:    3 * time.Second,
		Attempts:    5, // 每轮尝试次数
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	results := StartProbePeriodically(ctx, targets, cfg)

	count := 0
	for res := range results {
		count++
		t.Logf("[RESULT] target=%s attempts=%d failures=%d loss=%.2f%%",
			res.Target, res.Attempts, res.Failures, res.LossRate*100)
	}

	if count == 0 {
		t.Errorf("no results received")
	}
}
