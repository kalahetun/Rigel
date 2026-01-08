package upload

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"log/slog"
	"rigel-client/limit_rate"
	"rigel-client/split_compose"
	"rigel-client/util"

	"golang.org/x/time/rate"
)

// =======================
// mock uploadChunk，不发 HTTP
// =======================
func mockUploadChunk(task ChunkTask, hops string, limiter *rate.Limiter, logger *slog.Logger) error {
	chunk_, _ := task.s.Get(task.Index)
	chunk := chunk_.(*split_compose.ChunkState)

	// 模拟上传
	time.Sleep(5 * time.Millisecond)

	task.s.Set(task.Index, &split_compose.ChunkState{
		Index:      chunk.Index,
		FileName:   chunk.FileName,
		ObjectName: chunk.ObjectName,
		Offset:     chunk.Offset,
		Size:       chunk.Size,
		LastSend:   time.Now(),
		Acked:      true,
	})
	return nil
}

// =======================
// Test SplitFile
// =======================
func TestSplitFile(t *testing.T) {
	tmpFile, err := ioutil.TempFile("", "splitfile_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	data := make([]byte, 10*1024*1024)
	tmpFile.Write(data)
	tmpFile.Close()

	chunks := util.NewSafeMap()
	err = split_compose.SplitFile(tmpFile.Name(), "testfile", chunks)
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks.GetAll()) == 0 {
		t.Errorf("expected at least 1 chunk")
	}
}

// =======================
// Test RateLimitedReader
// =======================
func TestRateLimitedReader(t *testing.T) {
	data := make([]byte, 1024*1024)
	r := bytes.NewReader(data)
	ctx := context.Background()
	limiter := rate.NewLimiter(rate.Limit(1024*1024/8), 1024*1024/8) // 1MB/s

	rr := limit_rate.NewRateLimitedReader(ctx, r, limiter)

	start := time.Now()
	readBytes, err := io.Copy(ioutil.Discard, rr)
	if err != nil {
		t.Fatal(err)
	}
	duration := time.Since(start)
	if readBytes != int64(len(data)) {
		t.Errorf("readBytes mismatch")
	}
	if duration < time.Second {
		t.Errorf("rate limit not enforced")
	}
}

// =======================
// Test CollectExpiredChunks
// =======================
func TestCollectExpiredChunks(t *testing.T) {
	chunks := util.NewSafeMap()
	now := time.Now()
	chunks.Set("1", &split_compose.ChunkState{Index: "1", Acked: false, LastSend: now.Add(-time.Minute)})
	chunks.Set("2", &split_compose.ChunkState{Index: "2", Acked: true})

	expired, finished, _ := CollectExpiredChunks(chunks, 10*time.Second)
	if len(expired) != 1 {
		t.Errorf("expected 1 expired chunk, got %d", len(expired))
	}
	if finished {
		t.Errorf("finished should be false")
	}
}

// =======================
// Test StartChunkTimeoutChecker
// =======================
func TestStartChunkTimeoutChecker(t *testing.T) {
	chunks := util.NewSafeMap()
	now := time.Now()
	chunks.Set("1", &split_compose.ChunkState{Index: "1", Acked: false, LastSend: now.Add(-time.Second)})

	events := make(chan ChunkEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartChunkTimeoutChecker(ctx, chunks, 1*time.Millisecond, 500*time.Millisecond, events)

	ev := <-events
	if ev.Type != ChunkExpired {
		t.Errorf("expected ChunkExpired, got %v", ev.Type)
	}
}

// =======================
// Test UploadToGCSbyReDirectHttpsV2 (mocked)
// =======================
func TestUploadToGCSbyReDirectHttpsV2_Mock(t *testing.T) {
	// logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// 临时文件
	tmpFile, err := ioutil.TempFile("", "upload_testfile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	data := make([]byte, 1*1024*1024) // 1MB
	for i := range data {
		data[i] = byte(i % 256)
	}
	tmpFile.Write(data)
	tmpFile.Close()

	uploadInfo := UploadFileInfo{
		LocalFilePath: tmpFile.Name(),
		BucketName:    "mock-bucket",
		FileName:      "testfile.txt",
		CredFile:      "mock-cred.json",
	}

	routingInfo := RoutingInfo{
		Routing: []PathInfo{
			{Hops: "127.0.0.1:8080", Rate: 10},
		},
	}

	// 拆分文件
	chunks := util.NewSafeMap()
	err = split_compose.SplitFile(uploadInfo.LocalFilePath, uploadInfo.FileName, chunks)
	if err != nil {
		t.Fatal(err)
	}

	// WorkerPool
	workerPool := NewWorkerPool(100, routingInfo, mockUploadChunk, logger)

	done := make(chan struct{})
	events := make(chan ChunkEvent, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 超时检查
	StartChunkTimeoutChecker(ctx, chunks, 1*time.Millisecond, 5*time.Millisecond, events)

	// 事件循环
	go ChunkEventLoop(ctx, chunks, workerPool, uploadInfo, events, done, logger)

	// 分片提交循环
	StartChunkSubmitLoop(ctx, chunks, workerPool, uploadInfo, logger)

	// 等待完成
	select {
	case <-done:
		chunks_ := chunks.GetAll()
		for _, v := range chunks_ {
			chunk := v.(*split_compose.ChunkState)
			if !chunk.Acked {
				t.Errorf("chunk %s not acked", chunk.Index)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for upload done")
	}
}
