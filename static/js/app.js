const { createApp } = Vue;

// RSA encryption helper
async function encryptPassword(publicKeyBase64, password) {
    // Import the public key
    const binaryPublicKey = Uint8Array.from(atob(publicKeyBase64), c => c.charCodeAt(0));

    const publicKey = await crypto.subtle.importKey(
        'spki',
        binaryPublicKey,
        {
            name: 'RSA-OAEP',
            hash: 'SHA-256'
        },
        true,
        ['encrypt']
    );

    // Encode password
    const encoder = new TextEncoder();
    const passwordData = encoder.encode(password);

    // Encrypt
    const encrypted = await crypto.subtle.encrypt(
        {
            name: 'RSA-OAEP'
        },
        publicKey,
        passwordData
    );

    // Convert to base64
    const encryptedArray = new Uint8Array(encrypted);
    let binary = '';
    for (let i = 0; i < encryptedArray.byteLength; i++) {
        binary += String.fromCharCode(encryptedArray[i]);
    }
    return btoa(binary);
}

createApp({
    data() {
        return {
            connected: false,
            connectionMode: 'ssh',
            authMethod: 'password',
            config: {
                host: '',
                port: 22,
                username: '',
                password: '',
                privateKey: '',
                passphrase: ''
            },
            sessionId: '',
            sftpSessionId: '',
            ws: null,
            terminal: null,
            fitAddon: null,
            fileList: [],
            currentPath: '~',
            defaultPath: '~',
            showMkdirModal: false,
            newFolderName: '',
            uploadProgress: 0,
            // HTTP 长连接相关
            useHttpFallback: false,
            httpPollingTimer: null,
            sessionId: 'local_ws'
        };
    },

    async mounted() {
        this.initTerminal();
    },

    methods: {

        initTerminal() {
            this.terminal = new Terminal({
                cursorBlink: true,
                fontSize: 14,
                fontFamily: 'Menlo, Monaco, "Courier New", monospace',
                theme: {
                    background: '#0f172a',
                    foreground: '#ffffff',
                    cursor: '#4a9eff',
                    selection: '#4a9eff40'
                }
            });

            this.fitAddon = new FitAddon.FitAddon();
            this.terminal.loadAddon(this.fitAddon);

            const container = document.getElementById('terminal-container');
            this.terminal.open(container);
            this.fitAddon.fit();

            // Handle terminal resize
            window.addEventListener('resize', () => {
                this.fitAddon.fit();
                if (this.connectionMode === 'local' && this.useHttpFallback) {
                    // HTTP 模式下通过 API 发送 resize
                    this.sendHttpResize();
                } else if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                    const dimensions = this.getTerminalDimensions();
                    this.ws.send(JSON.stringify({
                        type: 'resize',
                        cols: dimensions.cols,
                        rows: dimensions.rows
                    }));
                }
            });

            // Send input to server
            this.terminal.onData(data => {
                if (this.connectionMode === 'local' && this.useHttpFallback) {
                    this.sendHttpInput(data);
                } else if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                    this.ws.send(JSON.stringify({
                        type: 'input',
                        data: data
                    }));
                }
            });
        },

        getTerminalDimensions() {
            return {
                cols: this.terminal.cols,
                rows: this.terminal.rows
            };
        },

        async connect() {
            if (this.connectionMode === 'local') {
                this.connectLocal();
            } else {
                await this.connectSSH();
            }
        },

        async connectSSH() {
            // 验证输入
            if (this.authMethod === 'password' && !this.config.password) {
                alert('请输入密码');
                return;
            }
            if (this.authMethod === 'key' && !this.config.privateKey) {
                alert('请提供私钥内容或上传私钥文件');
                return;
            }

            try {
                // 先获取公钥
                const keyResponse = await fetch('/api/public-key');
                const keyData = await keyResponse.json();

                // 创建要发送的配置
                let configToSend = { ...this.config };

                // 如果有密码，就加密它
                if (configToSend.password) {
                    const encryptedPassword = await encryptPassword(keyData.public_key, configToSend.password);
                    configToSend.encryptedPassword = encryptedPassword;
                    configToSend.password = ''; // 清除明文密码
                }

                // 使用配置连接
                const response = await fetch('/api/ssh/connect', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(configToSend)
                });

                if (!response.ok) {
                    const error = await response.text();
                    alert('连接失败：' + error);
                    return;
                }

                const data = await response.json();
                this.sessionId = data.session_id;

                // Connect SFTP
                const sftpResponse = await fetch('/api/sftp/connect', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(configToSend)
                });

                if (sftpResponse.ok) {
                    const sftpData = await sftpResponse.json();
                    this.sftpSessionId = sftpData.session_id;
                    this.getDefaultPath();
                }

                this.connectTerminal('ssh');
                this.connected = true;
            } catch (error) {
                alert('连接失败：' + error.message);
            }
        },

        async connectLocal() {
            this.useHttpFallback = false;

            // 先尝试 WebSocket 连接
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const wsUrl = `${protocol}//${window.location.host}/ws/terminal?session_id=local&mode=local`;

            // 创建临时 WebSocket 测试连接
            const testWs = new WebSocket(wsUrl);
            testWs.binaryType = 'arraybuffer';

            const wsSupported = await Promise.race([
                new Promise(resolve => {
                    testWs.onopen = () => resolve(true);
                }),
                new Promise(resolve => {
                    setTimeout(() => resolve(false), 2000);
                })
            ]);

            testWs.close();

            if (wsSupported) {
                // WebSocket 可用，使用正常连接
                this.sessionId = 'local_ws';
                this.connectTerminal('local');
            } else {
                // WebSocket 不可用，降级到 HTTP 长连接
                console.log('WebSocket not supported, using HTTP long polling');
                this.useHttpFallback = true;
                await this.connectLocalHttp();
            }

            this.connected = true;
        },

        async connectLocalHttp() {
            try {
                const response = await fetch('/api/local/connect', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({})
                });

                if (!response.ok) {
                    throw new Error('Failed to connect');
                }

                const data = await response.json();
                this.sessionId = data.session_id;

                // 开始 HTTP 轮询
                this.startHttpPolling();
            } catch (error) {
                alert('本地连接失败：' + error.message);
            }
        },

        startHttpPolling() {
            const poll = async () => {
                if (!this.sessionId || !this.useHttpFallback) return;

                try {
                    const response = await fetch(`/api/local/read?session_id=${encodeURIComponent(this.sessionId)}`);
                    const data = await response.json();

                    if (data.type === 'output' && data.data) {
                        // Base64 解码
                        const decoder = new TextDecoder('utf-8');
                        const binary = atob(data.data);
                        const bytes = new Uint8Array(binary.length);
                        for (let i = 0; i < binary.length; i++) {
                            bytes[i] = binary.charCodeAt(i);
                        }
                        const text = decoder.decode(bytes);
                        this.terminal.write(text);
                    } else if (data.type === 'close') {
                        console.log('Session closed');
                        return;
                    }
                } catch (error) {
                    console.error('Poll error:', error);
                }

                // 继续轮询
                this.httpPollingTimer = setTimeout(poll, 100);
            };

            poll();
        },

        async sendHttpInput(data) {
            if (!this.sessionId) return;

            try {
                await fetch(`/api/local/write?session_id=${encodeURIComponent(this.sessionId)}`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        type: 'input',
                        data: data
                    })
                });
            } catch (error) {
                console.error('Failed to send input:', error);
            }
        },

        async sendHttpResize() {
            if (!this.sessionId) return;

            const dimensions = this.getTerminalDimensions();
            try {
                await fetch(`/api/local/write?session_id=${encodeURIComponent(this.sessionId)}`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        type: 'resize',
                        cols: dimensions.cols,
                        rows: dimensions.rows
                    })
                });
            } catch (error) {
                console.error('Failed to send resize:', error);
            }
        },

        connectTerminal(mode) {
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            let wsUrl = `${protocol}//${window.location.host}/ws/terminal?session_id=${this.sessionId}&mode=${mode}`;

            this.ws = new WebSocket(wsUrl);
            // Set binary type to arraybuffer for proper handling
            this.ws.binaryType = 'arraybuffer';

            this.ws.onopen = () => {
                console.log('Terminal connected');
                // Calculate terminal size after connection
                this.fitAddon.fit();
                const dimensions = this.getTerminalDimensions();
                this.ws.send(JSON.stringify({
                    type: 'resize',
                    cols: dimensions.cols,
                    rows: dimensions.rows
                }));
            };

            this.ws.onmessage = (event) => {
                // Handle binary data (ArrayBuffer) and text data
                if (event.data instanceof ArrayBuffer) {
                    // Binary data - decode as UTF-8
                    const decoder = new TextDecoder('utf-8');
                    const text = decoder.decode(new Uint8Array(event.data));
                    this.terminal.write(text);
                } else {
                    // Text data
                    const data = event.data;
                    try {
                        const msg = JSON.parse(data);
                        if (msg.type === 'error') {
                            this.terminal.write(`\r\n\x1b[31m${msg.message}\x1b[0m\r\n`);
                            return;
                        }
                    } catch (e) {
                        // Not JSON, treat as terminal output
                    }
                    this.terminal.write(data);
                }
            };

            this.ws.onclose = () => {
                console.log('Terminal disconnected');
                this.terminal.write('\r\n\x1b[31m 连接已断开\x1b[0m\r\n');
            };

            this.ws.onerror = (error) => {
                console.error('WebSocket error:', error);
            };
        },

        disconnect() {
            // 停止 HTTP 轮询
            if (this.httpPollingTimer) {
                clearTimeout(this.httpPollingTimer);
                this.httpPollingTimer = null;
            }

            if (this.ws) {
                this.ws.close();
                this.ws = null;
            }

            // 关闭本地会话
            if (this.sessionId && this.useHttpFallback) {
                fetch(`/api/local/close?session_id=${encodeURIComponent(this.sessionId)}`, { method: 'POST' });
            }

            if (this.sessionId && !this.useHttpFallback) {
                fetch(`/api/ssh/disconnect?session_id=${this.sessionId}`, { method: 'POST' });
            }
            if (this.sftpSessionId) {
                fetch(`/api/sftp/disconnect?session_id=${this.sftpSessionId}`, { method: 'POST' });
            }

            this.sessionId = '';
            this.sftpSessionId = '';
            this.connected = false;
            this.fileList = [];
            this.config.password = '';
            this.config.privateKey = '';
            this.config.passphrase = '';
            this.useHttpFallback = false;
        },

        // 获取默认路径（HOME）
        async getDefaultPath() {
            if (!this.sftpSessionId) return;

            try {
                const response = await fetch(`/api/sftp/pwd?session_id=${this.sftpSessionId}`);
                const data = await response.json();

                if (data.success) {
                    this.defaultPath = data.data.path || '~';
                    this.currentPath = this.defaultPath;
                    this.loadFileList();
                } else {
                    this.currentPath = '~';
                    this.loadFileList();
                }
            } catch (error) {
                this.currentPath = '~';
                this.loadFileList();
            }
        },

        // 回到 HOME 目录
        goHome() {
            this.currentPath = this.defaultPath;
            this.loadFileList();
        },

        async loadFileList() {
            if (!this.sftpSessionId) return;

            try {
                const response = await fetch(`/api/sftp/list?session_id=${this.sftpSessionId}&path=${encodeURIComponent(this.currentPath)}`);
                const data = await response.json();

                if (data.success) {
                    this.fileList = data.data || [];
                } else {
                    alert(data.error || '加载文件列表失败');
                }
            } catch (error) {
                console.error('Failed to load file list:', error);
            }
        },

        refreshFileList() {
            this.loadFileList();
        },

        handleFileClick(file) {
            if (file.isDir) {
                this.navigateTo(file.name);
            }
        },

        navigateUp() {
            if (this.currentPath === '/' || this.currentPath === '') {
                return;
            }
            const parts = this.currentPath.split('/').filter(p => p);
            parts.pop();
            this.currentPath = '/' + parts.join('/');
            if (!this.currentPath) this.currentPath = '/';
            this.loadFileList();
        },

        navigateTo(dirName) {
            if (this.currentPath === '/') {
                this.currentPath = '/' + dirName;
            } else {
                this.currentPath = this.currentPath + '/' + dirName;
            }
            this.loadFileList();
        },

        navigateToPath() {
            this.loadFileList();
        },

        async downloadFile(file) {
            const downloadUrl = `/api/sftp/download?session_id=${this.sftpSessionId}&path=${encodeURIComponent(this.currentPath + '/' + file.name)}`;
            const a = document.createElement('a');
            a.href = downloadUrl;
            a.download = file.name;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
        },

        triggerUpload() {
            this.$refs.fileInput.click();
        },

        async handleFileUpload(event) {
            const file = event.target.files[0];
            if (!file) return;

            const formData = new FormData();
            formData.append('file', file);

            const remotePath = this.currentPath === '/' ? '/' + file.name : this.currentPath + '/' + file.name;

            try {
                this.uploadProgress = 10;
                const response = await fetch(`/api/sftp/upload?session_id=${this.sftpSessionId}&path=${encodeURIComponent(remotePath)}`, {
                    method: 'POST',
                    body: formData
                });

                this.uploadProgress = 100;
                const data = await response.json();

                if (data.success) {
                    alert('上传成功');
                    this.loadFileList();
                } else {
                    alert('上传失败：' + (data.error || '未知错误'));
                }
            } catch (error) {
                alert('上传失败：' + error.message);
            } finally {
                setTimeout(() => { this.uploadProgress = 0; }, 2000);
            }

            event.target.value = '';
        },

        async createFolder() {
            if (!this.newFolderName) return;

            const remotePath = this.currentPath === '/' ? '/' + this.newFolderName : this.currentPath + '/' + this.newFolderName;

            try {
                const response = await fetch(`/api/sftp/mkdir?session_id=${this.sftpSessionId}&path=${encodeURIComponent(remotePath)}`, {
                    method: 'POST'
                });

                const data = await response.json();

                if (data.success) {
                    this.showMkdirModal = false;
                    this.newFolderName = '';
                    this.loadFileList();
                } else {
                    alert('创建失败：' + (data.error || '未知错误'));
                }
            } catch (error) {
                alert('创建失败：' + error.message);
            }
        },

        async deleteFile(file) {
            if (!confirm(`确定要删除 ${file.name} 吗？`)) return;

            const remotePath = this.currentPath === '/' ? '/' + file.name : this.currentPath + '/' + file.name;

            try {
                const response = await fetch(`/api/sftp/remove?session_id=${this.sftpSessionId}&path=${encodeURIComponent(remotePath)}`, {
                    method: 'POST'
                });

                const data = await response.json();

                if (data.success) {
                    this.loadFileList();
                } else {
                    alert('删除失败：' + (data.error || '未知错误'));
                }
            } catch (error) {
                alert('删除失败：' + error.message);
            }
        },

        handleKeyFileUpload(event) {
            const file = event.target.files[0];
            if (!file) return;

            const reader = new FileReader();
            reader.onload = (e) => {
                this.config.privateKey = e.target.result;
                console.log('私钥已加载，长度:', this.config.privateKey.length);
            };
            reader.onerror = (err) => {
                console.error('读取私钥文件失败:', err);
                alert('读取私钥文件失败');
            };
            reader.readAsText(file);

            event.target.value = '';
        },

        // 根据文件扩展名返回图标
        getFileIcon(filename) {
            const ext = filename.split('.').pop().toLowerCase();
            const iconMap = {
                // 图片
                'jpg': '🖼️', 'jpeg': '🖼️', 'png': '🖼️', 'gif': '🖼️', 'bmp': '🖼️', 'svg': '🖼️', 'webp': '🖼️',
                // 文档
                'pdf': '📕', 'doc': '📘', 'docx': '📘', 'txt': '📄', 'md': '📝',
                // 表格
                'xls': '📊', 'xlsx': '📊', 'csv': '📊',
                // 压缩
                'zip': '📦', 'tar': '📦', 'gz': '📦', 'rar': '📦', '7z': '📦',
                // 代码
                'js': '📜', 'ts': '📜', 'py': '📜', 'go': '📜', 'java': '📜', 'c': '📜', 'cpp': '📜', 'h': '📜', 'hpp': '📜',
                'sh': '📜', 'bash': '📜', 'zsh': '📜', 'fish': '📜',
                'html': '🌐', 'htm': '🌐', 'css': '🎨', 'scss': '🎨', 'less': '🎨',
                'json': '⚙️', 'xml': '⚙️', 'yaml': '⚙️', 'yml': '⚙️', 'toml': '⚙️',
                // 媒体
                'mp3': '🎵', 'wav': '🎵', 'flac': '🎵', 'ogg': '🎵',
                'mp4': '🎬', 'avi': '🎬', 'mkv': '🎬', 'mov': '🎬', 'wmv': '🎬',
                // 可执行
                'exe': '⚡', 'bin': '⚡', 'run': '⚡', 'app': '⚡',
                // 配置
                'conf': '⚙️', 'config': '⚙️', 'ini': '⚙️', 'env': '🔐',
                // 日志
                'log': '📋',
                // 默认
                '': '📄'
            };
            return iconMap[ext] || '📄';
        },

        formatFileSize(size) {
            if (size === 0) return '0 B';
            const k = 1024;
            const sizes = ['B', 'KB', 'MB', 'GB'];
            const i = Math.floor(Math.log(size) / Math.log(k));
            return Math.round(size / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
        }
    }
}).mount('#app');
