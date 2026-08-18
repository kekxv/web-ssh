# Remote Target Status and Chrome 75 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show system and Docker information for the remote Web-SSH target, allow choosing an available target shell, and render correctly in Chrome 75 without emoji icon dependencies.

**Architecture:** The local service will authenticate and proxy a constrained set of read/write management APIs to the active remote Web-SSH session, forwarding only the remote session cookie. The browser selects a shell returned by that same remote target, sends it through the terminal proxy, and displays target management controls only for local or remote-Web-SSH sessions. Static, locally built CSS and a Chrome-75-compatible local Vue bundle replace browser-time Tailwind compilation and incompatible runtime syntax.

**Tech Stack:** Go 1.25, Gin, Gorilla WebSocket, Go standard testing, Vue global build, Tailwind CSS 3.4.1.

## Global Constraints

- Remote Web-SSH system and Docker requests must never execute against the intermediary host.
- Direct SSH mode must hide system information and Docker management unless remote implementations are explicitly added later.
- Shell choices must come from `/api/local/shells` on the remote Web-SSH target and must include an actually usable fallback.
- Chrome 75 must not receive JavaScript containing optional chaining (`?.`) or nullish coalescing (`??`).
- UI actions and file-type labels use text rather than emoji or an external icon service.
- No external icon or stylesheet CDN may be introduced.

---

### Task 1: Proxy target capabilities, system information, and Docker APIs

**Files:**
- Modify: `handlers/remote.go`
- Modify: `server.go`
- Test: `handlers/remote_test.go`

**Interfaces:**
- Produces `HandleRemoteShells`, `HandleRemoteSystemInfo`, and Docker proxy handlers accepting `session_id`.
- Produces a shared `remoteSessionRequest(r, method, path, body)` helper which sends the saved remote `session_id` cookie and propagates target status, content type, and JSON response body.
- Consumes the target's existing `/api/local/shells`, `/api/system/info`, and `/api/docker/*` endpoints.

- [x] **Step 1: Write failing proxy tests**

```go
func TestRemoteSystemInfoProxiesTargetResponse(t *testing.T) {
    target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if got := r.Header.Get("Cookie"); !strings.Contains(got, "session_id=target-cookie") { t.Fatalf("cookie = %q", got) }
        if r.URL.Path != "/api/system/info" { t.Fatalf("path = %s", r.URL.Path) }
        w.Header().Set("Content-Type", "application/json")
        io.WriteString(w, `{"hostname":"remote-host"}`)
    }))
    defer target.Close()
    id := addRemoteSessionForTest(target.URL, "target-cookie")
    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/api/remote/system/info?session_id="+url.QueryEscape(id), nil)
    HandleRemoteSystemInfo(rec, req)
    if got := rec.Body.String(); got != `{"hostname":"remote-host"}` { t.Fatalf("body = %s", got) }
}
```

- [x] **Step 2: Run the new tests and verify they fail because the handlers do not exist**

Run: `/usr/local/go/bin/go test ./handlers -run '^TestRemote(SystemInfo|Shells|Docker).*' -count=1`

Expected: FAIL with undefined proxy handler symbols.

- [x] **Step 3: Add the request helper, constrained proxy handlers, and authenticated routes**

```go
protectedApi.GET("/remote/system/info", func(c *gin.Context) { handlers.HandleRemoteSystemInfo(c.Writer, c.Request) })
protectedApi.GET("/remote/shells", func(c *gin.Context) { handlers.HandleRemoteShells(c.Writer, c.Request) })
protectedApi.GET("/remote/docker/available", func(c *gin.Context) { handlers.HandleRemoteDockerAvailable(c.Writer, c.Request) })
```

Use the same helper for list/images/log/stats and start/stop/restart/remove/clear-log requests, keeping every target path constant and accepting only `session_id`, request body, and existing Docker query parameters.

- [x] **Step 4: Run focused and repository Go tests**

Run: `/usr/local/go/bin/go test ./handlers -count=1 && /usr/local/go/bin/go test ./... -count=1`

Expected: PASS.

### Task 2: Pass the selected remote shell and make `sh` launch safely

**Files:**
- Modify: `handlers/remote.go`
- Modify: `handlers/terminal_unix.go`
- Test: `handlers/terminal_unix_test.go`

**Interfaces:**
- Consumes a `shell` query parameter for `/ws/remote/terminal`.
- Produces `localShellCommand(shell string) (*exec.Cmd, error)` to select shell-specific launch arguments.
- `zsh` and `bash` start with `--login`; `sh` starts without unsupported login arguments.

- [x] **Step 1: Write failing shell command tests**

```go
func TestLocalShellCommandDoesNotPassLoginFlagToSh(t *testing.T) {
    cmd, err := localShellCommand("/bin/sh")
    if err != nil { t.Fatal(err) }
    if len(cmd.Args) != 1 || cmd.Args[0] != "/bin/sh" { t.Fatalf("args = %#v", cmd.Args) }
}
```

- [x] **Step 2: Run the shell test and verify it fails because the helper is missing**

Run: `/usr/local/go/bin/go test ./handlers -run '^TestLocalShellCommand' -count=1`

Expected: FAIL with `undefined: localShellCommand`.

- [x] **Step 3: Implement command selection and append validated shell to the target WebSocket URL**

```go
remoteWsURL += "/ws/terminal?mode=local&shell=" + url.QueryEscape(r.URL.Query().Get("shell"))
```

Use `url.Values` rather than string concatenation so target URLs with query strings remain valid.

- [x] **Step 4: Run focused and repository Go tests**

