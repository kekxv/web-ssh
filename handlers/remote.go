package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

func remoteSessionRequest(r *http.Request, method, targetPath string, allowedQueryKeys ...string) (*http.Response, error) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		return nil, fmt.Errorf("session_id required")
	}

	manager := GetRemoteSessionManager()
	manager.mu.RLock()
	remoteSession, ok := manager.sessions[sessionID]
	manager.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	targetURL, err := url.Parse(strings.TrimRight(remoteSession.URL, "/") + targetPath)
	if err != nil {
		return nil, fmt.Errorf("invalid remote URL: %w", err)
	}
	query := url.Values{}
	for _, key := range allowedQueryKeys {
		if value := r.URL.Query().Get(key); value != "" {
			query.Set(key, value)
		}
	}
	targetURL.RawQuery = query.Encode()

	request, err := http.NewRequest(method, targetURL.String(), r.Body)
	if err != nil {
		return nil, err
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.AddCookie(&http.Cookie{Name: "session_id", Value: remoteSession.Cookie})

	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求远程服务器失败: %w", err)
	}
	return response, nil
}

func proxyRemoteResponse(w http.ResponseWriter, r *http.Request, method, targetPath string, allowedQueryKeys ...string) {
	response, err := remoteSessionRequest(r, method, targetPath, allowedQueryKeys...)
	if err != nil {
		if err.Error() == "session_id required" {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err.Error() == "session not found" {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()

	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	if _, err := io.Copy(w, response.Body); err != nil {
		log.Printf("failed to proxy remote response for %s: %v", targetPath, err)
	}
}

func HandleRemoteShells(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodGet, "/api/local/shells")
}

func HandleRemoteSystemInfo(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodGet, "/api/system/info")
}

func HandleRemoteDockerAvailable(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodGet, "/api/docker/available")
}

func HandleRemoteDockerListContainers(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodGet, "/api/docker/containers")
}

func HandleRemoteDockerListImages(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodGet, "/api/docker/images")
}

func HandleRemoteDockerStartContainer(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodPost, "/api/docker/container/start")
}

func HandleRemoteDockerStopContainer(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodPost, "/api/docker/container/stop")
}

func HandleRemoteDockerRestartContainer(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodPost, "/api/docker/container/restart")
}

func HandleRemoteDockerRemoveContainer(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodPost, "/api/docker/container/remove")
}

func HandleRemoteDockerContainerLogs(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodGet, "/api/docker/container/logs", "id", "tail")
}

func HandleRemoteDockerContainerLogSize(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodGet, "/api/docker/container/log-size", "id")
}

func HandleRemoteDockerClearLogs(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodPost, "/api/docker/container/clear-logs")
}

func HandleRemoteDockerContainerStats(w http.ResponseWriter, r *http.Request) {
	proxyRemoteResponse(w, r, http.MethodGet, "/api/docker/container/stats", "id")
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
	remoteWS, err := url.Parse(strings.TrimRight(remoteWsURL, "/") + "/ws/terminal")
	if err != nil {
		localConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"远程服务器地址无效"}`))
		return
	}
	query := url.Values{}
	query.Set("mode", "local")
	if shell := r.URL.Query().Get("shell"); shell != "" {
		query.Set("shell", shell)
	}
	remoteWS.RawQuery = query.Encode()
	remoteWsURL = remoteWS.String()

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

	w.WriteHeader(resp.StatusCode)
	if err := copyDownloadSnapshot(w, resp.Body, resp.ContentLength); err != nil {
		log.Printf("failed to stream remote download %s: %v", path, err)
	}
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

	file, err := uploadedFilePart(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 创建请求到远程服务器
	remoteURL := remoteSession.URL + "/api/local/file/upload?path=" + url.QueryEscape(path)

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	req, err := http.NewRequest("POST", remoteURL, pr)
	if err != nil {
		file.Close()
		pr.Close()
		pw.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "session_id", Value: remoteSession.Cookie})

	uploadErrCh := make(chan error, 1)
	go func() {
		defer file.Close()

		part, err := writer.CreateFormFile("file", file.FileName())
		if err != nil {
			writer.Close()
			pw.CloseWithError(err)
			uploadErrCh <- err
			return
		}

		if _, err := io.Copy(part, file); err != nil {
			writer.Close()
			pw.CloseWithError(err)
			uploadErrCh <- err
			return
		}

		if err := writer.Close(); err != nil {
			pw.CloseWithError(err)
			uploadErrCh <- err
			return
		}

		uploadErrCh <- pw.Close()
	}()

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		pr.Close()
		go logAsyncUploadError(uploadErrCh)
		http.Error(w, "请求远程服务器失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("failed to copy remote upload response: %v", err)
	}
	select {
	case uploadErr := <-uploadErrCh:
		if uploadErr != nil {
			log.Printf("failed to stream upload to remote server: %v", uploadErr)
		}
	default:
	}
}

func logAsyncUploadError(uploadErrCh <-chan error) {
	if uploadErr := <-uploadErrCh; uploadErr != nil {
		log.Printf("failed to stream upload to remote server: %v", uploadErr)
	}
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
