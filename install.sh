#!/bin/bash

# Web SSH 一键安装脚本 (自提取安装包)
set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

# 设置安装路径
INSTALL_DIR="/opt/web-ssh"
BINARY_PATH="$INSTALL_DIR/web-ssh"
CONFIG_FILE="$INSTALL_DIR/users.json"
SERVICE_FILE="/etc/systemd/system/web-ssh.service"
SERVICE_NAME="web-ssh"
LOG_FILE="/var/log/web-ssh-install.log"

print_header() {
    echo -e "${BLUE}=======================================${NC}"
    echo -e "${BLUE}    Web SSH 堡垒机 - 自动化安装脚本    ${NC}"
    echo -e "${BLUE}=======================================${NC}"
}

check_root() {
    if [ "$EUID" -ne 0 ]; then
        echo -e "${RED}错误: 请使用 root 用户运行此脚本${NC}"
        exit 1
    fi
}

check_systemd() {
    if ! command -v systemctl >/dev/null 2>&1; then
        echo -e "${RED}错误: 您的系统不支持 systemd，无法进行自动化管理${NC}"
        exit 1
    fi
}

is_installed() {
    [ -f "$BINARY_PATH" ] || \
    [ -f "$SERVICE_FILE" ] || \
    systemctl list-unit-files "${SERVICE_NAME}.service" --no-legend 2>/dev/null | grep -q "^${SERVICE_NAME}.service"
}

get_current_port() {
    local port="8080"
    local extracted_port=""

    if [ -f "$SERVICE_FILE" ]; then
        extracted_port=$(grep "ExecStart" "$SERVICE_FILE" | sed -n 's/.*-port \([0-9]*\).*/\1/p' | head -n 1)
        if [ -n "$extracted_port" ]; then
            port="$extracted_port"
        fi
    fi

    echo "$port"
}

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

write_admin_config() {
    local admin_pwd=$1
    local pwd_hash
    local created_at

    pwd_hash=$(printf "%s" "$admin_pwd" | sha256sum | cut -d' ' -f1 | xxd -r -p | base64 | tr -d '\n')
    created_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

    cat > "$CONFIG_FILE" <<EOF
{
  "admin": {
    "username": "admin",
    "password": "$pwd_hash",
    "created_at": "$created_at"
  }
}
EOF
    chmod 600 "$CONFIG_FILE"
}

write_service_file() {
    local port=$1

    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Web SSH Bastion Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$BINARY_PATH -port $port
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
}

extract_binary() {
    local tmp_binary="$INSTALL_DIR/.web-ssh.new.$$"
    local skip

    skip=$(awk '/^__BINARY_BELOW__/ {print NR + 1; exit 0; }' "$0")
    if [ -z "$skip" ]; then
        echo -e "${RED}错误: 安装包损坏，未找到二进制数据标记${NC}"
        exit 1
    fi

    tail -n +"$skip" "$0" > "$tmp_binary"
    chmod +x "$tmp_binary"
    mv "$tmp_binary" "$BINARY_PATH"
}

apply_install_or_upgrade() {
    local port=$1
    local is_update=$2
    local backup_path=""

    trap '' HUP
    mkdir -p "$INSTALL_DIR"

    if [ "$is_update" = "true" ]; then
        echo "[$(date '+%F %T')] 开始升级流程，端口: $port"
    else
        echo "[$(date '+%F %T')] 开始安装流程，端口: $port"
    fi

    if [ "$is_update" = "true" ] && [ -f "$BINARY_PATH" ]; then
        backup_path="$INSTALL_DIR/web-ssh.bak.$(date '+%Y%m%d%H%M%S')"
        cp "$BINARY_PATH" "$backup_path"
        echo "[$(date '+%F %T')] 已备份旧程序: $backup_path"
    fi

    rollback_on_error() {
        echo "[$(date '+%F %T')] 安装/升级失败，正在尝试回滚..."
        if [ -n "$backup_path" ] && [ -f "$backup_path" ]; then
            cp "$backup_path" "$BINARY_PATH"
            chmod +x "$BINARY_PATH"
            systemctl restart "$SERVICE_NAME" || true
        fi
    }
    trap rollback_on_error ERR

    if [ "$is_update" = "true" ]; then
        echo "[$(date '+%F %T')] 正在停止旧服务..."
        systemctl stop "$SERVICE_NAME" || true
    fi

    echo "[$(date '+%F %T')] 正在提取程序文件..."
    extract_binary

    echo "[$(date '+%F %T')] 正在写入 systemd 服务文件..."
    write_service_file "$port"

    echo "[$(date '+%F %T')] 正在启动/重启服务..."
    systemctl daemon-reload
    systemctl enable "$SERVICE_NAME"
    systemctl restart "$SERVICE_NAME"

    if systemctl is-active --quiet "$SERVICE_NAME"; then
        trap - ERR
        if [ "$is_update" = "true" ]; then
            echo "[$(date '+%F %T')] Web SSH 升级完成，服务运行正常"
        else
            echo "[$(date '+%F %T')] Web SSH 安装完成，服务运行正常"
        fi
        return 0
    fi

    echo "[$(date '+%F %T')] 错误: 服务启动失败，请运行 'journalctl -u $SERVICE_NAME' 查看原因"
    exit 1
}

