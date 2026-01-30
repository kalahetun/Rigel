package routing

import (
	"control-plane/storage"
	"fmt"
	"log/slog"
	"math"
	"strings"
)

type PathInfo struct {
	Hops string `json:"hops"`
	Rate int64  `json:"rate"`
	//Weight int64  `json:"weight"`
}

type RoutingInfo struct {
	Routing []PathInfo `json:"routing"`
}

// 输入是client区域和cloud storage 区域
func (g *GraphManager) Routing(startContinent, endContinent, serverIP string, logger *slog.Logger) RoutingInfo {

	logger.Info("Routing start", "startContinent", startContinent, "endContinent", endContinent)

	//client直接接入sever
	if startContinent == endContinent {
		return RoutingInfo{}
	}

	// 获取所有节点
	allNodes := g.GetNodes()

	// 根据大洲过滤 start 和 end 节点 && 展现寻找最优路径
	var startNodes []*storage.NetworkTelemetry
	var endNodes []*storage.NetworkTelemetry

	for _, node := range allNodes {
		if node.Continent == startContinent {
			startNodes = append(startNodes, node)
		}
		if node.Continent == endContinent {
			endNodes = append(endNodes, node)
		}
	}

	//todo 直接上传到sever
	if len(startNodes) == 0 || len(endNodes) == 0 {
		logger.Warn("No nodes found for start or end continent",
			"startContinent", startContinent, "endContinent", endContinent)
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
		logger.Warn("No path found between continents",
			"startContinent", startContinent, "endContinent", endContinent)
	} else {
		logger.Info("Shortest path found", "startContinent", startContinent,
			"endContinent", endContinent, "path", bestPath, "totalRisk", minCost)
	}

	var hops []string
	hopMap := make(map[string]string)
	for _, h := range bestPath {
		tempIP := strings.Split(h, "-")[0]
		if _, ok := hopMap[tempIP]; !ok {
			hops = append(hops, tempIP)
		}
	}
	var hops_ []string
	for _, h := range hops {
		hops_ = append(hops_, h+":8090") //gateway port
	}
	merged := strings.Join(hops_, ",")
	merged += "," + serverIP

	//计算速率
	rate := ComputeAdmissionRate(Task{WeightU: 1, MinRate: 10, MaxRate: 20}, minCost, 1.0, 100, g.logger)

	return RoutingInfo{[]PathInfo{PathInfo{merged, int64(rate)}}}
}
