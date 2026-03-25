package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"web-ssh/handlers"
)

var (
	localServerURL  string
	remoteServerURL string
	localClient     *http.Client
	remoteClient    *http.Client
	testUsername    string = "admin"
	testPassword    string = "admin123"
	usersBackup     []byte
)

// backupUsers 备份 users.json
func backupUsers() {
	data, err := os.ReadFile("users.json")
	if err == nil {
		usersBackup = data
		os.Remove("users.json")
	}
}

// restoreUsers 恢复 users.json
func restoreUsers() {
	if len(usersBackup) > 0 {
		os.WriteFile("users.json", usersBackup, 0644)
	}
}

// TestMain 设置测试环境
func TestMain(m *testing.M) {
	// 备份并删除 users.json，让服务器创建默认用户
	backupUsers()

	// 启动本地服务器 (端口 18080)
	localPort := 18080
	localServerURL = fmt.Sprintf("http://127.0.0.1:%d", localPort)
	go func() {
		r := SetupServer(localPort)
		r.Run(fmt.Sprintf(":%d", localPort))
	}()

	// 启动远程服务器 (端口 18081)
	remotePort := 18081
	remoteServerURL = fmt.Sprintf("http://127.0.0.1:%d", remotePort)
	go func() {
		r := SetupServer(remotePort)
		r.Run(fmt.Sprintf(":%d", remotePort))
	}()

	// 等待服务器启动
	time.Sleep(500 * time.Millisecond)

	// 创建 HTTP 客户端
	jar1, _ := cookiejar.New(nil)
	jar2, _ := cookiejar.New(nil)
	localClient = &http.Client{Jar: jar1, Timeout: 10 * time.Second}
	remoteClient = &http.Client{Jar: jar2, Timeout: 10 * time.Second}

	// 运行测试
	code := m.Run()

	// 恢复 users.json
	restoreUsers()

	os.Exit(code)
}

// TestHealthCheck 测试服务器健康检查
func TestHealthCheck(t *testing.T) {
	// 测试本地服务器
	resp, err := localClient.Get(localServerURL + "/api/public-key")
	if err != nil {
		t.Fatalf("本地服务器无法访问: %v", err)
	}
	resp.Body.Close()
	t.Logf("✓ 本地服务器正常 (%s)", localServerURL)

	// 测试远程服务器
	resp, err = remoteClient.Get(remoteServerURL + "/api/public-key")
	if err != nil {
		t.Fatalf("远程服务器无法访问: %v", err)
	}
	resp.Body.Close()
	t.Logf("✓ 远程服务器正常 (%s)", remoteServerURL)
}

// TestLocalLogin 测试本地登录
func TestLocalLogin(t *testing.T) {
	loginURL := localServerURL + "/api/auth/login"
	payload := map[string]string{
		"username": testUsername,
		"password": testPassword,
	}
	jsonData, _ := json.Marshal(payload)

	resp, err := localClient.Post(loginURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	t.Logf("登录响应: %s", string(body))

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if success, ok := result["success"].(bool); !ok || !success {
		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("登录失败: %s", errMsg)
		}
		t.Fatalf("登录失败: %v", result)
	}

	t.Logf("✓ 本地登录成功 (用户: %s)", testUsername)
}

// TestRemoteLoginDirect 直接测试远程服务器登录
func TestRemoteLoginDirect(t *testing.T) {
	loginURL := remoteServerURL + "/api/auth/login"
	payload := map[string]string{
		"username": testUsername,
		"password": testPassword,
	}
	jsonData, _ := json.Marshal(payload)

	resp, err := remoteClient.Post(loginURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	t.Logf("登录响应: %s", string(body))

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if success, ok := result["success"].(bool); !ok || !success {
		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("登录失败: %s", errMsg)
		}
		t.Fatalf("登录失败: %v", result)
	}

	t.Logf("✓ 远程登录成功 (用户: %s)", testUsername)
}

