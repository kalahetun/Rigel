package etcd_client

import (
	"fmt"
	"log"
	"log/slog"
	"testing"
	"time"
)

func TestEtcdClient(t *testing.T) {

	logger := slog.New(slog.NewTextHandler(log.Writer(), nil))

	// 假设嵌入式 etcd server 已经在 127.0.0.1:2379 启动
	cli, err := NewEtcdClient([]string{"http://127.0.0.1:2379"}, 5*time.Second)
	if err != nil {
		t.Fatal("Failed to connect to etcd:", err)
	}
	defer cli.Close()

	// 监听 /test/ 前缀
	WatchPrefix(cli, "/test/", func(eventType, key, value string, logger *slog.Logger) {
		log.Printf("[WATCH] %s %s = %s", eventType, key, value)
	}, logger)

	// 写入一些 key
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("/test/key%d", i)
		val := fmt.Sprintf("val-%d", i)
		PutKey(cli, key, val, "", logger)
		time.Sleep(5000 * time.Millisecond) // 给 watch 一点时间捕获事件
	}

	// 读取 key
	GetKey(cli, "/test/key1", logger)

	// 等待 watch 输出
	log.Println("Waiting 2 seconds to capture watch events...")
	time.Sleep(2 * time.Second)
}
