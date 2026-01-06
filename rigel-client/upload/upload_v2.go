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
	"strconv"
	"strings"
	"time"
)

const (
	maxInflight = 3  // 最大并发
	maxMbps     = 40 // 总带宽上限 Mbps
)

type ChunkEventType int

const (
	ChunkExpired ChunkEventType = iota
	ChunkFinished
)

type ChunkEvent struct {
	Type    ChunkEventType
	Indexes []string
}

// 这个函数主要是 分片+限流+ack+compose 功能的upload
func UploadToGCSbyReDirectHttpsV2(localFilePath, bucketName, fileName, credFile, hops string,
	reqHeaders http.Header, logger *slog.Logger) error {

	ctx := context.Background()

	//获取分片
	chunks := util.NewSafeMap()
	_ = split_compose.SplitFile(localFilePath, fileName, chunks)

	//启动定时重传 & check传输完毕
	events := make(chan ChunkEvent, 100)
	StartChunkTimeoutChecker(ctx, chunks, 10*time.Duration(time.Second), 120*time.Duration(time.Second), events)

	// 定时器控制最大等待时间
	done := make(chan struct{})

	//events 消费
	ChunkEventLoop(ctx, chunks, bucketName, fileName, credFile, events, done, logger)

	//限流相关的逻辑
	rateStr := reqHeaders.Get("X-Rate")
	rate_ := maxMbps
	if len(rateStr) > 0 {
		rate_, _ = strconv.Atoi(rateStr)
	}
	//默认限流40Mbps
	rate_ = maxMbps
	bytesPerSec := rate_ * 1024 * 1024 / 8 // Mbps → bytes/sec
	limiter := rate.NewLimiter(rate.Limit(bytesPerSec), bytesPerSec)

	//启动消费者 默认一个http并发度
	workerPool := NewWorkerPool(1, 100, uploadChunk, logger)

	StartChunkSubmitLoop(ctx, chunks, workerPool, localFilePath, bucketName, fileName, hops, credFile, limiter)

	// 5分钟超时定时器
	timeout := 5 * time.Minute
	select {
	case <-done:
		logger.Info("FunctionA 正常完成")
	case <-time.After(timeout):
		logger.Warn("等待 5 分钟超时，退出等待")
		return fmt.Errorf("等待 5 分钟超时，退出等待, fileName: %s", fileName)
	}

	logger.Info("主程序执行完毕")
	return nil
}

func CollectExpiredChunks(
	s *util.SafeMap,
	expire time.Duration,
) (expired []string, finished bool) {
	now := time.Now()
	finished = true // 先假设都 ack 了

	chunks_ := s.GetAll()

	for _, v := range chunks_ {
		v_, ok := v.(*split_compose.ChunkState)
		if !ok {
			continue
		}

		if !v_.Acked {
			finished = false // 只要发现一个没 ack，就没完成

			if !v_.LastSend.IsZero() && now.Sub(v_.LastSend) > expire {
				expired = append(expired, v_.Index)
			}
		}
	}

	return expired, finished
}

func StartChunkTimeoutChecker(
	ctx context.Context,
	s *util.SafeMap,
	interval time.Duration,
	expire time.Duration,
	events chan<- ChunkEvent,
) {
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				expired, finished := CollectExpiredChunks(s, expire)

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

			case <-ctx.Done():
				return
			}
		}
	}()
}

func ChunkEventLoop(ctx context.Context, s *util.SafeMap, bucketName, fileName, credFile string,
	events <-chan ChunkEvent, done chan struct{}, logger *slog.Logger) {
	for {
		select {
		case ev := <-events:
			switch ev.Type {
			case ChunkExpired:

				//handleChunkRetry(ev.Indexes)

			case ChunkFinished:
				var parts = []string{}
				chunks_ := s.GetAll()
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
				split_compose.ComposeTree(ctx, bucketName, fileName, credFile, parts)
				close(done)
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

type ChunkTask struct {
	ctx           context.Context
	Index         string
	s             *util.SafeMap
	localFilePath string
	bucketName    string
	fileName      string
	hops          string
	credFile      string
	limiter       *rate.Limiter //限流
}

type WorkerPool struct {
	taskCh chan ChunkTask
}

func NewWorkerPool(
	workerNum int,
	queueSize int,
	handler func(ChunkTask) error,
	logger *slog.Logger,
) *WorkerPool {
	p := &WorkerPool{
		taskCh: make(chan ChunkTask, queueSize),
	}

	for i := 0; i < workerNum; i++ {
		go func(workerID int) {
			for task := range p.taskCh {
				err := handler(task)
				if err != nil {
					logger.Error("handle task", "worker", workerID, "err", err)
				} else {
					logger.Info("handle task", "worker", workerID, "task", task)
				}
			}
		}(i)
	}

	return p
}

func (p *WorkerPool) Submit(task ChunkTask) bool {
	select {
	case p.taskCh <- task:
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
	localFilePath string,
	bucketName string,
	fileName string,
	hops string,
	credFile string,
	limiter *rate.Limiter,
) {
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
					ctx:           ctx,
					Index:         v_.Index,
					s:             chunks,
					localFilePath: localFilePath,
					bucketName:    bucketName,
					fileName:      fileName,
					hops:          hops,
					credFile:      credFile,
					limiter:       limiter,
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

func uploadChunk(task ChunkTask) error {
	ctx := task.ctx

	// 1. 生成 access token（和 uploadChunkV2 保持一致）
	jsonBytes, err := os.ReadFile(task.credFile)
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
	file, err := os.Open(task.localFilePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	chunk_, _ := task.s.Get(task.Index)
	chunk := chunk_.(*split_compose.ChunkState)

	// 3. 读取 chunk 内容
	section := io.NewSectionReader(file, chunk.Offset, chunk.Size)

	// 3. 限流 reader
	body := limit_rate.NewRateLimitedReader(ctx, section, task.limiter)

	// 4. 解析 hops
	hopList := strings.Split(task.hops, ",")
	if len(hopList) == 0 {
		return fmt.Errorf("invalid X-Hops: %s", task.hops)
	}
	firstHop := hopList[0]

	// 5. 构造 URL
	url := fmt.Sprintf(
		"http://%s/%s/%s",
		firstHop,
		task.bucketName,
		task.fileName,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Hops", task.hops)
	req.Header.Set("X-Chunk-Index", "1")
	req.Header.Set("X-Rate-Limit-Enable", "true")

	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	task.s.Set(task.Index, &split_compose.ChunkState{
		Index:      chunk.Index,
		ObjectName: chunk.ObjectName,
		Offset:     chunk.Offset,
		Size:       chunk.Size,
		LastSend:   time.Now(),
		Acked:      false,
	})

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
		ObjectName: chunk.ObjectName,
		Offset:     chunk.Offset,
		Size:       chunk.Size,
		LastSend:   chunk.LastSend,
		Acked:      true,
	})

	return nil
}
