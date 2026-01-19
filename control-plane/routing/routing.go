package routing

import (
	"container/heap"
	"control-plane/storage"
	"fmt"
	"log/slog"
	"math"
	"strings"
)

// ----------------------- Priority Queue -----------------------
// 原PQNode结构（保留，用于优先级队列）
type PQNode struct {
	node string  // 仅存储当前节点，而非完整路径，优化内存
	cost float64 // 当前节点到起点的成本
	// 为了优先级队列排序，补充索引字段（container/heap要求）
	index int
}

// 原PriorityQueue结构（补充完整，保证container/heap可正常工作）
type PriorityQueue []*PQNode

func (pq PriorityQueue) Len() int           { return len(pq) }
func (pq PriorityQueue) Less(i, j int) bool { return pq[i].cost < pq[j].cost } // 最小堆，按成本升序排序
func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index, pq[j].index = i, j
}

func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*PQNode)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // 避免内存泄漏
	item.index = -1 // 标记为已弹出
	*pq = old[0 : n-1]
	return item
}

// ----------------------- Dijkstra -----------------------
func (g *GraphManager) Dijkstra(start, end string) ([]string, float64) {

	const (
		alpha = 1.2
	)

	edges := g.GetEdges()

	// 1. 构建图和节点集合（原逻辑保留，无问题）
	graph := make(map[string][]*Edge)
	nodes := make(map[string]struct{})
	for _, e := range edges {
		graph[e.SourceIp] = append(graph[e.SourceIp], e)
		nodes[e.SourceIp] = struct{}{}
		nodes[e.DestinationIp] = struct{}{}
	}

	// 2. 校验起点和终点是否存在（原逻辑保留，无问题）
	if _, ok := nodes[start]; !ok {
		return nil, math.Inf(1)
	}
	if _, ok := nodes[end]; !ok {
		return nil, math.Inf(1)
	}

	// 3. 初始化距离映射和前驱节点映射（原逻辑保留，无问题）
	dist := make(map[string]float64)
	prev := make(map[string]string)
	for node := range nodes {
		dist[node] = math.Inf(1)
	}
	dist[start] = 0

	// 4. 初始化优先级队列（优化：仅存储节点和成本，而非完整路径）
	pq := &PriorityQueue{}
	heap.Init(pq)
	heap.Push(pq, &PQNode{
		node: start,
		cost: 0,
	})

	// 5. 处理优先级队列（核心修改：添加节点成本校验）
	for pq.Len() > 0 {
		// 弹出当前成本最低的节点
		u := heap.Pop(pq).(*PQNode)
		currNode := u.node
		currCost := u.cost

		// 【核心修复】添加校验：如果当前弹出的成本大于已记录的最短距离，直接跳过该节点（已处理过更优路径）
		if currCost > dist[currNode] {
			continue
		}

		// 到达终点，回溯路径并返回
		if currNode == end {
			// 通过prev映射回溯路径
			path := []string{}
			for node := end; node != ""; node = prev[node] {
				path = append([]string{node}, path...)
			}
			return path, currCost
		}

		// 遍历当前节点的邻接边，更新最短路径
		for _, e := range graph[currNode] {
			nextNode := e.DestinationIp
			// 计算新路径成本
			newCost := currCost + e.EdgeWeight*alpha

			// 如果新路径更优，更新距离并推入优先级队列
			if newCost < dist[nextNode] {
				dist[nextNode] = newCost
				prev[nextNode] = currNode // 记录前驱节点
				heap.Push(pq, &PQNode{
					node: nextNode,
					cost: newCost,
				})
			}
		}
	}

	// 无法到达终点
	return nil, math.Inf(1)
}

type PathInfo struct {
	Hops string `json:"hops"`
	Rate int64  `json:"rate"`
	//Weight int64  `json:"weight"`
}

type RoutingInfo struct {
	Routing []PathInfo `json:"routing"`
}

// 输入是client区域和cloud storage 区域
func (g *GraphManager) Routing(startC, endC string, logger *slog.Logger) RoutingInfo {
	logger.Info("Routing start", "startC", startC, "endC", endC)

	// 获取所有节点
	allNodes := g.GetNodes()

	// 根据大洲过滤 start 和 end 节点 && 展现寻找最优路径
	var startNodes []*storage.NetworkTelemetry
	var endNodes []*storage.NetworkTelemetry

	for _, node := range allNodes {
		if node.Continent == startC {
			startNodes = append(startNodes, node)
		}
		if node.Continent == endC {
			endNodes = append(endNodes, node)
		}
	}

	if len(startNodes) == 0 || len(endNodes) == 0 {
		logger.Warn("No nodes found for start or end continent", "startC", startC, "endC", endC)
		return RoutingInfo{}
	}

	// 遍历 start × end 节点组合，寻找最短路径
	var bestPath []string
	type Path struct {
		path []string
		cost float64
	}
	var tempPaths []Path
	minCost := math.Inf(1)

	for _, sNode := range startNodes {
		for _, eNode := range endNodes {

			path, cost := g.Dijkstra(InNode(sNode.PublicIP), OutNode(eNode.PublicIP))

			tempPaths = append(tempPaths, Path{path, cost})
			if len(path) > 0 && cost < minCost {
				minCost = cost
				bestPath = path
			}
		}
	}

	logger.Info("All candidate paths", "paths", fmt.Sprintf("%+v", tempPaths))

	// 输出结果
	if len(bestPath) == 0 {
		logger.Warn("No path found between continents", "startC", startC, "endC", endC)
	} else {
		logger.Info("Shortest path found",
			"startC", startC,
			"endC", endC,
			"path", bestPath,
			"totalRisk", minCost)
	}

	hops := []string{}
	hopMap := make(map[string]string)
	for _, h := range bestPath {
		tempIP := strings.Split(h, "-")[0]
		if _, ok := hopMap[tempIP]; !ok {
			hops = append(hops, tempIP)
		}
	}
	hops_ := []string{}
	for _, h := range hops {
		hops_ = append(hops_, h+":8095")
	}
	merged := strings.Join(hops_, ",")

	return RoutingInfo{[]PathInfo{PathInfo{merged, 10485760}}}
}

//todo 计算限流
