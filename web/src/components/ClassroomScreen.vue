<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  Alert, Badge, Button, Card, Input, Layout, LayoutContent, LayoutHeader,
  message, Modal, Space, Tag, Typography,
} from 'ant-design-vue'
import { AudioOutlined, DesktopOutlined, ReloadOutlined, SettingOutlined, SoundOutlined } from '@ant-design/icons-vue'
import { RelayClient, loadSettings, saveSettings } from '../relay'
import { fmtDuration } from '../audio'

const { Text, Title } = Typography

type Settings = { url: string; token: string; id: string; name: string }
const settings = reactive<Settings>(loadSettings('client', {
  url: (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws',
  token: '',
  id: 'pc-' + Math.random().toString(36).slice(2, 6),
  name: '',
}))
const showSettings = ref(false)
const client = new RelayClient()
const status = ref<'idle' | 'connecting' | 'open' | 'closed'>('idle')
client.on('status', () => {
  status.value = client.status
  if (client.status === 'open') {
    client.send({
      type: 'register', role: 'client', clientId: settings.id,
      name: settings.name || settings.id, token: settings.token,
    })
  }
})
const speaking = ref(false)
const lastSpoken = ref('')

/* message history for the classroom screen */
type HistItem = { time: string; kind: string; text: string; playing: boolean }
const history = ref<HistItem[]>([])

/* Web Speech TTS */
let voice: SpeechSynthesisVoice | null = null
onMounted(() => {
  const pick = () => {
    const vs = speechSynthesis.getVoices()
    voice = vs.find((v) => v.lang.startsWith('zh')) ?? vs[0] ?? null
  }
  pick()
  speechSynthesis.onvoiceschanged = pick
})

function speakWeb(text: string, onDone: () => void) {
  const u = new SpeechSynthesisUtterance(text)
  u.lang = 'zh-CN'
  if (voice) u.voice = voice
  u.onend = onDone
  u.onerror = onDone
  speechSynthesis.speak(u)
}

client.on('msg', (e) => {
  const p = e.payload ?? { kind: 'text', text: '' }
  const ack = (state: string) => client.send({ type: 'ack', ack: { msgId: e.msgId!, from: settings.id, state } })
  ack('received')
  const item: HistItem = { time: new Date().toLocaleTimeString('zh-CN', { hour12: false }), kind: p.kind!, text: p.text ?? '', playing: false }
  history.value.unshift(item)

  if (p.kind === 'tts') {
    item.playing = true
    speaking.value = true
    lastSpoken.value = p.text ?? ''
    speakWeb(p.text ?? '', () => {
      item.playing = false
      speaking.value = false
      ack('spoken')
    })
  } else if (p.kind === 'audio') {
    item.playing = true
    speaking.value = true
    // decode base64 wav and play
    const bin = atob(p.b64 ?? '')
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    const ctx = new AudioContext()
    ctx.decodeAudioData(bytes.buffer).then((buf) => {
      const src = ctx.createBufferSource()
      src.buffer = buf
      src.connect(ctx.destination)
      src.onended = () => {
        item.playing = false
        speaking.value = false
        ctx.close()
        ack('spoken')
      }
      src.start()
    }).catch(() => {
      item.playing = false
      speaking.value = false
      ctx.close()
      message.error('音频解码失败')
    })
  } else {
    ack('displayed')
  }
})
client.on('replaced', () => { message.warning('本机 ID 被新连接取代') })

function connect() {
  saveSettings('client', { ...settings })
  client.connect(settings.url, settings.token)
}
connect()
onBeforeUnmount(() => { client.close(); speechSynthesis.cancel() })
</script>

<template>
  <Layout style="min-height: 100vh">
    <LayoutHeader class="header">
      <div class="brand">
        <DesktopOutlined />
        <span>教室传话 · 教室电脑 {{ settings.id }}</span>
        <Tag v-if="speaking" color="red" class="speaking-tag"><SoundOutlined /> 正在播放</Tag>
      </div>
      <div class="header-right">
        <Badge :status="status === 'open' ? 'success' : status === 'connecting' ? 'processing' : 'error'"
               :text="status === 'open' ? '已连接' : status === 'connecting' ? '连接中…' : '未连接'" />
        <Button size="small" @click="showSettings = true"><SettingOutlined /> 设置</Button>
      </div>
    </LayoutHeader>

    <LayoutContent class="content">
      <Alert v-if="status !== 'open'" type="error" show-icon style="margin-bottom:16px"
             message="未连接" :description="`无法连接 ${settings.url}，请在设置中检查地址`" />

      <Card class="big-display" :class="{ active: speaking }">
        <div v-if="lastSpoken && speaking" class="now-speaking">{{ lastSpoken }}</div>
        <div v-else-if="lastSpoken" class="now-speaking dim">{{ lastSpoken }}</div>
        <div v-else class="now-speaking dim">等待教师消息…</div>
      </Card>

      <Card title="收到的消息" style="margin-top:16px; border-radius:10px">
        <div v-if="history.length === 0" class="empty">
          <Text type="secondary">还没有收到消息</Text>
        </div>
        <div v-for="(h, i) in history" :key="i" class="hist-item">
          <Tag :color="h.kind === 'tts' ? 'blue' : h.kind === 'audio' ? 'red' : 'green'">
            {{ h.kind === 'tts' ? 'TTS' : h.kind === 'audio' ? '语音' : '通知' }}
          </Tag>
          <div class="hist-text" :class="{ playing: h.playing }">{{ h.text || '(语音消息)' }}</div>
          <Text type="secondary">{{ h.time }}</Text>
        </div>
      </Card>
    </LayoutContent>
  </Layout>

  <Modal v-model:open="showSettings" title="教室电脑设置" ok-text="重连" cancel-text="关闭" @ok="connect">
    <Space direction="vertical" style="width:100%">
      <div>
        <Text>服务器 WebSocket 地址</Text>
        <Input v-model:value="settings.url" placeholder="ws://192.168.1.10:8080/ws" />
      </div>
      <div>
        <Text>本机 ID（唯一，重连后保持）</Text>
        <Input v-model:value="settings.id" />
      </div>
      <div>
        <Text>显示名称</Text>
        <Input v-model:value="settings.name" placeholder="如：一号机房-03" />
      </div>
      <div>
        <Text>Token</Text>
        <Input.Password v-model:value="settings.token" />
      </div>
    </Space>
  </Modal>
</template>

<style scoped>
.header {
  display: flex; align-items: center; justify-content: space-between;
  background: #001529; padding: 0 20px; height: 56px; line-height: 56px;
}
.brand { color: #fff; font-size: 17px; font-weight: 600; display: flex; align-items: center; gap: 10px; }
.speaking-tag { margin-left: 8px; }
.header-right { display: flex; align-items: center; gap: 12px; color: #fff; }
.header-right :deep(.ant-badge) { color: #fff; }
.content { padding: 16px; max-width: 900px; margin: 0 auto; }

.big-display {
  border-radius: 12px; padding: 8px;
  border: 2px solid #f0f0f0;
  transition: border-color 0.3s, box-shadow 0.3s;
}
.big-display.active {
  border-color: #1677ff;
  box-shadow: 0 0 24px rgba(22, 119, 255, 0.25);
}
.now-speaking { font-size: 34px; font-weight: 600; text-align: center; padding: 28px 12px; word-break: break-all; }
.now-speaking.dim { color: #bbb; font-weight: 400; }

.hist-item {
  display: flex; align-items: center; gap: 10px;
  padding: 10px 4px; border-bottom: 1px solid #f5f5f5;
}
.hist-item:last-child { border-bottom: none; }
.hist-text { flex: 1; }
.hist-text.playing { color: #1677ff; font-weight: 600; }
.empty { text-align: center; padding: 16px; }
</style>
