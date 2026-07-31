//go:build !windows

package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const dockerSocketPath = "/var/run/docker.sock"

// dockerHTTPClient 返回通过 Unix socket 连接 Docker daemon 的 HTTP 客户端
func dockerHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				dialer := net.Dialer{}
				return dialer.DialContext(ctx, "unix", dockerSocketPath)
			},
		},
		Timeout: 30 * time.Second,
	}
}

// dockerAPIRequest 发起 Docker API 请求
func dockerAPIRequest(method, path string, body io.Reader) (*http.Response, error) {
	client := dockerHTTPClient()
	url := "http://localhost" + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return client.Do(req)
}

// DockerAvailable 检测 Docker socket 是否可用
func DockerAvailable(c *gin.Context) {
	if _, err := os.Stat(dockerSocketPath); err != nil {
		c.JSON(http.StatusOK, gin.H{"available": false, "error": err.Error()})
		return
	}
	// 尝试连接 Docker daemon
	resp, err := dockerAPIRequest("GET", "/_ping", nil)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"available": false, "error": err.Error()})
		return
	}
	resp.Body.Close()
	c.JSON(http.StatusOK, gin.H{"available": resp.StatusCode == http.StatusOK})
}

// DockerListContainers 列出所有容器
func DockerListContainers(c *gin.Context) {
	resp, err := dockerAPIRequest("GET", "/containers/json?all=1&size=0", nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	var containers []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 简化返回数据，同时获取日志大小
	result := make([]map[string]interface{}, 0, len(containers))
	for _, ctr := range containers {
		names, _ := ctr["Names"].([]interface{})
		name := ""
		if len(names) > 0 {
			name = strings.TrimPrefix(fmt.Sprintf("%v", names[0]), "/")
		}

		// 获取端口映射信息
		ports, _ := ctr["Ports"].([]interface{})
		portStrs := make([]string, 0)
		for _, p := range ports {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			ip, _ := pm["IP"].(string)
			publicPort, _ := pm["PublicPort"].(float64)
			privatePort, _ := pm["PrivatePort"].(float64)
			typ, _ := pm["Type"].(string)
			if publicPort > 0 {
				portStrs = append(portStrs, fmt.Sprintf("%s:%v->%v/%s", ip, publicPort, privatePort, typ))
			} else {
				portStrs = append(portStrs, fmt.Sprintf("%v/%s", privatePort, typ))
			}
		}

		// 获取日志文件大小
		containerID, _ := ctr["Id"].(string)
		logSize := int64(0)
		logPath := ""
		if containerID != "" {
			logPath, _ = getContainerLogPath(containerID)
			if logPath != "" {
				if info, err := os.Stat(logPath); err == nil {
					logSize = info.Size()
				}
			}
		}

		result = append(result, map[string]interface{}{
			"id":       containerID,
			"name":     name,
			"image":    ctr["Image"],
			"state":    ctr["State"],
			"status":   ctr["Status"],
			"created":  ctr["Created"],
			"ports":    strings.Join(portStrs, ", "),
			"command":  ctr["Command"],
			"log_size": logSize,
			"log_path": logPath,
		})
	}

	c.JSON(http.StatusOK, gin.H{"containers": result})
}

// DockerListImages 列出所有镜像
func DockerListImages(c *gin.Context) {
	resp, err := dockerAPIRequest("GET", "/images/json", nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	var images []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result := make([]map[string]interface{}, 0, len(images))
	for _, img := range images {
		tags, _ := img["RepoTags"].([]interface{})
		tagStrs := make([]string, 0)
		for _, t := range tags {
			if s, ok := t.(string); ok && s != "<none>:<none>" {
				tagStrs = append(tagStrs, s)
			}
		}

		size, _ := img["Size"].(float64)

		// 安全地获取 short_id
		idStr, _ := img["Id"].(string)
		idStr = strings.TrimPrefix(idStr, "sha256:")
		shortID := idStr
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}

		result = append(result, map[string]interface{}{
			"id":        img["Id"],
			"tags":      tagStrs,
			"size":      size,
			"created":   img["Created"],
			"short_id":  shortID,
		})
	}

	c.JSON(http.StatusOK, gin.H{"images": result})
}

// dockerContainerAction 执行容器操作（start/stop/restart）
func dockerContainerAction(c *gin.Context, action string) {
	var req struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}

	resp, err := dockerAPIRequest("POST", fmt.Sprintf("/containers/%s/%s", req.ID, action), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.JSON(resp.StatusCode, gin.H{"error": string(body)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DockerStartContainer 启动容器
func DockerStartContainer(c *gin.Context) {
	dockerContainerAction(c, "start")
}

// DockerStopContainer 停止容器
func DockerStopContainer(c *gin.Context) {
	var req struct {
		ID      string `json:"id"`
		Timeout int    `json:"timeout"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}
	if req.Timeout == 0 {
		req.Timeout = 10
	}

	resp, err := dockerAPIRequest("POST", fmt.Sprintf("/containers/%s/stop?t=%d", req.ID, req.Timeout), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.JSON(resp.StatusCode, gin.H{"error": string(body)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DockerRestartContainer 重启容器
func DockerRestartContainer(c *gin.Context) {
	var req struct {
		ID      string `json:"id"`
		Timeout int    `json:"timeout"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}
	if req.Timeout == 0 {
		req.Timeout = 10
	}

	resp, err := dockerAPIRequest("POST", fmt.Sprintf("/containers/%s/restart?t=%d", req.ID, req.Timeout), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.JSON(resp.StatusCode, gin.H{"error": string(body)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DockerRemoveContainer 删除容器
func DockerRemoveContainer(c *gin.Context) {
	var req struct {
		ID    string `json:"id"`
		Force bool   `json:"force"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}

	force := ""
	if req.Force {
		force = "?force=1"
	}
	resp, err := dockerAPIRequest("DELETE", fmt.Sprintf("/containers/%s%s", req.ID, force), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.JSON(resp.StatusCode, gin.H{"error": string(body)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DockerContainerLogs 获取容器日志（直接读取日志文件，不依赖 Docker API 的 tail 参数）
func DockerContainerLogs(c *gin.Context) {
	containerID := c.Query("id")
	if containerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}
	tailLines := 500
	if v, err := strconv.Atoi(c.DefaultQuery("tail", "500")); err == nil && v > 0 {
		tailLines = v
	}

	// 获取容器日志文件路径
	logPath, err := getContainerLogPath(containerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	f, err := os.Open(logPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法打开日志文件: " + err.Error()})
		return
	}
	defer f.Close()

	const maxOutputSize = 500000 // 最大输出 500KB

	// 获取文件大小
	info, err := f.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	fileSize := info.Size()
	if fileSize == 0 {
		c.JSON(http.StatusOK, gin.H{"logs": ""})
		return
	}

	// 从文件末尾向前读取，收集最后 N 行
	// 每次读取 64KB 块
	var lines []string
	const chunkSize = 64 * 1024
	var offset int64 = fileSize

	for offset > 0 && len(lines) <= tailLines {
		if offset > chunkSize {
			offset -= chunkSize
		} else {
			offset = 0
		}

		_, err := f.Seek(offset, 0)
		if err != nil {
			break
		}

		chunk := make([]byte, fileSize-offset)
		n, err := f.Read(chunk)
		if err != nil {
			break
		}

		// 解析 JSON 日志格式（每行一个 JSON 对象）
		// 格式: {"log":"...","stream":"stdout","time":"..."}
		scanner := bufio.NewScanner(bytes.NewReader(chunk[:n]))
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		var chunkLines []string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			// 尝试解析 JSON 日志
			logText := parseDockerJSONLog(line)
			if logText != "" {
				chunkLines = append(chunkLines, logText)
			}
		}

		// 将新读取的行插入到前面（因为是从后往前读的）
		lines = append(chunkLines, lines...)

		if len(lines) > tailLines {
			lines = lines[len(lines)-tailLines:]
		}
	}

	// 拼接日志并限制大小
	output := strings.Join(lines, "\n")
	if len(output) > maxOutputSize {
		output = output[len(output)-maxOutputSize:]
	}

	c.JSON(http.StatusOK, gin.H{"logs": output})
}

// parseDockerJSONLog 解析 Docker JSON 日志行，提取日志内容
func parseDockerJSONLog(line string) string {
	if len(line) < 2 || line[0] != '{' {
		// 不是 JSON 格式，直接返回
		return line
	}

	// 快速提取 "log" 字段值，避免完整 JSON 解析
	logKey := `"log":"`
	idx := strings.Index(line, logKey)
	if idx < 0 {
		return ""
	}

	start := idx + len(logKey)
	if start >= len(line) {
		return ""
	}

	var result strings.Builder
	for i := start; i < len(line); i++ {
		ch := line[i]
		if ch == '"' && (i == 0 || line[i-1] != '\\') {
			break
		}
		if ch == '\\' && i+1 < len(line) {
			next := line[i+1]
			switch next {
			case 'n':
				result.WriteByte('\n')
				i++
			case 'r':
				result.WriteByte('\r')
				i++
			case 't':
				result.WriteByte('\t')
				i++
			case '\\':
				result.WriteByte('\\')
				i++
			case '"':
				result.WriteByte('"')
				i++
			default:
				result.WriteByte(ch)
			}
		} else {
			result.WriteByte(ch)
		}
	}

	return result.String()
}

// getContainerLogPath 获取容器日志文件路径
func getContainerLogPath(containerID string) (string, error) {
	resp, err := dockerAPIRequest("GET", fmt.Sprintf("/containers/%s/json", containerID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var info map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}

	logPath, ok := info["LogPath"].(string)
	if !ok || logPath == "" {
		return "", fmt.Errorf("unable to find log path for container %s", containerID)
	}
	return logPath, nil
}

// DockerContainerLogSize 获取容器日志文件大小
func DockerContainerLogSize(c *gin.Context) {
	containerID := c.Query("id")
	if containerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}

	logPath, err := getContainerLogPath(containerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	info, err := os.Stat(logPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"size":     info.Size(),
		"log_path": logPath,
	})
}

// DockerClearLogs 清空容器日志
func DockerClearLogs(c *gin.Context) {
	var req struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}

	logPath, err := getContainerLogPath(req.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 清空日志文件（truncate to 0）
	if err := os.Truncate(logPath, 0); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DockerContainerStats 获取容器资源使用统计
func DockerContainerStats(c *gin.Context) {
	containerID := c.Query("id")
	if containerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}

	resp, err := dockerAPIRequest("GET", fmt.Sprintf("/containers/%s/stats?stream=0", containerID), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 计算 CPU 使用率
	cpuDelta := float64(0)
	systemDelta := float64(0)
	cpuPercent := float64(0)

	if cpuStats, ok := stats["cpu_stats"].(map[string]interface{}); ok {
		if usage, ok := cpuStats["cpu_usage"].(map[string]interface{}); ok {
			if totalUsage, ok := usage["total_usage"].(float64); ok {
				cpuDelta = totalUsage
			}
		}
		if sysUsage, ok := cpuStats["system_cpu_usage"].(float64); ok {
			systemDelta = sysUsage
		}
	}

	if precpuStats, ok := stats["precpu_stats"].(map[string]interface{}); ok {
		if usage, ok := precpuStats["cpu_usage"].(map[string]interface{}); ok {
			if totalUsage, ok := usage["total_usage"].(float64); ok {
				cpuDelta -= totalUsage
			}
		}
		if sysUsage, ok := precpuStats["system_cpu_usage"].(float64); ok {
			systemDelta -= sysUsage
		}
	}

	if systemDelta > 0 && cpuDelta > 0 {
		numCPUs := float64(1)
		if onlineCPUs, ok := stats["cpu_stats"].(map[string]interface{}); ok {
			if v, ok := onlineCPUs["online_cpus"].(float64); ok {
				numCPUs = v
			}
		}
		cpuPercent = (cpuDelta / systemDelta) * numCPUs * 100.0
	}

	// 内存使用
	memUsage := float64(0)
	memLimit := float64(0)
	memPercent := float64(0)

	if memStats, ok := stats["memory_stats"].(map[string]interface{}); ok {
		if usage, ok := memStats["usage"].(float64); ok {
			memUsage = usage
		}
		if limit, ok := memStats["limit"].(float64); ok {
			memLimit = limit
		}
	}
	if memLimit > 0 {
		memPercent = (memUsage / memLimit) * 100.0
	}

	c.JSON(http.StatusOK, gin.H{
		"cpu_percent":    cpuPercent,
		"mem_usage":      memUsage,
		"mem_limit":      memLimit,
		"mem_percent":    memPercent,
	})
}
