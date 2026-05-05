package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type progressReader struct {
	r           io.Reader
	total       int64
	current     int64
	lastPercent int
	label       string
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.current += int64(n)
	if pr.total > 0 {
		percent := int(pr.current * 100 / pr.total)
		if percent >= pr.lastPercent+10 {
			fmt.Fprintf(os.Stderr, "  [%s] %3d%% (%s / %s)\n", pr.label, percent, formatBytes(pr.current), formatBytes(pr.total))
			pr.lastPercent = percent
		}
	} else {
		// 未知总大小时，每 10 MB 打印一次
		if pr.current >= int64(pr.lastPercent)*10*1024*1024 {
			fmt.Fprintf(os.Stderr, "  [%s] downloaded %s\n", pr.label, formatBytes(pr.current))
			pr.lastPercent++
		}
	}
	return n, err
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func parseContentRangeTotal(contentRange string) int64 {
	idx := strings.LastIndex(contentRange, "/")
	if idx < 0 {
		return 0
	}
	total, _ := strconv.ParseInt(contentRange[idx+1:], 10, 64)
	return total
}
