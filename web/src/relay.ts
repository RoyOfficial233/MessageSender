// WebSocket client with auto-reconnect + queued sends.
// Wire protocol (JSON envelopes) matches relay/hub.go:
//   out: register | msg | ack | ping
//   in:  presence | msg | ack | report | replaced | pong | error

export type ClientInfo = {
  id: string
  name: string
  online: boolean
  voices?: string[]
}

export type Payload = {
  kind: 'tts' | 'audio' | 'text'
  text?: string
  b64?: string
  voice?: string
  rate?: number
  volume?: number
  stay?: number
}

export type Envelope = {
  type: string
  role?: string
  clientId?: string
  name?: string
  token?: string
  voices?: string[]
  clients?: ClientInfo[]
  msgId?: string
  target?: string[]
  payload?: Payload
  ack?: { msgId: string; from: string; state: string }
  report?: { msgId: string; delivered: string[]; missed: string[] }
  error?: string
}

export type Status = 'idle' | 'connecting' | 'open' | 'closed'

export class RelayClient {
  ws: WebSocket | null = null
  url = ''
  token = ''
  status: Status = 'idle'
  private queue: string[] = []
  private handlers = new Map<string, Set<(e: Envelope) => void>>()
  private retryTimer: number | null = null
  private manualClose = false
  private retryDelay = 1000

  on(type: string, fn: (e: Envelope) => void): () => void {
    if (!this.handlers.has(type)) this.handlers.set(type, new Set())
    this.handlers.get(type)!.add(fn)
    return () => this.handlers.get(type)?.delete(fn)
  }

  private emit(type: string, e: Envelope) {
    this.handlers.get(type)?.forEach((fn) => fn(e))
    this.handlers.get('*')?.forEach((fn) => fn(e))
  }

  connect(url: string, token = '') {
    this.url = url
    this.token = token
    this.manualClose = false
    this.open()
  }

  private open() {
    if (this.ws) {
      this.ws.onclose = null
      try { this.ws.close() } catch { /* ignore */ }
    }
    this.status = 'connecting'
    this.emit('status', { type: 'status' } as Envelope)
    const ws = new WebSocket(this.url)
    this.ws = ws
    ws.onopen = () => {
      this.status = 'open'
      this.retryDelay = 1000
      this.emit('status', { type: 'status' } as Envelope)
      // flush queued frames
      while (this.queue.length) {
        const m = this.queue.shift()!
        ws.send(m)
      }
    }
    ws.onmessage = (ev) => {
      let e: Envelope
      try { e = JSON.parse(ev.data) } catch { return }
      this.emit(e.type, e)
    }
    ws.onclose = () => {
      this.status = 'closed'
      this.emit('status', { type: 'status' } as Envelope)
      if (!this.manualClose) this.scheduleReconnect()
    }
    ws.onerror = () => { /* onclose follows */ }
  }

  private scheduleReconnect() {
    if (this.retryTimer != null) return
    this.retryTimer = window.setTimeout(() => {
      this.retryTimer = null
      this.open()
    }, this.retryDelay)
    this.retryDelay = Math.min(this.retryDelay * 2, 15000)
  }

  send(e: Envelope) {
    const raw = JSON.stringify(e)
    if (this.status === 'open' && this.ws) this.ws.send(raw)
    else this.queue.push(raw)
  }

  close() {
    this.manualClose = true
    if (this.retryTimer != null) { clearTimeout(this.retryTimer); this.retryTimer = null }
    this.ws?.close()
    this.status = 'idle'
  }
}

// BroadcastChannel sync across teacher tabs (presence cache + ack log tail).
export const tabBus = typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel('ms-relay') : null

// localStorage persistence helpers (conn settings survive reloads).
export function loadSettings<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem('ms:' + key)
    return raw ? { ...fallback, ...JSON.parse(raw) } : fallback
  } catch { return fallback }
}

export function saveSettings(key: string, val: unknown) {
  try { localStorage.setItem('ms:' + key, JSON.stringify(val)) } catch { /* ignore */ }
}
