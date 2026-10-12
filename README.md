# 教室传话系统 (Classroom MessageSender)

老师向多台教室电脑实时传话：**TTS 语音播报**、**录音直发**、**文字弹窗通知**。

- 后端（两种可选）：Go 自建 WebSocket 中继 **或** Cloudflare Worker (Durable Objects)
- 教室电脑客户端：Go（SAPI TTS / WAV 播放 / 弹窗）
- 前端：Vue 3 + Ant Design Vue 4（纯静态）

```
┌──────────────┐        WSS        ┌───────────────┐        WS         ┌──────────────┐
│  老师浏览器    │ ───────────────▶ │    中继服务     │ ───────────────▶ │ 教室电脑 ×N   │
│  (教师控制台)  │                   │  Go 或 CF DO   │                   │ relay-client │
└──────────────┘ ◀─── ack/report ──└───────────────┘◀── ack/reconnect ─└──────────────┘
```

## 两种服务端，一个协议

中继协议是同一套 JSON envelope，三种角色**互接互换，零改动**：

| 服务端 | 目录 | 适用 |
|--------|------|------|
| Go relay-server | `cmd/relay-server` | 教师机/内网服务器直接跑，单二进制，也可托管前端 |
| Cloudflare Worker | `worker/` | **无服务器部署**：`npx wrangler deploy` 一条命令上线，免费额度内可跑，前端+中继一体 |

已实测：Go 客户端与 Vue 前端连接 CF Worker（本地 wrangler dev），
presence/msg/report/ack 全链路与 Go 服务端行为一致（SMOKE-PASS）。

> 为什么 Worker 必须用 Durable Objects：普通 Worker 请求间不共享状态、
> 无法持有 WebSocket 集线器；DO + WebSocket Hibernation API 才能做广播。
> 空闲 socket 几乎零成本，SQLite-backed DO 类可在免费计划使用。

## 目录

```
relay/            Go 中继核心（Hub + WebSocket，含单元/集成测试）
worker/           CF Worker 中继（Durable Objects，协议与 Go 版一致）
cmd/relay-server  Go 服务器：WS 中继 + 可选静态托管 (-dist web/dist)
cmd/relay-client  教室电脑客户端：SAPI TTS 播报、WAV 播放、弹窗、断线重连
cmd/smoke-teacher 端到端冒烟测试（模拟教师发 TTS，验证 ack 回流）
web/              Vue 3 + Ant Design Vue 前端
```

## 快速开始（本机演示，Go 服务端）

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

## 部署到 Cloudflare（Worker 当服务端，推荐免运维方案）

> ⚠️ 注意：中继用的是 **Durable Objects**，必须部署为 **Workers** 项目。
> **不要建 Pages 项目** —— Pages 只能跑静态页/函数，跑不了 DO，
> 且在 Pages 里执行 `wrangler deploy` 会报 `Missing entry-point`（根目录已无 wrangler.toml）。

**方式一：本地/命令行部署（最简单）**

```bash
cd web && npm install && npm run build && cd ..
cd worker && npm install
cp .dev.vars.example .dev.vars        # 本地开发 token；生产用 wrangler secret
npx wrangler dev                      # 本地试跑: http://127.0.0.1:8787
npx wrangler login                    # 首次登录（浏览器授权）
npx wrangler secret put TOKEN         # 生产鉴权（可跳过=无鉴权）
npx wrangler deploy                   # 上线，得到 https://classroom-relay.<账号>.workers.dev
```

**方式二：Git 连接自动部署（Workers Builds）**

在 Cloudflare 控制台创建 **Workers & Pages → Create → Workers → Connect to Git**，选本仓库后设置：

| 设置项 | 值 |
|--------|-----|
| Root directory | `/`（仓库根） |
| Build command | `cd web && npm ci && npm run build` |
| Deploy command | `cd worker && npm ci && npx wrangler deploy` |

（若面板要求填写 wrangler 配置路径，填 `worker/wrangler.toml`。）

部署后：
- 老师：`https://classroom-relay.xxx.workers.dev/#/teacher`
- 教室电脑：`relay-client.exe -url wss://classroom-relay.xxx.workers.dev/ws -token <token>`

注意：Worker 单条 WS 消息上限 1MiB，语音直发请控制在 ~20 秒内（超限会被拒绝并提示）。

### 费用：免费计划够用

Durable Objects 在 **Workers Free 计划内免费**（本项目的 `new_sqlite_classes` 配置正是免费计划要求的 SQLite 后端）。按官方计费规则估算本项目的用量：

| 计费项 | 免费额度 | 本项目用量 |
|--------|----------|-----------|
| DO 请求 | 10 万/天 | 建 WS 连接时每台 1 次；**收到的**消息按 20:1 折算（客户端 20s 心跳 ×50 台 ×24h ≈ 1.1 万/天）；**发出的**消息（presence/消息广播）完全免费 |
| DO 时长 | 13,000 GB-s/天 | 用了 Hibernation API：空闲即休眠不计费，只在处理消息的毫秒级时间计费 |
| DO 存储 | 5 GB | **0** —— 本中继不使用 DO 存储，连接状态在内存+附件 |
| 静态资源请求 | 免费无限 | 前端页面的加载 |

教室场景（每天 8–10 节课、几十台电脑）远达不到上限。超出免费额度时 Free 计划的行为是**直接报错停止服务**（不会悄悄扣费）；需要更大余量再升 Workers Paid（$5/月起，含 100 万请求/月）。

## 前端单独部署（静态托管，需另行运行中继）

| 平台 | 配置 | 命令 |
|------|------|------|
| Netlify | `netlify.toml` | `netlify deploy --prod` |
| Vercel | `vercel.json` | `vercel --prod` |
| CF Pages | 无需配置文件 | `npx wrangler pages deploy web/dist` |

部署后在教师控制台「服务器」设置里填中继地址，如 `wss://classroom-relay.xxx.workers.dev/ws`。

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

- `token`/`TOKEN` 共享令牌：register 时校验，防止陌生连接
- Go 服务端置于反代后可启用 TLS（wss://）；Worker 天然 https/wss
- 教室电脑以当前用户运行即可；弹窗与 TTS 无需管理员权限

## 测试

```bash
go test ./relay/            # 11 单元 + 3 集成（真实 WS 端到端）
cd worker && npx tsc --noEmit  # Worker 类型检查
go run ./cmd/smoke-teacher    # 冒烟：需服务器+客户端在线（MS_URL/MS_TOKEN 可覆盖）
```

## 教室电脑开机自启（Windows）

把客户端快捷方式放入启动文件夹 (Win+R → `shell:startup`)：

```
目标: C:\path\relay-client.exe -url wss://中继地址/ws -id pc-01 -name 一号机房 -token mysecret
```

或用任务计划程序设置登录时启动、断电重启后自动恢复。
