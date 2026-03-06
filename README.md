# Web SSH 堡垒机

一个基于 Go 语言的网页版 SSH 堡垒机应用，支持 Web 终端和 SFTP 文件管理。

## 功能特性

1. **Web 终端**
   - 基于 Xterm.js 的终端模拟器
   - 支持 SSH 远程连接
   - 支持本机 Bash 模式

2. **SFTP 文件管理**
   - 文件列表展示
   - 文件上传/下载
   - 目录创建/删除
   - 文件删除
   - 目录导航

3. **连接管理**
   - 支持密码认证
   - 支持私钥认证（可上传或粘贴）
   - 会话管理

## 技术栈

### 后端
- Go 1.21+
- Gin - Web 框架
- golang.org/x/crypto/ssh - SSH 客户端
- gorilla/websocket - WebSocket 通信
- pkg/sftp - SFTP 文件传输
- kr/pty - PTY 管理（本地 Bash）

### 前端
- Vue 3 - 响应式框架
- Xterm.js - 终端模拟器
- Xterm-addon-fit - 终端自适应
- CSS Flexbox - 界面布局

## 快速开始

### 1. 安装依赖

```bash
go mod tidy
```

### 2. 编译

```bash
go build -o web-ssh .
```

### 3. 运行

```bash
./web-ssh
```

### 4. 访问

打开浏览器访问：http://localhost:8080

## 使用说明

### 本机 Bash 模式

1. 选择"本机 Bash"模式
2. 点击"连接"
3. 直接在终端执行命令

### SSH 远程连接

1. 选择"SSH 远程连接"模式
2. 输入服务器信息：
   - 主机地址
   - 端口（默认 22）
   - 用户名
   - 认证方式（密码/私钥）
3. 点击"连接"
4. 左侧显示 SFTP 文件管理器
5. 右侧显示终端

### SFTP 操作

- **浏览目录**: 双击文件夹
- **返回上级**: 点击 ⬆️ 按钮
- **上传文件**: 点击 ⬆️ 图标选择文件
- **下载文件**: 点击文件旁的 ⬇️ 图标
- **新建文件夹**: 点击 📁 图标
- **删除文件**: 点击文件旁的 🗑️ 图标

## 项目结构

```
web-ssh/
├── main.go              # 程序入口
├── go.mod
├── go.sum
├── handlers/
│   ├── ssh.go          # SSH 连接处理
│   ├── terminal.go     # 终端 WebSocket 处理
│   └── sftp.go         # SFTP 文件操作
├── models/
│   └── connection.go   # 连接配置模型
└── static/
    ├── index.html      # 主页面
    ├── css/
    │   └── style.css   # 样式
    └── js/
        └── app.js      # 前端逻辑
```

## API 接口

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | /api/ssh/connect | 建立 SSH 连接 |
| POST | /api/ssh/disconnect | 断开 SSH 连接 |
| POST | /api/sftp/connect | 建立 SFTP 连接 |
| GET | /api/sftp/list | 列出目录内容 |
| GET | /api/sftp/download | 下载文件 |
| POST | /api/sftp/upload | 上传文件 |
| POST | /api/sftp/mkdir | 创建目录 |
| POST | /api/sftp/remove | 删除文件/目录 |
| GET | /api/sftp/pwd | 获取当前路径 |
| POST | /api/sftp/cd | 切换目录 |
| GET | /ws/terminal | WebSocket 终端连接 |

## 注意事项

- 生产环境请配置正确的 SSH 主机密钥验证
- 建议启用 HTTPS 以保护传输安全
- 本地 Bash 模式仅在 Linux/macOS 系统可用
