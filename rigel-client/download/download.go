package download

import (
	"cloud.google.com/go/storage"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// DownloadFromGCSbyClient 从 GCS bucket 下载文件到本地
func DownloadFromGCSbyClient(ctx context.Context, localFilePath, bucketName, objectName, credFile string,
	pre string, logger *slog.Logger) error {

	logger.Info("Downloading file from GCS bucket using client library", slog.String("pre", pre),
		slog.String("objectName", objectName), slog.String("localFilePath", localFilePath))

	// 设置凭证
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	// 创建客户端
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create storage client: %w", err)
	}
	defer client.Close()

	// 获取 bucket
	bucket := client.Bucket(bucketName)

	// 创建 reader
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	rc, err := bucket.Object(objectName).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("failed to create object reader: %w", err)
	}
	defer rc.Close()

	// 创建本地文件
	f, err := os.Create(localFilePath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer f.Close()

	// 写入本地文件
	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("failed to copy object to local file: %w", err)
	}

	logger.Info("Download success client library", slog.String("pre", pre),
		slog.String("objectName", objectName), slog.String("localFilePath", localFilePath))

	return nil
}