// TestRemoteProxy 完整测试远程代理功能
func TestRemoteProxy(t *testing.T) {
	// Step 1: 登录本地服务器
	t.Log("=== Step 1: 登录本地服务器 ===")
	loginURL := localServerURL + "/api/auth/login"
	payload := map[string]string{
		"username": testUsername,
		"password": testPassword,
	}
	jsonData, _ := json.Marshal(payload)

	resp, err := localClient.Post(loginURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		t.Fatalf("登录本地服务器失败: %v", err)
	}
	resp.Body.Close()

	// Step 2: 通过代理登录远程服务器
	t.Log("=== Step 2: 通过代理登录远程服务器 ===")
	remoteLoginURL := localServerURL + "/api/remote/login"
	remotePayload := map[string]string{
		"url":      remoteServerURL,
		"username": testUsername,
		"password": testPassword,
	}
	remoteJSON, _ := json.Marshal(remotePayload)

	remoteLoginResp, err := localClient.Post(remoteLoginURL, "application/json", bytes.NewBuffer(remoteJSON))
	if err != nil {
		t.Fatalf("远程登录请求失败: %v", err)
	}
	defer remoteLoginResp.Body.Close()

	var remoteLoginResult map[string]interface{}
	body, _ := io.ReadAll(remoteLoginResp.Body)
	json.Unmarshal(body, &remoteLoginResult)

	t.Logf("远程登录响应状态: %d", remoteLoginResp.StatusCode)
	t.Logf("远程登录响应: %s", string(body))

	if remoteLoginResp.StatusCode != 200 {
		t.Fatalf("远程登录失败，状态码: %d, 响应: %s", remoteLoginResp.StatusCode, string(body))
	}

	sessionID, ok := remoteLoginResult["session_id"].(string)
	if !ok || sessionID == "" {
		t.Fatal("未获取到远程会话 ID")
	}
	t.Logf("✓ 获取到远程会话 ID: %s", sessionID)

	// Step 3: 测试远程文件列表
	t.Log("=== Step 3: 测试远程文件列表 ===")
	fileListURL := localServerURL + "/api/remote/file/list?session_id=" + url.QueryEscape(sessionID) + "&path=~"

	fileListResp, err := localClient.Get(fileListURL)
	if err != nil {
		t.Fatalf("远程文件列表请求失败: %v", err)
	}
	defer fileListResp.Body.Close()

	body, _ = io.ReadAll(fileListResp.Body)
	t.Logf("文件列表响应状态: %d", fileListResp.StatusCode)

	var fileListResult map[string]interface{}
	json.Unmarshal(body, &fileListResult)

	if fileListResp.StatusCode != 200 {
		t.Errorf("远程文件列表请求失败，响应: %s", string(body))
	} else {
		t.Logf("✓ 远程文件列表获取成功")
		if data, ok := fileListResult["data"].([]interface{}); ok {
			t.Logf("  文件数量: %d", len(data))
		}
	}

	// Step 4: 断开远程会话
	t.Log("=== Step 4: 断开远程会话 ===")
	disconnectURL := localServerURL + "/api/remote/disconnect?session_id=" + url.QueryEscape(sessionID)
	disconnectResp, err := localClient.Post(disconnectURL, "application/json", nil)
	if err != nil {
		t.Logf("断开连接请求失败: %v", err)
	} else {
		disconnectResp.Body.Close()
		t.Logf("✓ 远程会话已断开")
	}
}

// TestWebSocketURL 测试 WebSocket URL 构建
func TestWebSocketURL(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"http://127.0.0.1:8080", "ws://127.0.0.1:8080/ws/terminal?mode=local"},
		{"https://example.com", "wss://example.com/ws/terminal?mode=local"},
		{"http://192.168.1.100:8080", "ws://192.168.1.100:8080/ws/terminal?mode=local"},
	}

	for _, tc := range testCases {
		wsURL := strings.Replace(tc.input, "http://", "ws://", 1)
		wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
		wsURL += "/ws/terminal?mode=local"

		if wsURL != tc.expected {
			t.Errorf("URL 转换错误: input=%s, got=%s, expected=%s", tc.input, wsURL, tc.expected)
		} else {
			t.Logf("✓ %s -> %s", tc.input, wsURL)
		}
	}
}

// TestRemoteSessionManager 测试远程会话管理器
func TestRemoteSessionManager(t *testing.T) {
	sm := handlers.GetRemoteSessionManager()

	// 使用登录 API 来创建会话，而不是直接操作内部结构
	// 这里只测试管理器是否存在
	if sm == nil {
		t.Fatal("远程会话管理器为空")
	}

	t.Log("✓ 远程会话管理器存在")
}