<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api'
import { locale, setLocale, t, type Locale } from '../i18n'
import AppSelect from './AppSelect.vue'

const props = defineProps<{ authenticated?: boolean }>()
const pending = ref(false)
const error = ref('')
const options = [{ value: 'zh-CN', label: '简体中文' }, { value: 'en-US', label: 'English' }]
async function change(value: string | number) {
  // 只接受已支持语言；统一下拉使用值事件，不再依赖系统 select 的 DOM。
  if (value !== 'zh-CN' && value !== 'en-US') return
  const previous = locale.value
  const next = value as Locale
  if (pending.value || next === previous) return
  pending.value = true
  error.value = ''
  setLocale(next)
  try {
    if (props.authenticated) await api('/preferences/locale', { method: 'PATCH', body: JSON.stringify({ locale: next }) })
    window.dispatchEvent(new CustomEvent('devflow-locale-changed', { detail: next }))
  } catch (cause) {
    setLocale(previous)
    error.value = cause instanceof Error ? cause.message : t('语言设置保存失败，请重试')
  } finally { pending.value = false }
}
</script>

<template>
  <div class="locale-switcher">
    <AppSelect class="locale-select" :model-value="locale" :options="options" :disabled="pending" :label="t('界面语言')" @update:model-value="change"/>
    <span v-if="error" class="locale-error" role="alert">{{ error }}</span>
  </div>
</template>

<style scoped>
.locale-switcher{position:relative;display:flex;align-items:center;flex:none;color:var(--muted-foreground)}
.locale-switcher :deep(.locale-select){width:max-content;min-width:108px;max-width:none;box-shadow:none}
.locale-error{position:absolute;top:calc(100% + 6px);right:0;z-index:100;width:240px;max-width:calc(100vw - 24px);padding:10px;background:var(--card);border:1px solid var(--danger-border);border-radius:var(--ui-control-radius);color:var(--destructive);font-size:var(--ui-font-caption);line-height:1.6}
</style>
