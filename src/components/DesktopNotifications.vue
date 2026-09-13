<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useWorkspaceStore } from '../stores/workspace'
import { api } from '../api'
import { t } from '../i18n'
import { desktopScope, desktopPreferences, saveDesktopPreferences, desktopPermission, desktopPermissionGuidance, unlockNotificationSound, ringNotification, startDesktopNotifications, nativeBridge, type DesktopPermission } from '../desktopNotifications'
const workspace = useWorkspaceStore(), route = useRoute()
const props = defineProps<{ runtimeOnly?: boolean }>()
const scope = computed(() => desktopScope(workspace.session))
const enabled = ref(false), sound = ref(true), message = ref(''), busy = ref(false), permission = ref<DesktopPermission>('default'), permissionMessage = ref(false)
const permissionLabel = computed(() => ({ granted: '系统权限：已允许', denied: '系统权限：已拒绝', default: '系统权限：待授权', unsupported: '系统权限：不支持', error: '系统权限：读取失败' })[permission.value])
const notificationAction = computed(() => enabled.value ? '关闭提醒' : busy.value ? '正在请求授权…' : '开启系统提醒')
const notificationHint = computed(() => message.value || desktopPermissionGuidance(permission.value))
let stop: (() => void) | undefined
function showPermissionGuidance(next: DesktopPermission) { permissionMessage.value = true; message.value = desktopPermissionGuidance(next) }
async function refreshPermission() {
  const next = await desktopPermission(); permission.value = next
  // 用户在浏览器/系统设置中修复拒绝后，不能继续展示旧的“已拒绝”说明。
  if (next === 'granted' && permissionMessage.value) { permissionMessage.value = false; message.value = enabled.value ? desktopPermissionGuidance('granted') : '系统通知权限已允许。请点击“开启系统提醒”完成当前账号的提醒开关。' }
  return next
}
function sync() { const prefs = desktopPreferences(scope.value); enabled.value = prefs.enabled; sound.value = prefs.sound; void refreshPermission() }
async function enable() {
  if (busy.value) return
  busy.value = true
  try {
    // 两种权限均从本次用户点击启动，不能等后台轮询时再触发授权。
    const audio = unlockNotificationSound().catch(() => {})
    const nextPermission = await desktopPermission(true)
    permission.value = nextPermission
    await audio
    if (nextPermission !== 'granted') { showPermissionGuidance(nextPermission); return }
    permissionMessage.value = false
    saveDesktopPreferences(scope.value, { enabled: true }); sync()
    message.value = '已开启跨应用提醒。首次开启不会重放历史消息；可点击“测试通知”确认系统横幅。'
  } catch { permission.value = 'error'; showPermissionGuidance('error') } finally { busy.value = false }
}
function disable() { permissionMessage.value = false; saveDesktopPreferences(scope.value, { enabled: false }); sync(); message.value = '已关闭此账号在当前浏览器的系统提醒' }
function toggleSound() { saveDesktopPreferences(scope.value, { sound: !sound.value }); sync() }
async function testSound() { permissionMessage.value = false; try { if (nativeBridge()) await nativeBridge()!.postMessage({ action: 'testSound' }); else { await unlockNotificationSound(); ringNotification() }; message.value = '已试听；系统静音或专注模式可能影响提醒' } catch { message.value = '声音被阻止，请检查系统或浏览器声音设置' } }
async function testNotice() {
  try {
    const nextPermission = await desktopPermission(); permission.value = nextPermission
    if (nextPermission !== 'granted') { showPermissionGuidance(nextPermission); return }
    permissionMessage.value = false
    if (nativeBridge()) await nativeBridge()!.postMessage({ action: 'testNotification', sound: sound.value })
    else { const notice = new Notification('TaskLoom · 测试提醒', { body: '跨应用提醒已开启，新的业务通知将显示在这里。', silent: true }); setTimeout(() => notice.close(), 10000); if (sound.value) { await unlockNotificationSound(); ringNotification() } }
    message.value = '已请求系统显示测试提醒，请查看屏幕通知区域'
  } catch { permission.value = 'error'; permissionMessage.value = true; message.value = '系统没有接受测试通知。' + desktopPermissionGuidance('error') }
}
const status = (event: Event) => { permissionMessage.value = false; message.value = String((event as CustomEvent).detail || '') }
onMounted(() => { sync(); if (props.runtimeOnly && !window.devflowDesktopAgent) stop = startDesktopNotifications(api, () => workspace.identityConflict || workspace.switchingProject ? null : workspace.session); window.addEventListener('devflow-desktop-status', status); window.addEventListener('storage', sync); window.addEventListener('devflow-desktop-settings', sync) })
onBeforeUnmount(() => { stop?.(); window.removeEventListener('devflow-desktop-status', status); window.removeEventListener('storage', sync); window.removeEventListener('devflow-desktop-settings', sync) })
</script>
<template>
  <aside v-if="!runtimeOnly && scope && route.path === '/notifications'" class="desktop-notice-settings" :aria-label="t('系统通知设置')">
    <strong>{{t('跨应用提醒')}}</strong><span class="desktop-notice-state">{{t(enabled?'已开启':'未开启')}}</span><span class="desktop-notice-permission" :class="'is-'+permission">{{t(permissionLabel)}}</span>
    <button type="button" class="btn compact" :disabled="busy" @click="enabled?disable():enable()">{{t(notificationAction)}}</button>
    <button v-if="['denied','unsupported','error'].includes(permission)" type="button" class="btn compact" :disabled="busy" @click="refreshPermission">{{t('重新检查授权')}}</button>
    <button type="button" class="btn compact" :aria-pressed="sound" @click="toggleSound">{{t(sound?'振铃：开':'振铃：关')}}</button>
    <button type="button" class="btn compact" @click="testSound">{{t('试听振铃')}}</button><button type="button" class="btn compact" @click="testNotice">{{t('测试通知')}}</button>
    <span class="desktop-notice-hint" role="status">{{t(notificationHint)}}</span>
  </aside>