start_detached_upgrade() {
    local port=$1
    local script_path
    local detached_script

    script_path=$(readlink -f "$0" 2>/dev/null || echo "$0")
    mkdir -p "$INSTALL_DIR"
    detached_script="$INSTALL_DIR/.web-ssh-upgrade.$(date '+%Y%m%d%H%M%S').sh"
    cp "$script_path" "$detached_script"
    chmod 600 "$detached_script"
    touch "$LOG_FILE"
    chmod 600 "$LOG_FILE"

    echo -e "${BLUE}>>> 已进入升级模式，实际升级将在后台继续执行${NC}"
    echo -e "${BLUE}>>> 即使当前 SSH/网页终端断开，升级也不会被终止${NC}"
    echo -e "升级日志: ${BLUE}$LOG_FILE${NC}"
    echo -e "查看进度: ${BLUE}tail -f $LOG_FILE${NC}"
    echo -e "查看服务: ${BLUE}systemctl status $SERVICE_NAME${NC}"

    if command -v setsid >/dev/null 2>&1; then
        nohup setsid bash "$detached_script" --apply "$port" true "$detached_script" >> "$LOG_FILE" 2>&1 < /dev/null &
    else
        nohup bash "$detached_script" --apply "$port" true "$detached_script" >> "$LOG_FILE" 2>&1 < /dev/null &
    fi
}

print_success() {
    local is_update=$1
    local port=$2
    local ip_addr

    echo -e "\n${GREEN}=======================================${NC}"
    if [ "$is_update" = "true" ]; then
        echo -e "${GREEN}    更新任务已提交到后台执行            ${NC}"
    else
        echo -e "${GREEN}    安装成功！服务已启动并设为开机自启    ${NC}"
    fi
    echo -e "${GREEN}=======================================${NC}"

    ip_addr=$(hostname -I | awk '{print $1}')
    echo -e "访问地址: ${BLUE}http://$ip_addr:$port${NC}"
    echo -e "管理账号: ${BLUE}admin${NC}"
    echo -e "配置文件: $CONFIG_FILE"
    echo -e "\n常用命令:"
    echo "  查看状态: systemctl status $SERVICE_NAME"
    echo "  停止服务: systemctl stop $SERVICE_NAME"
    echo "  查看日志: journalctl -u $SERVICE_NAME -f"
    if [ "$is_update" = "true" ]; then
        echo "  查看升级日志: tail -f $LOG_FILE"
    fi
}

if [ "${1:-}" = "--apply" ]; then
    check_root
    check_systemd
    apply_install_or_upgrade "${2:-8080}" "${3:-false}"
    if [ -n "${4:-}" ] && [ -f "${4:-}" ]; then
        rm -f "$4"
    fi
    exit 0
fi

print_header
check_root
check_systemd

IS_UPDATE=false
if is_installed; then
    IS_UPDATE=true
    echo -e "${GREEN}>>> 检测到已安装 Web SSH，进入升级模式...${NC}"
fi

mkdir -p "$INSTALL_DIR"

# --- 交互式设置 (提前进行，以便确认端口) ---
echo -e "\n${BLUE}>>> 配置选项${NC}"

# 1. 设置运行端口
CURRENT_PORT=$(get_current_port)
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

# 检查端口占用 (如果是升级且端口没变，跳过检查)
if [ "$PORT" != "$CURRENT_PORT" ] || [ "$IS_UPDATE" = false ]; then
    if command -v lsof >/dev/null 2>&1 && lsof -Pi :"$PORT" -sTCP:LISTEN -t >/dev/null 2>&1; then
        echo -e "${YELLOW}警告: 端口 $PORT 已被占用，请在安装完成后手动调整或重新运行脚本${NC}"
    fi
fi

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
        if [ "$ADMIN_PWD" == "$ADMIN_PWD_CONFIRM" ] && [ -n "$ADMIN_PWD" ]; then
            break
        else
            echo -e "${RED}密码不匹配或为空，请重新输入${NC}"
        fi
    done

    write_admin_config "$ADMIN_PWD"
fi

if [ "$IS_UPDATE" = true ]; then
    start_detached_upgrade "$PORT"
    print_success true "$PORT"
    exit 0
fi

echo -e "${BLUE}>>> 正在安装 Web SSH...${NC}"
apply_install_or_upgrade "$PORT" false
print_success false "$PORT"

# 务必在脚本末尾添加 exit 0，防止 shell 尝试执行二进制数据
exit 0
__BINARY_BELOW__
