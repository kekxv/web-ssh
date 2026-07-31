//go:build windows

package handlers

import (
	"fmt"
	"net/http"
	"os"
	"runtime"

	"github.com/gin-gonic/gin"
)

// SystemInfo Windows 下返回基础系统信息
func SystemInfo(c *gin.Context) {
	hostname, _ := os.Hostname()

	c.JSON(http.StatusOK, gin.H{
		"cpu": map[string]interface{}{
			"model":         runtime.GOARCH,
			"cores":         runtime.NumCPU(),
			"usage_percent": 0,
		},
		"memory": map[string]interface{}{
			"total":         0,
			"used":          0,
			"free":          0,
			"usage_percent": 0,
		},
		"disks":    []interface{}{},
		"hostname": hostname,
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"uptime":   0,
		"load_avg": []float64{0, 0, 0},
	})
}

// FormatUptime 格式化运行时间
func FormatUptime(seconds uint64) string {
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%d天%d小时%d分钟", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d小时%d分钟", hours, minutes)
	}
	return fmt.Sprintf("%d分钟", minutes)
}
