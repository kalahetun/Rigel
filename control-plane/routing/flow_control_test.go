package routing

import (
	"math"
	"testing"
)

/*
Test for ComputeAdmissionRate (UMW-Opt style)

参数说明：
1. Task.WeightU  => w_k^U 任务权重（优先级），高值优先
2. Task.MaxRate => 链路瓶颈 / 最大允许速率
3. cost         => C_k^*(t) 路径成本 = sum(w_e^M * Q_e + V*p_e)
4. alpha        => 公平性参数，α=1.0 -> 比例公平
5. V            => 控制队列规模对速率的影响
*/

// 小工具：近似比较浮点数
func approxEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

func TestComputeAdmissionRate(t *testing.T) {
	alpha := 1.0
	V := 100.0

	tests := []struct {
		name     string
		task     Task
		cost     float64
		expected float64
	}{
		{
			name: "Low cost, weight=1",
			task: Task{
				ID:      "task1",
				WeightU: 1.0,
				MaxRate: 50,
			},
			cost:     10.0,
			expected: 10.0, // V*WeightU / cost = 100*1/10 = 10
		},
		{
			name: "High cost, weight=2",
			task: Task{
				ID:      "task2",
				WeightU: 2.0,
				MaxRate: 50,
			},
			cost:     20.0,
			expected: 10.0, // 100*2/20 = 10
		},
		{
			name: "Exceed MaxRate",
			task: Task{
				ID:      "task3",
				WeightU: 10.0,
				MaxRate: 5.0,
			},
			cost:     1.0,
			expected: 5.0, // 100*10/1 = 1000 > MaxRate => clip to 5
		},
		{
			name: "Zero cost safety",
			task: Task{
				ID:      "task4",
				WeightU: 1.0,
				MaxRate: 50,
			},
			cost:     0.0,
			expected: 0.0, // cost <= 0 -> rate = 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate := ComputeAdmissionRate(tt.task, tt.cost, alpha, V, nil)
			if !approxEqual(rate, tt.expected, 1e-6) {
				t.Errorf("expected %.2f, got %.2f", tt.expected, rate)
			}
		})
	}
}
