package routing

import (
	"container/heap"
	"math"
)

// ----------------------- Priority Queue -----------------------
type PQNode struct {
	path  []string
	cost  float64
	index int
}

type PriorityQueue []*PQNode

func (pq PriorityQueue) Len() int           { return len(pq) }
func (pq PriorityQueue) Less(i, j int) bool { return pq[i].cost < pq[j].cost }
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

// ----------------------- Dijkstra -----------------------
func Dijkstra(edges []*Edge, start, end string, hopPenalty float64) ([]string, float64) {
	graph := make(map[string][]*Edge)
	nodes := make(map[string]struct{})
	for _, e := range edges {
		graph[e.SourceIp] = append(graph[e.SourceIp], e)
		nodes[e.SourceIp] = struct{}{}
		nodes[e.DestinationIp] = struct{}{}
	}

	if _, ok := nodes[start]; !ok {
		return nil, math.Inf(1)
	}
	if _, ok := nodes[end]; !ok {
		return nil, math.Inf(1)
	}

	dist := make(map[string]float64)
	prev := make(map[string]string)
	for node := range nodes {
		dist[node] = math.Inf(1)
	}
	dist[start] = 0

	pq := &PriorityQueue{}
	heap.Init(pq)
	heap.Push(pq, &PQNode{path: []string{start}, cost: 0})

	for pq.Len() > 0 {
		u := heap.Pop(pq).(*PQNode)
		curr := u.path[len(u.path)-1]
		currCost := u.cost

		if curr == end {
			return u.path, currCost
		}

		for _, e := range graph[curr] {
			alt := currCost + e.EdgeWeight + hopPenalty
			if alt < dist[e.DestinationIp] {
				dist[e.DestinationIp] = alt
				newPath := append([]string{}, u.path...)
				newPath = append(newPath, e.DestinationIp)
				heap.Push(pq, &PQNode{path: newPath, cost: alt})
				prev[e.DestinationIp] = curr
			}
		}
	}
	return nil, math.Inf(1)
}

// ----------------------- Yen K-Shortest Paths -----------------------
func YenKShortestPaths(edges []*Edge, start, end string, k int, hopPenalty float64) [][]string {
	var paths [][]string

	// 1. 找到最短路径
	sp, _ := Dijkstra(edges, start, end, hopPenalty)
	if sp == nil {
		return paths
	}
	paths = append(paths, sp)

	candidates := &PriorityQueue{}
	heap.Init(candidates)

	for i := 1; i < k; i++ {
		lastPath := paths[i-1]
		for j := 0; j < len(lastPath)-1; j++ {
			rootPath := lastPath[:j+1]
			spurNode := lastPath[j]

			// 移除与 rootPath 冲突的边
			var tempEdges []*Edge
			for _, e := range edges {
				conflict := false
				for _, p := range paths {
					if len(p) > j && equalSlices(p[:j+1], rootPath) && p[j+1] == e.DestinationIp && e.SourceIp == spurNode {
						conflict = true
						break
					}
				}
				if !conflict {
					tempEdges = append(tempEdges, e)
				}
			}

			// 计算 spurPath
			spurPath, spurCost := Dijkstra(tempEdges, spurNode, end, hopPenalty)
			if spurPath == nil {
				continue
			}

			// 合并 rootPath 和 spurPath
			totalPath := append(copyPath(rootPath[:len(rootPath)-1]), spurPath...)
			// rootPath cost
			rootCost := 0.0
			for m := 0; m < len(rootPath)-1; m++ {
				e := findEdge(edges, rootPath[m], rootPath[m+1])
				if e != nil {
					rootCost += e.EdgeWeight + hopPenalty
				}
			}
			totalCost := rootCost + spurCost

			heap.Push(candidates, &PQNode{
				path: totalPath,
				cost: totalCost,
			})
		}

		if candidates.Len() == 0 {
			break
		}

		next := heap.Pop(candidates).(*PQNode)
		paths = append(paths, next.path)
	}

	return paths
}

// ----------------------- 工具函数 -----------------------
func copyPath(p []string) []string {
	newP := make([]string, len(p))
	copy(newP, p)
	return newP
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func findEdge(edges []*Edge, src, dst string) *Edge {
	for _, e := range edges {
		if e.SourceIp == src && e.DestinationIp == dst {
			return e
		}
	}
	return nil
}
