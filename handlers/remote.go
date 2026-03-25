package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// RemoteSession 管理远程 web-ssh 会话
type RemoteSession struct {
	ID         string
	URL        string
	Cookie     string // 远程服务器的 session cookie
	Username   string
	RemoteConn *websocket.Conn // 远程 WebSocket 连接
	LastActive time.Time
	mu         sync.Mutex
}

// RemoteSessionManager 管理所有远程会话
type RemoteSessionManager struct {
	sessions map[string]*RemoteSession
	mu       sync.RWMutex
}

// NewRemoteSessionManager 创建新的远程会话管理器
func NewRemoteSessionManager() *RemoteSessionManager {
	return &RemoteSessionManager{
		sessions: make(map[string]*RemoteSession),
	}
}

// globalRemoteSessionManager 全局远程会话管理器
var globalRemoteSessionManager = NewRemoteSessionManager()

// GetRemoteSessionManager 获取全局远程会话管理器
func GetRemoteSessionManager() *RemoteSessionManager {
	return globalRemoteSessionManager
}

// RemoteLoginRequest 远程登录请求
type RemoteLoginRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// RemoteLoginResponse 远程登录响应
type RemoteLoginResponse struct {
	Success    bool   `json:"success"`
	SessionID  string `json:"session_id,omitempty"`
	Error      string `json:"error,omitempty"`
	RemoteUser string `json:"remote_user,omitempty"`
}

// HandleRemoteLogin 处理远程 web-ssh 登录
func HandleRemoteLogin(w http.ResponseWriter, r *http.Request) {
	var req RemoteLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 验证输入
	if req.URL == "" || req.Username == "" || req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "请填写完整的登录信息",
		})
		return
	}

	// 确保 URL 格式正确
	remoteURL := req.URL
	if !strings.HasPrefix(remoteURL, "http://") && !strings.HasPrefix(remoteURL, "https://") {
		remoteURL = "http://" + remoteURL
	}

	// 获取远程服务器的公钥
	pubKeyURL := remoteURL + "/api/public-key"
	pubKeyResp, err := http.Get(pubKeyURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "无法连接到远程服务器: " + err.Error(),
		})
		return
	}
	defer pubKeyResp.Body.Close()

	var pubKeyData map[string]string
	if err := json.NewDecoder(pubKeyResp.Body).Decode(&pubKeyData); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "解析远程服务器响应失败",
		})
		return
	}

	// 构建登录请求
	loginURL := remoteURL + "/api/auth/login"
	loginPayload := map[string]string{
		"username": req.Username,
		"password": req.Password,
	}

	// 如果远程服务器支持加密，可以使用加密
	// 这里简化处理，直接发送明文密码（实际生产环境应该加密）
	loginJSON, _ := json.Marshal(loginPayload)

	loginReq, err := http.NewRequest("POST", loginURL, bytes.NewBuffer(loginJSON))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "创建登录请求失败",
		})
		return
	}
	loginReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	loginResp, err := client.Do(loginReq)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "登录远程服务器失败: " + err.Error(),
		})
		return
	}
	defer loginResp.Body.Close()

	var loginResult map[string]interface{}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginResult); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "解析登录响应失败",
		})
		return
	}

	// 检查登录是否成功
	if success, ok := loginResult["success"].(bool); !ok || !success {
		errMsg := "登录失败"
		if e, ok := loginResult["error"].(string); ok {
			errMsg = e
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   errMsg,
		})
		return
	}

	// 获取 session cookie
	var sessionCookie string
	for _, cookie := range loginResp.Cookies() {
		if cookie.Name == "session_id" {
			sessionCookie = cookie.Value
			break
		}
	}

	if sessionCookie == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "未获取到远程会话",
		})
		return
	}

	// 创建远程会话
	sessionID := generateSessionID()
	remoteSession := &RemoteSession{
		ID:         sessionID,
		URL:        remoteURL,
		Cookie:     sessionCookie,
		Username:   req.Username,
		LastActive: time.Now(),
	}

	sm := GetRemoteSessionManager()
	sm.mu.Lock()
	sm.sessions[sessionID] = remoteSession
	sm.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(RemoteLoginResponse{
		Success:    true,
		SessionID:  sessionID,
		RemoteUser: req.Username,
	})

	log.Printf("Remote login successful: %s -> %s (session: %s)", req.Username, remoteURL, sessionID)
}

