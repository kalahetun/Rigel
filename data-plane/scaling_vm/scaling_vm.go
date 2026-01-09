package scaling_vm

import (
	"context"
	"fmt"
	"log/slog"

	compute "cloud.google.com/go/compute/apiv1"
	"google.golang.org/api/option"
	computepb "google.golang.org/genproto/googleapis/cloud/compute/v1"
	"google.golang.org/protobuf/proto"
)

// CreateVM 创建GCP Compute Engine虚拟机实例
// credFile: JSON格式的服务账号凭证文件
// zone: 例如 "us-central1-a"
// vmName: VM名字
func CreateVM(
	ctx context.Context,
	logger *slog.Logger,
	projectID string,
	zone string,
	vmName string,
	credFile string,
) error {
	// 1️⃣ 创建客户端（直接使用凭证文件）
	instancesClient, err := compute.NewInstancesRESTClient(ctx, option.WithCredentialsFile(credFile))
	if err != nil {
		logger.Error("创建 Instances 客户端失败", "error", err)
		return err
	}
	defer instancesClient.Close()

	// 2️⃣ SSH 公钥
	sshKey := "matth:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICrnRGaFqSdQWQ7H0Ia0po0nDG88pMj8pa7wXkQLXSmQ matth@arcturus"

	// 3️⃣ 启动盘配置
	bootDisk := &computepb.AttachedDisk{
		AutoDelete: proto.Bool(true),
		Boot:       proto.Bool(true),
		Type:       proto.String(computepb.AttachedDisk_PERSISTENT.String()),
		InitializeParams: &computepb.AttachedDiskInitializeParams{
			SourceImage: proto.String("projects/debian-cloud/global/images/family/debian-12"),
			DiskSizeGb:  proto.Int64(10),
		},
	}

	// 4️⃣ 网络接口配置（默认网络，带公网IP）
	networkInterface := &computepb.NetworkInterface{
		Network: proto.String("global/networks/default"),
		AccessConfigs: []*computepb.AccessConfig{
			{
				Name: proto.String("External NAT"),
				Type: proto.String("ONE_TO_ONE_NAT"), // 让系统自动分配公网IP
			},
		},
	}

	// 5️⃣ 构建 VM 实例对象
	instance := &computepb.Instance{
		Name:        proto.String(vmName),
		MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/e2-medium", zone)),
		Disks:       []*computepb.AttachedDisk{bootDisk},
		NetworkInterfaces: []*computepb.NetworkInterface{
			networkInterface,
		},
		Tags: &computepb.Tags{
			Items: []string{"http-server", "https-server", "lb-health-check", "default-allow-internal"}, // 防火墙标签
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

	// 6️⃣ 创建请求
	req := &computepb.InsertInstanceRequest{
		Project:          projectID,
		Zone:             zone,
		InstanceResource: instance,
	}

	// 7️⃣ 发送请求
	op, err := instancesClient.Insert(ctx, req)
	if err != nil {
		logger.Error("创建 VM 失败", "vmName", vmName, "zone", zone, "error", err)
		return err
	}

	logger.Info("VM 创建操作已启动", "vmName", vmName, "operation", op.Proto().GetName())
	logger.Info(fmt.Sprintf("可通过命令检查状态: gcloud compute operations describe %s --zone %s --project %s",
		op.Proto().GetName(), zone, projectID))
	return nil
}

// GetVMExternalIP 获取指定 VM 的公网 IP
func GetVMExternalIP(ctx context.Context, logger *slog.Logger, projectID, zone, vmName, credFile string) (string, error) {
	// 创建客户端（使用凭证文件）
	client, err := compute.NewInstancesRESTClient(ctx, option.WithCredentialsFile(credFile))
	if err != nil {
		return "", fmt.Errorf("创建 Instances 客户端失败: %w", err)
	}
	defer client.Close()

	// 构建请求
	req := &computepb.GetInstanceRequest{
		Project:  projectID,
		Zone:     zone,
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
	logger.Info("获取 VM 公网 IP", "vmName", vmName, "ip", natIP)
	return natIP, nil
}

// DeleteVM 删除指定的 VM
func DeleteVM(ctx context.Context, logger *slog.Logger, projectID, zone, vmName, credFile string) error {
	// 创建客户端（使用凭证文件）
	instancesClient, err := compute.NewInstancesRESTClient(ctx, option.WithCredentialsFile(credFile))
	if err != nil {
		logger.Error("创建 Instances 客户端失败", "error", err)
		return err
	}
	defer instancesClient.Close()

	// 构建删除请求
	req := &computepb.DeleteInstanceRequest{
		Project:  projectID,
		Zone:     zone,
		Instance: vmName,
	}

	// 发送删除请求
	op, err := instancesClient.Delete(ctx, req)
	if err != nil {
		logger.Error("删除 VM 失败", "vmName", vmName, "zone", zone, "error", err)
		return err
	}

	logger.Info("VM 删除操作已启动", "vmName", vmName, "operation", op.Proto().GetName())
	logger.Info(fmt.Sprintf("可通过命令检查状态: gcloud compute operations describe %s --zone %s --project %s",
		op.Proto().GetName(), zone, projectID))
	return nil
}
