package upload

import (
	"context"
	"fmt"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
	"io"
	"log/slog"
	"net/http"
	"os"
	"rigel-client/limit_rate"
	"rigel-client/split_compose"
	"rigel-client/util"
	"strings"
	"time"
)

type ChunkEventType int

const (
	ChunkExpired ChunkEventType = iota
	ChunkFinished
)

type ChunkEvent struct {
	Type    ChunkEventType
	Indexes []*split_compose.ChunkState
}

type ChunkTask struct {
	ctx        context.Context
	Index      string
	s          *util.SafeMap
	uploadInfo UploadFileInfo
	objectName string
}

type WorkerPool struct {
	taskCh chan ChunkTask
}

type PathInfo struct {
	Hops string `json:"hops"`
	Rate int64  `json:"rate"`
	//Weight int64  `json:"weight"`
}

type RoutingInfo struct {
	Routing []PathInfo `json:"routing"`
}

type UploadFileInfo struct {
	LocalFilePath string
	BucketName    string
	FileName      string
	CredFile      string
}

// 分片+限流+ack+compose 功能的upload
func UploadToGCSbyReDirectHttpsV2(uploadInfo UploadFileInfo, routingInfo RoutingInfo, logger *slog.Logger) error {

	// 定时器控制最大等待时间
	done := make(chan struct{})
	ctx := context.Background()
	localFilePath := uploadInfo.LocalFilePath
	fileName := uploadInfo.FileName

	//获取分片
	chunks := util.NewSafeMap()
	_ = split_compose.SplitFile(localFilePath, fileName, chunks)

	fmt.Println("分片数据", chunks.GetAll())

	//启动定时重传 & check传输完毕
	events := make(chan ChunkEvent, 100)
	interval := 10 * time.Duration(time.Second)
	expire := 120 * time.Duration(time.Second)
	StartChunkTimeoutChecker(ctx, chunks, interval, expire, events)

	//启动消费者 默认一个http并发度
	fmt.Println("NewWorkerPool")
	workerPool := NewWorkerPool(100, routingInfo, uploadChunk, logger)

	//events 消费
	fmt.Println("ChunkEventLoop")
	ChunkEventLoop(ctx, chunks, workerPool, uploadInfo, events, done, logger)

	// 4. 启动分片上传
	fmt.Println("StartChunkSubmitLoop")
	StartChunkSubmitLoop(ctx, chunks, workerPool, uploadInfo, logger)

	// 5分钟超时定时器
	timeout := 5 * time.Minute
	select {
	case <-done:
		logger.Info("FunctionA 正常完成", fileName)
	case <-time.After(timeout):
		logger.Warn("等待 5 分钟超时，退出等待", fileName)
		return fmt.Errorf("等待 5 分钟超时，退出等待, fileName: %s", fileName)
	}

	logger.Info("主程序执行完毕", fileName)
	return nil
}

func CollectExpiredChunks(
	s *util.SafeMap,
	expire time.Duration,
) (expired []*split_compose.ChunkState, finished, unfinished bool) {
	now := time.Now()
	finished = true // 先假设都 ack 了

	chunks_ := s.GetAll()

	for _, v := range chunks_ {
		v_, ok := v.(*split_compose.ChunkState)
		if !ok {
			continue
		}

		//还没发送完不能resubmit
		if v_.LastSend.IsZero() {
			return expired, false, true
		}

		if !v_.Acked {
			finished = false // 只要发现一个没 ack，就没完成

			if !v_.LastSend.IsZero() && now.Sub(v_.LastSend) > expire {
				expired = append(expired, v_)
			}
		}
	}

	return expired, finished, false
}

func StartChunkTimeoutChecker(
	ctx context.Context,
	s *util.SafeMap,
	interval time.Duration,
	expire time.Duration,
	events chan<- ChunkEvent,
) {
	ticker := time.NewTicker(interval)

	fmt.Println("定时器启动")

	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				expired, finished, unfinished := CollectExpiredChunks(s, expire)

				if !unfinished {
					if finished {
						events <- ChunkEvent{
							Type: ChunkFinished,
						}
						return
					}

					if len(expired) > 0 {
						events <- ChunkEvent{
							Type:    ChunkExpired,
							Indexes: expired,
						}
					}
				}

			case <-ctx.Done():
				return
			}
		}
	}()
}

