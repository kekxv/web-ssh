package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
