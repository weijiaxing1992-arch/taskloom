<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { api } from '../api'
import { t } from '../i18n'
import OrganizationModal from './OrganizationModal.vue'

type Member = { id: string; name: string; email: string; active: boolean; tenantRole: string; role: string | null }
type Candidates = { items: Member[]; canManage: boolean; currentUserId: string; isTenantAdmin: boolean }
const props = defineProps<{ project: { id: string; name: string; code: string } }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'changed', projectId: string): void }>()
const items = ref<Member[]>([]), selected = ref<string[]>([]), baseline = ref<string[]>([]), query = ref('')
const loading = ref(false), saving = ref(false), loaded = ref(false), canManage = ref(false), locked = ref(false)
const error = ref(''), notice = ref(''), discardOpen = ref(false), discardPrompt = ref<HTMLElement | null>(null)
const currentUserId = ref(''), isTenantAdmin = ref(false)
let disposed = false, generation = 0, controller = new AbortController(), resolveLeave: ((value: boolean) => void) | null = null
const identityEvents = ['devflow-auth-expired', 'devflow-identity-changed', 'devflow-account-disabled', 'devflow-password-change-required']
const selectedSet = computed(() => new Set(selected.value)), baselineSet = computed(() => new Set(baseline.value))
const filtered = computed(() => {
  const search = query.value.trim().toLowerCase()
  return items.value.filter(item => !search || `${item.name} ${item.email}`.toLowerCase().includes(search))
})
const added = computed(() => items.value.filter(item => selectedSet.value.has(item.id) && !baselineSet.value.has(item.id)).map(item => item.id))
const removed = computed(() => items.value.filter(item => baselineSet.value.has(item.id) && !selectedSet.value.has(item.id)).map(item => item.id))
const dirty = computed(() => added.value.length > 0 || removed.value.length > 0)
const mutable = computed(() => loaded.value && canManage.value && !locked.value && !loading.value && !saving.value && !discardOpen.value)
const allFilteredSelected = computed(() => filtered.value.length > 0 && filtered.value.every(item => selectedSet.value.has(item.id)))
const someFilteredSelected = computed(() => filtered.value.some(item => selectedSet.value.has(item.id)) && !allFilteredSelected.value)
const message = (cause: unknown) => cause instanceof Error ? cause.message : '操作失败，请稍后重试'

function current(version: number, projectId: string) { return !disposed && !locked.value && version === generation && projectId === props.project.id }
function protectedMember(item: Member) { return baselineSet.value.has(item.id) && (item.id === currentUserId.value || item.tenantRole === 'tenant_admin' || !isTenantAdmin.value && ['tenant_admin', 'project_admin'].includes(item.role || '')) }
function roleLabel(role: string | null) { const labels: Record<string, string> = { viewer: '只读', tenant_admin: '企业管理员', project_admin: '项目管理员', frontend: '前端', backend: '后端', qa: '测试', product: '产品', algorithm: '算法', ui: 'UI 设计', frontend_lead: '前端组长', backend_lead: '后端组长' }; return role && labels[role] ? t(labels[role]) : role || '—' }
function settleLeave(value: boolean) { const resolve = resolveLeave; resolveLeave = null; resolve?.(value) }
function clearDraft() { items.value = []; selected.value = []; baseline.value = []; query.value = ''; currentUserId.value = ''; isTenantAdmin.value = false; loaded.value = false; canManage.value = false; discardOpen.value = false; error.value = ''; notice.value = '' }
function acceptCandidates(result: Candidates) {
  if (result.canManage !== true) throw new Error('你没有管理项目成员的权限')
  if (typeof result.currentUserId !== 'string' || !result.currentUserId || typeof result.isTenantAdmin !== 'boolean' || !Array.isArray(result.items) || result.items.some(item => !item || typeof item.id !== 'string' || !item.id || typeof item.name !== 'string' || typeof item.email !== 'string' || typeof item.active !== 'boolean' || typeof item.tenantRole !== 'string' || !(item.role === null || typeof item.role === 'string' && !!item.role)) || new Set(result.items.map(item => item.id)).size !== result.items.length) throw new Error('成员列表加载结果异常，请重新加载')
  items.value = result.items.map(item => ({ ...item }))
  currentUserId.value = result.currentUserId; isTenantAdmin.value = result.isTenantAdmin
  baseline.value = items.value.filter(item => item.role !== null).map(item => item.id)
  selected.value = [...baseline.value]; loaded.value = true; canManage.value = true
}

