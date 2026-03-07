package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"web-ssh/handlers"
)

func main() {
	// Create session manager
	sessionManager := handlers.NewSSHSessionManager()

	// Create handlers
	terminalHandler := handlers.NewTerminalHandler(sessionManager)
	sftpHandler := handlers.NewSFTPHandler(sessionManager)

	// Setup Gin
	r := gin.Default()

	// Serve static files
	r.Static("/vendor", "./vendor")
	r.Static("/js", "./static/js")
	r.StaticFile("/index.html", "./static/index.html")
	r.StaticFile("/", "./static/index.html")

	// API routes
	api := r.Group("/api")
	{
		// Get public key for encryption
		api.GET("/public-key", func(c *gin.Context) {
			handlers.GetPublicKey(c.Writer, c.Request)
		})

		// SSH connection
		api.POST("/ssh/connect", func(c *gin.Context) {
			handlers.ConnectSSH(c.Writer, c.Request, sessionManager)
		})

		api.POST("/ssh/disconnect", func(c *gin.Context) {
			sessionID := c.Query("session_id")
			sessionManager.RemoveSession(sessionID)
			c.JSON(http.StatusOK, gin.H{"success": true})
		})

		// SFTP operations
		api.POST("/sftp/connect", func(c *gin.Context) {
			handlers.CreateSSHSessionForSFTP(c.Writer, c.Request, sessionManager)
		})

		api.POST("/sftp/disconnect", func(c *gin.Context) {
			sessionID := c.Query("session_id")
			sftpHandler.CloseSFTPClient(sessionID)
			c.JSON(http.StatusOK, gin.H{"success": true})
		})

		api.GET("/sftp/list", func(c *gin.Context) {
			sftpHandler.HandleListDir(c.Writer, c.Request)
		})
		api.GET("/sftp/download", func(c *gin.Context) {
			sftpHandler.HandleDownload(c.Writer, c.Request)
		})
		api.POST("/sftp/upload", func(c *gin.Context) {
			sftpHandler.HandleUpload(c.Writer, c.Request)
		})
		api.POST("/sftp/mkdir", func(c *gin.Context) {
			sftpHandler.HandleMkdir(c.Writer, c.Request)
		})
		api.POST("/sftp/remove", func(c *gin.Context) {
			sftpHandler.HandleRemove(c.Writer, c.Request)
		})
		api.GET("/sftp/pwd", func(c *gin.Context) {
			sftpHandler.HandlePwd(c.Writer, c.Request)
		})
		api.POST("/sftp/cd", func(c *gin.Context) {
			sftpHandler.HandleCd(c.Writer, c.Request)
		})
	}

	// WebSocket routes
	r.GET("/ws/terminal", func(c *gin.Context) {
		terminalHandler.HandleTerminal(c.Writer, c.Request)
	})

	// HTTP Long Polling routes (fallback for WebSocket)
	r.POST("/api/local/connect", func(c *gin.Context) {
		handlers.LocalSessionRequest(c.Writer, c.Request, terminalHandler)
	})
	r.GET("/api/local/read", func(c *gin.Context) {
		handlers.LocalSessionRead(c.Writer, c.Request, terminalHandler)
	})
	r.POST("/api/local/write", func(c *gin.Context) {
		handlers.LocalSessionWrite(c.Writer, c.Request, terminalHandler)
	})
	r.POST("/api/local/close", func(c *gin.Context) {
		handlers.LocalSessionClose(c.Writer, c.Request, terminalHandler)
	})

	// Start server
	log.Println("Starting Web SSH server on :8080")
	log.Println("Open http://localhost:8080 in your browser")

	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
