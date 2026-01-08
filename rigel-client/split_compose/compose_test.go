package split_compose

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

func TestComposeTree(t *testing.T) {
	ctx := context.Background()

	// 使用测试 GCS bucket
	bucketName := "test-bucket"
	objectName := "final-object"
	credFile := "path/to/your/credentials.json" // 本地服务账号 JSON

	// 模拟小文件块
	parts := []string{"part1", "part2", "part3", "part4"}

	// 初始化客户端
	client, err := storage.NewClient(ctx, option.WithCredentialsFile(credFile))
	if err != nil {
		t.Fatalf("failed to create GCS client: %v", err)
	}
	defer client.Close()

	bkt := client.Bucket(bucketName)

	// 先上传模拟的 part 文件
	for _, p := range parts {
		w := bkt.Object(p).NewWriter(ctx)
		if _, err := io.Copy(w, bytes.NewReader([]byte("test data "+p))); err != nil {
			t.Fatalf("failed to write part %s: %v", p, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("failed to close writer for part %s: %v", p, err)
		}
	}

	// 调用 ComposeTree
	if err := ComposeTree(ctx, bucketName, objectName, credFile, parts); err != nil {
		t.Fatalf("ComposeTree failed: %v", err)
	}

	// 检查最终对象是否存在
	_, err = bkt.Object(objectName).Attrs(ctx)
	if err != nil {
		t.Fatalf("final object not found: %v", err)
	}

	// 检查临时对象是否已经删除
	for level := 0; level < 10; level++ { // 假设最多10层
		for i := 0; i < len(parts); i += 32 {
			tmp := objectName + ".compose." + strconv.Itoa(level) + "." + strconv.Itoa(i)
			_, err := bkt.Object(tmp).Attrs(ctx)
			if err == nil {
				t.Errorf("temporary object %s should have been deleted", tmp)
			}
		}
	}

	t.Log("ComposeTree test passed")
}