// 每次打开的候选和保存快照都绑定明确的项目 ID；取消请求不足以防止迟到响应，因此同时校验世代。
async function load() {
  if (disposed || locked.value || saving.value || dirty.value) return
  controller.abort(); controller = new AbortController()
  const version = ++generation, projectId = props.project.id
  loading.value = true; loaded.value = false; error.value = ''; canManage.value = false
  try {
    const result = await api<Candidates>(`/projects/${encodeURIComponent(projectId)}/members?candidates=1`, { headers: { 'X-DevFlow-Project': projectId }, signal: controller.signal })
    if (!current(version, projectId)) return
    acceptCandidates(result)
  } catch (cause) { if (current(version, projectId)) error.value = message(cause) }
  finally { if (current(version, projectId)) loading.value = false }
}

function toggleMember(id: string, checked: boolean) {
  const item = items.value.find(item => item.id === id)
  if (!mutable.value || !item || !checked && protectedMember(item)) return
  const next = new Set(selected.value)
  if (checked) next.add(id); else next.delete(id)
  selected.value = [...next]; notice.value = ''
}
function toggleFiltered(checked: boolean) {
  if (!mutable.value) return
  const next = new Set(selected.value)
  for (const item of filtered.value) { if (checked) next.add(item.id); else if (!protectedMember(item)) next.delete(item.id) }
  selected.value = [...next]; notice.value = ''
}

async function save() {
  if (!mutable.value || !dirty.value) return
  const projectId = props.project.id, version = generation
  // 只从已验证候选生成差量；绝不提交整份成员/角色替换，也不把搜索隐藏的成员误删。
  const addUserIds = [...added.value], removeUserIds = [...removed.value]
  if (removeUserIds.some(id => protectedMember(items.value.find(item => item.id === id)!))) { error.value = '不能移除自己或企业管理员'; return }
  if (addUserIds.length + removeUserIds.length > 200) { error.value = '每次最多调整 200 位成员，请分批保存'; return }
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const result = await api<Candidates>(`/projects/${encodeURIComponent(projectId)}/members`, { method: 'PATCH', headers: { 'X-DevFlow-Project': projectId }, signal: controller.signal, body: JSON.stringify({ addUserIds, removeUserIds, role: 'viewer' }) })
    if (!current(version, projectId)) return
    acceptCandidates(result)
    notice.value = t('项目成员已保存：新增 {added} 人，移除 {removed} 人', { added: addUserIds.length, removed: removeUserIds.length })
    emit('changed', projectId)
  } catch (cause) { if (current(version, projectId)) error.value = message(cause) }
  finally { if (current(version, projectId)) saving.value = false }
}

