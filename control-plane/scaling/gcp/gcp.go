package gcp

import (
	compute "cloud.google.com/go/compute/apiv1"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/api/option"
	computepb "google.golang.org/genproto/googleapis/cloud/compute/v1"
	"google.golang.org/protobuf/proto"
	"log/slog"
)

const (
	FireWallTag  = "default-allow-internal"
	InstanceType = "e2-medium"
	SourceImage  = "projects/debian-cloud/global/images/family/debian-12"
)

type GCPConfig struct {
	ProjectID string `json:"projectID"` // GCP 项目 ID
	Zone      string `json:"zone"`      // 机房
	//VMPrefix  string `json:"vmPrefix"`  // VM 名称前缀（不是具体名字）
	CredFile string `json:"credFile"` // GCP 凭证文件路径
}

func NewScalingOperate(gcpCfg *GCPConfig, sshKey string,
	pre string, logger *slog.Logger) *ScalingOperate {

	// 初始化ScalingOperate：GCPConfig字段 + 常量 + sshKey
	so := &ScalingOperate{
		projectID: gcpCfg.ProjectID,
		zone:      gcpCfg.Zone,
		//vmPrefix:     gcpCfg.VMPrefix,
		credFile:     gcpCfg.CredFile,
		fireWallTag:  FireWallTag,  // 填充常量
		instanceType: InstanceType, // 填充常量
		sourceImage:  SourceImage,  // 填充常量
		sshKey:       sshKey,       // 填充额外入参
	}

	logger.Info("NewGetSize", slog.String("pre", pre), slog.Any("ScalingOperate", *so))
	return so
}

// ExtractGCPFromInterface 解析JSON字符串为*GCPConfig，自动填充常量默认值
func ExtractGCPFromInterface(data string) (*GCPConfig, error) {
	if data == "" {
		return nil, errors.New("输入的JSON字符串为空")
	}

	config := &GCPConfig{}
	if err := json.Unmarshal([]byte(data), config); err != nil {
		return nil, err
	}

	return config, nil
}

type ScalingOperate struct {
	projectID    string
	zone         string
	vmPrefix     string
	credFile     string
	fireWallTag  string
	instanceType string
	sourceImage  string
	sshKey       string
}

// CreateVM 创建GCP Compute Engine虚拟机实例
// credFile: JSON格式的服务账号凭证文件
// zone: 例如 "us-central1-a"
// vmName: VM名字
func (gc *ScalingOperate) CreateVM(
	ctx context.Context,
	vmName string,
	pre string,
	logger *slog.Logger,
) error {
	// 创建客户端（直接使用凭证文件）
	instancesClient, err := compute.NewInstancesRESTClient(ctx, option.WithCredentialsFile(gc.credFile))
	if err != nil {
		logger.Error("创建 Instances 客户端失败", slog.String("pre", pre), "error", err)
		return err
	}
	defer instancesClient.Close()

	// SSH 公钥
	sshKey := gc.sshKey

	// 启动盘配置
	bootDisk := &computepb.AttachedDisk{
		AutoDelete: proto.Bool(true),
		Boot:       proto.Bool(true),
		Type:       proto.String(computepb.AttachedDisk_PERSISTENT.String()),
		InitializeParams: &computepb.AttachedDiskInitializeParams{
			SourceImage: proto.String(gc.sourceImage),
			DiskSizeGb:  proto.Int64(10), // 确保客户端在使用后被关闭
		},
	}
	// 设置用于SSH访问的公钥

	// 网络接口配置（默认网络，带公网IP）
	networkInterface := &computepb.NetworkInterface{
		// 配置虚拟机的启动盘，包括自动删除、启动类型、源镜像和磁盘大小
		Network: proto.String("global/networks/default"),
		AccessConfigs: []*computepb.AccessConfig{ // 自动删除
			{ // 作为启动盘
				Name: proto.String("External NAT"),   // 持久化磁盘类型
				Type: proto.String("ONE_TO_ONE_NAT"), // 让系统自动分配公网IP
			}, // Debian 12镜像
		}, // 10GB大小
	}

	// 构建 VM 实例对象
	instance := &computepb.Instance{
		// 配置网络接口，使用默认网络并启用公网IP
		Name:        proto.String(vmName),
		MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", gc.zone, gc.instanceType)), // 使用默认网络
		Disks:       []*computepb.AttachedDisk{bootDisk},
		NetworkInterfaces: []*computepb.NetworkInterface{
			networkInterface,
		},
		//"default-allow-internal" 防火墙标签 需要自己配置
		Tags: &computepb.Tags{
			Items: []string{"http-server", "https-server", "lb-health-check", gc.fireWallTag},
		},
		Metadata: &computepb.Metadata{
			Items: []*computepb.Items{
				{
					Key:   proto.String("ssh-keys"),
					Value: proto.String(sshKey),
				},
			},
		},
	}

	// 创建请求
	req := &computepb.InsertInstanceRequest{
		Project:          gc.projectID,
		Zone:             gc.zone,
		InstanceResource: instance,
	}

	// 发送请求
	op, err := instancesClient.Insert(ctx, req)
	if err != nil {
		logger.Error("创建 VM 失败", slog.String("pre", pre), slog.String("vmName", vmName),
			slog.String("zone", gc.zone), slog.Any("err", err))
		return err
	}

	logger.Info("VM 创建操作已启动", slog.String("pre", pre),
		slog.String("vmName", vmName), slog.String("operation", op.Proto().GetName()))
	logger.Info("检查操作状态",
		slog.String("pre", pre),
		slog.String("operation", op.Proto().GetName()),
		slog.String("zone", gc.zone),
		slog.String("project", gc.projectID),
		slog.String("cmd", fmt.Sprintf("gcloud compute operations describe %s --zone %s --project %s",
			op.Proto().GetName(), gc.zone, gc.projectID)),
	)

	return nil
}