func ChunkEventLoop(ctx context.Context, chunks *util.SafeMap, workerPool *WorkerPool,
	uploadInfo UploadFileInfo, events <-chan ChunkEvent, done chan struct{}, logger *slog.Logger) {

	fmt.Println("事件循环启动")

	for {
		select {
		case ev := <-events:
			switch ev.Type {
			case ChunkExpired:
				logger.Warn("超时重传", "indexes", ev.Indexes)
				StartChunkSubmitLoop(ctx, chunks, workerPool, uploadInfo, logger)
			case ChunkFinished:
				var parts = []string{}
				bucketName := uploadInfo.BucketName
				fileName := uploadInfo.FileName
				credFile := uploadInfo.CredFile

				logger.Info("传输完成", "fileName", fileName)
				chunks_ := chunks.GetAll()
				for _, v := range chunks_ {
					v_, ok := v.(*split_compose.ChunkState)
					if !ok {
						continue
					}
					if !v_.Acked {
						logger.Error("upload failed", "fileName", fileName, "index", v_.Index)
						return
					}
					parts = append(parts, v_.ObjectName)
				}
				_ = split_compose.ComposeTree(ctx, bucketName, fileName, credFile, parts)
				close(done)
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

func NewWorkerPool(
	queueSize int,
	routingInfo RoutingInfo,
	handler func(ChunkTask, string, *rate.Limiter, *slog.Logger) error,
	logger *slog.Logger,
) *WorkerPool {
	p := &WorkerPool{
		taskCh: make(chan ChunkTask, queueSize),
	}

	workerNum := len(routingInfo.Routing)

	for i := 0; i < workerNum; i++ {
		go func(workerID int, pathInfo PathInfo) {

			rate_ := pathInfo.Rate                 //maxMbps
			bytesPerSec := rate_ * 1024 * 1024 / 8 // Mbps → bytes/sec
			limiter := rate.NewLimiter(rate.Limit(bytesPerSec), int(bytesPerSec))

			logger.Info("Worker 启动", "worker", workerID, "rate", rate_, "hops", pathInfo.Hops)

			for task := range p.taskCh {

				err := handler(
					task,
					pathInfo.Hops,
					limiter,
					logger,
				)

				if err != nil {
					logger.Error("handle task", "worker", workerID, "err", err)
				} else {
					logger.Info("handle task", "worker", workerID, "task", task)
				}
			}
		}(i, routingInfo.Routing[i])
	}

	return p
}

func (p *WorkerPool) Submit(task ChunkTask) bool {
	select {
	case p.taskCh <- task:
		fmt.Println("submit task", task)
		return true
	default:
		// 队列满了，可以选择丢 / 打日志 / 统计
		return false
	}
}

func StartChunkSubmitLoop(
	ctx context.Context,
	chunks *util.SafeMap,
	workerPool *WorkerPool,
	uploadInfo UploadFileInfo,
	logger *slog.Logger,
) {
	logger.Info("开始分片上传", "fileName", uploadInfo.FileName)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			chunks_ := chunks.GetAll()

			for _, v := range chunks_ {
				v_, ok := v.(*split_compose.ChunkState)
				if !ok {
					continue
				}

				// 已 ack 的不用再发
				if v_.Acked {
					continue
				}

				task := ChunkTask{
					ctx:        ctx,
					Index:      v_.Index,
					s:          chunks,
					uploadInfo: uploadInfo,
					objectName: v_.ObjectName,
				}

				ok = workerPool.Submit(task)
				if !ok {
					// 队列满了，睡 10s 再来
					time.Sleep(10 * time.Second)
					continue
				}
			}
		}
	}()
}

func uploadChunk(task ChunkTask, hops string, rateLimiter *rate.Limiter, logger *slog.Logger) error {
	ctx := task.ctx

	logger.Info("开始上传分片", "fileName", task.uploadInfo.FileName, "index", task.Index, "hops", hops)

	// 1. 生成 access token（和 uploadChunkV2 保持一致）
	jsonBytes, err := os.ReadFile(task.uploadInfo.CredFile)
	if err != nil {
		return fmt.Errorf("read cred file: %w", err)
	}

	creds, err := google.CredentialsFromJSON(
		ctx,
		jsonBytes,
		"https://www.googleapis.com/auth/devstorage.full_control",
	)
	if err != nil {
		return fmt.Errorf("parse credentials: %w", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}

	// 2. 打开 chunk 文件（或整文件）
	file, err := os.Open(task.uploadInfo.LocalFilePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	chunk_, _ := task.s.Get(task.Index)
	chunk := chunk_.(*split_compose.ChunkState)

	// 3. 读取 chunk 内容
	section := io.NewSectionReader(file, chunk.Offset, chunk.Size)

	// 3. 限流 reader
	body := limit_rate.NewRateLimitedReader(ctx, section, rateLimiter)

	// 4. 解析 hops
	hopList := strings.Split(hops, ",")
	if len(hopList) == 0 {
		return fmt.Errorf("invalid X-Hops: %s", hops)
	}
	firstHop := hopList[0]

	// 5. 构造 URL
	url := fmt.Sprintf(
		"http://%s/%s/%s",
		firstHop,
		task.uploadInfo.BucketName,
		task.uploadInfo.FileName,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Hops", hops)
	req.Header.Set("X-Chunk-Index", "1")
	req.Header.Set("X-Rate-Limit-Enable", "true")

	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	task.s.Set(task.Index, &split_compose.ChunkState{
		Index:      chunk.Index,
		FileName:   chunk.FileName,
		ObjectName: chunk.ObjectName,
		Offset:     chunk.Offset,
		Size:       chunk.Size,
		LastSend:   time.Now(),
		Acked:      false,
	})
	logger.Info("开始上传分片", "fileName", task.uploadInfo.FileName, "index", task.Index, "hops", hops)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: %d %s", resp.StatusCode, string(b))
	}

	// 6. 成功后更新状态（重新 set，不 mutate）
	chunk_, _ = task.s.Get(task.Index)
	chunk = chunk_.(*split_compose.ChunkState)
	task.s.Set(task.Index, &split_compose.ChunkState{
		Index:      chunk.Index,
		FileName:   chunk.FileName,
		ObjectName: chunk.ObjectName,
		Offset:     chunk.Offset,
		Size:       chunk.Size,
		LastSend:   chunk.LastSend,
		Acked:      true,
	})
	logger.Info("上传分片成功", "fileName", task.uploadInfo.FileName, "index", task.Index, "hops", hops)

	return nil
}
