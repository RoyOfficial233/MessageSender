# 教室传话系统 (Classroom MessageSender)

老师向多台教室电脑实时传话：**TTS 语音播报**、**录音直发**、**文字弹窗通知**。

- 后端：Go（WebSocket 中继 + 教室电脑原生客户端）
- 前端：Vue 3 + Ant Design Vue 4（纯静态，可部署到 Netlify / Vercel / Cloudflare Pages）

```
┌──────────────┐        WSS        ┌───────────────┐        WS         ┌──────────────┐
│  老师浏览器    │ ───────────────▶ │  relay-server  │ ───────────────▶ │ 教室电脑 ×N   │
│  (教师控制台)  │                   │  (Go, 自建)    │                   │ relay-client │
└──────────────┘ ◀─── ack/report ──└───────────────┘◀── ack/reconnect ─└──────────────┘
```

## 为什么中继要自建

Netlify/Vercel/CF Pages 只能托管静态前端，**不能跑长连 WebSocket 服务**。
本方案：前端部署到任一静态平台，WebSocket 中继 `relay-server` 跑在你控制的机器上
（教师机、机房服务器、或任意 VPS / 内网电脑）。教室电脑与教师浏览器都连它。

## 目录

```
relay/            Go 中继核心（Hub + WebSocket，含单元/集成测试）
cmd/relay-server  服务器：WS 中继 + 可选静态托管 (-dist web/dist)
cmd/relay-client  教室电脑客户端：SAPI TTS 播报、WAV 播放、弹窗、断线重连
cmd/smoke-teacher 端到端冒烟测试（模拟教师发 TTS，验证 ack 回流）
web/              Vue 3 + Ant Design Vue 前端
```

## 快速开始（本机演示）

```bash
# 1. 构建全部
go build -o bin/relay-server.exe ./cmd/relay-server
go build -o bin/relay-client.exe ./cmd/relay-client
cd web && npm install && npm run build && cd ..

# 2. 启动服务器（前端也一并托管）
./bin/relay-server.exe -addr :8080 -token mysecret -dist web/dist

# 3. 每台教室电脑上启动客户端
./bin/relay-client.exe -url ws://教师机IP:8080/ws -id pc-01 -name "一号机房" -token mysecret

# 4. 老师打开浏览器
http://教师机IP:8080/#/teacher
```

## 前端部署

| 平台 | 配置 | 命令 |
|------|------|------|
| Netlify | `netlify.toml` 已就绪 | `netlify deploy --prod` |
| Vercel | `vercel.json` 已就绪 | `vercel --prod` |
| CF Pages | `wrangler.toml` 已就绪 | `npx wrangler pages deploy web/dist` |

部署后在教师控制台「服务器」设置里填中继地址，如 `ws://192.168.1.10:8080/ws`。

## 消息协议（JSON envelope）

```jsonc
// 教师 → 服务器 → 教室电脑
{ "type": "msg", "msgId": "m1", "target": ["pc-01", "*"],
  "payload": { "kind": "tts", "text": "请打开课本", "voice": "", "rate": 0, "volume": 100 } }
// payload.kind: "tts" | "audio"(b64 WAV) | "text"(弹窗)

// 服务器 → 教师：投递报告
{ "type": "report", "report": { "msgId": "m1", "delivered": ["pc-01"], "missed": [] } }

// 教室电脑 → 教师：回执
{ "type": "ack", "ack": { "msgId": "m1", "from": "pc-01", "state": "received|displayed|spoken" } }

// 在线状态广播
{ "type": "presence", "clients": [{ "id": "pc-01", "name": "一号机房", "online": true, "voices": [...] }] }
```

## 安全

- `-token` 共享令牌：register 时校验，防止陌生连接
- 生产建议置于反向代理（nginx/caddy）之后启用 TLS（wss://）
- 教室电脑以当前用户运行即可；弹窗与 TTS 无需管理员权限

## 测试

```bash
go test ./relay/          # 11 单元 + 3 集成（真实 WS 端到端）
go run ./cmd/smoke-teacher  # 冒烟：需服务器+客户端在线
```

## 教室电脑开机自启（Windows）

把客户端快捷方式放入启动文件夹 (Win+R → `shell:startup`)：

```
目标: C:\path\relay-client.exe -url ws://教师机:8080/ws -id pc-01 -name 一号机房 -token mysecret
```

或用任务计划程序设置登录时启动、断电重启后自动恢复。
