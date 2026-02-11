package split_compose

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"testing"

	"rigel-client/util"
)

func TestSplitFile(t *testing.T) {
	// 创建临时文件
	tmpFile, err := ioutil.TempFile("", "testfile")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// 写入 20 MB 数据
	data := make([]byte, 20*1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	fmt.Println("split file size", len(data))

	if _, err := tmpFile.Write(data); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tmpFile.Close()

	// 创建 SafeMap
	chunks := util.NewSafeMap()

	// 调用 SplitFile
	fileName := "testfile"
	if err := SplitFile(tmpFile.Name(), fileName, chunks, "", nil); err != nil {
		t.Fatalf("SplitFile failed: %v", err)
	}

	// 检查块数量
	expectedChunks := (len(data) + chunkSize - 1) / chunkSize
	if chunks.Len() != expectedChunks {
		t.Errorf("expected %d chunks, got %d", expectedChunks, chunks.Len())
	}

	// 检查每个 ChunkState
	for i := 0; i < expectedChunks; i++ {
		key := strconv.Itoa(i)
		v, ok := chunks.Get(key)
		if !ok {
			t.Errorf("chunk %d not found in map", i)
			continue
		}
		chunk, ok := v.(*ChunkState)
		if !ok {
			t.Errorf("chunk %d has wrong type", i)
			continue
		}
		fmt.Println("split chunk info", chunk)
		if chunk.Index != key {
			t.Errorf("chunk %d index mismatch, got %s", i, chunk.Index)
		}
		if chunk.FileName != fileName {
			t.Errorf("chunk %d filename mismatch, got %s", i, chunk.FileName)
		}
		if chunk.Size <= 0 {
			t.Errorf("chunk %d size invalid, got %d", i, chunk.Size)
		}
	}
}
