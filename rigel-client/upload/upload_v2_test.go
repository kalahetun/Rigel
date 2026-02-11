package upload

import (
	"context"
	"golang.org/x/time/rate"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"log/slog"
	"rigel-client/split_compose"
	"rigel-client/util"
)

// =======================
// mock uploadChunk，不发 HTTP
// =======================
func mockUploadChunk(task ChunkTask, hops string, logger *slog.Logger) error {
	chunk_, _ := task.s.Get(task.Index)
	chunk := chunk_.(*split_compose.ChunkState)

	// 模拟上传耗时
	time.Sleep(5 * time.Millisecond)

	// 上传成功，Acked = 2
	task.s.Set(task.Index, &split_compose.ChunkState{
		Index:      chunk.Index,
		FileName:   chunk.FileName,
		ObjectName: chunk.ObjectName,
		Offset:     chunk.Offset,
		Size:       chunk.Size,
		LastSend:   time.Now(),
		Acked:      2,
	})
	return nil
}

// =======================
// 测试 SplitFile
// =======================
func TestSplitFile(t *testing.T) {
	tmpFile, err := ioutil.TempFile("", "splitfile_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	data := make([]byte, 5*1024*1024) // 5MB
	tmpFile.Write(data)
	tmpFile.Close()

	chunks := util.NewSafeMap()
	err = split_compose.SplitFile(tmpFile.Name(), "testfile", chunks, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks.GetAll()) == 0 {
		t.Errorf("expected at least 1 chunk")
	}
}

// =======================
// 测试 CollectExpiredChunks
// =======================
func TestCollectExpiredChunks(t *testing.T) {
	chunks := util.NewSafeMap()
	now := time.Now()
	chunks.Set("1", &split_compose.ChunkState{Index: "1", Acked: 1, LastSend: now.Add(-time.Minute)})
	chunks.Set("2", &split_compose.ChunkState{Index: "2", Acked: 2, LastSend: now})

	expired, finished, unfinished := CollectExpiredChunks(chunks, 10*time.Second, "", nil)
	if len(expired) != 1 {
		t.Errorf("expected 1 expired chunk, got %d", len(expired))
	}
	if finished {
		t.Errorf("finished should be false")
	}
	if unfinished {
		t.Errorf("unfinished should be false because all chunks have Acked>=1")
	}
}

// =======================
// 测试 StartChunkTimeoutChecker + 事件循环
// =======================
func TestChunkTimeoutAndEventLoop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	tmpFile, err := ioutil.TempFile("", "upload_testfile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	data := make([]byte, 1*1024*1024)
	tmpFile.Write(data)
	tmpFile.Close()

	uploadInfo := UploadFileInfo{
		LocalFilePath: tmpFile.Name(),
		BucketName:    "mock-bucket",
		FileName:      "testfile.txt",
		CredFile:      "mock-cred.json",
	}

	// 拆分文件
	chunks := util.NewSafeMap()
	err = split_compose.SplitFile(uploadInfo.LocalFilePath, uploadInfo.FileName, chunks, "", logger)
	if err != nil {
		t.Fatal(err)
	}

	routingInfo := RoutingInfo{
		Routing: []PathInfo{{Hops: "127.0.0.1:8080", Rate: 10}},
	}

	// WorkerPool
	workerPool := NewWorkerPool(10, routingInfo, func(task ChunkTask, hops string, _ *rate.Limiter, _ *slog.Logger) error {
		return mockUploadChunk(task, hops, logger)
	}, "", logger)

	events := make(chan ChunkEvent, 10)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 超时检查器
	StartChunkTimeoutChecker(ctx, chunks, 10*time.Millisecond, 50*time.Millisecond, events, "", logger)

	// 事件循环
	go ChunkEventLoop(ctx, chunks, workerPool, uploadInfo, events, done, "", logger)

	// 开始分片提交
	go StartChunkSubmitLoop(ctx, chunks, workerPool, uploadInfo, false, nil, "", logger)

	// 等待完成
	select {
	case <-done:
		for _, v := range chunks.GetAll() {
			c := v.(*split_compose.ChunkState)
			if c.Acked != 2 {
				t.Errorf("chunk %s not fully uploaded, Acked=%d", c.Index, c.Acked)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for upload done")
	}
}
