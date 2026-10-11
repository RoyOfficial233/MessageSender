<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import {
  Alert, Avatar, Badge, Button, Card, Col, Collapse, CollapsePanel, Divider,
  Dropdown, Empty, Input, Layout, LayoutContent, LayoutHeader, LayoutSider,
  List, ListItem, ListItemMeta, Menu, MenuItem, message, Modal, Popconfirm,
  Progress, Radio, RadioGroup, RadioButton, Row, Select, SelectOption, Space,
  Statistic, Switch, Tag, Tooltip, Typography,
} from 'ant-design-vue'
import {
  AudioOutlined, CheckCircleOutlined, CheckOutlined, DesktopOutlined,
  DisconnectOutlined, DownOutlined, SoundOutlined, TeamOutlined, SendOutlined,
  ThunderboltOutlined, ReloadOutlined, SettingOutlined, PlayCircleOutlined,
  CloseCircleOutlined, ExclamationCircleOutlined,
} from '@ant-design/icons-vue'
import { RelayClient, loadSettings, saveSettings, tabBus } from '../relay'
import { Recorder, blobToB64, toWav16k, fmtDuration } from '../audio'

const { Text, Title } = Typography

/* ---------------- connection ---------------- */
type Settings = { url: string; token: string }
const settings = reactive<Settings>(loadSettings('conn', {
  url: (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws',
  token: '',
}))
const showSettings = ref(false)
const client = new RelayClient()
const status = ref<'idle' | 'connecting' | 'open' | 'closed'>('idle')
client.on('status', () => { status.value = client.status })

/* ---------------- presence ---------------- */
type PC = { id: string; name: string; voices?: string[] }
const pcs = ref<PC[]>([])
const selected = ref<string[]>([])
const selectedSet = computed(() => new Set(selected.value))

client.on('presence', (e) => {
  pcs.value = (e.clients ?? []).map((c) => ({ id: c.id, name: c.name || c.id, voices: c.voices }))
  if (selected.value.length === 0 && pcs.value.length > 0) selected.value = [pcs.value[0].id]
})
client.on('error', (e) => { message.error(`Relay: ${e.error || 'error'}`) })

client.on('status', () => {
  status.value = client.status
  if (client.status === 'open') {
    client.send({
      type: 'register', role: 'teacher',
      clientId: 'teacher-' + Math.random().toString(36).slice(2, 7),
      token: settings.token,
    })
  }
})

function connect() {
  saveSettings('conn', { ...settings })
  client.connect(settings.url, settings.token)
}
connect()

/* ---------------- message log ---------------- */
type LogEntry = {
  msgId: string
  kind: string
  summary: string
  target: string[]
  time: string
  delivered: string[]
  missed: string[]
  acks: Record<string, string> // pcId -> state
}
const log = ref<LogEntry[]>([])
client.on('report', (e) => {
  const entry = log.value.find((l) => l.msgId === e.report!.msgId)
  if (entry) {
    entry.delivered = e.report!.delivered ?? []
    entry.missed = e.report!.missed ?? []
  }
})
client.on('ack', (e) => {
  const entry = log.value.find((l) => l.msgId === e.ack!.msgId)
  if (entry) entry.acks[e.ack!.from] = e.ack!.state
})

function nowStr() {
  return new Date().toLocaleTimeString('zh-CN', { hour12: false })
}
function sendMsg(payload: Record<string, unknown>, kind: string, summary: string) {
  if (selected.value.length === 0) { message.warning('请先选择目标电脑'); return }
  if (client.status !== 'open') { message.error('未连接到服务器'); return }
  const msgId = 'm' + Date.now() + Math.random().toString(36).slice(2, 6)
  log.value.unshift({
    msgId, kind, summary, target: [...selected.value],
    time: nowStr(), delivered: [], missed: [], acks: {},
  })
  client.send({
    type: 'msg', msgId, target: selected.value.includes('*') ? ['*'] : [...selected.value],
    payload: payload as never,
  })
}

/* ---------------- TTS compose ---------------- */
const ttsText = ref('')
const ttsVoice = ref<string | undefined>(undefined)
const ttsRate = ref(0)
const ttsVolume = ref(100)
const quickPhrases = [
  '请注意，现在开始上课',
  '请保持安静',
  '请保存并提交作业',
  '下节课信息技术考试，请提前预习',
  '眼睛离屏幕远一点',
]

const voiceOptions = computed(() => {
  const set = new Set<string>()
  pcs.value.forEach((p) => p.voices?.forEach((v) => set.add(v)))
  return [...set].sort()
})

function sendTts() {
  const text = ttsText.value.trim()
  if (!text) { message.warning('请输入要播报的内容'); return }
  sendMsg({ kind: 'tts', text, voice: ttsVoice.value || '', rate: ttsRate.value, volume: ttsVolume.value }, 'tts', text)
}

/* ---------------- voice recording ---------------- */
const recording = ref(false)
const level = ref(0)
const recSecs = ref(0)
const recorder = new Recorder()
let recTimer: number | null = null

async function startRec() {
  if (client.status !== 'open') { message.error('未连接'); return }
  try {
    await recorder.start((l) => { level.value = l })
    recording.value = true
    recSecs.value = 0
    recTimer = window.setInterval(() => { recSecs.value += 0.25 }, 250)
  } catch (err) {
    message.error('无法访问麦克风: ' + (err as Error).name)
  }
}

async function stopRec(send: boolean) {
  if (!recording.value) return
  recording.value = false
  if (recTimer) { clearInterval(recTimer); recTimer = null }
  const { blob } = await recorder.stop()
  if (!send) return
  if (blob.size < 2000) { message.warning('录音太短'); return }
  try {
    const wav = await toWav16k(blob)
    const b64 = await blobToB64(wav)
    sendMsg({ kind: 'audio', b64 }, 'audio', `🎙 语音 ${fmtDuration(recSecs.value)} (${Math.round(wav.size / 1024)}KB)`)
  } catch (err) {
    message.error('编码失败: ' + String(err))
  }
}

/* ---------------- text banner ---------------- */
const bannerText = ref('')
function sendBanner() {
  const text = bannerText.value.trim()
  if (!text) { message.warning('请输入通知内容'); return }
  sendMsg({ kind: 'text', text, stay: 10 }, 'text', text)
}

/* ---------------- bulk actions ---------------- */
const allSelected = computed(() => pcs.value.length > 0 && selected.value.length === pcs.value.length)
function toggleAll() {
  selected.value = allSelected.value ? [] : pcs.value.map((p) => p.id)
}

onBeforeUnmount(() => client.close())
</script>

<template>
  <Layout style="min-height: 100vh">
    <LayoutHeader class="header">
      <div class="brand">
        <ThunderboltOutlined />
        <span>教室传话 · 教师端</span>
      </div>
      <div class="header-right">
        <Badge :status="status === 'open' ? 'success' : status === 'connecting' ? 'processing' : 'error'"
               :text="status === 'open' ? '已连接' : status === 'connecting' ? '连接中…' : '未连接'" />
        <Button size="small" @click="showSettings = true">
          <SettingOutlined /> 服务器
        </Button>
      </div>
    </LayoutHeader>

    <Layout>
      <LayoutSider width="300" theme="light" class="sider" border-right>
        <div class="sider-head">
          <Title :level="5" style="margin:0"><TeamOutlined /> 在线教室电脑</Title>
          <Button size="small" type="link" @click="toggleAll">
            {{ allSelected ? '取消全选' : '全选' }}
          </Button>
        </div>
        <div v-if="pcs.length === 0" class="empty-clients">
          <Empty description="暂无教室电脑在线" :image-style="{ height: '48px' }" />
          <Text type="secondary">在每台学生机上运行 relay-client 即可</Text>
        </div>
        <div v-else class="pc-list">
          <label v-for="pc in pcs" :key="pc.id" class="pc-item" :class="{ picked: selectedSet.has(pc.id) }">
            <input type="checkbox" :value="pc.id" v-model="selected" />
            <DesktopOutlined class="pc-icon" />
            <div class="pc-meta">
              <div class="pc-name">{{ pc.name }}</div>
              <div class="pc-id">{{ pc.id }}</div>
            </div>
            <Tag v-if="pc.voices?.length" color="blue" style="margin:0">TTS×{{ pc.voices.length }}</Tag>
          </label>
        </div>
      </LayoutSider>

      <LayoutContent class="content">
        <Row :gutter="[16, 16]">
          <Col :span="24">
            <Alert v-if="status !== 'open'" type="warning" show-icon
                   message="未连接中继服务器"
                   :description="`检查 relay-server 是否运行，当前地址 ${settings.url}`" />
          </Col>

          <!-- TTS card -->
          <Col :xs="24" :lg="12">
            <Card title="语音播报 (TTS)" class="card">
              <template #extra><SoundOutlined /></template>
              <Space direction="vertical" style="width:100%" :size="12">
                <Input.TextArea v-model:value="ttsText" :rows="3" :maxlength="500" show-count
                                placeholder="输入要在教室电脑上朗读的文字…" @keydown.ctrl.enter="sendTts" />
                <div class="quick-row">
                  <Text type="secondary" style="flex-shrink:0">快捷:</Text>
                  <Tag v-for="p in quickPhrases" :key="p" class="quick-tag" @click="ttsText = p">{{ p }}</Tag>
                </div>
                <Row :gutter="8">
                  <Col :span="10">
                    <Text type="secondary">音色</Text>
                    <Select v-model:value="ttsVoice" placeholder="自动" allow-clear style="width:100%" size="small">
                      <SelectOption v-for="v in voiceOptions" :key="v" :value="v">{{ v }}</SelectOption>
                    </Select>
                  </Col>
                  <Col :span="7">
                    <Text type="secondary">语速 {{ ttsRate > 0 ? '+' : '' }}{{ ttsRate }}</Text>
                    <input type="range" min="-5" max="5" step="1" v-model.number="ttsRate" class="slider" />
                  </Col>
                  <Col :span="7">
                    <Text type="secondary">音量 {{ ttsVolume }}%</Text>
                    <input type="range" min="0" max="100" step="5" v-model.number="ttsVolume" class="slider" />
                  </Col>
                </Row>
                <Button type="primary" block @click="sendTts" :disabled="!ttsText.trim()">
                  <SendOutlined /> 播报到选中电脑 (Ctrl+Enter)
                </Button>
              </Space>
            </Card>
          </Col>

          <!-- Voice record card -->
          <Col :xs="24" :lg="12">
            <Card title="实时喊话 (录音直发)" class="card">
              <template #extra><AudioOutlined /></template>
              <div class="rec-wrap">
                <template v-if="!recording">
                  <Button type="primary" shape="circle" size="large" danger @click="startRec">
                    <AudioOutlined style="font-size:22px" />
                  </Button>
                  <Text>点击开始录音，说完了自动转成语音发送</Text>
                </template>
                <template v-else>
                  <Button type="primary" shape="circle" size="large" danger @click="stopRec(true)" class="pulsing">
                    <CloseCircleOutlined style="font-size:22px" />
                  </Button>
                  <div class="rec-info">
                    <Text strong :danger="true">录音中 {{ fmtDuration(recSecs) }}</Text>
                    <Progress :percent="Math.min(100, level * 100)" :show-info="false" size="small"
                              stroke-color="#ff4d4f" />
                    <Button size="small" @click="stopRec(false)">取消</Button>
                  </div>
                </template>
              </div>
              <Alert style="margin-top:12px" type="info" show-icon
                     message="录音会被转码为 16kHz WAV，教室电脑直接播放" />
            </Card>
          </Col>

          <!-- Banner card -->
          <Col :span="24">
            <Card title="文字通知 (弹窗)" class="card">
              <Space.Compact style="width:100%">
                <Input v-model:value="bannerText" placeholder="在教室电脑上弹出通知窗口…" @keydown.enter="sendBanner" />
                <Button type="primary" @click="sendBanner" :disabled="!bannerText.trim()">
                  <SendOutlined /> 发送
                </Button>
              </Space.Compact>
            </Card>
          </Col>

          <!-- Log -->
          <Col :span="24">
            <Card title="发送记录" class="card">
              <div v-if="log.length === 0" class="empty-clients">
                <Empty description="还没有发送任何消息" :image-style="{ height: '48px' }" />
              </div>
              <List :data-source="log" size="small">
                <template #renderItem="{ item }">
                  <ListItem>
                    <ListItemMeta>
                      <template #title>
                        <Space>
                          <Tag :color="item.kind === 'tts' ? 'blue' : item.kind === 'audio' ? 'red' : 'green'">
                            {{ item.kind === 'tts' ? 'TTS' : item.kind === 'audio' ? '语音' : '通知' }}
                          </Tag>
                          <Text>{{ item.summary }}</Text>
                          <Text type="secondary">{{ item.time }}</Text>
                        </Space>
                      </template>
                      <template #description>
                        <Space wrap size="[4, 8]" style="margin-top:4px">
                          <template v-for="t in item.target" :key="t">
                            <Tag v-if="item.acks[t] === 'spoken' || item.acks[t] === 'displayed'" color="success"
                                 style="cursor:default">
                              <CheckOutlined /> {{ t }} {{ item.acks[t] === 'spoken' ? '已播报' : '已显示' }}
                            </Tag>
                            <Tag v-else-if="item.delivered.includes(t)" color="processing" style="cursor:default">
                              {{ t }} 已送达…
                            </Tag>
                            <Tag v-else-if="item.missed.includes(t)" color="error" style="cursor:default">
                              {{ t }} 离线
                            </Tag>
                            <Tag v-else style="cursor:default">{{ t }}</Tag>
                          </template>
                        </Space>
                      </template>
                    </ListItemMeta>
                  </ListItem>
                </template>
              </List>
            </Card>
          </Col>
        </Row>
      </LayoutContent>
    </Layout>
  </Layout>

  <Modal v-model:open="showSettings" title="服务器设置" ok-text="重连" cancel-text="关闭" @ok="connect">
    <Space direction="vertical" style="width:100%">
      <div>
        <Text>WebSocket 地址</Text>
        <Input v-model:value="settings.url" placeholder="ws://192.168.1.10:8080/ws" />
      </div>
      <div>
        <Text>Token（与 relay-server 一致，留空为无鉴权）</Text>
        <Input.Password v-model:value="settings.token" placeholder="sekrit" />
      </div>
    </Space>
  </Modal>
</template>

<style scoped src="./teacher.css"></style>
