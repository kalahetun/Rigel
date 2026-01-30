package routing

import (
	"io"
	"log/slog"
	"math"
	"os"
	"testing"

	"control-plane/storage"
)

// ----------------------- 辅助函数（适配map类型） -----------------------

// buildEdgeMap: 将切片转为map[string]*Edge（适配你的edges字段类型）
func buildEdgeMap(edges []*Edge) map[string]*Edge {
	edgeMap := make(map[string]*Edge)
	for _, e := range edges {
		// 自定义key规则（可根据你的实际业务调整，比如用源IP+目标IP）
		key := e.SourceIp + "->" + e.DestinationIp
		edgeMap[key] = e
	}
	return edgeMap
}

// buildNodeMap: 将切片转为map[string]*storage.NetworkTelemetry（适配你的nodes字段类型）
func buildNodeMap(nodes []*storage.NetworkTelemetry) map[string]*storage.NetworkTelemetry {
	nodeMap := make(map[string]*storage.NetworkTelemetry)
	for _, n := range nodes {
		// 自定义key规则（通常用PublicIP作为key，可根据你的实际逻辑调整）
		key := n.PublicIP
		nodeMap[key] = n
	}
	return nodeMap
}

// 确保GetEdges返回切片（适配Dijkstra的遍历逻辑）
func (g *GraphManager) GetEdges() []*Edge {
	edges := make([]*Edge, 0, len(g.edges))
	for _, e := range g.edges {
		edges = append(edges, e)
	}
	return edges
}

// 确保GetNodes返回切片（适配Routing的遍历逻辑）
func (g *GraphManager) GetNodes() []*storage.NetworkTelemetry {
	nodes := make([]*storage.NetworkTelemetry, 0, len(g.nodes))
	for _, n := range g.nodes {
		nodes = append(nodes, n)
	}
	return nodes
}

// ----------------------- 核心测试用例 -----------------------

// TestDijkstra: 测试Dijkstra算法（完全适配map类型）
func TestDijkstra(t *testing.T) {
	// 1. 初始化GraphManager（所有字段均为map类型）
	gm := &GraphManager{
		edges: buildEdgeMap([]*Edge{
			{SourceIp: "10.0.0.1-in", DestinationIp: "10.0.0.2-out", EdgeWeight: 10},
			{SourceIp: "10.0.0.2-in", DestinationIp: "10.0.0.3-out", EdgeWeight: 20},
			{SourceIp: "10.0.0.1-in", DestinationIp: "10.0.0.3-out", EdgeWeight: 25},
		}),
		nodes: buildNodeMap([]*storage.NetworkTelemetry{
			{PublicIP: "10.0.0.1", Continent: "Asia"},
			{PublicIP: "10.0.0.2", Continent: "Europe"},
			{PublicIP: "10.0.0.3", Continent: "America"},
		}),
		logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}

	// 测试用例1：有效最短路径
	path, cost := gm.Dijkstra("10.0.0.1-in", "10.0.0.3-out")
	t.Logf("有效路径测试 - 路径: %v, 总成本: %.2f", path, cost)

	// 验证成本（alpha=1.2，10+20=30*1.2=36）
	expectedCost := 36.0
	if math.Abs(cost-expectedCost) > 1e-6 {
		t.Errorf("成本验证失败：期望 %.2f，实际 %.2f", expectedCost, cost)
	}

	// 验证路径节点
	expectedPath := []string{"10.0.0.1-in", "10.0.0.2-out", "10.0.0.3-out"}
	if len(path) != len(expectedPath) {
		t.Errorf("路径长度验证失败：期望 %d，实际 %d", len(expectedPath), len(path))
	}
	for i := range expectedPath {
		if path[i] != expectedPath[i] {
			t.Errorf("路径节点[%d]验证失败：期望 %s，实际 %s", i, expectedPath[i], path[i])
		}
	}

	// 测试用例2：起点不存在
	path, cost = gm.Dijkstra("10.0.0.4-in", "10.0.0.3-out")
	if path != nil || !math.IsInf(cost, 1) {
		t.Error("无效起点测试失败：期望nil路径和+Inf成本")
	}

	// 测试用例3：终点不存在
	path, cost = gm.Dijkstra("10.0.0.1-in", "10.0.0.4-out")
	if path != nil || !math.IsInf(cost, 1) {
		t.Error("无效终点测试失败：期望nil路径和+Inf成本")
	}
}

