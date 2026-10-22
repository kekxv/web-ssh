package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// startTtydForTest 启动真实的 ttyd 二进制。未安装 ttyd 时跳过测试；
// 可通过 TTYD_BIN 指定二进制路径。
func startTtydForTest(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("ttyd integration test currently uses /bin/cat")
	}

	bin := os.Getenv("TTYD_BIN")
	if bin == "" {
		var err error
		bin, err = exec.LookPath("ttyd")
		if err != nil {
			t.Skip("ttyd not found; install ttyd or set TTYD_BIN to run this integration test")
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve ttyd port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	cmd := exec.Command(bin,
		"-W",
		"-p", strconv.Itoa(port),
		"-c", "tty:secret",
		"-b", "/ttyd",
		"/bin/cat",
	)
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ttyd: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d/ttyd", port)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)

	for {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/token", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("tty", "secret")

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return baseURL
			}
		}

		if time.Now().After(deadline) {
			t.Fatalf("ttyd did not become ready: %v\n%s", err, logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTtydIntegrationWithRealServer(t *testing.T) {
	baseURL := startTtydForTest(t)

	loginBody := fmt.Sprintf(`{"url":%q,"username":"tty","password":"secret","kind":"ttyd"}`, baseURL)
	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/remote/login", strings.NewReader(loginBody))
	loginRequest.Header.Set("Content-Type", "application/json")

	HandleRemoteLogin(loginRecorder, loginRequest)

	var login RemoteLoginResponse
	if err := json.Unmarshal(loginRecorder.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !login.Success {
		t.Fatalf("ttyd login failed: %s", login.Error)
	}
	if login.Kind != RemoteKindTtyd {
		t.Fatalf("login kind = %q, want %q", login.Kind, RemoteKindTtyd)
	}
	defer removeRemoteSessionForTest(login.SessionID)

	gateway := httptest.NewServer(http.HandlerFunc(HandleRemoteTerminal))
	defer gateway.Close()

	wsURL := "ws" + strings.TrimPrefix(gateway.URL, "http") +
		"/ws/remote/terminal?session_id=" + url.QueryEscape(login.SessionID)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial web-ssh gateway: %v", err)
	}
	defer conn.Close()

	if err := writeTtydInput(conn, "ttyd-real-server-ok\n"); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if got := readTtydOutputUntil(t, conn, "ttyd-real-server-ok", 5*time.Second); !strings.Contains(got, "ttyd-real-server-ok") {
		t.Fatalf("terminal output = %q, want marker", got)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(`{"type":"resize","cols":100,"rows":30}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	if err := writeTtydInput(conn, "after-resize\n"); err != nil {
		t.Fatalf("write input after resize: %v", err)
	}
	if got := readTtydOutputUntil(t, conn, "after-resize", 5*time.Second); !strings.Contains(got, "after-resize") {
		t.Fatalf("terminal output after resize = %q, want marker", got)
	}
}

func writeTtydInput(conn *websocket.Conn, data string) error {
	payload, err := json.Marshal(map[string]string{"type": "input", "data": data})
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, payload)
}

func readTtydOutputUntil(t *testing.T, conn *websocket.Conn, marker string, timeout time.Duration) string {
	t.Helper()

	var output strings.Builder
	deadline := time.Now().Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, message, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read terminal output: %v; output=%q", err, output.String())
		}
		output.Write(message)
		if strings.Contains(output.String(), marker) {
			return output.String()
		}
	}
}
