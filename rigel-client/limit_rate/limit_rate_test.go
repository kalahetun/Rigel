package limit_rate

import (
	"bytes"
	"context"
	"golang.org/x/time/rate"
	"io"
	"testing"
	"time"
)

func TestRateLimitedReader_Read(t *testing.T) {
	data := []byte("abcdefghijklmnopqrstuvwxyz") // 26 字节数据
	reader := bytes.NewReader(data)

	// 创建一个每秒允许 10 字节的限流器
	limiter := rate.NewLimiter(rate.Limit(10), 10)

	rr := NewRateLimitedReader(context.Background(), reader, limiter)

	buf := make([]byte, 5) // 每次读取 5 字节
	var result []byte

	start := time.Now()
	for {
		n, err := rr.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				break
			} else {
				t.Fatalf("Read error: %v", err)
			}
		}
	}
	elapsed := time.Since(start)

	// 验证读取的数据正确
	if !bytes.Equal(result, data) {
		t.Errorf("Read data mismatch. got: %s, want: %s", string(result), string(data))
	}

	// 验证限流大概生效（允许 10 字节/秒，26 字节大约需要 >= 2.6 秒）
	if elapsed < 2*time.Second {
		t.Errorf("Rate limiter seems not effective, elapsed: %v", elapsed)
	}
}
