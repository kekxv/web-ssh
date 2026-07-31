//go:build windows

package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// Windows 下 Docker 不支持 Unix socket，提供空实现
func DockerAvailable(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"available": false, "error": "Docker socket not supported on Windows"})
}

func DockerListContainers(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerListImages(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerStartContainer(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerStopContainer(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerRestartContainer(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerRemoveContainer(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerContainerLogs(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerContainerLogSize(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerClearLogs(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}

func DockerContainerStats(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "Docker not supported on Windows"})
}
