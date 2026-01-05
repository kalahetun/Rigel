package limit_rate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	chunkSize   = 10 * 1024 * 1024 // 10MB
	maxInflight = 3                // 最大并发
	maxMbps     = 40               // 总带宽上限 Mbps
)

type Chunk struct {
	Index  int
	Offset int64
	Size   int64
}

/* ================= 限速 Reader ================= */

type RateLimitedReader struct {
	r       io.Reader
	limiter *rate.Limiter
	ctx     context.Context
}

func (rr *RateLimitedReader) Read(p []byte) (int, error) {
	n, err := rr.r.Read(p)
	if n > 0 {
		_ = rr.limiter.WaitN(rr.ctx, n)
	}
	return n, err
}

/* ================= 切片（不进内存） ================= */

func splitFile(path string) ([]*Chunk, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var chunks []*Chunk
	var offset int64
	index := 0

	for offset < fi.Size() {
		size := int64(chunkSize)
		if offset+size > fi.Size() {
			size = fi.Size() - offset
		}

		chunks = append(chunks, &Chunk{
			Index:  index,
			Offset: offset,
			Size:   size,
		})

		offset += size
		index++
	}

	return chunks, nil
}

/* ================= 发送一个 chunk ================= */

func uploadChunk(
	ctx context.Context,
	path string,
	chunk *Chunk,
	url string,
	sem chan struct{},
	limiter *rate.Limiter,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	sem <- struct{}{}
	defer func() { <-sem }()

	file, err := os.Open(path)
	if err != nil {
		fmt.Println("open error:", err)
		return
	}
	defer file.Close()

	section := io.NewSectionReader(file, chunk.Offset, chunk.Size)

	body := &RateLimitedReader{
		r:       section,
		limiter: limiter,
		ctx:     ctx,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		fmt.Println("new request error:", err)
		return
	}

	req.Header.Set("X-Chunk-Index", fmt.Sprintf("%d", chunk.Index))
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("upload error:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("bad status:", resp.Status)
		return
	}

	fmt.Printf("chunk %d uploaded\n", chunk.Index)
}

/* ================= main ================= */

func main() {
	filePath := "bigfile.bin"                   // 本地大文件
	uploadURL := "http://127.0.0.1:8080/upload" // 你的 server

	ctx := context.Background()

	chunks, err := splitFile(filePath)
	if err != nil {
		panic(err)
	}

	fmt.Printf("total chunks: %d\n", len(chunks))

	// Mbps → bytes/sec
	bytesPerSec := maxMbps * 1024 * 1024 / 8

	limiter := rate.NewLimiter(
		rate.Limit(bytesPerSec),
		bytesPerSec,
	)

	sem := make(chan struct{}, maxInflight)
	var wg sync.WaitGroup

	start := time.Now()

	for _, c := range chunks {
		wg.Add(1)
		go uploadChunk(ctx, filePath, c, uploadURL, sem, limiter, &wg)
	}

	wg.Wait()

	fmt.Println("all done, elapsed:", time.Since(start))
}
