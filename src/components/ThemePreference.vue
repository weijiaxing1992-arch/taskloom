<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { api } from '../api'
import { t, timezone } from '../i18n'
import { applyThemePreferences, resolvedTheme, themeMode, themeRevision, type ThemeMode } from '../theme'
const choices: { value: ThemeMode; label: string; subtitle: string; icon: string }[] = [
  { value: 'light', label: '白色皮肤', subtitle: '明亮清晰的日间界面', icon: '☀' },
  { value: 'dark', label: '暗色皮肤', subtitle: '柔和舒适的深色界面', icon: '☾' },
  { value: 'auto', label: '按时间自动', subtitle: '白天浅色，夜晚深色', icon: '◐' },
]
const saving = ref(false), error = ref('')
let disposed = false
onBeforeUnmount(() => { disposed = true })
async function change(value: ThemeMode) {
  if (saving.value || value === themeMode.value) return
  const previous = themeMode.value
  const revision = applyThemePreferences({ themeMode: value })
  saving.value = true; error.value = ''
  try {
    const result = await api<{ themeMode: string }>('/preferences/display', { method: 'PATCH', body: JSON.stringify({ themeMode: value }) })
    if (themeRevision.value === revision) applyThemePreferences(result)
  } catch (cause) {
    if (themeRevision.value === revision) applyThemePreferences({ themeMode: previous })
    if (!disposed) error.value = cause instanceof Error ? cause.message : t('皮肤保存失败，请重试')
  } finally { if (!disposed) saving.value = false }
}
</script>

<template>
  <section class="theme-preference" :aria-label="t('界面皮肤')">
    <div class="theme-heading"><div><b>{{ t('界面皮肤') }}</b><p>{{ t('即时生效，自动保存到当前账号。') }}</p></div><small aria-live="polite">{{ t(saving ? '保存中…' : resolvedTheme === 'dark' ? '当前：暗色' : '当前：白色') }}</small></div>
    <div class="theme-choices" role="group" :aria-label="t('选择皮肤模式')">
      <button v-for="choice in choices" :key="choice.value" type="button" :class="['theme-choice', choice.value, { selected: themeMode === choice.value }]" :aria-pressed="themeMode === choice.value" :disabled="saving" @click="change(choice.value)">
        <span class="theme-preview" aria-hidden="true"><i></i><span><em></em><em></em><em></em></span><b>{{ choice.icon }}</b></span>
        <strong>{{ t(choice.label) }}<span v-if="themeMode === choice.value" aria-hidden="true">✓</span></strong><small>{{ t(choice.subtitle) }}</small>
      </button>
    </div>
    <p class="theme-schedule">{{ t('自动模式：07:00–19:00 使用白色，其余时间使用暗色。时区：{zone}', { zone: timezone }) }}</p>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.theme-preference{padding:22px 0;border-bottom:1px solid var(--line)}.theme-heading{display:flex;align-items:center;justify-content:space-between;gap:12px}.theme-heading p,.theme-heading small,.theme-schedule{color:var(--muted);font-size:12px}.theme-heading p{margin:6px 0 18px}.theme-choices{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.theme-choice{padding:10px;text-align:left;background:var(--surface);color:var(--ink);border:1px solid var(--line);border-radius:10px}.theme-choice.selected{border-color:var(--primary);box-shadow:0 0 0 1px var(--primary);background:var(--primary-soft)}.theme-choice:focus-visible{outline:3px solid #3478f6;outline-offset:3px}.theme-choice:disabled{cursor:wait;opacity:.7}.theme-choice strong{display:flex;justify-content:space-between;font-size:13px;margin:10px 0 4px}.theme-choice>small{font-size:11px;color:var(--muted)}.theme-preview{height:80px;display:flex;position:relative;border-radius:6px;overflow:hidden;background:#f8fafc;border:1px solid #d9e0ec}.theme-preview>i{width:24%;background:#17233c}.theme-preview>span{flex:1;padding:14px 12px}.theme-preview em{display:block;height:7px;border-radius:2px;background:#d5dceb;margin-bottom:7px}.theme-preview em:nth-child(2){width:72%}.theme-preview>b{position:absolute;right:10px;bottom:6px;color:#675bd5;font-size:22px}.dark .theme-preview{background:#161f30;border-color:#344259}.dark .theme-preview em{background:#344259}.dark .theme-preview>b{color:#c2b7ff}.auto .theme-preview{background:linear-gradient(110deg,#f8fafc 50%,#161f30 50%)}.auto .theme-preview>b{color:#c2b7ff}.theme-schedule{line-height:1.7;margin:14px 0 0}@media(max-width:680px){.theme-choices{grid-template-columns:1fr}.theme-preview{height:64px}}
</style>

<style scoped>
/* 预览图保留固定微缩色板，交互层改用全局语义令牌。 */
.theme-choice:focus-visible{outline-color:var(--ring)}
.theme-choice.selected{box-shadow:0 0 0 1px var(--ring)}
.theme-heading p,.theme-heading small,.theme-schedule{color:var(--muted-foreground)}
</style>
