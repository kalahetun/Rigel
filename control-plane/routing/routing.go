package routing

import (
	"container/heap"
	"math"
)

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
func Dijkstra(edges []*Edge, start, end string, hopPenalty float64) ([]string, float64) {
	// 构建邻接表和节点集合
	graph := make(map[string][]*Edge)
	nodes := make(map[string]struct{})
	for _, e := range edges {
		graph[e.SourceIp] = append(graph[e.SourceIp], e)
		nodes[e.SourceIp] = struct{}{}
		nodes[e.DestinationIp] = struct{}{}
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
			alt := dist[u.node] + e.Weight() + hopPenalty
			if alt < dist[e.DestinationIp] {
				dist[e.DestinationIp] = alt
				prev[e.DestinationIp] = u.node
				heap.Push(pq, &PQNode{node: e.DestinationIp, distance: alt})
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
