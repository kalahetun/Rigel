package scaling_vm

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"log/slog"
)

// =====================
// Create VM
// =====================

// CreateAWSVM 创建 AWS EC2 实例
//
// region:     e.g. "us-east-1"
// amiID:      e.g. "ami-0fc5d935ebf8bc3bc" (Debian 12 / Ubuntu 22.04 等)
// instanceType: e.g. "t3.medium"
// keyName:    EC2 KeyPair 名称
// securityGroupIDs: 安全组
// subnetID:   子网（决定 VPC / 公网 IP）
func CreateAWSVM(
	ctx context.Context,
	logger *slog.Logger,
	region string,
	amiID string,
	instanceType string,
	vmName string,
	keyName string,
	securityGroupIDs []string,
	subnetID string,
) (string, error) {

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
	)
	if err != nil {
		return "", err
	}

	ec2Client := ec2.NewFromConfig(cfg)

	input := &ec2.RunInstancesInput{
		ImageId:          aws.String(amiID),
		InstanceType:     types.InstanceType(instanceType),
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		KeyName:          aws.String(keyName),
		SecurityGroupIds: securityGroupIDs,
		SubnetId:         aws.String(subnetID),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInstance,
				Tags: []types.Tag{
					{Key: aws.String("Name"), Value: aws.String(vmName)},
				},
			},
		},
	}

	resp, err := ec2Client.RunInstances(ctx, input)
	if err != nil {
		logger.Error("创建 AWS EC2 失败", "error", err)
		return "", err
	}

	instanceID := aws.ToString(resp.Instances[0].InstanceId)

	logger.Info("AWS EC2 创建成功",
		"vmName", vmName,
		"instanceID", instanceID,
	)

	return instanceID, nil
}

// =====================
// Get External IP
// =====================

// GetAWSEC2ExternalIP 获取 EC2 公网 IP
func GetAWSEC2ExternalIP(
	ctx context.Context,
	logger *slog.Logger,
	region string,
	instanceID string,
) (string, error) {

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
	)
	if err != nil {
		return "", err
	}

	ec2Client := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}

	resp, err := ec2Client.DescribeInstances(ctx, input)
	if err != nil {
		return "", err
	}

	if len(resp.Reservations) == 0 ||
		len(resp.Reservations[0].Instances) == 0 {
		return "", fmt.Errorf("实例不存在")
	}

	inst := resp.Reservations[0].Instances[0]
	publicIP := aws.ToString(inst.PublicIpAddress)

	logger.Info("获取 AWS EC2 公网 IP",
		"instanceID", instanceID,
		"ip", publicIP,
		"state", inst.State.Name,
	)

	if publicIP == "" {
		return "", fmt.Errorf("实例尚未分配公网 IP")
	}

	return publicIP, nil
}

// =====================
// Delete VM
// =====================

// DeleteAWSVM 删除 AWS EC2 实例
func DeleteAWSVM(
	ctx context.Context,
	logger *slog.Logger,
	region string,
	instanceID string,
) error {

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
	)
	if err != nil {
		return err
	}

	ec2Client := ec2.NewFromConfig(cfg)

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}

	_, err = ec2Client.TerminateInstances(ctx, input)
	if err != nil {
		logger.Error("删除 AWS EC2 失败", "instanceID", instanceID, "error", err)
		return err
	}

	logger.Info("AWS EC2 删除操作已启动", "instanceID", instanceID)

	return nil
}
