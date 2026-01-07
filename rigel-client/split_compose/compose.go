package split_compose

import (
	"cloud.google.com/go/storage"
	"context"
	"fmt"
	"os"
)

// ===== GCS Compose（树形安全版） ====
func finalizeObject(ctx context.Context, bkt *storage.BucketHandle, tempName, finalName string) error {
	// copy temp → final
	_, err := bkt.Object(finalName).
		CopierFrom(bkt.Object(tempName)).
		Run(ctx)
	if err != nil {
		return err
	}

	// delete temp
	return bkt.Object(tempName).Delete(ctx)
}

func ComposeTree(
	ctx context.Context,
	bucket, objectName, credFile string,
	parts []string,
) error {

	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)
	client, err := storage.NewClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	bkt := client.Bucket(bucket)

	current := parts
	level := 0

	var tempObjects []string // 保存所有临时对象

	for len(current) > 1 {
		var next []string

		for i := 0; i < len(current); i += 32 {
			end := i + 32
			if end > len(current) {
				end = len(current)
			}

			group := current[i:end]
			tmp := fmt.Sprintf("%s.compose.%d.%d", objectName, level, i)

			var objs []*storage.ObjectHandle
			for _, p := range group {
				objs = append(objs, bkt.Object(p))
			}

			if _, err := bkt.Object(tmp).ComposerFrom(objs...).Run(ctx); err != nil {
				return err
			}

			next = append(next, tmp)
			tempObjects = append(tempObjects, tmp) // 记录临时对象
		}

		current = next
		level++
	}

	// 最终复制 temp → final
	if err := finalizeObject(ctx, bkt, current[0], objectName); err != nil {
		return err
	}

	// 删除所有中间临时对象（不包括 current[0]，已经在 finalizeObject 删除）
	for _, tmp := range tempObjects {
		if tmp != current[0] {
			_ = bkt.Object(tmp).Delete(ctx)
		}
	}

	return nil
}
