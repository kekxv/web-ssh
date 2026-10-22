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

const (
	RemoteKindWebSSH = "webssh"
	RemoteKindTtyd   = "ttyd"
)

// RemoteSession 管理远程 web-ssh / ttyd 会话
type RemoteSession struct {
	ID         string
	Kind       string // "webssh" 或 "ttyd"
	URL        string
	BasePath   string // ttyd --base-path，例如 /terminal
	Cookie     string // web-ssh 的 session cookie
	AuthHeader string // ttyd 的 Authorization 头（目前支持 Basic）
	Token      string // ttyd /token 返回的 AuthToken
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
	Kind     string `json:"kind,omitempty"` // webssh / ttyd / auto
}

// RemoteLoginResponse 远程登录响应
type RemoteLoginResponse struct {
	Success    bool   `json:"success"`
	Kind       string `json:"kind,omitempty"`
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
	sessionID := r.URL.Query().Get("session_id")
	manager := GetRemoteSessionManager()
	manager.mu.RLock()
	remoteSession, ok := manager.sessions[sessionID]
	manager.mu.RUnlock()
	if ok && remoteSession.Kind == RemoteKindTtyd {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"shells":        []string{},
			"current_shell": "",
			"kind":          RemoteKindTtyd,
			"fixed":         true,
		})
		return
	}
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

func normalizeRemoteURL(raw string) string {
	remoteURL := strings.TrimSpace(raw)
	if !strings.HasPrefix(remoteURL, "http://") && !strings.HasPrefix(remoteURL, "https://") {
		remoteURL = "http://" + remoteURL
	}
	return strings.TrimRight(remoteURL, "/")
}

func detectRemoteKind(remoteURL string) string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(remoteURL + "/api/public-key")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var payload map[string]json.RawMessage
			if json.NewDecoder(resp.Body).Decode(&payload) == nil {
				if _, ok := payload["public_key"]; ok {
					return RemoteKindWebSSH
				}
			}
		}
	}
	return RemoteKindTtyd
}

func loginTtyd(remoteURL, username, password string) (basePath, token, authHeader string, err error) {
	parsed, err := url.Parse(remoteURL)
	if err != nil {
		return "", "", "", fmt.Errorf("远程地址无效: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", "", fmt.Errorf("不支持的远程协议: %s", parsed.Scheme)
	}

	basePath = strings.TrimRight(parsed.Path, "/")
	if basePath == "/" {
		basePath = ""
	}

	tokenURL := parsed.Scheme + "://" + parsed.Host + basePath + "/token"
	req, err := http.NewRequest(http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", "", "", err
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
		authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("无法连接到 ttyd 服务器: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusProxyAuthRequired {
		return "", "", "", fmt.Errorf("ttyd 认证失败，请检查用户名和密码（当前支持 -c/--credential 模式）")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("ttyd /token 返回状态 %d，请确认地址是 ttyd 服务", resp.StatusCode)
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", "", fmt.Errorf("解析 ttyd /token 响应失败: %w", err)
	}
	return basePath, payload.Token, authHeader, nil
}

// HandleRemoteLogin 处理远程 web-ssh / ttyd 登录
func HandleRemoteLogin(w http.ResponseWriter, r *http.Request) {
	var req RemoteLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.URL) == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "请填写远程服务器地址",
		})
		return
	}

	remoteURL := normalizeRemoteURL(req.URL)
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" || kind == "auto" {
		kind = detectRemoteKind(remoteURL)
	}
	if kind != RemoteKindTtyd {
		kind = RemoteKindWebSSH
	}

	if kind == RemoteKindTtyd {
		basePath, token, authHeader, err := loginTtyd(remoteURL, req.Username, req.Password)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(RemoteLoginResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		sessionID := generateSessionID()
		remoteSession := &RemoteSession{
			ID:         sessionID,
			Kind:       RemoteKindTtyd,
			URL:        remoteURL,
			BasePath:   basePath,
			Token:      token,
			AuthHeader: authHeader,
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
			Kind:       RemoteKindTtyd,
			SessionID:  sessionID,
			RemoteUser: req.Username,
		})

		log.Printf("Remote ttyd login successful: %s -> %s (session: %s)", req.Username, remoteURL, sessionID)
		return
	}

	// web-ssh 远程本地登录仍需要用户名和密码
	if req.Username == "" || req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RemoteLoginResponse{
			Success: false,
			Error:   "请填写完整的登录信息",
		})
		return
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
		Kind:       RemoteKindWebSSH,
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
		Kind:       RemoteKindWebSSH,
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

	if remoteSession.Kind == RemoteKindTtyd {
		handleTtydTerminal(localConn, remoteSession)
		return
	}

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

