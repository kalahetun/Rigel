package split_compose

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	chunkSize = 8 * 1024 * 1024 // 8MB
)

type ChunkState struct {
	Index      int
	ObjectName string
	Offset     int64
	Size       int64

	LastSend time.Time
	Acked    bool
}

var (
	chunks = make(map[int]*ChunkState)
	mu     sync.RWMutex
)

func splitFile(path, objectName string) (map[int]*ChunkState, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	size := fi.Size()
	chunks := make(map[int]*ChunkState)

	var (
		offset int64
		index  int
	)

	for offset < size {
		partSize := int64(chunkSize)
		if offset+partSize > size {
			partSize = size - offset
		}

		partName := fmt.Sprintf("%s.part.%05d", objectName, index)

		chunks[index] = &ChunkState{
			Index:      index,
			ObjectName: partName,
			Offset:     offset,
			Size:       partSize,
			Acked:      false,
		}

		offset += partSize
		index++
	}

	return chunks, nil
}

func StartChunkDispatcher() {
	ticker := time.NewTicker(10 * time.Second)

	go func() {
		for range ticker.C {
			mu.Lock()
			for _, c := range chunks {
				if c.Acked {
					continue
				}

				// 从未发送 or 超时
				if c.LastSend.IsZero() ||
					time.Since(c.LastSend) > 2*time.Minute {

					err := sendChunk(c)
					if err == nil {
						c.LastSend = time.Now()
					}
				}
			}
			mu.Unlock()
		}
	}()
}

func markAck(index int) {
	mu.Lock()
	defer mu.Unlock()

	if c, ok := chunks[index]; ok {
		c.Acked = true
	}
}

func allAcked() bool {
	mu.RLock()
	defer mu.RUnlock()

	for _, c := range chunks {
		if !c.Acked {
			return false
		}
	}
	return true
}

func sendChunk(path string, chunk *ChunkState) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := io.NewSectionReader(
		f,
		chunk.Offset,
		chunk.Size,
	)

	req, err := http.NewRequest(
		"POST",
		"http://your-proxy/gcp/redirect/upload",
		reader,
	)
	if err != nil {
		return err
	}

	req.Header.Set("X-File-Name", chunk.ObjectName)
	req.Header.Set("X-Chunk-Index", strconv.Itoa(chunk.Index))
	req.Header.Set("Content-Length", strconv.FormatInt(chunk.Size, 10))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload failed: %s", resp.Status)
	}

	chunk.Acked = true
	return nil
}

func retryLoop(
	path string,
	chunks map[int]*ChunkState,
	mu *sync.Mutex,
) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		allAcked := true

		mu.Lock()
		for _, c := range chunks {
			if c.Acked {
				continue
			}

			allAcked = false

			if c.LastSend.IsZero() ||
				time.Since(c.LastSend) > 2*time.Minute {

				c.LastSend = time.Now()

				go func(chunk *ChunkState) {
					if err := sendChunk(path, chunk); err != nil {
						log.Printf("retry chunk %d failed: %v", chunk.Index, err)
					}
				}(c)
			}
		}
		mu.Unlock()

		if allAcked {
			return
		}
	}
}
