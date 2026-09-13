<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { t } from '../i18n'

const props = defineProps<{ requirementId: number }>()
const emit = defineEmits<{ (event: 'change', favorited: boolean): void }>()
type FavoriteState = { requirementId: number; favorited: boolean; createdAt?: string }
const favorited = ref<boolean | null>(null)
const loading = ref(true), saving = ref(false), error = ref(''), identityInvalid = ref(false)
let version = 0, disposed = false
const projectId = () => localStorage.getItem('devflow-project') || 'prj_orbit'
function target() { return { version: ++version, id: props.requirementId, project: projectId() } }
function current(request: ReturnType<typeof target>) { return !disposed && !identityInvalid.value && request.version === version && request.id === props.requirementId && request.project === projectId() }
function accept(state: FavoriteState, request: ReturnType<typeof target>) {
  if (!current(request)) return
  if (state.requirementId !== request.id || typeof state.favorited !== 'boolean') throw new Error('收藏状态加载失败')
  favorited.value = state.favorited
}
async function load() {
  if (disposed || identityInvalid.value) return
  const request = target()
  favorited.value = null; loading.value = true; saving.value = false; error.value = ''
  if (!Number.isSafeInteger(request.id) || request.id <= 0) { loading.value = false; error.value = '需求不存在'; return }
  try { accept(await api<FavoriteState>(`/requirements/${request.id}/favorite`, { headers: { 'X-TaskLoom-Project': request.project } }), request) }
  catch (cause: any) { if (current(request)) error.value = cause?.message || '收藏状态加载失败' }
  finally { if (current(request)) loading.value = false }
}
async function toggle() {
  if (disposed || identityInvalid.value || loading.value || saving.value || favorited.value === null) return
  const request = target(), method = favorited.value ? 'DELETE' : 'PUT'
  saving.value = true; error.value = ''
  try {
    const state = await api<FavoriteState>(`/requirements/${request.id}/favorite`, { method, headers: { 'X-TaskLoom-Project': request.project } })
    if (!current(request)) return
    accept(state, request)
    emit('change', state.favorited)
    window.dispatchEvent(new CustomEvent('devflow-favorites-changed', { detail: { requirementId: request.id, projectId: request.project, favorited: state.favorited } }))
  } catch (cause: any) { if (current(request)) error.value = cause?.message || '收藏操作失败，请重试' }
  finally { if (current(request)) saving.value = false }
}
function scopeChanged() { void load() }
function identityChanged() { version++; identityInvalid.value = true; favorited.value = null; loading.value = false; saving.value = false; error.value = '账号身份已在其他页面切换，请刷新后继续' }
watch(() => props.requirementId, () => { void load() }, { flush: 'sync' })
onMounted(() => { window.addEventListener('devflow-project-changed', scopeChanged); window.addEventListener('devflow-identity-changed', identityChanged); window.addEventListener('devflow-auth-expired', identityChanged); void load() })
onBeforeUnmount(() => { disposed = true; version++; window.removeEventListener('devflow-project-changed', scopeChanged); window.removeEventListener('devflow-identity-changed', identityChanged); window.removeEventListener('devflow-auth-expired', identityChanged) })
</script>

<template>
  <span class="requirement-favorite">
    <button type="button" class="favorite-toggle" :class="{ active: favorited }" :disabled="loading || saving || favorited === null || identityInvalid" :aria-pressed="favorited === true" :aria-busy="loading || saving" :title="t(favorited ? '取消收藏' : '收藏')" @click="toggle">
      <span aria-hidden="true">{{ favorited ? '★' : '☆' }}</span><span>{{ loading ? t('加载中…') : saving ? t('保存中…') : favorited ? t('已收藏') : t('收藏') }}</span>
    </button>
    <span v-if="error" class="favorite-error" role="alert">{{ t(error) }} <button v-if="!identityInvalid" type="button" :disabled="loading || saving" @click="load">{{ t('重试') }}</button></span>
  </span>
</template>

<style scoped>
.requirement-favorite{display:inline-flex;align-items:center;gap:8px;flex-wrap:wrap}.favorite-toggle{display:inline-flex;align-items:center;gap:5px;padding:6px 10px;min-height:32px;border:1px solid #e2e5ee;border-radius:7px;background:#fff;color:#667085;font-size:12px;white-space:nowrap}.favorite-toggle>span:first-child{font-size:17px;line-height:1}.favorite-toggle.active{background:#fffaeb;border-color:#fedf89;color:#935b00}.favorite-toggle:hover:enabled{border-color:#e5ad31;background:#fff8e6}.favorite-toggle:focus-visible,.favorite-error button:focus-visible{outline:2px solid #7168ec;outline-offset:3px}.favorite-toggle:disabled{opacity:.6;cursor:wait}.favorite-error{font-size:11px;color:#b42318;max-width:320px;overflow-wrap:anywhere}.favorite-error button{border:0;background:none;color:#5b5ce2;text-decoration:underline;font-size:inherit;padding:2px}
</style>
