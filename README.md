# LinkBridge Backend

校园即时通讯系统后端服务，基于 Go 语言开发。

## 功能特性

- 用户注册/登录（JWT 认证）
- 一对一会话管理
- 实时消息推送（WebSocket）
- 文本/图片/文件消息支持
- 文件上传服务

## 技术栈

- Go 1.21+
- SQLite / PostgreSQL
- Gorilla WebSocket
- 标准库 HTTP 服务器

## 快速开始

```bash
# 安装依赖
go mod download

# 运行服务
go run ./cmd/api

# 或编译后运行
go build -o linkbridge-api ./cmd/api
./linkbridge-api
```

### 本地配置（不提交密钥）

将本地环境变量放到 `LinkBridgeBackend/.env.local`（该文件已在 `.gitignore` 中忽略），然后用脚本启动：

```powershell
Copy-Item .env.example .env.local
notepad .env.local
.\scripts\run-dev.ps1
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| HTTP_ADDR | :8080 | HTTP 监听地址 |
| DATABASE_URL | sqlite::memory: | 数据库连接字符串 |
| LOG_LEVEL | info | 日志级别 |
| UPLOAD_DIR | ./uploads | 文件上传目录 |
| WECHAT_APPID | (空) | 小程序 AppID（用于 VoIP 签名/订阅消息） |
| WECHAT_APPSECRET | (空) | 小程序 AppSecret（仅后端保存） |
| WECHAT_CALL_SUBSCRIBE_TEMPLATE_ID | (空) | “来电提醒”订阅消息模板 ID（可选） |
| WECHAT_CALL_SUBSCRIBE_PAGE | pages/linkbridge/call/call | 订阅消息跳转页面（可选） |

## API 端点

### 认证
- `POST /v1/auth/register` - 用户注册
- `POST /v1/auth/login` - 用户登录
- `POST /v1/auth/logout` - 用户登出
- `GET /v1/auth/me` - 获取当前用户信息

### 用户
- `GET /v1/users?q=xxx` - 搜索用户
- `GET /v1/users/:id` - 获取用户信息
- `PUT /v1/users/me` - 更新当前用户信息

### 会话
- `GET /v1/sessions?status=active` - 获取会话列表
- `POST /v1/sessions` - 创建会话
- `POST /v1/sessions/:id/archive` - 归档会话

### 消息
- `GET /v1/sessions/:id/messages` - 获取消息列表
- `POST /v1/sessions/:id/messages` - 发送消息

### 文件
- `POST /v1/upload` - 上传文件
- `GET /uploads/:filename` - 下载文件

### WebSocket
- `GET /v1/ws?token=xxx` - WebSocket 连接

## 许可证

MIT
