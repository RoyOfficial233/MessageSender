<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { Button, Card, Typography } from 'ant-design-vue'
import { AudioOutlined, DesktopOutlined, ThunderboltOutlined } from '@ant-design/icons-vue'
import TeacherConsole from './components/TeacherConsole.vue'
import ClassroomScreen from './components/ClassroomScreen.vue'

const { Text, Title } = Typography

const mode = ref<'home' | 'teacher' | 'client'>('home')

function applyHash() {
  const h = location.hash.replace(/^#\/?/, '')
  mode.value = h === 'teacher' || h === 'client' ? h : 'home'
}
onMounted(() => {
  applyHash()
  window.addEventListener('hashchange', applyHash)
})
watch(mode, (m) => {
  const target = m === 'home' ? location.pathname : `#/${m}`
  if (m !== 'home' && location.hash !== `#/${m}`) {
    history.replaceState(null, '', `#/${m}`)
  }
})
</script>

<template>
  <TeacherConsole v-if="mode === 'teacher'" />
  <ClassroomScreen v-else-if="mode === 'client'" />
  <div v-else class="home">
    <Card class="home-card">
      <div class="logo"><ThunderboltOutlined /></div>
      <Title :level="3">教室传话系统</Title>
      <Text type="secondary">老师向教室电脑实时发送语音播报与通知</Text>
      <div class="choices">
        <Button type="primary" size="large" @click="mode = 'teacher'">
          <DesktopOutlined /> 我是老师（控制台）
        </Button>
        <Button size="large" @click="mode = 'client'">
          <AudioOutlined /> 这是教室电脑（被控端）
        </Button>
      </div>
      <div class="hint">
        <Text type="secondary">
          提示：教室电脑建议使用原生 relay-client（支持后台常驻、SAPI 播报、弹窗）
        </Text>
      </div>
    </Card>
  </div>
</template>

<style>
body { margin: 0; font-family: -apple-system, 'Segoe UI', 'Microsoft YaHei', sans-serif; }
.home {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #f0f5ff 0%, #e6f4ff 100%);
}
.home-card {
  text-align: center;
  padding: 32px 40px;
  border-radius: 16px;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.08);
}
.logo { font-size: 52px; color: #1677ff; margin-bottom: 8px; }
.choices { display: flex; gap: 16px; justify-content: center; margin: 28px 0 12px; }
.hint { margin-top: 8px; }
</style>
