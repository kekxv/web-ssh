# Web SSH 堡垒机

一个基于 Go 语言的网页版 SSH 堡垒机应用，支持 Web 终端、多级跳板机、用户持久化和 SFTP 文件管理。

## 🌟 核心特性

1. **多级跳板机支持**
   - 支持多达 4 级 SSH 跳板机（Jump Hosts）
   - 每级跳板机独立配置认证（密码/私钥）
   - 自动隧道建立，连接稳定

2. **用户管理与持久化**
   - **单文件持久化**：用户信息保存至 `users.json`，重启不丢失
   - **交互式安全**：支持登录后随时修改 Web 管理员密码
   - **会话联动**：退出登录自动切断所有关联的 SSH/SFTP 会话

3. **单文件分发 (Single Binary)**
   - 使用 Go `embed` 技术将前端所有静态资源直接打入二进制文件
   - 无需外部静态目录，一个文件即可随处运行

4. **Web 终端 & SFTP**
   - **实时终端**：基于 Xterm.js，支持终端自适应和快捷缩放
   - **双向同步**：左侧 SFTP 文件管理（上传/下载/删除/新建），右侧实时命令行
   - **本地模式**：支持直接登录本机 Bash 模式

## 🚀 快速开始

### 方式一：一键安装 (推荐 Linux 用户)

从 GitHub Releases 下载最新的 `web-ssh-installer.sh`：

```bash
# 下载并执行安装脚本
sudo bash web-ssh-installer.sh
```

脚本将自动完成：
- 交互式设置服务端口和管理员密码
- 安装至 `/opt/web-ssh`
- 配置 `systemd` 并设为开机自启

### 方式二：手动编译

需要 **Go 1.25+** 环境：

```bash
# 1. 克隆代码并安装依赖
go mod tidy

# 2. 编译为单文件二进制
go build -ldflags="-s -w" -o web-ssh .

# 3. 运行（可选指定端口）
./web-ssh -port 8080
```

## 🔐 账号管理

- **默认账号**：`admin` / `admin123`
- **修改密码**：登录后在右上角点击“修改密码”或在连接配置界面点击蓝色入口。
- **持久化**：所有用户配置存储在程序目录下的 `users.json` 中。

## 🛠️ 技术栈

### 后端
- **Go 1.25** - 核心语言
- **Gin** - Web 框架
- **golang.org/x/crypto/ssh** - SSH 协议实现
- **gorilla/websocket** - 实时通信
- **pkg/sftp** - SFTP 支持

### 前端
- **Vue 3** - 响应式 UI
- **Xterm.js** - 终端渲染
- **Tailwind CSS** - 现代 UI 样式

## 📦 GitHub Actions 自动构建

项目配置了自动构建流水线，每次推送标签（Tag 如 `v0.1.0`）都会自动产出：
- **Linux**：`web-ssh-installer.sh` (全能安装脚本)
- **Windows**：`web-ssh-windows-amd64.exe` (静态可执行文件)

## ⚠️ 注意事项

- 生产环境建议通过 Nginx/Caddy 配置 **HTTPS** 访问。
- `users.json` 包含加密后的密码哈希，请妥善保管。
- 安装脚本需要 root 权限以配置 `systemd`。

## 开源协议

MIT License
