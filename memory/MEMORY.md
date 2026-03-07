# Web SSH 堡垒机 - 项目状态

## 项目概述
基于 Go + Vue3 的网页版堡垒机系统，提供 SSH 远程终端和 SFTP 文件管理功能。

## 已实现功能

### 核心功能
- [x] 用户认证系统（SHA256 加密密码，支持会话管理）
- [x] SSH 远程连接（支持密码和私钥认证）
- [x] 跳板机支持（最多 4 层跳转，SSH 隧道，支持密码/私钥/私钥密码）
- [x] Web 终端（基于 xterm.js，WebSocket 通信）
- [x] SFTP 文件管理（上传/下载/删除/新建目录）
- [x] 本地 Bash 模式（支持 HTTP 长连接降级）
- [x] 本地文件管理

### 安全特性
- [x] RSA-AES 混合加密传输密码
- [x] 会话 Cookie 认证（HttpOnly，24 小时过期）
- [x] 用户密码修改功能
- [x] 管理员用户管理（添加/删除/列出用户）

### 技术栈
- **后端**: Go 1.25+, Gin, golang.org/x/crypto/ssh, gorilla/websocket, pkg/sftp, creack/pty
- **前端**: Vue 3, xterm.js, xterm-addon-fit, TailwindCSS

## 项目结构
```
web-ssh/
├── main.go              # 程序入口，路由配置
├── go.mod / go.sum      # Go 模块依赖
├── handlers/
│   ├── ssh.go          # SSH 连接处理，跳板机逻辑
│   ├── terminal.go     # WebSocket 终端处理
│   ├── sftp.go         # SFTP 文件操作
│   └── auth.go         # 用户认证，会话管理
├── models/
│   └── connection.go   # 数据模型定义
└── static/
    ├── index.html      # 主页面
    └── js/
        └── app.js      # Vue 前端逻辑
```

## 运行方式
```bash
go build -o web-ssh .
./web-ssh
# 访问 http://localhost:8080
# 默认账号：admin / admin123
```

## 注意事项
1. 跳板机密码使用 RSA-OAEP + AES-GCM 混合加密
2. SSH 跳板机通过 `ssh.Dial` 逐层建立隧道连接
3. 本地模式支持 WebSocket 和 HTTP 长连接两种降级方案
4. 前端资源使用本地 vendor 目录（Vue, xterm.js, Tailwind Play CDN 脚本）

## 待改进项
- [ ] 密码哈希改用 bcrypt/argon2
- [ ] 支持会话持久化（当前为内存存储）
- [ ] 添加连接历史记录功能
- [ ] 支持多标签终端
- [ ] 添加密钥对文件上传 UI
