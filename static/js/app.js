const { createApp } = Vue;

// 使用 AES 混合加密方案，因为 RSA 有长度限制
async function encryptData(publicKeyBase64, data) {
    if (!window.crypto || !window.crypto.subtle) {
        console.warn('Crypto Subtle API is not available. This usually happens in non-secure contexts (HTTP). Falling back to plain text.');
        return null;
    }
    // 1. 生成随机 AES 密钥
    const aesKey = await crypto.subtle.generateKey(
        { name: 'AES-GCM', length: 256 },
        true,
        ['encrypt']
    );

    // 2. 生成随机 IV
    const iv = crypto.getRandomValues(new Uint8Array(12));

    // 3. 使用 AES 加密数据
    const encoder = new TextEncoder();
    const dataBuffer = encoder.encode(data);
    const encryptedData = await crypto.subtle.encrypt(
        { name: 'AES-GCM', iv: iv },
        aesKey,
        dataBuffer
    );

    // 4. 导出 AES 密钥
    const rawKey = await crypto.subtle.exportKey('raw', aesKey);

    // 5. 使用 RSA 公钥加密 AES 密钥
    const binaryPublicKey = Uint8Array.from(atob(publicKeyBase64), c => c.charCodeAt(0));
    const publicKey = await crypto.subtle.importKey(
        'spki',
        binaryPublicKey,
        { name: 'RSA-OAEP', hash: 'SHA-256' },
        true,
        ['encrypt']
    );

    const encryptedKey = await crypto.subtle.encrypt(
        { name: 'RSA-OAEP' },
        publicKey,
        rawKey
    );

    // 6. 返回格式：encryptedKey(256 字节) + iv(12 字节) + encryptedData
    const encryptedKeyArray = new Uint8Array(encryptedKey);
    const encryptedDataArray = new Uint8Array(encryptedData);

    // 组合：keyLength(4 字节) + encryptedKey + iv + encryptedData
    const keyLength = encryptedKeyArray.length;
    const keyLengthBytes = new Uint8Array(new Uint32Array([keyLength]).buffer);

    const result = new Uint8Array(4 + keyLength + 12 + encryptedDataArray.length);
    result.set(keyLengthBytes, 0);
    result.set(encryptedKeyArray, 4);
    result.set(iv, 4 + keyLength);
    result.set(encryptedDataArray, 4 + keyLength + 12);

    // 转为 base64
    let binary = '';
    for (let i = 0; i < result.length; i++) {
        binary += String.fromCharCode(result[i]);
    }
    return btoa(binary);
}

