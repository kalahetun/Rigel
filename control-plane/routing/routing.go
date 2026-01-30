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

// Post请求Body结构
type UserRouteRequest struct {
	FileName   string `json:"fileName"`        // 文件名
	Priority   int    `json:"priority"`        // 文件优先级
	ClientCont string `json:"clientContinent"` // 客户端大区
	ServerIP   string `json:"serverIP"`        // 目标服务器 IP 或域名
	//ServerCont     string `json:"serverContinent"` // 目标服务器大区
	Username      string `json:"username"`      // 客户端用户名
	CloudProvider string `json:"cloudProvider"` // 云服务提供商，例如 AWS, GCP, DO
	CloudRegion   string `json:"cloudRegion"`   // 云服务所在区域，例如 us-east-1
	CloudCity     string `json:"cloudCity"`     // 云服务所在城市，例如 Ashburn
}

// 输入是client区域和cloud storage 区域
func (g *GraphManager) Routing(startContinent string, request UserRouteRequest, logger *slog.Logger) RoutingInfo {

	logger.Info("Routing", "startContinent", startContinent, "userRouteRequest", request)

	// 获取所有节点
	allNodes := g.GetNodes()

	// 根据大洲过滤 start 和 end 节点 && 展现寻找最优路径
	var startNodes []*storage.NetworkTelemetry

	for _, node := range allNodes {
		if node.Continent == startContinent {
			startNodes = append(startNodes, node)
		}
	}

	//client所在大区没有接入点 直接公网传输
	if len(startNodes) == 0 {
		logger.Warn("No nodes found for start continent", "startContinent", startContinent)
		return RoutingInfo{}
	}

	cloudFull := fmt.Sprintf("%s_%s_%s",
		request.CloudProvider, request.CloudRegion, request.CloudCity)

	//没有到该cloud storage的路径
	if _, ok := g.FindEdgeBySuffix(cloudFull); !ok {
		logger.Warn("No cloud node found", "cloudFull", cloudFull)
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
		path, cost := g.Dijkstra(InNode(sNode.PublicIP), cloudFull)
		if path == nil {
			continue
		}
		tempPaths = append(tempPaths, Path{path, cost})
		if len(path) > 0 && cost < minCost {
			minCost = cost
			bestPath = path
		}
	}
	logger.Info("All candidate paths", "paths", fmt.Sprintf("%+v", tempPaths))

	// 输出结果
	if len(bestPath) == 0 {
		logger.Warn("No path found between continents",
			"startContinent", startContinent, "endContinent", cloudFull)
	} else {
		logger.Info("Shortest path found", "startContinent", startContinent,
			"endContinent", cloudFull, "path", bestPath, "totalRisk", minCost)
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
	merged += "," + request.ServerIP

	//计算速率
	rate := ComputeAdmissionRate(Task{WeightU: 1, MinRate: 10, MaxRate: 20}, minCost, 1.0, 100, g.logger)
	return RoutingInfo{[]PathInfo{PathInfo{merged, int64(rate)}}}
}
