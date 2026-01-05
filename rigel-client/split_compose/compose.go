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
	client *storage.Client,
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
		}

		current = next
		level++
	}

	return finalizeObject(ctx, bkt, current[0], objectName)
}