</template>
<style scoped>
.desktop-notice-settings{display:flex;flex-wrap:wrap;align-items:center;gap:8px 10px;padding:10px 18px;border-bottom:1px solid var(--line);background:var(--panel,#fff);font-size:13px;min-width:0}.desktop-notice-settings>strong{white-space:nowrap}.desktop-notice-settings>.btn{flex:none;min-height:34px;white-space:nowrap}.desktop-notice-state{color:var(--muted);white-space:nowrap}.desktop-notice-permission{display:inline-flex;align-items:center;min-height:26px;padding:2px 8px;border:1px solid var(--line);border-radius:999px;background:var(--surface,#fff);color:var(--muted);font-size:12px;white-space:nowrap}.desktop-notice-permission.is-granted{border-color:color-mix(in srgb,var(--success,#12a150) 35%,var(--line));background:color-mix(in srgb,var(--success,#12a150) 9%,var(--surface,#fff));color:var(--success,#0a7a3e)}.desktop-notice-permission.is-denied,.desktop-notice-permission.is-error{border-color:color-mix(in srgb,var(--danger,#d63b4c) 35%,var(--line));background:color-mix(in srgb,var(--danger,#d63b4c) 8%,var(--surface,#fff));color:var(--danger,#b42330)}.desktop-notice-permission.is-default{border-color:color-mix(in srgb,var(--primary) 30%,var(--line));background:var(--primary-soft);color:var(--primary)}.desktop-notice-hint{order:2;flex:1 0 100%;min-width:0;padding-top:1px;color:var(--muted);font-size:12px;line-height:1.55;overflow-wrap:anywhere}@media(max-width:1040px){.desktop-notice-settings{align-items:flex-start}.desktop-notice-hint{padding-top:3px}}@media(max-width:640px){.desktop-notice-settings{gap:8px;padding:10px 12px}.desktop-notice-settings>.btn{flex:1 1 calc(50% - 4px);min-width:0;min-height:40px;padding-inline:8px}.desktop-notice-hint{padding-top:3px}}
</style>
