// Cloudflare Durable Object port of relay/hub.go — identical JSON envelope
// protocol (register | presence | msg | ack | report | replaced | pong |
// error), so the Go relay-client and the Vue frontend connect unchanged.
//
// Uses the WebSocket Hibernation API: sockets stay connected across isolate
// evictions; per-socket identity (role/id/name/voices) persists in WebSocket
// attachments and is rebuilt into the in-memory index on wake.
import { Env } from "./env"

type PeerInfo = {
  role: "teacher" | "client"
  id: string
  name: string
  voices: string[]
}

function send(ws: WebSocket, obj: unknown): void {
  // Pre-stringified envelopes must pass through untouched — stringifying a
  // string again would double-encode it on the wire.
  const raw = typeof obj === "string" ? obj : JSON.stringify(obj)
  if (ws.readyState === WebSocket.OPEN) ws.send(raw)
}

function trySend(ws: WebSocket, raw: string): boolean {
  if (ws.readyState !== WebSocket.OPEN) return false
  try {
    ws.send(raw)
    return true
  } catch {
    return false
  }
}

export class RelayHub {
  constructor(private ctx: DurableObjectState, private env: Env) {}

  private teachers = new Set<WebSocket>()
  private clients = new Map<string, WebSocket>() // clientId -> socket
  private indexed = false

  // ---- lifecycle ----

  private ensureIndex(): void {
    // After hibernation the DO is re-created with empty in-memory state;
    // identity of each live socket survives in its attachment.
    if (this.indexed) return
    this.teachers = new Set()
    this.clients = new Map()
    for (const ws of this.ctx.getWebSockets()) {
      const info = this.info(ws)
      if (!info) continue
      if (info.role === "teacher") this.teachers.add(ws)
      else this.clients.set(info.id, ws)
    }
    this.indexed = true
  }

  async fetch(req: Request): Promise<Response> {
    if (req.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return new Response("expected websocket upgrade", { status: 400 })
    }
    const pair = new WebSocketPair()
    this.ctx.acceptWebSocket(pair[1])
    return new Response(null, { status: 101, webSocket: pair[0] })
  }

  // ---- hibernation events ----

  async webSocketMessage(ws: WebSocket, msg: string | ArrayBuffer): Promise<void> {
    this.ensureIndex()
    if (typeof msg !== "string") {
      send(ws, { type: "error", error: "binary frames not supported" })
      return
    }
    let e: any
    try {
      e = JSON.parse(msg)
    } catch {
      send(ws, { type: "error", error: "bad json" })
      return
    }
    switch (e.type) {
      case "ping":
        send(ws, { type: "pong" })
        return
      case "register":
        this.register(ws, e)
        return
      case "msg":
        this.route(ws, e)
        return
      case "ack":
        this.forwardAck(ws, e)
        return
      default:
        send(ws, { type: "error", error: `unknown or premature envelope type ${e.type}` })
    }
  }

  async webSocketClose(ws: WebSocket): Promise<void> {
    this.ensureIndex()
    this.drop(ws)
  }

  async webSocketError(ws: WebSocket): Promise<void> {
    this.ensureIndex()
    this.drop(ws)
  }

  // ---- helpers ----

  private info(ws: WebSocket): PeerInfo | null {
    try {
      return ws.deserializeAttachment() as PeerInfo | null
    } catch {
      return null
    }
  }

  private drop(ws: WebSocket): void {
    this.teachers.delete(ws)
    let wasClient = false
    for (const [id, sock] of this.clients) {
      if (sock === ws) {
        this.clients.delete(id)
        wasClient = true
      }
    }
    if (wasClient) this.broadcastPresence()
  }

  private presence(): Array<{ id: string; name: string; online: boolean; voices: string[] }> {
    return [...this.clients.entries()].map(([id, ws]) => {
      const info = this.info(ws)
      return { id, name: info?.name || id, online: true, voices: info?.voices || [] }
    })
  }

  private broadcastPresence(): void {
    const raw = JSON.stringify({ type: "presence", clients: this.presence() })
    for (const t of this.teachers) send(t, raw)
  }

  // ---- protocol (mirrors relay/hub.go) ----

  private register(ws: WebSocket, e: any): void {
    const token = this.env.TOKEN ?? ""
    if (token !== "" && e.token !== token) {
      send(ws, { type: "error", error: "invalid token" })
      try {
        ws.close(1008, "invalid token")
      } catch {
        /* already closed */
      }
      return
    }
    if (e.role === "teacher") {
      ws.serializeAttachment({ role: "teacher", id: e.clientId || "teacher", name: "", voices: [] })
      this.teachers.add(ws)
      send(ws, { type: "presence", clients: this.presence() })
      return
    }
    if (e.role === "client" && typeof e.clientId === "string" && e.clientId) {
      const prev = this.clients.get(e.clientId)
      if (prev && prev !== ws) {
        send(prev, { type: "replaced" })
        try {
          prev.close(4000, "replaced")
        } catch {
          /* already closed */
        }
        this.clients.delete(e.clientId)
      }
      const info: PeerInfo = {
        role: "client",
        id: e.clientId,
        name: typeof e.name === "string" && e.name ? e.name : e.clientId,
        voices: Array.isArray(e.voices) ? e.voices : [],
      }
      ws.serializeAttachment(info)
      this.clients.set(e.clientId, ws)
      this.broadcastPresence()
      return
    }
    send(ws, { type: "error", error: "bad register envelope" })
  }

  private route(ws: WebSocket, e: any): void {
    const from = this.info(ws)
    if (!from || from.role !== "teacher") {
      send(ws, { type: "error", error: "only teachers may send messages" })
      return
    }
    const targets: string[] = Array.isArray(e.target)
      ? e.target.filter((t: unknown) => typeof t === "string" && t)
      : []
    if (targets.length === 0) {
      send(ws, { type: "error", error: "empty target" })
      return
    }
    const payload = e.payload ?? {}
    if (payload && typeof payload.b64 === "string" && payload.b64.length > 1_000_000) {
      send(ws, {
        type: "error",
        error: "payload too large (>1MB WebSocket message limit); keep voice notes within 20s",
      })
      return
    }
    const msgId: string = e.msgId || `m${Date.now()}-${Math.floor(Math.random() * 1e6)}`
    const raw = JSON.stringify({ type: "msg", msgId, target: targets, payload })

    const delivered: string[] = []
    const missed: string[] = []
    const done = new Set<string>()
    if (targets.includes("*")) {
      for (const [id, sock] of this.clients) {
        done.add(id)
        if (trySend(sock, raw)) delivered.push(id)
        else missed.push(id)
      }
    }
    for (const t of targets) {
      if (t === "*" || done.has(t)) continue
      done.add(t)
      const sock = this.clients.get(t)
      if (sock && trySend(sock, raw)) delivered.push(t)
      else missed.push(t)
    }
    send(ws, { type: "report", report: { msgId, delivered, missed } })
  }

  private forwardAck(ws: WebSocket, e: any): void {
    const from = this.info(ws)
    if (!e.ack || !e.ack.msgId) {
      send(ws, { type: "error", error: "bad ack envelope" })
      return
    }
    const ack = {
      msgId: e.ack.msgId,
      from: typeof e.ack.from === "string" && e.ack.from ? e.ack.from : from?.id || "unknown",
      state: e.ack.state || "",
    }
    const raw = JSON.stringify({ type: "ack", ack })
    for (const t of this.teachers) send(t, raw)
  }
}
