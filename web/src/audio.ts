// Audio helpers: microphone capture -> base64 payload, level meter, formatting.

export class Recorder {
  private rec: MediaRecorder | null = null
  private stream: MediaStream | null = null
  private ctx: AudioContext | null = null
  private raf = 0
  private chunks: Blob[] = []
  private startedAt = 0

  async start(onLevel: (level: number) => void): Promise<void> {
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    })
    const mime = ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4'].find((m) =>
      MediaRecorder.isTypeSupported?.(m),
    )
    this.rec = new MediaRecorder(
      this.stream,
      mime ? { mimeType: mime, audioBitsPerSecond: 64000 } : undefined,
    )
    this.chunks = []
    this.rec.ondataavailable = (ev) => {
      if (ev.data.size) this.chunks.push(ev.data)
    }
    this.rec.start(250)

    // level meter
    this.ctx = new AudioContext()
    const src = this.ctx.createMediaStreamSource(this.stream)
    const analyser = this.ctx.createAnalyser()
    analyser.fftSize = 512
    src.connect(analyser)
    const buf = new Uint8Array(analyser.frequencyBinCount)
    this.startedAt = performance.now()
    const tick = () => {
      analyser.getByteTimeDomainData(buf)
      let peak = 0
      for (let i = 0; i < buf.length; i++) peak = Math.max(peak, Math.abs(buf[i] - 128) / 128)
      onLevel(peak)
      this.raf = requestAnimationFrame(tick)
    }
    this.raf = requestAnimationFrame(tick)
  }

  /** Stops and resolves the recorded blob (webm/opus or mp4). */
  stop(): Promise<{ blob: Blob; durationSec: number; mime: string }> {
    return new Promise((resolve) => {
      const rec = this.rec
      if (!rec || rec.state === 'inactive') {
        this.cleanup()
        resolve({ blob: new Blob(this.chunks), durationSec: 0, mime: '' })
        return
      }
      rec.onstop = () => {
        const durationSec = (performance.now() - this.startedAt) / 1000
        const blob = new Blob(this.chunks, { type: rec.mimeType || 'audio/webm' })
        this.cleanup()
        resolve({ blob, durationSec, mime: rec.mimeType || 'audio/webm' })
      }
      rec.stop()
    })
  }

  private cleanup() {
    cancelAnimationFrame(this.raf)
    this.ctx?.close().catch(() => {})
    this.stream?.getTracks().forEach((t) => t.stop())
    this.rec = null
    this.stream = null
    this.ctx = null
  }
}

export function blobToB64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => {
      const s = String(r.result)
      resolve(s.slice(s.indexOf(',') + 1)) // strip data: prefix
    }
    r.onerror = reject
    r.readAsDataURL(blob)
  })
}

export function fmtDuration(sec: number): string {
  const s = Math.max(0, Math.round(sec))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

export function fmtSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

/**
 * Encode a recorded blob as 16kHz mono 16-bit PCM WAV — the one format the
 * Windows SoundPlayer on classroom PCs plays natively without codecs.
 */
export async function toWav16k(blob: Blob): Promise<Blob> {
  const ctx = new AudioContext()
  try {
    const buf = await ctx.decodeAudioData(await blob.arrayBuffer())
    const outRate = 16000
    const ratio = buf.sampleRate / outRate
    const outLen = Math.floor(buf.numberOfChannels === 2 ? Math.ceil(buf.length / ratio) : buf.length / ratio)
    // mixdown to mono
    const mono = new Float32Array(buf.length)
    for (let ch = 0; ch < buf.numberOfChannels; ch++) {
      const d = buf.getChannelData(ch)
      for (let i = 0; i < buf.length; i++) mono[i] += d[i] / buf.numberOfChannels
    }
    // nearest-neighbour resample (speech quality is fine at 16k)
    const out = new Float32Array(outLen)
    for (let i = 0; i < outLen; i++) out[i] = mono[Math.min(mono.length - 1, Math.floor(i * ratio))]

    const bytes = new DataView(new ArrayBuffer(44 + outLen * 2))
    const w = (off: number, s: string) => { for (let i = 0; i < s.length; i++) bytes.setUint8(off + i, s.charCodeAt(i)) }
    w(0, 'RIFF')
    bytes.setUint32(4, 36 + outLen * 2, true)
    w(8, 'WAVE')
    w(12, 'fmt ')
    bytes.setUint32(16, 16, true)
    bytes.setUint16(20, 1, true) // PCM
    bytes.setUint16(22, 1, true) // mono
    bytes.setUint32(24, outRate, true)
    bytes.setUint32(28, outRate * 2, true)
    bytes.setUint16(32, 2, true)
    bytes.setUint16(34, 16, true)
    w(36, 'data')
    bytes.setUint32(40, outLen * 2, true)
    for (let i = 0; i < outLen; i++) {
      const s = Math.max(-1, Math.min(1, out[i]))
      bytes.setInt16(44 + i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true)
    }
    return new Blob([bytes.buffer], { type: 'audio/wav' })
  } finally {
    ctx.close().catch(() => {})
  }
}
