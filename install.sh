#!/bin/bash

# Web SSH 一键安装脚本 (自提取安装包)
set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}=======================================${NC}"
echo -e "${BLUE}    Web SSH 堡垒机 - 自动化安装脚本    ${NC}"
echo -e "${BLUE}=======================================${NC}"

# 检查是否为 root 用户
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}错误: 请使用 root 用户运行此脚本${NC}"
    exit 1
fi

# 检查系统是否支持 systemd
if ! command -v systemctl >/dev/null 2>&1; then
    echo -e "${RED}错误: 您的系统不支持 systemd，无法进行自动化管理${NC}"
    exit 1
fi

# 设置安装路径
INSTALL_DIR="/opt/web-ssh"
BINARY_PATH="$INSTALL_DIR/web-ssh"
CONFIG_FILE="$INSTALL_DIR/users.json"

mkdir -p "$INSTALL_DIR"

# --- 提取二进制文件 ---
echo -e "${BLUE}>>> 正在提取程序文件...${NC}"
# 查找二进制数据开始的行号
SKIP=$(awk '/^__BINARY_BELOW__/ {print NR + 1; exit 0; }' "$0")

if [ -z "$SKIP" ]; then
    echo -e "${RED}错误: 安装包损坏，未找到二进制数据标记${NC}"
    exit 1
fi

# 提取二进制数据到目标路径
tail -n +$SKIP "$0" > "$BINARY_PATH"
chmod +x "$BINARY_PATH"

# --- 交互式设置 ---
echo -e "\n${BLUE}>>> 基础配置${NC}"

# 1. 设置运行端口
read -p "请输入服务运行端口 [默认 8080]: " PORT
PORT=${PORT:-8080}

# 检查端口占用
if lsof -Pi :$PORT -sTCP:LISTEN -t >/dev/null 2>&1 ; then
    echo -e "${RED}警告: 端口 $PORT 已被占用，请在安装完成后手动调整或重新运行脚本${NC}"
fi

# 2. 设置管理员密码
echo -e "\n${BLUE}>>> 管理员账号配置 (用户名: admin)${NC}"
while true; do
    read -s -p "请设置管理员密码: " ADMIN_PWD
    echo ""
    read -s -p "请再次输入密码以确认: " ADMIN_PWD_CONFIRM
    echo ""
    if [ "$ADMIN_PWD" == "$ADMIN_PWD_CONFIRM" ] && [ ! -z "$ADMIN_PWD" ]; then
        break
    else
        echo -e "${RED}密码不匹配或为空，请重新输入${NC}"
    fi
done

# 生成密码哈希
PWD_HASH=$(printf "%s" "$ADMIN_PWD" | sha256sum | cut -d' ' -f1 | xxd -r -p | base64 | tr -d '\n')
CREATED_AT=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# 创建 users.json
cat > "$CONFIG_FILE" <<EOF
{
  "admin": {
    "username": "admin",
    "password": "$PWD_HASH",
    "created_at": "$CREATED_AT"
  }
}
EOF
chmod 600 "$CONFIG_FILE"

# 创建 systemd 服务文件
cat > /etc/systemd/system/web-ssh.service <<EOF
[Unit]
Description=Web SSH Bastion Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$BINARY_PATH -port $PORT
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# 启动服务
echo -e "\n${BLUE}>>> 正在启动服务...${NC}"
systemctl daemon-reload
systemctl enable web-ssh
systemctl restart web-ssh

# 检查状态
if systemctl is-active --quiet web-ssh; then
    echo -e "\n${GREEN}=======================================${NC}"
    echo -e "${GREEN}    安装成功！服务已启动并设为开机自启    ${NC}"
    echo -e "${GREEN}=======================================${NC}"
    
    IP_ADDR=$(hostname -I | awk '{print $1}')
    echo -e "访问地址: ${BLUE}http://$IP_ADDR:$PORT${NC}"
    echo -e "管理账号: ${BLUE}admin${NC}"
    echo -e "配置文件: $CONFIG_FILE"
    echo -e "\n常用命令:"
    echo "  查看状态: systemctl status web-ssh"
    echo "  停止服务: systemctl stop web-ssh"
    echo "  查看日志: journalctl -u web-ssh -f"
else
    echo -e "${RED}错误: 服务启动失败，请运行 'journalctl -u web-ssh' 查看原因${NC}"
fi

# 务必在脚本末尾添加 exit 0，防止 shell 尝试执行二进制数据
exit 0
__BINARY_BELOW__