func ttydWebSocketTarget(remoteURL, basePath string) (string, string, error) {
	parsed, err := url.Parse(remoteURL)
	if err != nil {
		return "", "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("不支持的远程协议: %s", parsed.Scheme)
	}

	wsScheme := "ws"
	originScheme := "http"
	if parsed.Scheme == "https" {
		wsScheme = "wss"
		originScheme = "https"
	}

	if basePath == "" {
		basePath = strings.TrimRight(parsed.Path, "/")
		if basePath == "/" {
			basePath = ""
		}
	}

	wsURL := wsScheme + "://" + parsed.Host + basePath + "/ws"
	origin := originScheme + "://" + parsed.Host
	return wsURL, origin, nil
}

func sendRemoteGatewayError(conn *websocket.Conn, message string) {
	data, _ := json.Marshal(map[string]string{"type": "error", "message": message})
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

func handleTtydTerminal(localConn *websocket.Conn, remoteSession *RemoteSession) {
	wsURL, origin, err := ttydWebSocketTarget(remoteSession.URL, remoteSession.BasePath)
	if err != nil {
		sendRemoteGatewayError(localConn, "ttyd 地址无效: "+err.Error())
		return
	}

	dialer := websocket.Dialer{
		Subprotocols:     []string{"tty"},
		HandshakeTimeout: 15 * time.Second,
	}
	header := http.Header{}
	if remoteSession.AuthHeader != "" {
		header.Set("Authorization", remoteSession.AuthHeader)
	}
	// ttyd -O/--check-origin 会校验 Origin 与 Host 一致。
	header.Set("Origin", origin)

	log.Printf("Connecting to ttyd WebSocket: %s", wsURL)
	remoteConn, resp, err := dialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("Failed to connect to ttyd WebSocket: %v", err)
		sendRemoteGatewayError(localConn, "无法连接到 ttyd 服务器: "+err.Error())
		return
	}
	defer remoteConn.Close()

	remoteSession.mu.Lock()
	remoteSession.RemoteConn = remoteConn
	remoteSession.mu.Unlock()

	// ttyd 必须收到首帧 JSON_DATA 才会创建 PTY 进程。
	initial, _ := json.Marshal(map[string]interface{}{
		"AuthToken": remoteSession.Token,
		"columns":   80,
		"rows":      24,
	})
	if err := remoteConn.WriteMessage(websocket.BinaryMessage, initial); err != nil {
		sendRemoteGatewayError(localConn, "初始化 ttyd 会话失败: "+err.Error())
		return
	}

	log.Printf("ttyd WebSocket connected successfully")
	relayTtydTerminal(localConn, remoteConn)
}

func relayTtydTerminal(localConn, remoteConn *websocket.Conn) {
	done := make(chan struct{}, 2)

	// 本地浏览器 -> ttyd：JSON 控制帧转换为 ttyd 的 "1 字节命令 + 载荷" 帧
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, message, err := localConn.ReadMessage()
			if err != nil {
				return
			}

			var msg struct {
				Type string `json:"type"`
				Data string `json:"data"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}

			switch msg.Type {
			case "input":
				payload := make([]byte, 0, len(msg.Data)+1)
				payload = append(payload, '0')
				payload = append(payload, msg.Data...)
				if err := remoteConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
					return
				}
			case "resize":
				body, _ := json.Marshal(map[string]int{
					"columns": msg.Cols,
					"rows":    msg.Rows,
				})
				payload := make([]byte, 0, len(body)+1)
				payload = append(payload, '1')
				payload = append(payload, body...)
				if err := remoteConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
					return
				}
			}
		}
	}()

	// ttyd -> 本地浏览器：去掉命令字节，只把终端输出交给 xterm。
	// '1'（窗口标题）和 '2'（客户端偏好）在这里忽略，ttyd 模式只支持终端。
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, message, err := remoteConn.ReadMessage()
			if err != nil {
				return
			}
			if len(message) == 0 {
				continue
			}
			switch message[0] {
			case '0':
				if len(message) > 1 {
					if err := localConn.WriteMessage(websocket.BinaryMessage, message[1:]); err != nil {
						return
					}
				}
			case '1', '2':
				// ttyd 的标题/偏好设置帧，终端模式下忽略。
			default:
				log.Printf("Ignoring unknown ttyd frame type: %q", message[0])
			}
		}
	}()

	<-done
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
