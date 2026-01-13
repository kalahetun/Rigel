package etcd_server

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestStartEmbeddedEtcd(t *testing.T) {
	serverIP := "127.0.0.1" // 测试用本地 IP
	dataDir := "test.etcd"

	logger := slog.New(slog.NewTextHandler(log.Writer(), nil))

	// 测试集群列表，这里单机模拟多个节点可以用不同端口
	serverList := []string{"127.0.0.1"} // 如果本机模拟多个节点，可以加多个 IP 或用 127.0.0.1 不同端口

	// 节点名用 IP 做后缀，保证唯一
	nodeName := "etcd-" + strings.ReplaceAll(serverIP, ".", "-")

	// 清理残留数据
	os.RemoveAll(dataDir)

	// 启动嵌入式 etcd
	etcdServer, err := StartEmbeddedEtcd(serverList, serverIP, dataDir, nodeName, logger)
	if err != nil {
		t.Fatalf("Failed to start embedded etcd: %v", err)
	}
	defer etcdServer.Close()

	// 等一小会儿让 etcd 完全启动
	time.Sleep(2 * time.Second)

	// 尝试用 client 连接
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"http://" + serverIP + ":2379"},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create etcd client: %v", err)
	}
	defer cli.Close()

	// 测试写入 / 读取
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := "/test/key1"
	val := "hello"
	_, err = cli.Put(ctx, key, val)
	if err != nil {
		t.Fatalf("Failed to put key: %v", err)
	}

	resp, err := cli.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get key: %v", err)
	}

	if len(resp.Kvs) == 0 || string(resp.Kvs[0].Value) != val {
		t.Fatalf("Unexpected value: got %v, want %v", resp.Kvs, val)
	}

	log.Println("Embedded etcd test passed!")
}