createApp({
    data() {
        return {
            isLoggedIn: false,
            currentUser: '',
            loginForm: {
                username: '',
                password: ''
            },
            loginError: '',
            connected: false,
            connectionMode: 'ssh',
            authMethod: 'password',
            config: {
                host: '',
                port: 22,
                username: '',
                password: '',
                privateKey: '',
                passphrase: '',
                jumpHosts: null  // 跳板机配置数组
            },
            remoteConfig: {
                url: '',
                username: '',
                password: ''
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
            transfer: {
                active: false,
                type: '',
                fileName: '',
                progress: 0,
                loaded: 0,
                total: 0,
                speed: 0,
                status: '',
                error: '',
                startedAt: 0
            },
            transferClearTimer: null,
            // HTTP 长连接相关
            useHttpFallback: false,
            httpPollingTimer: null,
            isLocalMode: false,
            isRemoteLocalMode: false,
            showPasswordModal: false,
            passwordForm: {
                oldPassword: '',
                newPassword: ''
            },
            passwordError: '',
            passwordSuccess: '',
            theme: localStorage.getItem('theme') || 'light',
            isDragging: false,
            showFileManager: false,
            fileViewMode: 'list',
            localShell: '/bin/zsh',
            availableShells: ['/bin/zsh', '/bin/bash', '/bin/sh'],
            remoteShell: '/bin/bash',
            remoteAvailableShells: ['/bin/bash', '/bin/sh'],

            // 系统仪表盘
            systemInfo: null,
            systemInfoTimer: null,
            showDashboard: false,

            // Docker 管理
            dockerAvailable: false,
            showDockerModal: false,
            dockerTab: 'containers',
            dockerContainers: [],
            dockerImages: [],
            dockerLoading: false,
            dockerContainerLogs: '',
            dockerLogSize: 0,
            dockerLogPath: '',
            dockerSelectedContainer: '',
            dockerLogsLoading: false,
            dockerStatsMap: {}
        };
    },

    watch: {
        theme(newTheme) {
            localStorage.setItem('theme', newTheme);
            this.applyTheme();
        },

        showFileManager(show) {
            if (show) {
                this.loadFileList();
            }
        },

        connectionMode(mode) {
            if (mode === 'local') {
                this.fetchAvailableShells();
            }
        },

        connected(val) {
            if (val && this.managementSupported()) {
                this.fetchSystemInfo();
                this.systemInfoTimer = setInterval(this.fetchSystemInfo, 10000);
                this.checkDockerAvailable();
            } else {
                if (this.systemInfoTimer) {
                    clearInterval(this.systemInfoTimer);
                    this.systemInfoTimer = null;
                }
                this.systemInfo = null;
                this.dockerAvailable = false;
                this.showDashboard = false;
                this.showDockerModal = false;
            }
        },

        showDockerModal(val) {
            if (val && this.managementSupported()) {
                this.fetchDockerContainers();
                this.fetchDockerImages();
            }
        }
    },

    async mounted() {
        // Apply theme on load
        this.applyTheme();
        // Check if already logged in
        await this.checkAuth();
        this.initTerminal();
    },

    methods: {
        toggleTheme() {
            this.theme = this.theme === 'light' ? 'dark' : 'light';
            localStorage.setItem('theme', this.theme);
            this.applyTheme();
        },

        applyTheme() {
            const isDark = this.theme === 'dark';
            if (isDark) {
                document.documentElement.classList.add('dark');
            } else {
                document.documentElement.classList.remove('dark');
            }

            if (this.terminal) {
                // Xterm.js v5+ 标准更新主题方式
                this.terminal.options.theme = isDark ? {
                    background: '#0f172a',
                    foreground: '#ffffff',
                    cursor: '#4a9eff',
                    selectionBackground: '#4a9eff40',
                    black: '#000000',
                    red: '#ff5555',
                    green: '#50fa7b',
                    yellow: '#f1fa8c',
                    blue: '#bd93f9',
                    magenta: '#ff79c6',
                    cyan: '#8be9fd',
                    white: '#bfbfbf',
                    brightBlack: '#4d4d4d',
                    brightRed: '#ff6e67',
                    brightGreen: '#5af78e',
                    brightYellow: '#f4f99d',
                    brightBlue: '#caa9fa',
                    brightMagenta: '#ff92d0',
                    brightCyan: '#9aedfe',
                    brightWhite: '#e6e6e6'
                } : {
                    background: '#f8fafc',
                    foreground: '#0f172a',
                    cursor: '#3b82f6',
                    selectionBackground: '#b4d7ff',
                    black: '#000000',
                    red: '#cd3131',
                    green: '#008a00',
                    yellow: '#8a6c00',
                    blue: '#0451a5',
                    magenta: '#bc05bc',
                    cyan: '#007691',
                    white: '#555555',
                    brightBlack: '#666666',
                    brightRed: '#cd3131',
                    brightGreen: '#00a600',
                    brightYellow: '#a17d00',
                    brightBlue: '#0451a5',
                    brightMagenta: '#bc05bc',
                    brightCyan: '#007691',
                    brightWhite: '#a5a5a5'
                };
                
                // 强制终端重绘以应用新颜色
                if (this.terminal.refresh) {
                    this.terminal.refresh(0, this.terminal.rows - 1);
                }
            }
        },

        async checkAuth() {
            try {
                const response = await fetch('/api/auth/check');
                const data = await response.json();
                if (data.authenticated) {
                    this.isLoggedIn = true;
                    this.currentUser = data.username;
                }
            } catch (error) {
                console.error('Auth check failed:', error);
            }
        },

        async fetchAvailableShells() {
            try {
                const response = await fetch('/api/local/shells');
                const data = await response.json();
                if (data.shells && data.shells.length > 0) {
                    this.availableShells = data.shells;
                    this.localShell = this.preferredShell(data.shells, data.current_shell);
                }
            } catch (error) {
                // Fallback to defaults
            }
        },

        preferredShell(shells, currentShell) {
            const priorities = ['zsh', 'bash', 'sh'];
            for (const name of priorities) {
                const shell = shells.find(item => this.shellBaseName(item) === name);
                if (shell) return shell;
            }
            if (currentShell && shells.includes(currentShell)) return currentShell;
            return shells[0] || currentShell || '/bin/sh';
        },

        selectAvailableShell(shells, selectedShell, currentShell) {
            if (selectedShell && shells.includes(selectedShell)) {
                return selectedShell;
            }
            return this.preferredShell(shells, currentShell);
        },

        async fetchRemoteAvailableShells() {
            try {
                const response = await fetch('/api/remote/shells?session_id=' + encodeURIComponent(this.sessionId));
                if (!response.ok) {
                    throw new Error('无法读取远端 Shell 列表');
                }
                const data = await response.json();
                if (data.shells && data.shells.length > 0) {
                    this.remoteAvailableShells = data.shells;
                    this.remoteShell = this.selectAvailableShell(data.shells, this.remoteShell, data.current_shell);
                }
            } catch (error) {
                console.warn('Failed to fetch remote shells:', error);
            }
        },

        shellBaseName(shell) {
            return String(shell || '').split(/[\\/]/).pop();
        },

        async login() {
            try {
                // Get public key for encryption
                const keyResponse = await fetch('/api/public-key');
                const keyData = await keyResponse.json();

                // Encrypt password
                const encryptedPassword = await encryptData(keyData.public_key, this.loginForm.password);
                
                const loginPayload = {
                    username: this.loginForm.username
                };
                
                if (encryptedPassword) {
                    loginPayload.encrypted_password = encryptedPassword;
                } else {
                    loginPayload.password = this.loginForm.password;
                }

                const response = await fetch('/api/auth/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(loginPayload)
                });

                const data = await response.json();

                if (response.ok && data.success) {
                    this.isLoggedIn = true;
                    this.currentUser = this.loginForm.username;
                    this.loginError = '';
                    this.loginForm.password = '';
                } else {
                    this.loginError = data.error || '登录失败，请检查用户名和密码';
                }
            } catch (error) {
                this.loginError = '登录失败：' + error.message;
            }
        },

        async logout() {
            try {
                await fetch('/api/auth/logout', { method: 'POST' });
            } catch (error) {
                console.error('Logout failed:', error);
            }

            // Clear all state
            if (this.ws) {
                this.ws.close();
            }
            if (this.httpPollingTimer) {
                clearTimeout(this.httpPollingTimer);
            }
            if (this.systemInfoTimer) {
                clearInterval(this.systemInfoTimer);
                this.systemInfoTimer = null;
            }

            this.isLoggedIn = false;
            this.currentUser = '';
            this.connected = false;
            this.sessionId = '';
            this.sftpSessionId = '';
            this.fileList = [];
            this.loginForm.username = '';
            this.loginForm.password = '';
            this.systemInfo = null;
            this.dockerAvailable = false;
            this.showDashboard = false;
            this.showDockerModal = false;
        },

        async changePassword() {
            this.passwordError = '';
            this.passwordSuccess = '';

            if (!this.passwordForm.oldPassword || !this.passwordForm.newPassword) {
                this.passwordError = '请填写旧密码和新密码';
                return;
            }

            try {
                // Get public key for encryption
                const keyResponse = await fetch('/api/public-key');
                const keyData = await keyResponse.json();

                // Encrypt passwords
                const encryptedOldPassword = await encryptData(keyData.public_key, this.passwordForm.oldPassword);
                const encryptedNewPassword = await encryptData(keyData.public_key, this.passwordForm.newPassword);

                const changePayload = {
                    username: this.currentUser
                };
                
                if (encryptedOldPassword) {
                    changePayload.encrypted_old_password = encryptedOldPassword;
                } else {
                    changePayload.old_password = this.passwordForm.oldPassword;
                }
                
                if (encryptedNewPassword) {
                    changePayload.encrypted_new_password = encryptedNewPassword;
                } else {
                    changePayload.new_password = this.passwordForm.newPassword;
                }

                const response = await fetch('/api/auth/change-password', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(changePayload)
                });

                const data = await response.json();

                if (response.ok && data.success) {
                    this.passwordSuccess = '密码修改成功';
                    setTimeout(() => {
                        this.showPasswordModal = false;
                        this.passwordForm.oldPassword = '';
                        this.passwordForm.newPassword = '';
                        this.passwordSuccess = '';
                    }, 1500);
                } else {
                    this.passwordError = data.error || '修改失败';
                }
            } catch (error) {
                this.passwordError = '修改失败：' + error.message;
            }
        },

        initTerminal() {
            this.terminal = new Terminal({
                cursorBlink: true,
                fontSize: 14,
                fontFamily: '"JetBrainsMono Nerd Font", "JetBrains Mono", "FiraCode Nerd Font", Menlo, Monaco, monospace',
                theme: this.theme === 'dark' ? {
                    background: '#0f172a',
                    foreground: '#ffffff',
                    cursor: '#4a9eff',
                    selectionBackground: '#4a9eff40',
                    black: '#000000',
                    red: '#ff5555',
                    green: '#50fa7b',
                    yellow: '#f1fa8c',
                    blue: '#bd93f9',
                    magenta: '#ff79c6',
                    cyan: '#8be9fd',
                    white: '#bfbfbf',
                    brightBlack: '#4d4d4d',
                    brightRed: '#ff6e67',
                    brightGreen: '#5af78e',
                    brightYellow: '#f4f99d',
                    brightBlue: '#caa9fa',
                    brightMagenta: '#ff92d0',
                    brightCyan: '#9aedfe',
                    brightWhite: '#e6e6e6'
                } : {
                    background: '#f8fafc',
                    foreground: '#0f172a',
                    cursor: '#3b82f6',
                    selectionBackground: '#b4d7ff',
                    black: '#000000',
                    red: '#cd3131',
                    green: '#008a00',
                    yellow: '#8a6c00',
                    blue: '#0451a5',
                    magenta: '#bc05bc',
                    cyan: '#007691',
                    white: '#555555',
                    brightBlack: '#666666',
                    brightRed: '#cd3131',
                    brightGreen: '#00a600',
                    brightYellow: '#a17d00',
                    brightBlue: '#0451a5',
                    brightMagenta: '#bc05bc',
                    brightCyan: '#007691',
                    brightWhite: '#a5a5a5'
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
                    // Use binary message for input data
                    const encoder = new TextEncoder();
                    const message = JSON.stringify({ type: 'input', data: data });
                    this.ws.send(encoder.encode(message));
                }

                // Auto refresh file list on Enter in local mode
                if (this.connectionMode === 'local' && (data === '\r' || data === '\n')) {
                    setTimeout(() => {
                        this.loadFileList();
                    }, 300); // Small delay to let shell process the command
                }

                // Auto refresh file list on Enter in remote local mode
                if (this.isRemoteLocalMode && (data === '\r' || data === '\n')) {
                    setTimeout(() => {
                        this.loadRemoteFileList();
                    }, 300);
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
            } else if (this.connectionMode === 'remoteLocal') {
                await this.connectRemoteLocal();
            } else {
                await this.connectSSH();
            }
        },

        async connectSSH() {
            console.log('Starting SSH connection...');

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
                console.log('Fetching public key...');
                const keyResponse = await fetch('/api/public-key');
                console.log('Public key response:', keyResponse.status);

                if (!keyResponse.ok) {
                    throw new Error('Failed to get public key');
                }

                const keyData = await keyResponse.json();
                console.log('Got public key:', keyData.public_key ? 'yes' : 'no');

                // 创建要发送的配置
                let configToSend = {
                    host: this.config.host,
                    port: this.config.port,
                    username: this.config.username
                };

                // 加密密码字段（如果存在）
                if (this.config.password) {
                    console.log('Encrypting password...');
                    const encryptedPassword = await encryptData(keyData.public_key, this.config.password);
                    if (encryptedPassword) {
                        configToSend.encryptedPassword = encryptedPassword;
                    } else {
                        configToSend.password = this.config.password;
                    }
                    console.log('Password handled');
                }

                // 加密私钥字段（如果存在）
                if (this.config.privateKey) {
                    console.log('Encrypting private key...');
                    const encryptedPrivateKey = await encryptData(keyData.public_key, this.config.privateKey);
                    if (encryptedPrivateKey) {
                        configToSend.encryptedPrivateKey = encryptedPrivateKey;
                    } else {
                        configToSend.privateKey = this.config.privateKey;
                    }
                    console.log('Private key handled');
                }

                // 加密私钥密码字段（如果存在）
                if (this.config.passphrase) {
                    console.log('Encrypting passphrase...');
                    const encryptedPassphrase = await encryptData(keyData.public_key, this.config.passphrase);
                    if (encryptedPassphrase) {
                        configToSend.encryptedPassphrase = encryptedPassphrase;
                    } else {
                        configToSend.passphrase = this.config.passphrase;
                    }
                    console.log('Passphrase handled');
                }

                // 加密跳板机配置（如果存在）
                if (this.config.jumpHosts && this.config.jumpHosts.length > 0) {
                    console.log('Encrypting jump hosts...');
                    configToSend.jumpHosts = [];
                    for (let i = 0; i < this.config.jumpHosts.length; i++) {
                        const jump = this.config.jumpHosts[i];
                        const encryptedJump = {
                            host: jump.host,
                            port: jump.port,
                            username: jump.username
                        };
                        // 加密密码
                        if (jump.password) {
                            const encryptedPassword = await encryptData(keyData.public_key, jump.password);
                            if (encryptedPassword) {
                                encryptedJump.encryptedPassword = encryptedPassword;
                            } else {
                                encryptedJump.password = jump.password;
                            }
                        }
                        // 加密私钥
                        if (jump.privateKey) {
                            const encryptedPrivateKey = await encryptData(keyData.public_key, jump.privateKey);
                            if (encryptedPrivateKey) {
                                encryptedJump.encryptedPrivateKey = encryptedPrivateKey;
                            } else {
                                encryptedJump.privateKey = jump.privateKey;
                            }
                        }
                        // 加密私钥密码
                        if (jump.passphrase) {
                            const encryptedPassphrase = await encryptData(keyData.public_key, jump.passphrase);
                            if (encryptedPassphrase) {
                                encryptedJump.encryptedPassphrase = encryptedPassphrase;
                            } else {
                                encryptedJump.passphrase = jump.passphrase;
                            }
                        }
                        configToSend.jumpHosts.push(encryptedJump);
                    }
                    console.log('Jump hosts handled, count:', configToSend.jumpHosts.length);
                }

                console.log('Sending config:', JSON.stringify(configToSend, null, 2));

                // 使用配置连接
                const response = await fetch('/api/ssh/connect', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(configToSend)
                });

                console.log('Connect response:', response.status);

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

                this.connected = true;

                // 登录成功后立即进行一次大小检测，确保后端 shell 获取正确的行列数
                setTimeout(() => {
                    if (this.fitAddon) {
                        this.fitAddon.fit();
                        const dimensions = this.getTerminalDimensions();
                        console.log('Initial terminal resize:', dimensions);

                        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                            const encoder = new TextEncoder();
                            const message = JSON.stringify({
                                type: 'resize',
                                cols: dimensions.cols,
                                rows: dimensions.rows
                            });
                            this.ws.send(encoder.encode(message));
                        } else if (this.useHttpFallback) {
                            this.sendHttpResize();
                        }
                    }
                    // 自动聚焦终端
                    if (this.terminal) {
                        this.terminal.focus();
                    }
                }, 100);

                this.connectTerminal('ssh');
            } catch (error) {
                console.error('SSH connection error:', error);
                alert('连接失败：' + error.message);
            }
        },

        async connectLocal() {
            this.isLocalMode = false;
            this.useHttpFallback = false;

            // 先尝试 WebSocket 连接（通过 Cookie 认证）
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const wsUrl = `${protocol}//${window.location.host}/ws/terminal?mode=local&shell=${encodeURIComponent(this.localShell)}`;

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

            // 设置本地模式并获取默认路径
            this.isLocalMode = true;
            this.currentPath = '~';  // 使用 ~ 表示 home 目录
            this.defaultPath = '~';
            this.connected = true;

            // 登录成功后立即进行一次大小检测
            setTimeout(() => {
                if (this.fitAddon) {
                    this.fitAddon.fit();
                    const dimensions = this.getTerminalDimensions();
                    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                        const encoder = new TextEncoder();
                        const message = JSON.stringify({
                            type: 'resize',
                            cols: dimensions.cols,
                            rows: dimensions.rows
                        });
                        this.ws.send(encoder.encode(message));
                    } else if (this.useHttpFallback) {
                        this.sendHttpResize();
                    }
                }
                // 自动聚焦终端
                if (this.terminal) {
                    this.terminal.focus();
                }
            }, 100);

            this.loadFileList();
        },

        async connectRemoteLocal() {
            if (!this.remoteConfig.url || !this.remoteConfig.username || !this.remoteConfig.password) {
                alert('请填写完整的远程服务器信息');
                return;
            }

            try {
                // 1. 登录远程 web-ssh
                const loginResponse = await fetch('/api/remote/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        url: this.remoteConfig.url,
                        username: this.remoteConfig.username,
                        password: this.remoteConfig.password
                    })
                });

                if (!loginResponse.ok) {
                    const error = await loginResponse.json();
                    alert('登录远程服务器失败：' + (error.error || '未知错误'));
                    return;
                }

                const loginData = await loginResponse.json();
                this.sessionId = loginData.session_id;
                await this.fetchRemoteAvailableShells();
                this.isRemoteLocalMode = true;

                // 2. 连接远程终端
                this.connectRemoteTerminal();

                // 3. 加载文件列表
                this.connected = true;
                this.currentPath = '~';
                this.defaultPath = '~';

                setTimeout(() => {
                    if (this.fitAddon) {
                        this.fitAddon.fit();
                        const dimensions = this.getTerminalDimensions();
                        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                            const encoder = new TextEncoder();
                            const message = JSON.stringify({
                                type: 'resize',
                                cols: dimensions.cols,
                                rows: dimensions.rows
                            });
                            this.ws.send(encoder.encode(message));
                        }
                    }
                    // 自动聚焦终端
                    if (this.terminal) {
                        this.terminal.focus();
                    }
                }, 100);

                this.loadRemoteFileList();
            } catch (error) {
                alert('连接远程服务器失败：' + error.message);
            }
        },

        connectRemoteTerminal() {
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const wsUrl = `${protocol}//${window.location.host}/ws/remote/terminal?session_id=${encodeURIComponent(this.sessionId)}&shell=${encodeURIComponent(this.remoteShell)}`;

            this.ws = new WebSocket(wsUrl);
            this.ws.binaryType = 'arraybuffer';

            this.ws.onopen = () => {
                console.log('Remote terminal connected');
                this.fitAddon.fit();
                const dimensions = this.getTerminalDimensions();
                const encoder = new TextEncoder();
                const message = JSON.stringify({
                    type: 'resize',
                    cols: dimensions.cols,
                    rows: dimensions.rows
                });
                this.ws.send(encoder.encode(message));
            };

            this.ws.onmessage = (event) => {
                if (event.data instanceof ArrayBuffer) {
                    const decoder = new TextDecoder('utf-8');
                    const text = decoder.decode(new Uint8Array(event.data));
                    this.terminal.write(text);
                } else {
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
                console.log('Remote terminal disconnected');
                this.terminal.write('\r\n\x1b[31m 远程连接已断开\x1b[0m\r\n');
            };

            this.ws.onerror = (error) => {
                console.error('Remote WebSocket error:', error);
            };
        },

        async loadRemoteFileList() {
            if (!this.sessionId) return;

            try {
                const response = await fetch(`/api/remote/file/list?session_id=${encodeURIComponent(this.sessionId)}&path=${encodeURIComponent(this.currentPath)}`);
                const data = await response.json();

                if (data.success) {
                    this.fileList = data.data || [];
                    if (data.path) {
                        this.currentPath = data.path;
                    }
                } else {
                    console.error('Failed to load remote file list:', data.error);
                }
            } catch (error) {
                console.error('Failed to load remote file list:', error);
            }
        },

        async connectLocalHttp() {
            try {
                const response = await fetch('/api/local/connect', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ shell: this.localShell })
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
            // SSH 模式需要传递 session_id，本地模式使用 Cookie 认证
            let wsUrl = `${protocol}//${window.location.host}/ws/terminal?mode=${mode}`;
            if (mode === 'ssh' && this.sessionId) {
                wsUrl += `&session_id=${encodeURIComponent(this.sessionId)}`;
            }
            if (mode === 'local') {
                wsUrl += `&shell=${encodeURIComponent(this.localShell)}`;
            }

            this.ws = new WebSocket(wsUrl);
            // Set binary type to arraybuffer for proper handling
            this.ws.binaryType = 'arraybuffer';

            this.ws.onopen = () => {
                console.log('Terminal connected');
                // Calculate terminal size after connection
                this.fitAddon.fit();
                const dimensions = this.getTerminalDimensions();
                // Use binary message for resize
                const encoder = new TextEncoder();
                const message = JSON.stringify({
                    type: 'resize',
                    cols: dimensions.cols,
                    rows: dimensions.rows
                });
                this.ws.send(encoder.encode(message));
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
                // 先移除 onclose 回调，避免在 clear() 之后写入断开消息
                this.ws.onclose = null;
                this.ws.close();
                this.ws = null;
            }

            // 清空终端内容，确保重连时终端状态干净
            if (this.terminal) {
                this.terminal.clear();
            }

            // 关闭本地会话
            if (this.sessionId && this.useHttpFallback) {
                fetch(`/api/local/close?session_id=${encodeURIComponent(this.sessionId)}`, { method: 'POST' });
            }

            // 关闭远程本地会话
            if (this.sessionId && this.isRemoteLocalMode) {
                fetch(`/api/remote/disconnect?session_id=${encodeURIComponent(this.sessionId)}`, { method: 'POST' });
            }

            if (this.sessionId && !this.useHttpFallback && !this.isRemoteLocalMode) {
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
            this.isRemoteLocalMode = false;
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

        getPathSeparator(path) {
            if (!path) return '/';
            if (path.includes('\\')) return '\\';
            // Windows drive letter without backslash (e.g., "C:")
            if (/^[A-Za-z]:$/.test(path)) return '\\';
            return '/';
        },

        joinPath(parent, segment) {
            if (!parent || parent === '') return segment;
            const sep = this.getPathSeparator(parent);
            if (parent === sep) return parent + segment;
            // Windows root (e.g., "C:\")
            if (sep === '\\' && parent.endsWith('\\')) return parent + segment;
            // Windows bare drive letter (e.g., "C:")
            if (sep === '\\' && /^[A-Za-z]:$/.test(parent)) return parent + sep + segment;
            return parent + sep + segment;
        },

        splitPath(path) {
            if (!path) return [];
            const sep = this.getPathSeparator(path);
            return path.split(sep).filter(p => p);
        },

        formatModTime(timestamp) {
            if (!timestamp) return '';
            const date = new Date(timestamp * 1000);
            const month = String(date.getMonth() + 1).padStart(2, '0');
            const day = String(date.getDate()).padStart(2, '0');
            const hours = String(date.getHours()).padStart(2, '0');
            const minutes = String(date.getMinutes()).padStart(2, '0');
            return `${month}-${day} ${hours}:${minutes}`;
        },

        // 回到 HOME 目录
        goHome() {
            this.currentPath = this.defaultPath;
            this.loadFileList();
        },

        async loadFileList() {
            if (!this.sftpSessionId && !this.isLocalMode && !this.isRemoteLocalMode) return;

            // 如果是远程本地模式，使用 loadRemoteFileList
            if (this.isRemoteLocalMode) {
                await this.loadRemoteFileList();
                return;
            }

            try {
                let url;
                if (this.isLocalMode) {
                    url = `/api/local/file/list?path=${encodeURIComponent(this.currentPath)}`;
                } else {
                    url = `/api/sftp/list?session_id=${this.sftpSessionId}&path=${encodeURIComponent(this.currentPath)}`;
                }

                const response = await fetch(url);
                const data = await response.json();

                if (data.success) {
                    this.fileList = data.data || [];
                    // 更新当前路径（本地模式）
                    if (this.isLocalMode && data.path) {
                        this.currentPath = data.path;
                    }
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
            const sep = this.getPathSeparator(this.currentPath);
            if (this.currentPath === sep || this.currentPath === '') return;
            // Windows root check (e.g., C:\)
            if (sep === '\\' && /^[A-Za-z]:\\$/.test(this.currentPath)) return;

            const parts = this.splitPath(this.currentPath);
            parts.pop();
            if (parts.length === 0) {
                if (sep === '\\') {
                    const match = this.currentPath.match(/^([A-Za-z]:)/);
                    this.currentPath = match ? match[1] + '\\' : 'C:\\';
                } else {
                    this.currentPath = '/';
                }
            } else {
                let result = parts.join(sep);
                if (sep === '/' && !result.startsWith('/')) {
                    result = '/' + result;
                } else if (sep === '\\') {
                    const origMatch = this.currentPath.match(/^([A-Za-z]:)/);
                    if (origMatch && !result.match(/^[A-Za-z]:/)) {
                        result = origMatch[1] + '\\' + result;
                    }
                    // Ensure drive letter has trailing backslash when it's the root
                    if (/^[A-Za-z]:$/.test(result)) {
                        result = result + '\\';
                    }
                }
                this.currentPath = result;
            }
            this.loadFileList();
        },

        navigateTo(dirName) {
            this.currentPath = this.joinPath(this.currentPath, dirName);
            this.loadFileList();
        },

        navigateToPath() {
            this.loadFileList();
        },

        startTransfer(type, fileName, total, status) {
            if (this.transferClearTimer) {
                clearTimeout(this.transferClearTimer);
                this.transferClearTimer = null;
            }

            this.transfer = {
                active: true,
                type,
                fileName,
                progress: 0,
                loaded: 0,
                total: total || 0,
                speed: 0,
                status: status || '',
                error: '',
                startedAt: performance.now()
            };
        },

        updateTransferProgress(loaded, total, status) {
            const nextTotal = total || this.transfer.total || 0;
            const elapsed = Math.max((performance.now() - this.transfer.startedAt) / 1000, 0.001);
            const progress = nextTotal > 0 ? Math.min(100, Math.round((loaded / nextTotal) * 100)) : 0;

            this.transfer.loaded = loaded;
            this.transfer.total = nextTotal;
            this.transfer.speed = loaded / elapsed;
            this.transfer.progress = progress;
            if (status) {
                this.transfer.status = status;
            }
        },

        completeTransfer(status) {
            const total = this.transfer.total || this.transfer.loaded;
            this.transfer.loaded = total;
            this.transfer.total = total;
            this.transfer.progress = 100;
            this.transfer.status = status || '完成';
            this.transfer.error = '';
            this.scheduleTransferClear();
        },

        failTransfer(message) {
            this.transfer.active = true;
            this.transfer.status = '失败';
            this.transfer.error = message;
        },

        clearTransfer() {
            if (this.transferClearTimer) {
                clearTimeout(this.transferClearTimer);
                this.transferClearTimer = null;
            }
            this.transfer = {
                active: false,
                type: '',
                fileName: '',
                progress: 0,
                loaded: 0,
                total: 0,
                speed: 0,
                status: '',
                error: '',
                startedAt: 0
            };
        },

        scheduleTransferClear() {
            if (this.transferClearTimer) {
                clearTimeout(this.transferClearTimer);
            }
            this.transferClearTimer = setTimeout(() => {
                this.clearTransfer();
            }, 1200);
        },

        async readErrorResponse(response) {
            const text = await response.text();
            if (!text) return response.statusText || `HTTP ${response.status}`;
            try {
                const data = JSON.parse(text);
                return data.error || data.message || text;
            } catch (error) {
                return text;
            }
        },

        saveBlob(blob, fileName) {
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = fileName;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
        },

        async downloadFile(file) {
            let downloadUrl;
            const path = this.joinPath(this.currentPath, file.name);
            if (this.isRemoteLocalMode) {
                downloadUrl = `/api/remote/file/download?session_id=${encodeURIComponent(this.sessionId)}&path=${encodeURIComponent(path)}`;
            } else if (this.isLocalMode) {
                downloadUrl = `/api/local/file/download?path=${encodeURIComponent(path)}`;
            } else {
                downloadUrl = `/api/sftp/download?session_id=${this.sftpSessionId}&path=${encodeURIComponent(path)}`;
            }

            this.startTransfer('download', file.name, file.size || 0, '准备下载...');
            try {
                const response = await fetch(downloadUrl);
                if (!response.ok) {
                    throw new Error(await this.readErrorResponse(response));
                }

                const headerTotal = Number(response.headers.get('Content-Length')) || 0;
                const total = headerTotal || file.size || 0;
                this.updateTransferProgress(0, total, '下载中...');

                if (!response.body || !response.body.getReader) {
                    const blob = await response.blob();
                    this.updateTransferProgress(blob.size, total || blob.size, '保存文件...');
                    this.saveBlob(blob, file.name);
                    this.completeTransfer('下载完成');
                    return;
                }

                const reader = response.body.getReader();
                const chunks = [];
                let loaded = 0;

                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    chunks.push(value);
                    loaded += value.byteLength;
                    this.updateTransferProgress(loaded, total, '下载中...');
                }

                const blob = new Blob(chunks, {
                    type: response.headers.get('Content-Type') || 'application/octet-stream'
                });
                this.updateTransferProgress(total || loaded, total || loaded, '保存文件...');
                this.saveBlob(blob, file.name);
                this.completeTransfer('下载完成');
            } catch (error) {
                this.failTransfer(`文件 ${file.name} 下载失败：${error.message}`);
            }
        },

        triggerUpload() {
            this.$refs.fileInput.click();
        },

        onDragOver(e) {
            this.isDragging = true;
        },

        onDragLeave(e) {
            this.isDragging = false;
        },

        async onDrop(e) {
            this.isDragging = false;
            const files = e.dataTransfer.files;
            if (files.length > 0) {
                for (let i = 0; i < files.length; i++) {
                    await this.uploadSingleFile(files[i]);
                }
            }
        },

        async handleFileUpload(event) {
            const files = event.target.files;
            if (!files || files.length === 0) return;

            for (let i = 0; i < files.length; i++) {
                await this.uploadSingleFile(files[i]);
            }

            event.target.value = '';
        },

        async uploadSingleFile(file) {
            const formData = new FormData();
            formData.append('file', file);

            const remotePath = this.joinPath(this.currentPath, file.name);

            try {
                let url;
                if (this.isRemoteLocalMode) {
                    url = `/api/remote/file/upload?session_id=${encodeURIComponent(this.sessionId)}&path=${encodeURIComponent(remotePath)}`;
                } else if (this.isLocalMode) {
                    url = `/api/local/file/upload?path=${encodeURIComponent(remotePath)}`;
                } else {
                    url = `/api/sftp/upload?session_id=${this.sftpSessionId}&path=${encodeURIComponent(remotePath)}`;
                }

                this.startTransfer('upload', file.name, file.size || 0, '准备上传...');
                const data = await this.uploadWithProgress(url, formData);

                if (data.success) {
                    this.completeTransfer('上传完成');
                    await this.loadFileList();
                } else {
                    throw new Error(data.error || '未知错误');
                }
            } catch (error) {
                this.failTransfer(`文件 ${file.name} 上传失败：${error.message}`);
            }
        },

        uploadWithProgress(url, formData) {
            return new Promise((resolve, reject) => {
                const xhr = new XMLHttpRequest();
                xhr.open('POST', url, true);

                xhr.upload.onprogress = (event) => {
                    if (event.lengthComputable) {
                        const status = event.loaded >= event.total ? '服务器处理中...' : '上传中...';
                        this.updateTransferProgress(event.loaded, event.total, status);
                    } else {
                        this.transfer.status = '上传中...';
                    }
                };

                xhr.onload = () => {
                    let data = null;
                    try {
                        data = xhr.responseText ? JSON.parse(xhr.responseText) : {};
                    } catch (error) {
                        reject(new Error(xhr.responseText || '服务器响应格式错误'));
                        return;
                    }

                    if (xhr.status >= 200 && xhr.status < 300 && data.success !== false) {
                        this.updateTransferProgress(this.transfer.total || this.transfer.loaded, this.transfer.total || this.transfer.loaded, '处理完成...');
                        resolve(data);
                        return;
                    }

                    reject(new Error((data && data.error) || xhr.responseText || `上传失败 (${xhr.status})`));
                };

                xhr.onerror = () => reject(new Error('网络错误或连接已断开'));
                xhr.onabort = () => reject(new Error('上传已取消'));
                xhr.ontimeout = () => reject(new Error('上传超时'));

                xhr.send(formData);
            });
        },

        async createFolder() {
            if (!this.newFolderName) return;

            const remotePath = this.joinPath(this.currentPath, this.newFolderName);

            try {
                let url;
                if (this.isRemoteLocalMode) {
                    url = `/api/remote/file/mkdir?session_id=${encodeURIComponent(this.sessionId)}&path=${encodeURIComponent(remotePath)}`;
                } else if (this.isLocalMode) {
                    url = `/api/local/file/mkdir?path=${encodeURIComponent(remotePath)}`;
                } else {
                    url = `/api/sftp/mkdir?session_id=${this.sftpSessionId}&path=${encodeURIComponent(remotePath)}`;
                }

                const response = await fetch(url, {
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

            const remotePath = this.joinPath(this.currentPath, file.name);

            try {
                let url;
                if (this.isRemoteLocalMode) {
                    url = `/api/remote/file/remove?session_id=${encodeURIComponent(this.sessionId)}&path=${encodeURIComponent(remotePath)}`;
                } else if (this.isLocalMode) {
                    url = `/api/local/file/remove?path=${encodeURIComponent(remotePath)}`;
                } else {
                    url = `/api/sftp/remove?session_id=${this.sftpSessionId}&path=${encodeURIComponent(remotePath)}`;
                }

                const response = await fetch(url, {
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

        // 添加跳板机
        addJumpHost() {
            if (!this.config.jumpHosts) {
                this.config.jumpHosts = [];
            }
            if (this.config.jumpHosts.length >= 4) {
                alert('最多支持 4 层跳板机');
                return;
            }
            this.config.jumpHosts.push({
                host: '',
                port: 22,
                username: '',
                password: '',
                authMethod: 'password',
                privateKey: '',
                passphrase: ''
            });
        },

        // 删除跳板机
        removeJumpHost(index) {
            if (!this.config.jumpHosts) return;
            this.config.jumpHosts.splice(index, 1);
            // 如果删除后数组为空，设置为 null
            if (this.config.jumpHosts.length === 0) {
                this.config.jumpHosts = null;
            }
        },

        // 跳板机认证方式切换
        onJumpAuthMethodChange(jump) {
            if (jump.authMethod === 'key' && !jump.privateKey) {
                jump.privateKey = '';
            }
            if (jump.authMethod === 'password') {
                jump.password = jump.password || '';
            }
        },

        // ==================== 系统仪表盘 ====================

        managementSupported() {
            return WebSSHManagement.managementSupported(this);
        },

        managementEndpoint(path, params) {
            return WebSSHManagement.managementEndpoint(this, path, params);
        },

        async fetchSystemInfo() {
            if (!this.managementSupported()) {
                this.systemInfo = null;
                return;
            }
            try {
                var resp = await fetch(this.managementEndpoint('system/info'));
                if (!resp.ok) throw new Error(resp.statusText);
                var data = await resp.json();
                this.systemInfo = data;
            } catch (e) {
                console.error('Failed to fetch system info:', e);
                this.systemInfo = null;
            }
        },

        openDashboard() {
            if (this.managementSupported()) {
                this.showDashboard = true;
            }
        },

        formatUptime(seconds) {
            if (!seconds) return '未知';
            var days = Math.floor(seconds / 86400);
            var hours = Math.floor((seconds % 86400) / 3600);
            var minutes = Math.floor((seconds % 3600) / 60);
            if (days > 0) return days + '天' + hours + '小时' + minutes + '分钟';
            if (hours > 0) return hours + '小时' + minutes + '分钟';
            return minutes + '分钟';
        },

        getUsageColor(percent) {
            if (percent >= 90) return '#ef4444';
            if (percent >= 70) return '#f59e0b';
            return '#22c55e';
        },

        // ==================== Docker 管理 ====================

        async checkDockerAvailable() {
            if (!this.managementSupported()) {
                this.dockerAvailable = false;
                return;
            }
            try {
                var resp = await fetch(this.managementEndpoint('docker/available'));
                if (!resp.ok) throw new Error(resp.statusText);
                var data = await resp.json();
                this.dockerAvailable = data.available === true;
            } catch (e) {
                this.dockerAvailable = false;
            }
        },

        async fetchDockerContainers() {
            if (!this.managementSupported()) {
                this.dockerContainers = [];
                return;
            }
            this.dockerLoading = true;
            try {
                var resp = await fetch(this.managementEndpoint('docker/containers'));
                if (!resp.ok) throw new Error(resp.statusText);
                var data = await resp.json();
                this.dockerContainers = data.containers || [];
            } catch (e) {
                console.error('Failed to fetch containers:', e);
                this.dockerContainers = [];
            }
            this.dockerLoading = false;
        },

        async fetchDockerImages() {
            if (!this.managementSupported()) {
                this.dockerImages = [];
                return;
            }
            this.dockerLoading = true;
            try {
                var resp = await fetch(this.managementEndpoint('docker/images'));
                if (!resp.ok) throw new Error(resp.statusText);
                var data = await resp.json();
                this.dockerImages = data.images || [];
            } catch (e) {
                console.error('Failed to fetch images:', e);
                this.dockerImages = [];
            }
            this.dockerLoading = false;
        },

        async dockerAction(containerId, action) {
            if (!this.managementSupported()) return;
            var payload = { id: containerId };
            if (action === 'remove') {
                if (!confirm('确认删除此容器？')) return;
                payload.force = true;
            }
            try {
                var resp = await fetch(this.managementEndpoint('docker/container/' + action), {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                var data = await resp.json();
                if (data.success) {
                    this.fetchDockerContainers();
                } else {
                    alert('操作失败: ' + (data.error || '未知错误'));
                }
            } catch (e) {
                alert('操作失败: ' + e.message);
            }
        },

        async viewContainerLogs(containerId) {
            if (!this.managementSupported()) return;
            this.dockerSelectedContainer = containerId;
            this.dockerLogsLoading = true;
            this.dockerContainerLogs = '加载中...';

            // 先获取当前容器的日志大小（从列表中查找）
            var ctr = null;
            for (var i = 0; i < this.dockerContainers.length; i++) {
                if (this.dockerContainers[i].id === containerId) {
                    ctr = this.dockerContainers[i];
                    break;
                }
            }
            if (ctr) {
                this.dockerLogSize = ctr.log_size || 0;
                this.dockerLogPath = ctr.log_path || '';
            }

            // 根据日志大小决定加载行数
            var tail = 500;
            if (this.dockerLogSize > 1073741824) { // > 1GB
                tail = 200;
            } else if (this.dockerLogSize > 104857600) { // > 100MB
                tail = 300;
            }

            try {
                var resp = await fetch(this.managementEndpoint('docker/container/logs', { id: containerId, tail: tail }));
                var data = await resp.json();
                this.dockerContainerLogs = data.logs || '(无日志)';
            } catch (e) {
                this.dockerContainerLogs = '获取日志失败: ' + e.message;
            }
            this.dockerLogsLoading = false;
        },

        async clearContainerLogs(containerId) {
            if (!this.managementSupported()) return;
            if (!confirm('确认清空此容器的日志？清空后无法恢复。')) return;
            try {
                var resp = await fetch(this.managementEndpoint('docker/container/clear-logs'), {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ id: containerId })
                });
                var data = await resp.json();
                if (data.success) {
                    // 更新列表中该容器的日志大小
                    for (var i = 0; i < this.dockerContainers.length; i++) {
                        if (this.dockerContainers[i].id === containerId) {
                            this.dockerContainers[i].log_size = 0;
                            break;
                        }
                    }
                    // 如果正在查看该容器的日志，更新面板
                    if (this.dockerSelectedContainer === containerId) {
                        this.dockerLogSize = 0;
                        this.dockerContainerLogs = '(日志已清空)';
                    }
                    alert('日志已清空');
                } else {
                    alert('清空失败: ' + (data.error || '未知错误'));
                }
            } catch (e) {
                alert('清空失败: ' + e.message);
            }
        },

        formatDockerSize(bytes) {
            if (!bytes || bytes <= 0) return '0 B';
            var k = 1024;
            var sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
            var i = Math.min(Math.max(Math.floor(Math.log(bytes) / Math.log(k)), 0), sizes.length - 1);
            return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
        },

        formatDockerTimestamp(ts) {
            if (!ts) return '-';
            var d = new Date(ts * 1000);
            var pad = function(n) { return n < 10 ? '0' + n : '' + n; };
            return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' +
                   pad(d.getHours()) + ':' + pad(d.getMinutes());
        },

        getContainerStateClass(state) {
            var s = (state || '').toLowerCase();
            if (s === 'running') return 'bg-green-100 dark:bg-green-900/30 text-green-600 dark:text-green-400';
            if (s === 'exited' || s === 'dead') return 'bg-red-100 dark:bg-red-900/30 text-red-600 dark:text-red-400';
            if (s === 'paused') return 'bg-yellow-100 dark:bg-yellow-900/30 text-yellow-600 dark:text-yellow-400';
            return 'bg-slate-100 dark:bg-slate-700 text-slate-600 dark:text-slate-400';
        },

        // 根据文件扩展名返回本地图标名称，不依赖客户端 Emoji 字体
        getFileIcon(filename) {
            const ext = filename.split('.').pop().toLowerCase();
            const iconMap = {
                // 图片
                'jpg': 'image', 'jpeg': 'image', 'png': 'image', 'gif': 'image', 'bmp': 'image', 'svg': 'image', 'webp': 'image',
                // 文档
                'pdf': 'document', 'doc': 'document', 'docx': 'document', 'txt': 'text', 'md': 'text',
                // 表格
                'xls': 'sheet', 'xlsx': 'sheet', 'csv': 'sheet',
                // 压缩
                'zip': 'archive', 'tar': 'archive', 'gz': 'archive', 'rar': 'archive', '7z': 'archive',
                // 代码
                'js': 'code', 'ts': 'code', 'py': 'code', 'go': 'code', 'java': 'code', 'c': 'code', 'cpp': 'code', 'h': 'code', 'hpp': 'code',
                'sh': 'terminal', 'bash': 'terminal', 'zsh': 'terminal', 'fish': 'terminal',
                'html': 'web', 'htm': 'web', 'css': 'code', 'scss': 'code', 'less': 'code',
                'json': 'settings', 'xml': 'settings', 'yaml': 'settings', 'yml': 'settings', 'toml': 'settings',
                // 媒体
                'mp3': 'audio', 'wav': 'audio', 'flac': 'audio', 'ogg': 'audio',
                'mp4': 'video', 'avi': 'video', 'mkv': 'video', 'mov': 'video', 'wmv': 'video',
                // 可执行
                'exe': 'application', 'bin': 'application', 'run': 'application', 'app': 'application',
                // 配置
                'conf': 'settings', 'config': 'settings', 'ini': 'settings', 'env': 'settings',
                // 日志
                'log': 'log',
                // 默认
                '': 'file'
            };
            return iconMap[ext] || 'file';
        },

        fileIconHref(filename) {
            return '/vendor/icons.svg#file-' + this.getFileIcon(filename);
        },

        formatFileSize(size) {
            if (!size || size <= 0) return '0 B';
            const k = 1024;
            const sizes = ['B', 'KB', 'MB', 'GB'];
            const i = Math.min(Math.max(Math.floor(Math.log(size) / Math.log(k)), 0), sizes.length - 1);
            return Math.round(size / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
        },

        formatSpeed(bytesPerSecond) {
            if (!bytesPerSecond || bytesPerSecond < 0) return '0 B/s';
            return `${this.formatFileSize(bytesPerSecond)}/s`;
        }
    }
}).mount('#app');