// GetVMExternalIP 获取指定 VM 的公网 IP
func (gc *ScalingOperate) GetVMPublicIP(ctx context.Context, vmName string,
	pre string, logger *slog.Logger) (string, error) {
	// 创建客户端（使用凭证文件）
	client, err := compute.NewInstancesRESTClient(ctx, option.WithCredentialsFile(gc.credFile))
	if err != nil {
		return "", fmt.Errorf("创建 Instances 客户端失败: %w", err)
	}
	defer client.Close()

	// 构建请求
	req := &computepb.GetInstanceRequest{
		Project:  gc.projectID,
		Zone:     gc.zone,
		Instance: vmName,
	}

	// 查询 VM
	vm, err := client.Get(ctx, req)
	if err != nil {
		return "", fmt.Errorf("获取 VM 信息失败: %w", err)
	}

	if len(vm.NetworkInterfaces) == 0 || len(vm.NetworkInterfaces[0].AccessConfigs) == 0 {
		return "", fmt.Errorf("没有网络接口或公网配置")
	}

	natIP := vm.NetworkInterfaces[0].AccessConfigs[0].GetNatIP()
	logger.Info("获取 VM 公网 IP", slog.String("pre", pre), "vmName", vmName, "ip", natIP)
	return natIP, nil
}

// DeleteVM 删除指定的 VM
func (gc *ScalingOperate) DeleteVM(ctx context.Context, vmName string, pre string, logger *slog.Logger) error {

	// 创建客户端（使用凭证文件）
	instancesClient, err := compute.NewInstancesRESTClient(ctx, option.WithCredentialsFile(gc.credFile))
	if err != nil {
		logger.Error("创建 Instances 客户端失败", slog.String("pre", pre), "error", err)
		return err
	}
	defer instancesClient.Close()

	// 构建删除请求
	req := &computepb.DeleteInstanceRequest{
		Project:  gc.projectID,
		Zone:     gc.zone,
		Instance: vmName,
	}

	// 发送删除请求
	op, err := instancesClient.Delete(ctx, req)
	if err != nil {
		logger.Error("删除 VM 失败", slog.String("pre", pre), slog.String("vmName", vmName),
			slog.String("zone", gc.zone), slog.Any("err", err))
		return err
	}

	logger.Info("VM 删除操作已启动", slog.String("pre", pre),
		slog.String("vmName", vmName), slog.String("operation", op.Proto().GetName()))
	logger.Info("可通过命令检查状态",
		slog.String("pre", pre),
		slog.String("operation", op.Proto().GetName()),
		slog.String("zone", gc.zone),
		slog.String("project", gc.projectID),
		slog.String("cmd", fmt.Sprintf(
			"gcloud compute operations describe %s --zone %s --project %s",
			op.Proto().GetName(), gc.zone, gc.projectID)),
	)

	return nil
}
