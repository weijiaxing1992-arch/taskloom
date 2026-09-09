<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useWorkspaceStore } from '../stores/workspace'
import { api } from '../api'
import { t } from '../i18n'
import { desktopScope, desktopPreferences, saveDesktopPreferences, desktopPermission, unlockNotificationSound, ringNotification, startDesktopNotifications, nativeBridge } from '../desktopNotifications'
const workspace = useWorkspaceStore(), route = useRoute()
const props = defineProps<{ runtimeOnly?: boolean }>()
const scope = computed(() => desktopScope(workspace.session))
const enabled = ref(false), sound = ref(true), message = ref(''), busy = ref(false)
let stop: (() => void) | undefined
function sync() { const prefs = desktopPreferences(scope.value); enabled.value = prefs.enabled; sound.value = prefs.sound }
async function enable() {
  if (busy.value) return
  busy.value = true
  try {
    // 两种权限均从本次用户点击启动，不能等后台轮询时再触发授权。
    const audio = unlockNotificationSound().catch(() => {})
    const permission = await desktopPermission(true)
    await audio
    if (permission !== 'granted') { message.value = permission === 'unsupported' ? '此浏览器不支持系统通知，请使用支持通知的 HTTPS 桌面浏览器或 Mac 应用' : '通知未获授权，请在浏览器或系统设置中允许通知'; return }
    saveDesktopPreferences(scope.value, { enabled: true }); sync()
    message.value = '已开启跨应用提醒。首次开启不重放历史消息；请保持网页或应用运行。'
  } catch (error: any) { message.value = error.message || '无法开启通知' } finally { busy.value = false }
}
function disable() { saveDesktopPreferences(scope.value, { enabled: false }); sync(); message.value = '已关闭此账号在当前浏览器的系统提醒' }
function toggleSound() { saveDesktopPreferences(scope.value, { sound: !sound.value }); sync() }
async function testSound() { try { if (nativeBridge()) await nativeBridge()!.postMessage({ action: 'testSound' }); else { await unlockNotificationSound(); ringNotification() }; message.value = '已试听；系统静音或专注模式可能影响提醒' } catch { message.value = '声音被阻止，请检查系统或浏览器声音设置' } }
async function testNotice() {
  try {
    if (await desktopPermission() !== 'granted') { message.value = '请先开启系统提醒'; return }
    if (nativeBridge()) await nativeBridge()!.postMessage({ action: 'testNotification', sound: sound.value })
    else { const notice = new Notification('TaskLoom · 测试提醒', { body: '跨应用提醒已开启，新的业务通知将显示在这里。', silent: true }); setTimeout(() => notice.close(), 10000); if (sound.value) { await unlockNotificationSound(); ringNotification() } }
    message.value = '已请求系统显示测试提醒，请查看屏幕通知区域'
  } catch { message.value = '系统未接受测试通知，请检查权限' }
}
const status = (event: Event) => { message.value = String((event as CustomEvent).detail || '') }
onMounted(() => { sync(); if (props.runtimeOnly && !window.devflowDesktopAgent) stop = startDesktopNotifications(api, () => workspace.identityConflict || workspace.switchingProject ? null : workspace.session); window.addEventListener('devflow-desktop-status', status); window.addEventListener('storage', sync); window.addEventListener('devflow-desktop-settings', sync) })
onBeforeUnmount(() => { stop?.(); window.removeEventListener('devflow-desktop-status', status); window.removeEventListener('storage', sync); window.removeEventListener('devflow-desktop-settings', sync) })
</script>
<template>
  <aside v-if="!runtimeOnly && scope && route.path === '/notifications'" class="desktop-notice-settings" :aria-label="t('系统通知设置')">
    <strong>{{t('跨应用提醒')}}</strong><span class="desktop-notice-state">{{t(enabled?'已开启':'未开启')}}</span>
    <button type="button" class="btn compact" :disabled="busy" @click="enabled?disable():enable()">{{t(enabled?'关闭提醒':'开启系统提醒')}}</button>
    <button type="button" class="btn compact" :aria-pressed="sound" @click="toggleSound">{{t(sound?'振铃：开':'振铃：关')}}</button>
    <button type="button" class="btn compact" @click="testSound">{{t('试听振铃')}}</button><button type="button" class="btn compact" @click="testNotice">{{t('测试通知')}}</button>
    <span class="desktop-notice-hint" role="status">{{t(message||'覆盖全部通知分类；切换到其他应用时也可提醒。浏览器需授权，关闭全部页面后不再接收。')}}</span>
  </aside>
</template>
<style scoped>
.desktop-notice-settings{display:flex;flex-wrap:wrap;align-items:center;gap:8px;padding:10px 18px;border-bottom:1px solid var(--line);background:var(--panel,#fff);font-size:13px;min-width:0}.desktop-notice-state{color:var(--muted)}.desktop-notice-hint{flex:1 1 300px;color:var(--muted);font-size:12px;line-height:1.6;overflow-wrap:anywhere}@media(max-width:640px){.desktop-notice-settings{padding:10px 12px}.desktop-notice-hint{flex-basis:100%}}
</style>
