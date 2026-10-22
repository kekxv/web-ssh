package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func addRemoteSessionForTest(targetURL string) string {
	id := generateSessionID()
	manager := GetRemoteSessionManager()
	manager.mu.Lock()
	manager.sessions[id] = &RemoteSession{
		ID:     id,
		URL:    targetURL,
		Cookie: "target-cookie",
	}
	manager.mu.Unlock()
	return id
}

func removeRemoteSessionForTest(id string) {
	manager := GetRemoteSessionManager()
	manager.mu.Lock()
	delete(manager.sessions, id)
	manager.mu.Unlock()
}

func TestRemoteSystemInfoProxiesTargetResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "session_id=target-cookie") {
			t.Fatalf("cookie = %q, want remote session cookie", got)
		}
		if got, want := r.URL.Path, "/api/system/info"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"hostname":"remote-host"}`)
	}))
	defer target.Close()

	id := addRemoteSessionForTest(target.URL)
	defer removeRemoteSessionForTest(id)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/remote/system/info?session_id="+url.QueryEscape(id), nil)

	HandleRemoteSystemInfo(recorder, request)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := recorder.Body.String(), `{"hostname":"remote-host"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestRemoteShellsProxiesTargetResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/local/shells"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		_, _ = io.WriteString(w, `{"shells":["/bin/bash","/bin/sh"],"current_shell":"/bin/bash"}`)
	}))
	defer target.Close()

	id := addRemoteSessionForTest(target.URL)
	defer removeRemoteSessionForTest(id)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/remote/shells?session_id="+url.QueryEscape(id), nil)

	HandleRemoteShells(recorder, request)

	if got, want := recorder.Body.String(), `{"shells":["/bin/bash","/bin/sh"],"current_shell":"/bin/bash"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestRemoteDockerActionProxiesOnlyTargetRequest(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/docker/container/restart"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := r.Method, http.MethodPost; got != want {
			t.Fatalf("method = %q, want %q", got, want)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(body), `{"id":"remote-container"}`; got != want {
			t.Fatalf("body = %q, want %q", got, want)
		}
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer target.Close()

	id := addRemoteSessionForTest(target.URL)
	defer removeRemoteSessionForTest(id)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/remote/docker/container/restart?session_id="+url.QueryEscape(id), bytes.NewBufferString(`{"id":"remote-container"}`))
	request.Header.Set("Content-Type", "application/json")

	HandleRemoteDockerRestartContainer(recorder, request)

	if got, want := recorder.Body.String(), `{"success":true}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestRemoteLoginTtydUsesTokenAndStoresSession(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/ttyd/token"; got != want {
			t.Errorf("path = %q, want %q", got, want)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "tty" || pass != "secret" {
			t.Errorf("basic auth = %q/%q/%v, want tty/secret/true", user, pass, ok)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"token":"token-abc"}`)
	}))
	defer target.Close()

	body := `{"url":"` + target.URL + `/ttyd","username":"tty","password":"secret","kind":"ttyd"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/remote/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	HandleRemoteLogin(recorder, request)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	var response RemoteLoginResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success {
		t.Fatalf("login failed: %s", response.Error)
	}
	if got, want := response.Kind, RemoteKindTtyd; got != want {
		t.Fatalf("kind = %q, want %q", got, want)
	}
	if response.SessionID == "" {
		t.Fatal("session_id is empty")
	}

	manager := GetRemoteSessionManager()
	manager.mu.RLock()
	session := manager.sessions[response.SessionID]
	manager.mu.RUnlock()
	defer removeRemoteSessionForTest(response.SessionID)

	if session == nil {
		t.Fatal("ttyd session was not stored")
	}
	if got, want := session.BasePath, "/ttyd"; got != want {
		t.Fatalf("base path = %q, want %q", got, want)
	}
	if got, want := session.Token, "token-abc"; got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
	if session.AuthHeader == "" {
		t.Fatal("authorization header was not stored")
	}
}

func TestRemoteShellsForTtydReturnsFixedShell(t *testing.T) {
	id := generateSessionID()
	manager := GetRemoteSessionManager()
	manager.mu.Lock()
	manager.sessions[id] = &RemoteSession{
		ID:   id,
		Kind: RemoteKindTtyd,
		URL:  "http://ttyd.invalid",
	}
	manager.mu.Unlock()
	defer removeRemoteSessionForTest(id)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/remote/shells?session_id="+url.QueryEscape(id), nil)

	HandleRemoteShells(recorder, request)

	var payload struct {
		Shells []string `json:"shells"`
		Kind   string   `json:"kind"`
		Fixed  bool     `json:"fixed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, want := payload.Kind, RemoteKindTtyd; got != want {
		t.Fatalf("kind = %q, want %q", got, want)
	}
	if !payload.Fixed || len(payload.Shells) != 0 {
		t.Fatalf("unexpected shell payload: %+v", payload)
	}
}

func TestTtydTerminalBridgesFrames(t *testing.T) {
	received := make(chan []byte, 4)

	ttydServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/ws"; got != want {
			t.Errorf("ttyd path = %q, want %q", got, want)
			return
		}
		if got := websocket.Subprotocols(r); len(got) != 1 || got[0] != "tty" {
			t.Errorf("subprotocols = %v, want [tty]", got)
			return
		}
		if got, want := r.Header.Get("Authorization"), "Basic dHR5OnNlY3JldA=="; got != want {
			t.Errorf("authorization = %q, want %q", got, want)
			return
		}
		if got, want := r.Header.Get("Origin"), "http://"+r.Host; got != want {
			t.Errorf("origin = %q, want %q", got, want)
			return
		}

		upgrader := websocket.Upgrader{
			Subprotocols: []string{"tty"},
			CheckOrigin:  func(r *http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		_, first, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read initial frame: %v", err)
			return
		}
		received <- first

		// ttyd 在 PTY 启动后会先发送标题/偏好帧，这两类帧不应写入终端。
		_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{'0'}, []byte("hello")...))
		_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{'1'}, []byte("title")...))
		_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{'2'}, []byte(`{"x":1}`)...))

		for i := 0; i < 2; i++ {
			_, message, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read client frame %d: %v", i, err)
				return
			}
			received <- message
		}
	}))
	defer ttydServer.Close()

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handleTtydTerminal(conn, &RemoteSession{
			ID:         "test",
			Kind:       RemoteKindTtyd,
			URL:        ttydServer.URL,
			AuthHeader: "Basic dHR5OnNlY3JldA==",
			Token:      "token-abc",
		})
	}))
	defer gateway.Close()

	wsURL := "ws" + strings.TrimPrefix(gateway.URL, "http") + "/"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"input","data":"ls\n"}`)); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, output, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read terminal output: %v", err)
	}
	if got, want := string(output), "hello"; got != want {
		t.Fatalf("terminal output = %q, want %q", got, want)
	}

	first := <-received
	var initial struct {
		AuthToken string `json:"AuthToken"`
		Columns   int    `json:"columns"`
		Rows      int    `json:"rows"`
	}
	if err := json.Unmarshal(first, &initial); err != nil {
		t.Fatalf("decode initial frame: %v", err)
	}
	if initial.AuthToken != "token-abc" || initial.Columns != 80 || initial.Rows != 24 {
		t.Fatalf("initial frame = %+v", initial)
	}

	inputFrame := <-received
	if got, want := string(inputFrame), "0ls\n"; got != want {
		t.Fatalf("input frame = %q, want %q", got, want)
	}
	resizeFrame := <-received
	if got, want := string(resizeFrame), `1{"columns":120,"rows":40}`; got != want {
		t.Fatalf("resize frame = %q, want %q", got, want)
	}
}