Run: `/usr/local/go/bin/go test ./handlers -count=1 && /usr/local/go/bin/go test ./... -count=1`

Expected: PASS.

### Task 3: Select target endpoints and shell in the UI, and hide unsupported SSH panels

**Files:**
- Modify: `static/index.html`
- Modify: `static/js/app.js`
- Test: `static/js/app.test.js`

**Interfaces:**
- Produces `managementEndpoint(path)` returning `/api/remote/<path>?session_id=...` for remote-Web-SSH and `/api/<path>` for local mode.
- Produces `managementSupported`, true only in local and remote-Web-SSH sessions.
- Produces `remoteShell` and `remoteAvailableShells`; `connectRemoteLocal` loads them before terminal connection.

- [x] **Step 1: Write failing source-level behavior tests**

```js
test('remote management endpoints include the proxy session id', () => {
  assert.equal(managementEndpoint('system/info'), '/api/remote/system/info?session_id=remote-id')
})

test('direct SSH does not support management panels', () => {
  assert.equal(managementSupported({ isLocalMode: false, isRemoteLocalMode: false }), false)
})
```

- [x] **Step 2: Run the JavaScript test and verify it fails before helper extraction**

Run: `node --test static/js/app.test.js`

Expected: FAIL because the tested helpers are not exported/defined.

- [x] **Step 3: Implement endpoint selection, remote shell selection, and conditional UI**

```html
<select v-model="remoteShell" v-if="remoteAvailableShells.length">
  <option v-for="shell in remoteAvailableShells" :key="shell" :value="shell">{{ shell }}</option>
</select>
<div v-else>将自动使用远端可用的 Shell</div>
<div v-if="managementSupported">…系统信息和 Docker 控件…</div>
```

Route every system/Docker fetch and mutation through `managementEndpoint`; on a target proxy failure, clear stale system/Docker state and show no misleading local result. Pass `remoteShell` to `/ws/remote/terminal`.

- [x] **Step 4: Run JavaScript behavior tests**

Run: `node --test static/js/app.test.js`

Expected: PASS.

### Task 4: Build static Chrome-75-compatible assets and replace emoji UI glyphs with text

**Files:**
- Modify: `package.json`
- Modify: `static/index.html`
- Modify: `static/js/app.js`
- Create: `static/css/tailwind.css`
- Modify: `static/vendor/vue.global.prod.js`
- Test: `static/js/app.test.js`

**Interfaces:**
- Produces `npm run build:css`, which compiles `static/src.css` into `static/css/tailwind.css` using Tailwind 3.4.1.
- `index.html` loads only local compiled CSS and a local Vue build compatible with Chrome 75.

- [x] **Step 1: Write failing asset compatibility tests**

```js
test('browser assets do not use unsupported Chrome 75 syntax', () => {
  for (const file of ['static/vendor/vue.global.prod.js', 'static/js/app.js']) {
    assert.equal(fs.readFileSync(file, 'utf8').includes('?.'), false)
    assert.equal(fs.readFileSync(file, 'utf8').includes('??'), false)
  }
})

test('page does not load the Tailwind browser runtime or emoji glyphs', () => {
  const page = fs.readFileSync('static/index.html', 'utf8')
  assert.equal(page.includes('/vendor/tailwind.js'), false)
  assert.equal(/[\u{1F000}-\u{1FAFF}]/u.test(page), false)
})
```

- [x] **Step 2: Run the asset test and verify it fails against current runtime files**

Run: `node --test static/js/app.test.js`

Expected: FAIL because the page loads Tailwind runtime and bundled assets contain unsupported syntax.

- [x] **Step 3: Build assets and replace visual glyphs with text**

```json
"build:css": "tailwindcss -i ./static/src.css -o ./static/css/tailwind.css --minify"
```

Replace the runtime Tailwind script with `/css/tailwind.css`, use a Vue 3 global build that parses in Chrome 75, and replace emoji-only labels/buttons/file labels with Chinese text such as `刷新`, `删除`, `文件夹`, `容器`, and `镜像`.

- [x] **Step 4: Run asset and application checks**

Run: `npm ci && npm run build:css && node --test static/js/app.test.js && /usr/local/go/bin/go test ./... -count=1`

Expected: PASS.

### Task 5: Verify the delivered behavior

**Files:**
- Modify: `README.md`

- [x] **Step 1: Document direct SSH management-panel behavior and Chrome 75 floor**

Add a concise compatibility note: remote Web-SSH proxies system/Docker state; direct SSH hides those panels; Chrome 75 is supported by local static assets.

- [x] **Step 2: Run final verification**

Run: `npm run build:css && node --test static/js/app.test.js && /usr/local/go/bin/go test ./... -count=1 && git diff --check && rg -n '/vendor/tailwind.js|[\x{1F000}-\x{1FAFF}]' static/index.html static/js/app.js || true`

Expected: all tests and checks pass; the final search has no matches.

- [ ] **Step 3: Commit**

```bash
git add handlers server.go static package.json package-lock.json README.md docs/superpowers/plans/2026-08-18-remote-target-chrome75.md
git commit -m "fix: target remote status and support chrome 75"
```

## Self-Review

- Spec coverage: Tasks 1 and 3 prevent local status leakage and proxy all remote Web-SSH Docker operations; Task 2 adds target-shell selection and a safe `sh` launch; Task 4 removes Chrome-75-incompatible browser assets and emoji UI glyphs.
- Placeholder scan: every task names its files, interfaces, commands, and behavior.
- Type consistency: `session_id` is the local proxy session identifier across management and terminal routes; target authentication remains the saved remote cookie.
