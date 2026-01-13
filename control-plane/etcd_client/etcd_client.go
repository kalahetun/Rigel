package etcd_client

import (
	"context"
	"log/slog"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// NewEtcdClient 创建一个 etcd client
func NewEtcdClient(endpoints []string, dialTimeout time.Duration) (*clientv3.Client, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return nil, err
	}
	return cli, nil
}

// PutKey 写入 key
func PutKey(cli *clientv3.Client, key, value string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := cli.Put(ctx, key, value)
	if err != nil {
		logger.Error("Put error:", err)
	} else {
		logger.Info("Put %s=%s\n", key, value)
	}
}

func DeleteKey(cli *clientv3.Client, key string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := cli.Delete(ctx, key)
	if err != nil {
		logger.Error("Delete error:", err)
		return
	}

	if resp.Deleted > 0 {
		logger.Info("Deleted key %s successfully\n", key)
	} else {
		logger.Warn("Key %s not found\n", key)
	}
}

// GetKey 获取 key
func GetKey(cli *clientv3.Client, key string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := cli.Get(ctx, key)
	if err != nil {
		logger.Error("Get error:", err)
		return
	}

	for _, kv := range resp.Kvs {
		logger.Info("Get %s = %s\n", string(kv.Key), string(kv.Value))
	}
}

// WatchPrefix 监听前缀 key 变化
func WatchPrefix(cli *clientv3.Client, prefix string, callback func(eventType, key, value string, logger *slog.Logger), logger *slog.Logger) {
	go func() {
		rch := cli.Watch(context.Background(), prefix, clientv3.WithPrefix(), clientv3.WithPrevKV())
		for wresp := range rch {
			for _, ev := range wresp.Events {
				callback(ev.Type.String(), string(ev.Kv.Key), string(ev.Kv.Value), logger)
			}
		}
	}()
}
