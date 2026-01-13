package routing

import (
	"container/heap"
	"math"
	"sync"
)

// ----------------------- Edge -----------------------
type Edge struct {
	Source          string  `json:"source"`            // A 节点名/ID
	Destination     string  `json:"destination"`       // B 节点名/ID
	SourceProvider  string  `json:"source_provider"`   // A 节点云服务商
	BandwidthPrice  float64 `json:"bandwidth_price"`   // A 节点出口带宽价格 ($/GB)
	Latency         float64 `json:"latency_ms"`        // A->B 时延
	CacheUsageRatio float64 `json:"cache_usage_ratio"` // 缓存占用比例 [0,1]
	EdgeWeight      float64 `json:"edge_weight"`       // 综合权重，用于最短路径计算

	mu sync.RWMutex // 保护动态字段
}

// UpdateWeight 安全更新 EdgeWeight
func (e *Edge) UpdateWeight(newWeight float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.EdgeWeight = newWeight
}

// Weight 安全读取 EdgeWeight
func (e *Edge) Weight() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.EdgeWeight
}

// ----------------------- GraphManager -----------------------
// 全局图管理器，维护 edges 和节点
type GraphManager struct {
	mu    sync.RWMutex
	edges map[string]*Edge    // key: "source->destination"
	nodes map[string]struct{} // 所有节点
}

// NewGraphManager 初始化
func NewGraphManager() *GraphManager {
	return &GraphManager{
		edges: make(map[string]*Edge),
		nodes: make(map[string]struct{}),
	}
}

// AddEdge 添加或更新边
func (g *GraphManager) AddEdge(e *Edge) {
	key := e.Source + "->" + e.Destination
	g.mu.Lock()
	defer g.mu.Unlock()
	g.edges[key] = e
	g.nodes[e.Source] = struct{}{}
	g.nodes[e.Destination] = struct{}{}
}

// RemoveEdge 删除边
func (g *GraphManager) RemoveEdge(source, dest string) {
	key := source + "->" + dest
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.edges, key)
}

// GetEdges 返回当前所有 edges
func (g *GraphManager) GetEdges() []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	list := make([]*Edge, 0, len(g.edges))
	for _, e := range g.edges {
		list = append(list, e)
	}
	return list
}

// UpdateEdgeWeight 更新指定边的权重
func (g *GraphManager) UpdateEdgeWeight(source, dest string, weight float64) {
	key := source + "->" + dest
	g.mu.RLock()
	e, ok := g.edges[key]
	g.mu.RUnlock()
	if ok {
		e.UpdateWeight(weight)
	}
}

// NodeExists 检查节点是否存在
func (g *GraphManager) NodeExists(node string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.nodes[node]
	return ok
}

// ----------------------- 优先队列 -----------------------
type PQNode struct {
	node     string
	distance float64
	index    int
}

type PriorityQueue []*PQNode

func (pq PriorityQueue) Len() int           { return len(pq) }
func (pq PriorityQueue) Less(i, j int) bool { return pq[i].distance < pq[j].distance }
func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	node := x.(*PQNode)
	node.index = n
	*pq = append(*pq, node)
}
func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	node := old[n-1]
	node.index = -1
	*pq = old[0 : n-1]
	return node
}

// ----------------------- Dijkstra 算法 -----------------------
// 使用 EdgeWeight 计算最短路径，并提前终止
// 如果 start/end 不存在或不可达，返回 nil 路径和 Inf
func Dijkstra(edges []*Edge, start, end string) ([]string, float64) {
	// 构建邻接表和节点集合
	graph := make(map[string][]*Edge)
	nodes := make(map[string]struct{})
	for _, e := range edges {
		graph[e.Source] = append(graph[e.Source], e)
		nodes[e.Source] = struct{}{}
		nodes[e.Destination] = struct{}{}
	}

	// 检查 start/end 是否存在
	if _, ok := nodes[start]; !ok {
		return nil, math.Inf(1)
	}
	if _, ok := nodes[end]; !ok {
		return nil, math.Inf(1)
	}

	// 初始化距离和前驱节点
	dist := make(map[string]float64)
	prev := make(map[string]string)
	for node := range nodes {
		dist[node] = math.Inf(1)
	}
	dist[start] = 0

	// 初始化优先队列
	pq := &PriorityQueue{}
	heap.Init(pq)
	heap.Push(pq, &PQNode{node: start, distance: 0})

	for pq.Len() > 0 {
		u := heap.Pop(pq).(*PQNode)

		// 提前终止
		if u.node == end {
			break
		}

		for _, e := range graph[u.node] {
			alt := dist[u.node] + e.Weight()
			if alt < dist[e.Destination] {
				dist[e.Destination] = alt
				prev[e.Destination] = u.node
				heap.Push(pq, &PQNode{node: e.Destination, distance: alt})
			}
		}
	}

	// 构建路径
	path := []string{}
	u := end
	if _, ok := prev[u]; ok || u == start {
		for u != "" {
			path = append([]string{u}, path...)
			u = prev[u]
		}
	} else {
		// end 不可达
		return nil, math.Inf(1)
	}

	return path, dist[end]
}

//// ----------------------- 动态更新 EdgeWeight -----------------------
//// computeWeight: 自定义函数根据 Edge 返回最新权重
//func UpdateEdges(edges []*Edge, computeWeight func(*Edge) float64, interval time.Duration) {
//	go func() {
//		for {
//			for _, e := range edges {
//				newWeight := computeWeight(e)
//				e.UpdateWeight(newWeight)
//			}
//			time.Sleep(interval)
//		}
//	}()
//}
//
//// ----------------------- 示例 computeWeight -----------------------
//// 可以根据 CacheUsageRatio、Latency、CPU、BandwidthPrice 等动态调整权重
//func ExampleComputeWeight(e *Edge) float64 {
//	e.mu.RLock()
//	defer e.mu.RUnlock()
//	// 示例逻辑：EdgeWeight = 原始 EdgeWeight * (1 + CacheUsageRatio)
//	return e.EdgeWeight * (1 + e.CacheUsageRatio)
//}
