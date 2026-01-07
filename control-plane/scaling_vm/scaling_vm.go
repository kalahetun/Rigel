package scaling_vm

import (
	"context"
	"fmt"
	"log/slog"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "google.golang.org/genproto/googleapis/cloud/compute/v1"
	"google.golang.org/protobuf/proto"
)

const (
	sshKey = "matth:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICrnRGaFqSdQWQ7H0Ia0po0nDG88pMj8pa7wXkQLXSmQ matth@arcturus"
)

// CreateVM 在 GCP 创建达拉斯机房的 VM，默认 2C4G，10GB Debian 12，自动打防火墙标签
func CreateVM(
	ctx context.Context,
	logger *slog.Logger,
	projectID string,
	vmName string,
	sshKey string, // 格式 username:ssh-ed25519 AAAA...
) error {
	zone := "us-central1-a"    // 达拉斯区域a区
	machineType := "e2-medium" // 2C4G

	// 创建 Compute Engine 客户端
	instancesClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		logger.Error("创建 Instances 客户端失败", "error", err)
		return err
	}
	defer instancesClient.Close()

	// 启动盘配置
	bootDisk := &computepb.AttachedDisk{
		AutoDelete: proto.Bool(true),
		Boot:       proto.Bool(true),
		Type:       proto.String(computepb.AttachedDisk_PERSISTENT.String()),
		InitializeParams: &computepb.AttachedDiskInitializeParams{
			SourceImage: proto.String("projects/debian-cloud/global/images/family/debian-12"),
			DiskSizeGb:  proto.Int64(10),
		},
	}

	// 网络接口配置（默认网络，带公网IP）
	networkInterface := &computepb.NetworkInterface{
		Network: proto.String("global/networks/default"),
		AccessConfigs: []*computepb.AccessConfig{
			{
				Name: proto.String("External NAT"),
				Type: proto.String("ONE_TO_ONE_NAT"),
			},
		},
	}

	// 构建 VM 实例对象
	instance := &computepb.Instance{
		Name:        proto.String(vmName),
		MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", zone, machineType)),
		Disks:       []*computepb.AttachedDisk{bootDisk},
		NetworkInterfaces: []*computepb.NetworkInterface{
			networkInterface,
		},
		Tags: &computepb.Tags{
			Items: []string{"http-server", "https-server", "lb-health-check"},
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
		Project:          projectID,
		Zone:             zone,
		InstanceResource: instance,
	}

	// 发送请求
	op, err := instancesClient.Insert(ctx, req)
	if err != nil {
		logger.Error("创建 VM 失败", "vmName", vmName, "zone", zone, "error", err)
		return err
	}

	logger.Info("VM 创建操作已启动", "vmName", vmName, "operation", op.Proto().GetName())
	logger.Info(fmt.Sprintf(
		"可通过命令检查状态: gcloud compute operations describe %s --zone %s --project %s",
		op.Proto().GetName(), zone, projectID,
	))

	return nil
}

func GetVMExternalIP(ctx context.Context, logger *slog.Logger, projectID, zone, vmName string) (string, error) {
	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()

	req := &computepb.GetInstanceRequest{
		Project:  projectID,
		Zone:     zone,
		Instance: vmName,
	}

	vm, err := client.Get(ctx, req)
	if err != nil {
		return "", err
	}

	if len(vm.NetworkInterfaces) == 0 || len(vm.NetworkInterfaces[0].AccessConfigs) == 0 {
		return "", fmt.Errorf("no network interfaces or access configs found")
	}

	ip := vm.NetworkInterfaces[0].AccessConfigs[0].NatIP
	logger.Info("获取 VM 公网 IP", "vmName", vmName, "ip", ip)
	return *ip, nil
}

// DeleteVM 删除指定的 GCP Compute Engine 虚拟机
func DeleteVM(ctx context.Context, logger *slog.Logger, projectID, zone, vmName string) error {
	// 1. 创建 Compute Engine 客户端
	instancesClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		logger.Error("创建 Instances 客户端失败", "error", err)
		return err
	}
	defer instancesClient.Close()

	// 2. 构建删除请求
	req := &computepb.DeleteInstanceRequest{
		Project:  projectID,
		Zone:     zone,
		Instance: vmName,
	}

	// 3. 发送删除请求
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
