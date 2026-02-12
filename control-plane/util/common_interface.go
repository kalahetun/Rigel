package util

type ProbeTask struct {
	TargetType string // "node" | "cloud_storage"
	Provider   string // node 可为空，cloud storage 用 google/aws/azure
	IP         string
	Port       int
	Region     string // cloud storage 用，node 可为空
	City       string // cloud storage 用，node 可为空
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