// TestRouting: 测试Routing方法（完全适配map类型）
func TestRouting(t *testing.T) {
	// 1. 初始化GraphManager（所有字段为map类型）
	gm := &GraphManager{
		logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
		// nodes: map[string]*storage.NetworkTelemetry
		nodes: buildNodeMap([]*storage.NetworkTelemetry{
			{PublicIP: "192.168.1.1", Continent: "Asia"},    // 亚洲起点
			{PublicIP: "192.168.2.1", Continent: "Europe"},  // 欧洲中间节点
			{PublicIP: "192.168.3.1", Continent: "America"}, // 美洲终点
		}),
		// edges: map[string]*Edge
		edges: buildEdgeMap([]*Edge{
			{SourceIp: InNode("192.168.1.1"), DestinationIp: OutNode("192.168.2.1"), EdgeWeight: 5},
			{SourceIp: InNode("192.168.2.1"), DestinationIp: OutNode("192.168.3.1"), EdgeWeight: 8},
		}),
	}

	// 初始化logger（Go 1.21+ 正确写法，测试中屏蔽输出）
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// 测试用例1：有效大洲路由
	routingInfo := gm.Routing("Asia", UserRouteRequest{}, logger)
	t.Logf("有效大洲路由测试 - 结果: %+v", routingInfo)

	// 验证路由结果非空
	if len(routingInfo.Routing) == 0 {
		t.Error("有效路由测试失败：期望非空RoutingInfo")
	}
	pathInfo := routingInfo.Routing[0]

	// 验证hops格式
	expectedHops := "192.168.1.1:8095,192.168.2.1:8095,192.168.3.1:8095"
	if pathInfo.Hops != expectedHops {
		t.Errorf("Hops格式验证失败：期望 %s，实际 %s", expectedHops, pathInfo.Hops)
	}

	// 验证rate为正数
	if pathInfo.Rate <= 0 {
		t.Error("Rate验证失败：期望正数值，实际", pathInfo.Rate)
	}

	// 测试用例2：起点大洲无节点
	routingInfo = gm.Routing("Africa", UserRouteRequest{}, logger)
	if len(routingInfo.Routing) != 0 {
		t.Error("无效起点大洲测试失败：期望空RoutingInfo")
	}

	// 测试用例3：无有效路径
	gmNoPath := &GraphManager{
		logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
		nodes: buildNodeMap([]*storage.NetworkTelemetry{
			{PublicIP: "192.168.1.1", Continent: "Asia"},
			{PublicIP: "192.168.4.1", Continent: "America"}, // 无连接的终点
		}),
		edges: buildEdgeMap([]*Edge{
			{SourceIp: InNode("192.168.1.1"), DestinationIp: OutNode("192.168.2.1"), EdgeWeight: 5},
		}),
	}
	routingInfo = gmNoPath.Routing("Asia", UserRouteRequest{}, logger)
	if len(routingInfo.Routing) != 0 {
		t.Error("无路径测试失败：期望空RoutingInfo")
	}
}

// BenchmarkDijkstra: 性能基准测试（适配所有map类型）
func BenchmarkDijkstra(b *testing.B) {
	gm := &GraphManager{
		logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
		nodes: buildNodeMap([]*storage.NetworkTelemetry{
			{PublicIP: "10.0.0.1", Continent: "Asia"},
			{PublicIP: "10.0.0.2", Continent: "Europe"},
			{PublicIP: "10.0.0.3", Continent: "America"},
			{PublicIP: "10.0.0.4", Continent: "Africa"},
		}),
		edges: buildEdgeMap([]*Edge{
			{SourceIp: "10.0.0.1-in", DestinationIp: "10.0.0.2-out", EdgeWeight: 10},
			{SourceIp: "10.0.0.2-in", DestinationIp: "10.0.0.3-out", EdgeWeight: 20},
			{SourceIp: "10.0.0.1-in", DestinationIp: "10.0.0.3-out", EdgeWeight: 25},
			{SourceIp: "10.0.0.3-in", DestinationIp: "10.0.0.4-out", EdgeWeight: 15},
		}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gm.Dijkstra("10.0.0.1-in", "10.0.0.4-out")
	}
}