// HandleRemoteDisconnect 断开远程会话
func HandleRemoteDisconnect(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	sm := GetRemoteSessionManager()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, ok := sm.sessions[sessionID]; ok {
		if session.RemoteConn != nil {
			session.RemoteConn.Close()
		}
		delete(sm.sessions, sessionID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// HandleRemoteTerminal 处理远程终端 WebSocket 代理
func HandleRemoteTerminal(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	sm := GetRemoteSessionManager()
	sm.mu.RLock()
	remoteSession, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// 升级本地 WebSocket 连接
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	localConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer localConn.Close()

	// 连接到远程 WebSocket
	remoteWsURL := strings.Replace(remoteSession.URL, "http://", "ws://", 1)
	remoteWsURL = strings.Replace(remoteWsURL, "https://", "wss://", 1)
	remoteWsURL += "/ws/terminal?mode=local"

	log.Printf("Connecting to remote WebSocket: %s", remoteWsURL)

	// 创建带有 Cookie 的请求头
	header := http.Header{}
	header.Set("Cookie", "session_id="+remoteSession.Cookie)

	remoteConn, _, err := websocket.DefaultDialer.Dial(remoteWsURL, header)
	if err != nil {
		log.Printf("Failed to connect to remote WebSocket: %v", err)
		localConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"无法连接到远程服务器: `+err.Error()+`"}`))
		return
	}
	defer remoteConn.Close()

	log.Printf("Remote WebSocket connected successfully")

	remoteSession.mu.Lock()
	remoteSession.RemoteConn = remoteConn
	remoteSession.mu.Unlock()

	// 双向数据转发
	done := make(chan struct{})

	// 本地 -> 远程
	go func() {
		defer close(done)
		for {
			_, message, err := localConn.ReadMessage()
			if err != nil {
				return
			}
			remoteConn.WriteMessage(websocket.BinaryMessage, message)
		}
	}()

	// 远程 -> 本地
	for {
		select {
		case <-done:
			return
		default:
			_, message, err := remoteConn.ReadMessage()
			if err != nil {
				return
			}
			localConn.WriteMessage(websocket.BinaryMessage, message)
		}
	}
}

// HandleRemoteFileList 处理远程文件列表
func HandleRemoteFileList(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	path := r.URL.Query().Get("path")

	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	sm := GetRemoteSessionManager()
	sm.mu.RLock()
	remoteSession, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// 请求远程服务器
	remoteURL := remoteSession.URL + "/api/local/file/list?path=" + url.QueryEscape(path)
	req, err := http.NewRequest("GET", remoteURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.AddCookie(&http.Cookie{Name: "session_id", Value: remoteSession.Cookie})

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "请求远程服务器失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 转发响应
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// HandleRemoteFileDownload 处理远程文件下载
func HandleRemoteFileDownload(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	path := r.URL.Query().Get("path")

	if sessionID == "" || path == "" {
		http.Error(w, "session_id and path required", http.StatusBadRequest)
		return
	}

	sm := GetRemoteSessionManager()
	sm.mu.RLock()
	remoteSession, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// 请求远程服务器
	remoteURL := remoteSession.URL + "/api/local/file/download?path=" + url.QueryEscape(path)
	req, err := http.NewRequest("GET", remoteURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.AddCookie(&http.Cookie{Name: "session_id", Value: remoteSession.Cookie})

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "请求远程服务器失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 转发响应头
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if contentDisposition := resp.Header.Get("Content-Disposition"); contentDisposition != "" {
		w.Header().Set("Content-Disposition", contentDisposition)
	}
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		w.Header().Set("Content-Length", contentLength)
	}

	io.Copy(w, resp.Body)
}

// HandleRemoteFileUpload 处理远程文件上传
func HandleRemoteFileUpload(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	path := r.URL.Query().Get("path")

	if sessionID == "" || path == "" {
		http.Error(w, "session_id and path required", http.StatusBadRequest)
		return
	}

	sm := GetRemoteSessionManager()
	sm.mu.RLock()
	remoteSession, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// 解析 multipart form
	err := r.ParseMultipartForm(100 << 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 创建请求到远程服务器
	remoteURL := remoteSession.URL + "/api/local/file/upload?path=" + url.QueryEscape(path)

	// 构建 multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	io.Copy(part, file)
	writer.Close()

	req, err := http.NewRequest("POST", remoteURL, &buf)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "session_id", Value: remoteSession.Cookie})

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "请求远程服务器失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// HandleRemoteFileMkdir 处理远程创建目录
func HandleRemoteFileMkdir(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	path := r.URL.Query().Get("path")

	if sessionID == "" || path == "" {
		http.Error(w, "session_id and path required", http.StatusBadRequest)
		return
	}

	sm := GetRemoteSessionManager()
	sm.mu.RLock()
	remoteSession, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	remoteURL := remoteSession.URL + "/api/local/file/mkdir?path=" + url.QueryEscape(path)
	req, err := http.NewRequest("POST", remoteURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.AddCookie(&http.Cookie{Name: "session_id", Value: remoteSession.Cookie})

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "请求远程服务器失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// HandleRemoteFileRemove 处理远程删除文件
func HandleRemoteFileRemove(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	path := r.URL.Query().Get("path")

	if sessionID == "" || path == "" {
		http.Error(w, "session_id and path required", http.StatusBadRequest)
		return
	}

	sm := GetRemoteSessionManager()
	sm.mu.RLock()
	remoteSession, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	remoteURL := remoteSession.URL + "/api/local/file/remove?path=" + url.QueryEscape(path)
	req, err := http.NewRequest("POST", remoteURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.AddCookie(&http.Cookie{Name: "session_id", Value: remoteSession.Cookie})

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "请求远程服务器失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// ProxyWebSocketInput 代理 WebSocket 输入数据
func ProxyWebSocketInput(input string) (string, error) {
	// Base64 编码输入数据
	return base64.StdEncoding.EncodeToString([]byte(input)), nil
}