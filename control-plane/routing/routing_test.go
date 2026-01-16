package routing

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

// 测试动态 EdgeWeight 和 Dijkstra
func TestDijkstraDynamic(t *testing.T) {
	edges := []*Edge{
		{Source: "A", Destination: "B", EdgeWeight: 1, Latency: 10, CacheUsageRatio: 0.2},
		{Source: "B", Destination: "C", EdgeWeight: 2, Latency: 5, CacheUsageRatio: 0.1},
		{Source: "A", Destination: "C", EdgeWeight: 4, Latency: 20, CacheUsageRatio: 0.5},
		{Source: "C", Destination: "D", EdgeWeight: 1, Latency: 8, CacheUsageRatio: 0.3},
		{Source: "B", Destination: "D", EdgeWeight: 5, Latency: 15, CacheUsageRatio: 0.4},
	}

	// 后台更新 EdgeWeight
	UpdateEdges(edges, func(e *Edge) float64 {
		// 模拟动态变化：EdgeWeight = 原始 * (1 + CacheUsageRatio) * 随机浮动
		e.mu.RLock()
		defer e.mu.RUnlock()
		randomFactor := 0.9 + 0.2*rand.Float64() // 0.9~1.1
		return e.EdgeWeight * (1 + e.CacheUsageRatio) * randomFactor
	}, time.Second)

	// 等待第一次更新
	time.Sleep(100 * time.Millisecond)

	// 每秒计算一次最短路径，循环3次
	for i := 0; i < 3; i++ {
		path, total := Dijkstra(edges, "A", "D", 0)
		if path == nil {
			fmt.Println("Path from A to D does not exist")
		} else {
			fmt.Println("Shortest path:", path)
			fmt.Printf("Total EdgeWeight: %.2f\n", total)
		}
		time.Sleep(time.Second)
	}

	// 测试 start 不存在
	path, total := Dijkstra(edges, "X", "D", 0)
	if path != nil || total != 1/math.Inf(1) {
		fmt.Println("Start node X does not exist test passed, path:", path, "total:", total)
	} else {
		fmt.Println("Start node X does not exist test failed")
	}

	// 测试 end 不存在
	path, total = Dijkstra(edges, "A", "Y", 0)
	if path != nil || total != 1/math.Inf(1) {
		fmt.Println("End node Y does not exist test passed, path:", path, "total:", total)
	} else {
		fmt.Println("End node Y does not exist test failed")
	}
}

// ----------------------- 动态更新 EdgeWeight -----------------------
func UpdateEdges(edges []*Edge, computeWeight func(*Edge) float64, interval time.Duration) {
	go func() {
		for {
			for _, e := range edges {
				newWeight := computeWeight(e)
				e.UpdateWeight(newWeight)
			}
			time.Sleep(interval)
		}
	}()
}
