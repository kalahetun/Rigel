package base

import "log/slog"

type AWSConfig struct {
	BucketName string // S3 桶名
	Region     string // AWS 区域
	AccessKey  string // Access Key
	SecretKey  string // Secret Key
}

// ExtractAWSFromInterface 从接口中提取 AWS 配置（仿照 ExtractGCPFromInterface 实现）
func ExtractAWSFromInterface(iface interface{}, pre string, logger *slog.Logger) *AWSConfig {
	if iface == nil {
		logger.Error("AWS interface is nil", slog.String("pre", pre))
		return nil
	}

	// 类型断言转换为 AWSConfig（需根据实际数据结构调整）
	awsCfg, ok := iface.(*AWSConfig)
	if !ok {
		logger.Error("Convert interface to AWSConfig failed", slog.String("pre", pre))
		return nil
	}

	// 校验必填字段
	if awsCfg.BucketName == "" || awsCfg.Region == "" || awsCfg.AccessKey == "" || awsCfg.SecretKey == "" {
		logger.Error("AWS config missing required fields", slog.String("pre", pre),
			slog.String("bucket", awsCfg.BucketName),
			slog.String("region", awsCfg.Region))
		return nil
	}

	return awsCfg
}
