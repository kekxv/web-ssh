package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"web-ssh/handlers"
)

//go:embed static/*
var staticFS embed.FS

func getEmbeddedFile(subFS fs.FS, path string) []byte {
	data, err := fs.ReadFile(subFS, path)
	if err != nil {
		return []byte{}
	}
	return data
}

func main() {
	// Create session manager
	sessionManager := handlers.NewSSHSessionManager()

	// Initialize AuthManager with session manager
	handlers.SetSSHSessionManager(sessionManager)

	// Create handlers
	terminalHandler := handlers.NewTerminalHandler(sessionManager)
	sftpHandler := handlers.NewSFTPHandler(sessionManager)

	// Setup Gin
	r := gin.Default()

	// Get the static subdirectory from embedded FS
	subFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}

	// Serve specific paths or use a custom handler for index.html
	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", getEmbeddedFile(subFS, "index.html"))
	})
	r.GET("/index.html", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", getEmbeddedFile(subFS, "index.html"))
	})

	// Serve JS, Vendor, CSS etc. from embedded FS
	r.GET("/js/:file", func(c *gin.Context) {
		file := c.Param("file")
		c.Data(http.StatusOK, "application/javascript", getEmbeddedFile(subFS, "js/"+file))
	})
	r.GET("/vendor/:file", func(c *gin.Context) {
		file := c.Param("file")
		content := getEmbeddedFile(subFS, "vendor/"+file)
		contentType := "text/plain"
		if strings.HasSuffix(file, ".js") {
			contentType = "application/javascript"
		} else if strings.HasSuffix(file, ".css") {
			contentType = "text/css"
		}
		c.Data(http.StatusOK, contentType, content)
	})
	
	// Handle assets that might be in subdirectories of vendor or other folders
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if len(path) > 0 && path[0] == '/' {
			path = path[1:]
		}
		// If it's a known static path prefix, serve from embed
		if strings.HasPrefix(path, "js/") || strings.HasPrefix(path, "vendor/") || strings.HasPrefix(path, "css/") {
			data, err := fs.ReadFile(subFS, path)
			if err == nil {
				contentType := "application/octet-stream"
				if strings.HasSuffix(path, ".js") {
					contentType = "application/javascript"
				} else if strings.HasSuffix(path, ".css") {
					contentType = "text/css"
				}
				c.Data(http.StatusOK, contentType, data)
				return
			}
		}
		
		// For API routes, let them pass through (if they weren't matched already)
		if strings.HasPrefix(path, "api/") || strings.HasPrefix(path, "ws/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		
		// Fallback to index.html
		c.Data(http.StatusOK, "text/html; charset=utf-8", getEmbeddedFile(subFS, "index.html"))
	})

	// Public API routes (no auth required)
	publicApi := r.Group("/api")
	{
		// Auth endpoints
		publicApi.POST("/auth/login", func(c *gin.Context) {
			handlers.HandleLogin(c.Writer, c.Request)
		})
		publicApi.POST("/auth/logout", func(c *gin.Context) {
			handlers.HandleLogout(c.Writer, c.Request)
		})
		publicApi.GET("/auth/check", func(c *gin.Context) {
			handlers.HandleCheckAuth(c.Writer, c.Request)
		})
		publicApi.POST("/auth/change-password", func(c *gin.Context) {
			handlers.HandleChangePassword(c.Writer, c.Request)
		})

		// Get public key for encryption (needed before login)
		publicApi.GET("/public-key", func(c *gin.Context) {
			handlers.GetPublicKey(c.Writer, c.Request)
		})
	}

	// Protected API routes (auth required)
	protectedApi := r.Group("/api")
	protectedApi.Use(func(c *gin.Context) {
		// Get session from cookie
		cookie, err := c.Cookie("session_id")
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}

		auth := handlers.GetAuthManager()
		session, ok := auth.GetSession(cookie)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired"})
			c.Abort()
			return
		}

		// Add username to context for logging
		log.Printf("Request by user: %s", session.Username)
		c.Next()
	})
	{
		// SSH connection
		protectedApi.POST("/ssh/connect", func(c *gin.Context) {
			handlers.ConnectSSH(c.Writer, c.Request, sessionManager)
		})

		protectedApi.POST("/ssh/disconnect", func(c *gin.Context) {
			sessionID := c.Query("session_id")
			sessionManager.RemoveSession(sessionID)
			c.JSON(http.StatusOK, gin.H{"success": true})
		})

		// SFTP operations
		protectedApi.POST("/sftp/connect", func(c *gin.Context) {
			handlers.CreateSSHSessionForSFTP(c.Writer, c.Request, sessionManager)
		})

		protectedApi.POST("/sftp/disconnect", func(c *gin.Context) {
			sessionID := c.Query("session_id")
			sftpHandler.CloseSFTPClient(sessionID)
			c.JSON(http.StatusOK, gin.H{"success": true})
		})

		protectedApi.GET("/sftp/list", func(c *gin.Context) {
			sftpHandler.HandleListDir(c.Writer, c.Request)
		})
		protectedApi.GET("/sftp/download", func(c *gin.Context) {
			sftpHandler.HandleDownload(c.Writer, c.Request)
		})
		protectedApi.POST("/sftp/upload", func(c *gin.Context) {
			sftpHandler.HandleUpload(c.Writer, c.Request)
		})
		protectedApi.POST("/sftp/mkdir", func(c *gin.Context) {
			sftpHandler.HandleMkdir(c.Writer, c.Request)
		})
		protectedApi.POST("/sftp/remove", func(c *gin.Context) {
			sftpHandler.HandleRemove(c.Writer, c.Request)
		})
		protectedApi.GET("/sftp/pwd", func(c *gin.Context) {
			sftpHandler.HandlePwd(c.Writer, c.Request)
		})
		protectedApi.POST("/sftp/cd", func(c *gin.Context) {
			sftpHandler.HandleCd(c.Writer, c.Request)
		})

		// HTTP Long Polling routes (fallback for WebSocket)
		protectedApi.POST("/local/connect", func(c *gin.Context) {
			handlers.LocalSessionRequest(c.Writer, c.Request, terminalHandler)
		})
		protectedApi.GET("/local/read", func(c *gin.Context) {
			handlers.LocalSessionRead(c.Writer, c.Request, terminalHandler)
		})
		protectedApi.POST("/local/write", func(c *gin.Context) {
			handlers.LocalSessionWrite(c.Writer, c.Request, terminalHandler)
		})
		protectedApi.POST("/local/close", func(c *gin.Context) {
			handlers.LocalSessionClose(c.Writer, c.Request, terminalHandler)
		})

		// Local file management routes
		protectedApi.GET("/local/file/list", func(c *gin.Context) {
			handlers.LocalFileList(c.Writer, c.Request)
		})
		protectedApi.GET("/local/file/download", func(c *gin.Context) {
			handlers.LocalFileDownload(c.Writer, c.Request)
		})
		protectedApi.POST("/local/file/upload", func(c *gin.Context) {
			handlers.LocalFileUpload(c.Writer, c.Request)
		})
		protectedApi.POST("/local/file/mkdir", func(c *gin.Context) {
			handlers.LocalFileMkdir(c.Writer, c.Request)
		})
		protectedApi.POST("/local/file/remove", func(c *gin.Context) {
			handlers.LocalFileRemove(c.Writer, c.Request)
		})
		protectedApi.GET("/local/file/pwd", func(c *gin.Context) {
			handlers.LocalFilePwd(c.Writer, c.Request)
		})
		protectedApi.POST("/local/file/cd", func(c *gin.Context) {
			handlers.LocalFileCd(c.Writer, c.Request)
		})

		// User management (admin only)
		protectedApi.POST("/admin/users/add", func(c *gin.Context) {
			handlers.HandleAddUser(c.Writer, c.Request)
		})
		protectedApi.GET("/admin/users/list", func(c *gin.Context) {
			handlers.HandleListUsers(c.Writer, c.Request)
		})
		protectedApi.POST("/admin/users/delete", func(c *gin.Context) {
			handlers.HandleDeleteUser(c.Writer, c.Request)
		})
	}

	// WebSocket routes (auth required)
	r.GET("/ws/terminal", func(c *gin.Context) {
		mode := c.Query("mode")
		sessionID := c.Query("session_id")

		if mode == "ssh" {
			if sessionID == "" {
				log.Printf("WebSocket SSH mode requires session_id parameter")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "session_id required for SSH mode"})
				c.Abort()
				return
			}
			cookie, err := c.Cookie("session_id")
			if err != nil {
				log.Printf("WebSocket auth failed: no cookie")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				c.Abort()
				return
			}
			auth := handlers.GetAuthManager()
			_, ok := auth.GetSession(cookie)
			if !ok {
				log.Printf("WebSocket auth failed: invalid session")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				c.Abort()
				return
			}
		} else {
			cookie, err := c.Cookie("session_id")
			if err != nil {
				log.Printf("WebSocket auth failed: no cookie")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				c.Abort()
				return
			}
			auth := handlers.GetAuthManager()
			session, ok := auth.GetSession(cookie)
			if !ok {
				log.Printf("WebSocket auth failed: invalid session")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				c.Abort()
				return
			}
			log.Printf("WebSocket authenticated for user: %s", session.Username)
		}

		terminalHandler.HandleTerminal(c.Writer, c.Request)
	})

	// Start server
	log.Println("Starting Web SSH server on :8080")
	log.Println("Default credentials: admin / admin123")
	log.Println("Open http://localhost:8080 in your browser")

	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
