import { ref, watch } from 'vue'
import { timezone } from './i18n'
import './theme.css'

// 模式来自账号显示偏好（App 初始化/设置保存），本模块不另存一个全局 localStorage 副本。
// themeMode 是用户选择，resolvedTheme 是当前实际皮肤，自动模式不能反写成固定模式。
export type ThemeMode = 'light' | 'dark' | 'auto'
export const themeMode = ref<ThemeMode>('light')
export const resolvedTheme = ref<'light' | 'dark'>('light')
export const themeRevision = ref(0)
// 自动模式按账号时区 07:00（含）至 19:00（不含）为浅色，不采用系统 prefers-color-scheme。
export function resolveTheme(mode: ThemeMode, date = new Date(), zone = 'Asia/Shanghai'): 'light' | 'dark' {
  if (mode !== 'auto') return mode === 'dark' ? 'dark' : 'light'
  let hour: number
  try { hour = Number(new Intl.DateTimeFormat('en-GB', { timeZone: zone, hour: '2-digit', hourCycle: 'h23' }).format(date)) }
  catch { hour = date.getUTCHours() }
  return hour >= 7 && hour < 19 ? 'light' : 'dark'
}
export function refreshTheme() {
  resolvedTheme.value = resolveTheme(themeMode.value, new Date(), timezone.value)
  document.documentElement.dataset.theme = resolvedTheme.value
  document.documentElement.style.colorScheme = resolvedTheme.value
}
export function applyThemePreferences(value: { themeMode?: string }) {
  // 每次应用递增版本；ThemePreference 用版本判断异步 PATCH 是否仍属于当前选择，
  // 仅对应版本失败才回退，防止旧请求覆盖新账号或后一次已应用的偏好。
  themeMode.value = ['light', 'dark', 'auto'].includes(value.themeMode || '') ? value.themeMode as ThemeMode : 'light'
  refreshTheme()
  return ++themeRevision.value
}
// App 持有定时器生命周期，不依赖设置页是否打开；唤醒、切回标签及时区变化都刷新。
// 调用方卸载时必须执行返回的清理函数，避免重挂 App 后叠加定时器和监听器。
export function startThemeClock() {
  const stop = watch(timezone, refreshTheme)
  const timer = window.setInterval(refreshTheme, 30_000)
  window.addEventListener('focus', refreshTheme)
  document.addEventListener('visibilitychange', refreshTheme)
  refreshTheme()
  return () => { stop(); window.clearInterval(timer); window.removeEventListener('focus', refreshTheme); document.removeEventListener('visibilitychange', refreshTheme) }
}
