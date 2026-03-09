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
SERVICE_FILE="/etc/systemd/system/web-ssh.service"

# 检测是否已安装
IS_UPDATE=false
if [ -f "$BINARY_PATH" ]; then
    IS_UPDATE=true
    echo -e "${GREEN}>>> 检测到已安装 Web SSH，正在进入更新模式...${NC}"
fi

mkdir -p "$INSTALL_DIR"

# --- 交互式设置 (提前进行，以便确认端口) ---
echo -e "\n${BLUE}>>> 配置选项${NC}"

# 1. 设置运行端口
CURRENT_PORT=8080
if [ "$IS_UPDATE" = true ] && [ -f "$SERVICE_FILE" ]; then
    # 从现有的 service 文件中提取端口
    EXTRACTED_PORT=$(grep ExecStart "$SERVICE_FILE" | sed -n 's/.*-port \([0-9]*\).*/\1/p')
    if [ ! -z "$EXTRACTED_PORT" ]; then
        CURRENT_PORT=$EXTRACTED_PORT
    fi
fi

if [ "$IS_UPDATE" = true ]; then
    read -p "当前运行端口为 $CURRENT_PORT，是否需要修改？(y/N): " CHANGE_PORT
    if [[ "$CHANGE_PORT" =~ ^[Yy]$ ]]; then
        read -p "请输入新的服务运行端口: " PORT
        PORT=${PORT:-$CURRENT_PORT}
    else
        PORT=$CURRENT_PORT
    fi
else
    read -p "请输入服务运行端口 [默认 8080]: " PORT
    PORT=${PORT:-8080}
fi

# 检查端口占用 (如果是更新且端口没变，跳过检查)
if [ "$PORT" != "$CURRENT_PORT" ] || [ "$IS_UPDATE" = false ]; then
    if lsof -Pi :$PORT -sTCP:LISTEN -t >/dev/null 2>&1 ; then
        echo -e "${RED}警告: 端口 $PORT 已被占用，请在安装完成后手动调整或重新运行脚本${NC}"
    fi
fi

# --- 防火墙检查与处理 ---
setup_firewall() {
    local port=$1
    if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
        echo -e "${BLUE}检测到 UFW 防火墙已启用${NC}"
        read -p "是否允许端口 $port 通过防火墙？(Y/n): " ALLOW_FW
        if [[ ! "$ALLOW_FW" =~ ^[Nn]$ ]]; then
            ufw allow "$port/tcp"
            echo -e "${GREEN}UFW 规则已更新${NC}"
        fi
    elif command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld; then
        echo -e "${BLUE}检测到 Firewalld 防火墙已启用${NC}"
        read -p "是否允许端口 $port 通过防火墙？(Y/n): " ALLOW_FW
        if [[ ! "$ALLOW_FW" =~ ^[Nn]$ ]]; then
            firewall-cmd --permanent --add-port="$port/tcp"
            firewall-cmd --reload
            echo -e "${GREEN}Firewalld 规则已更新${NC}"
        fi
    fi
}
setup_firewall "$PORT"

# 2. 设置管理员密码
NEED_PWD_SETUP=true
if [ "$IS_UPDATE" = true ] && [ -f "$CONFIG_FILE" ]; then
    read -p "检测到已有配置文件，是否需要重置管理员 (admin) 密码？(y/N): " RESET_PWD
    if [[ ! "$RESET_PWD" =~ ^[Yy]$ ]]; then
        NEED_PWD_SETUP=false
    fi
fi

if [ "$NEED_PWD_SETUP" = true ]; then
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

    # 创建/覆盖 users.json
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
fi

# --- 提取二进制文件 ---
if [ "$IS_UPDATE" = true ]; then
    echo -e "${BLUE}>>> 正在停止旧服务以进行更新...${NC}"
    systemctl stop web-ssh || true
fi

echo -e "${BLUE}>>> 正在提取程序文件...${NC}"
# 查找二进制数据开始的行号
SKIP=$(awk '/^__BINARY_BELOW__/ {print NR + 1; exit 0; }' "$0")

if [ -z "$SKIP" ]; then
    echo -e "${RED}错误: 安装包损坏，未找到二进制数据标记${NC}"
    exit 1
fi

# 提取二进制数据到目标路径 (覆盖旧文件)
tail -n +$SKIP "$0" > "$BINARY_PATH"
chmod +x "$BINARY_PATH"

# 创建/更新 systemd 服务文件
cat > "$SERVICE_FILE" <<EOF
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
echo -e "\n${BLUE}>>> 正在启动/重启服务...${NC}"
systemctl daemon-reload
systemctl enable web-ssh
systemctl restart web-ssh

# 检查状态
if systemctl is-active --quiet web-ssh; then
    echo -e "\n${GREEN}=======================================${NC}"
    if [ "$IS_UPDATE" = true ]; then
        echo -e "${GREEN}    更新成功！服务已重新启动            ${NC}"
    else
        echo -e "${GREEN}    安装成功！服务已启动并设为开机自启    ${NC}"
    fi
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
