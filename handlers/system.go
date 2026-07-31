//go:build !windows

package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

// SystemInfo 返回系统信息
func SystemInfo(c *gin.Context) {
	cpu := getCPUInfo()
	cpuUsage := getCPUUsage()
	cpu["usage_percent"] = cpuUsage

	mem := getMemInfo()
	disks := getDiskInfo()
	uptime := getUptime()
	loadAvg := getLoadAvg()

	hostname, _ := os.Hostname()

	c.JSON(http.StatusOK, gin.H{
		"cpu":       cpu,
		"memory":    mem,
		"disks":     disks,
		"hostname":  hostname,
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
		"uptime":    uptime,
		"load_avg":  loadAvg,
	})
}

// getCPUInfo 从 /proc/cpuinfo 获取 CPU 信息
func getCPUInfo() map[string]interface{} {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return map[string]interface{}{
			"model": "Unknown",
			"cores": runtime.NumCPU(),
		}
	}
	defer file.Close()

	model := "Unknown"
	cores := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				model = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(line, "processor") {
			cores++
		}
	}

	if cores == 0 {
		cores = runtime.NumCPU()
	}

	return map[string]interface{}{
		"model": model,
		"cores": cores,
	}
}

// getCPUUsage 通过两次采样 /proc/stat 计算 CPU 使用率
func getCPUUsage() float64 {
	idle1, total1 := readCPUStat()
	time.Sleep(500 * time.Millisecond)
	idle2, total2 := readCPUStat()

	if total2-total1 == 0 {
		return 0
	}

	idleDelta := float64(idle2 - idle1)
	totalDelta := float64(total2 - total1)
	usage := (1.0 - idleDelta/totalDelta) * 100.0

	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}

	return float64(int(usage*10)) / 10.0 // 保留一位小数
}

// readCPUStat 读取 /proc/stat 中的 CPU 行
func readCPUStat() (idle, total uint64) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return 0, 0
			}
			var values []uint64
			for _, f := range fields[1:] {
				v, err := strconv.ParseUint(f, 10, 64)
				if err != nil {
					continue
				}
				values = append(values, v)
				total += v
			}
			// idle 是第 4 个字段（index 3）
			if len(values) >= 4 {
				idle = values[3]
			}
			return
		}
	}
	return 0, 0
}

// getMemInfo 从 /proc/meminfo 获取内存信息（单位 MB）
func getMemInfo() map[string]interface{} {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return map[string]interface{}{
			"total":         0,
			"used":          0,
			"free":          0,
			"usage_percent": 0,
		}
	}
	defer file.Close()

	memInfo := make(map[string]uint64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			key := strings.TrimSuffix(parts[0], ":")
			val, err := strconv.ParseUint(parts[1], 10, 64)
			if err != nil {
				continue
			}
			memInfo[key] = val
		}
	}

	// /proc/meminfo 中的值单位是 kB
	totalKB := memInfo["MemTotal"]
	freeKB := memInfo["MemFree"]
	buffersKB := memInfo["Buffers"]
	cachedKB := memInfo["Cached"]
	sReclaimableKB := memInfo["SReclaimable"]

	// 实际可用内存 = free + buffers + cached + SReclaimable
	availableKB := freeKB + buffersKB + cachedKB + sReclaimableKB
	usedKB := totalKB - availableKB

	totalMB := totalKB / 1024
	usedMB := usedKB / 1024
	freeMB := availableKB / 1024

	var usagePercent float64
	if totalMB > 0 {
		usagePercent = float64(int(float64(usedMB)/float64(totalMB)*1000)) / 10.0
	}

	return map[string]interface{}{
		"total":         totalMB,
		"used":          usedMB,
		"free":          freeMB,
		"usage_percent": usagePercent,
	}
}

// getDiskInfo 获取磁盘使用信息
func getDiskInfo() []map[string]interface{} {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer file.Close()

	// 需要过滤的虚拟文件系统类型
	virtualFS := map[string]bool{
		"sysfs": true, "proc": true, "devtmpfs": true, "devpts": true,
		"tmpfs": true, "securityfs": true, "cgroup": true, "cgroup2": true,
		"pstore": true, "debugfs": true, "hugetlbfs": true, "mqueue": true,
		"fusectl": true, "binfmt_misc": true, "configfs": true,
		"tracefs": true, "overlay": false, // overlay 可能是 docker 容器根分区，保留
		"nsfs": true, "sunrpc": true, "rpc_pipefs": true,
		"autofs": true, "efivarfs": true,
	}

	seen := make(map[string]bool)
	var disks []map[string]interface{}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		device := fields[0]
		mountPoint := fields[1]
		fsType := fields[2]

		// 过滤虚拟文件系统
		if virtualFS[fsType] {
			continue
		}

		// 过滤不是真实设备的挂载点
		if strings.HasPrefix(device, "none") || strings.HasPrefix(device, "tmpfs") ||
			strings.HasPrefix(device, "devpts") || strings.HasPrefix(device, "sysfs") {
			continue
		}

		// 跳过已经挂载过的设备（去重）
		if seen[device] {
			continue
		}
		seen[device] = true

		var stat syscall.Statfs_t
		if err := syscall.Statfs(mountPoint, &stat); err != nil {
			continue
		}

		totalBytes := stat.Blocks * uint64(stat.Bsize)
		freeBytes := stat.Bfree * uint64(stat.Bsize)
		availBytes := stat.Bavail * uint64(stat.Bsize)
		usedBytes := totalBytes - freeBytes

		totalGB := float64(totalBytes) / (1024 * 1024 * 1024)
		usedGB := float64(usedBytes) / (1024 * 1024 * 1024)
		freeGB := float64(availBytes) / (1024 * 1024 * 1024)

		var usagePercent float64
		if totalBytes > 0 {
			usagePercent = float64(int(float64(usedBytes)/float64(totalBytes)*1000)) / 10.0
		}

		disks = append(disks, map[string]interface{}{
			"device":        device,
			"mount_point":   mountPoint,
			"fs_type":       fsType,
			"total":         float64(int(totalGB*10)) / 10.0,
			"used":          float64(int(usedGB*10)) / 10.0,
			"free":          float64(int(freeGB*10)) / 10.0,
			"usage_percent": usagePercent,
		})
	}

	return disks
}

// getUptime 获取系统运行时间（秒）
func getUptime() uint64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return uint64(uptime)
}

// getLoadAvg 获取系统负载均值
func getLoadAvg() []float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return []float64{0, 0, 0}
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return []float64{0, 0, 0}
	}

	loadAvg := make([]float64, 3)
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			continue
		}
		loadAvg[i] = float64(int(v*100)) / 100.0
	}
	return loadAvg
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