function showDiscard() { discardOpen.value = true; void nextTick(() => discardPrompt.value?.focus()) }
function requestClose() {
  if (saving.value) return false
  if (dirty.value) { showDiscard(); return false }
  settleLeave(false); emit('close'); return true
}
function keepEditing() { discardOpen.value = false; settleLeave(false) }
function discardChanges() {
  if (saving.value) return
  selected.value = [...baseline.value]; discardOpen.value = false; settleLeave(true); emit('close')
}
function canLeave(): boolean | Promise<boolean> {
  if (saving.value) return false
  if (!dirty.value) return true
  showDiscard(); settleLeave(false)
  return new Promise(resolve => { resolveLeave = resolve })
}
function beforeProjectChange(event: Event) { if (saving.value || dirty.value) { event.preventDefault(); if (!saving.value) showDiscard() } }
function beforeUnload(event: BeforeUnloadEvent) { if (dirty.value || saving.value) { event.preventDefault(); event.returnValue = '' } }
function invalidate() { locked.value = true; generation++; controller.abort(); clearDraft(); loading.value = false; saving.value = false; settleLeave(false) }
watch(() => props.project.id, () => { generation++; controller.abort(); settleLeave(false); clearDraft(); saving.value = false; void load() }, { immediate: true, flush: 'sync' })
onBeforeRouteLeave(canLeave); onBeforeRouteUpdate(canLeave)
onMounted(() => { for (const event of identityEvents) window.addEventListener(event, invalidate); window.addEventListener('devflow-before-project-change', beforeProjectChange); window.addEventListener('devflow-project-changed', invalidate); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { disposed = true; generation++; controller.abort(); settleLeave(false); clearDraft(); for (const event of identityEvents) window.removeEventListener(event, invalidate); window.removeEventListener('devflow-before-project-change', beforeProjectChange); window.removeEventListener('devflow-project-changed', invalidate); window.removeEventListener('beforeunload', beforeUnload) })
defineExpose({ requestClose, canLeave })
</script>

<template>
  <OrganizationModal :title="t('管理项目成员')" :busy="saving" wide @close="requestClose">
    <div class="project-members" :aria-busy="loading || saving">
      <div class="members-project"><div><b>{{ project.name }}</b><code>{{ project.code }}</code></div><span>{{ t('仅调整当前项目') }}</span></div>
      <p class="members-help">{{ t('新增成员默认获得只读角色；已有成员保留原角色。加入项目不会激活账号，也不会更改其他项目。') }}</p>
      <p v-if="locked" class="field-error" role="alert">{{ t('账号或项目已变化，请关闭后重新打开') }}</p>
      <p v-if="error" class="field-error" role="alert">{{ t(error) }}</p>
      <p v-if="notice" class="members-notice" role="status">{{ notice }}</p>
      <div v-if="loading" class="members-state" role="status">{{ t('正在加载项目成员…') }}</div>
      <div v-else-if="!loaded && !locked" class="members-state"><button type="button" class="btn" @click="load">{{ t('重新加载') }}</button></div>
      <template v-if="loaded">
        <div class="members-toolbar"><input v-model="query" type="search" :disabled="saving || discardOpen" :aria-label="t('搜索成员姓名或邮箱')" :placeholder="t('搜索成员姓名或邮箱')"><span>{{ t('已选 {count} 人', { count: selected.length }) }}</span></div>
        <div class="members-selection"><label><input type="checkbox" :checked="allFilteredSelected" :indeterminate="someFilteredSelected" :disabled="!mutable || !filtered.length" @change="toggleFiltered(($event.target as HTMLInputElement).checked)">{{ t(query.trim() ? '全选筛选结果' : '全选企业成员') }}</label><span>{{ t('当前显示 {count} 人', { count: filtered.length }) }}</span></div>
        <div class="members-list">
          <label v-for="item in filtered" :key="item.id" class="members-row" :class="{ selected: selectedSet.has(item.id), protected: protectedMember(item) }">
            <input type="checkbox" :checked="selectedSet.has(item.id)" :disabled="!mutable || protectedMember(item)" :aria-label="t('选择成员 {name}', { name: item.name })" @change="toggleMember(item.id, ($event.target as HTMLInputElement).checked)">
            <span class="members-avatar" aria-hidden="true">{{ item.name.slice(0, 1) }}</span>
            <span class="members-person"><b>{{ item.name }}<small v-if="item.id === currentUserId">{{ t('自己') }}</small></b><span>{{ item.email }}</span></span>
            <span class="members-meta"><span v-if="!item.active" class="members-badge inactive">{{ t('未激活') }}</span><span v-if="item.tenantRole === 'tenant_admin'" class="members-badge">{{ t('企业管理员') }}</span><span v-if="item.role" class="members-role">{{ roleLabel(item.role) }}</span><span class="members-membership" :class="{ added: selectedSet.has(item.id) && !baselineSet.has(item.id), removed: !selectedSet.has(item.id) && baselineSet.has(item.id) }">{{ t(baselineSet.has(item.id) ? selectedSet.has(item.id) ? '已加入' : '待移除' : selectedSet.has(item.id) ? '待加入' : '未加入') }}</span></span>
          </label>
          <div v-if="!filtered.length" class="members-state">{{ t('没有符合条件的成员') }}</div>
        </div>
        <p class="members-help members-lock-note">{{ t('不能从项目中移除自己或企业管理员；未激活账号在激活前仍无法登录。') }}<span v-if="!isTenantAdmin"> {{ t('委派管理员不能移除项目管理员。') }}</span></p>
      </template>
    </div>
    <template #footer>
      <div v-if="discardOpen" ref="discardPrompt" class="members-discard" tabindex="-1" role="alert"><p>{{ t('放弃尚未保存的项目成员调整？') }}</p><div><button type="button" class="btn" @click="keepEditing">{{ t('继续编辑') }}</button><button type="button" class="btn danger-outline" @click="discardChanges">{{ t('放弃修改') }}</button></div></div>
      <template v-else><span class="members-delta" role="status">{{ t('新增 {added} 人 · 移除 {removed} 人', { added: added.length, removed: removed.length }) }}</span><button type="button" class="btn" :disabled="saving" @click="requestClose">{{ t('关闭') }}</button><button type="button" class="btn primary" :disabled="!mutable || !dirty" @click="save">{{ t(saving ? '保存中…' : '保存成员调整') }}</button></template>
    </template>
  </OrganizationModal>
</template>

<style scoped>
.project-members{display:grid;gap:14px;min-width:0}.members-project{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:14px 16px;border:1px solid var(--line);border-radius:10px;background:var(--surface-soft)}.members-project>div{display:flex;align-items:center;gap:10px;min-width:0;overflow-wrap:anywhere}.members-project b{font-size:15px}.members-project code{color:var(--muted);font-size:11px}.members-project>span,.members-help{font-size:12px;line-height:1.7;color:var(--muted)}.members-help{margin:0}.members-toolbar{display:flex;align-items:center;gap:12px}.members-toolbar input{min-width:0;flex:1;width:100%;font:inherit;color:var(--ink);background:var(--surface);border:1px solid var(--line);border-radius:8px;padding:10px 12px;font-size:13px}.members-toolbar>span{white-space:nowrap;font-size:12px;color:var(--ink);font-variant-numeric:tabular-nums}.members-selection{display:flex;justify-content:space-between;align-items:center;gap:12px;font-size:12px;color:var(--muted)}.members-selection label{display:flex;align-items:center;gap:9px;color:var(--ink);min-height:32px}.members-list{max-height:390px;overflow:auto;border:1px solid var(--line);border-radius:10px;overscroll-behavior:contain}.members-row{display:flex;align-items:center;gap:10px;min-height:68px;padding:12px 14px;cursor:pointer;border-bottom:1px solid var(--line);box-sizing:border-box}.members-row:last-child{border-bottom:0}.members-row.selected{background:color-mix(in srgb,var(--primary) 4%,var(--surface))}.members-row:hover{background:var(--surface-soft)}.members-row.protected{cursor:default}.members-row input,.members-selection input{flex:none;width:16px;height:16px;accent-color:var(--primary)}.members-avatar{display:grid;place-items:center;flex:none;width:32px;height:32px;border-radius:50%;background:var(--primary-soft);color:var(--primary);font-size:13px;font-weight:650}.members-person{display:grid;gap:5px;flex:1;min-width:0}.members-person b{font-size:13px;overflow-wrap:anywhere}.members-person b small{font-size:10px;font-weight:400;color:var(--muted);margin-left:6px}.members-person>span{color:var(--muted);font-size:11px;overflow-wrap:anywhere}.members-meta{display:flex;align-items:center;justify-content:flex-end;gap:7px;flex-wrap:wrap;max-width:48%}.members-badge{border-radius:5px;padding:3px 6px;font-size:10px;color:var(--muted);background:var(--surface-soft);border:1px solid var(--line)}.members-badge.inactive{color:var(--warning,#ad7424);background:color-mix(in srgb,var(--warning,#ad7424) 8%,var(--surface));border-color:color-mix(in srgb,var(--warning,#ad7424) 25%,var(--line))}.members-role,.members-membership{font-size:11px;color:var(--muted)}.members-membership.added{color:var(--primary);font-weight:650}.members-membership.removed{color:var(--danger,#c24145);font-weight:650}.members-state{padding:26px 12px;text-align:center;color:var(--muted);font-size:13px}.members-notice{margin:0;padding:10px 12px;background:var(--primary-soft);color:var(--primary);border-radius:8px;font-size:12px}.members-delta{margin-right:auto;align-self:center;font-size:12px;color:var(--muted);font-variant-numeric:tabular-nums}.members-discard{display:flex;align-items:center;justify-content:space-between;gap:12px;width:100%}.members-discard p{font-size:13px;line-height:1.7;margin:0}.members-discard>div{display:flex;gap:8px;flex:none}.members-discard:focus-visible,.project-members input:focus-visible{outline:2px solid var(--primary);outline-offset:3px}.project-members .field-error{margin:0}
@media(max-width:640px){.members-project{align-items:flex-start;flex-direction:column;gap:5px}.members-project>div{flex-wrap:wrap}.members-toolbar{align-items:stretch;flex-direction:column;gap:7px}.members-toolbar input{font-size:16px}.members-toolbar>span{align-self:flex-end}.members-row{padding:12px 10px;gap:8px;flex-wrap:wrap}.members-person{flex-basis:calc(100% - 80px)}.members-meta{margin-left:64px;max-width:calc(100% - 64px);justify-content:flex-start}.members-list{max-height:45dvh}.members-discard{align-items:flex-start;flex-direction:column}.members-discard>div{width:100%;justify-content:flex-end}.members-delta{width:100%;margin-bottom:4px}.members-selection label{min-height:44px}}
</style>
