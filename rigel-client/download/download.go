package download

import (
	"fmt"
	"strings"
)

// buildLocalFileName 构造本地文件名
func buildLocalFileName(filename string, start, length int64, split bool) string {

	if !split {
		return filename
	}

	basename := filename
	ext := ""
	if idx := strings.LastIndex(filename, "."); idx != -1 {
		basename = filename[:idx]
		ext = filename[idx:]
	}

	// 全量读取（length≤0）
	//if length <= 0 {
	//	return fmt.Sprintf("%s_full%s", basename, ext)
	//}

	// 指定范围读取
	start_ := fmt.Sprintf("%.1f", start)
	end_ := fmt.Sprintf("%.1f", start+length)
	return fmt.Sprintf("%s_%s-%s%s", basename, start_, end_, ext)
}
