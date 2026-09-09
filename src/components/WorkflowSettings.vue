<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { t } from '../i18n'
import { ScrollArea } from './ui/scroll-area'
import { useSettingsDialog, useSettingsScope } from './settingsScope'

type Status = { id: number; key: string; name: string; color: string; category: string; enabled: boolean; system: boolean }
type Edge = { from: string; to: string; roles: string[] }
type Workflow = { initialStatus: string; endStatuses: string[]; transitions: Edge[]; version: number; canManage: boolean; roles: { key: string; name: string }[] }
const scope = useSettingsScope(), statuses = ref<Status[]>([]), roles = ref<Workflow['roles']>([]), canManage = ref(false), loading = ref(false), saving = ref(false), loaded = ref(false)
const error = ref(''), notice = ref(''), conflict = ref(false), baseline = ref(''), editor = ref<{ from: string; to: string } | null>(null)
const modal = useSettingsDialog(computed(() => !!editor.value), () => closeRole())
const form = reactive({ initialStatus: '', endStatuses: [] as string[], transitions: [] as Edge[], version: 0 })
const dirty = computed(() => loaded.value && JSON.stringify(form) !== baseline.value)
const mutable = computed(() => loaded.value && canManage.value && !loading.value && !saving.value && !scope.locked.value)
const permittedRoles = computed(() => roles.value.filter(role => role.key !== 'viewer'))
const initialOptions = computed(() => statuses.value.filter(status => status.enabled && ['todo','doing'].includes(status.category)))
const selectedEdge = computed(() => editor.value ? form.transitions.find(edge => edge.from === editor.value!.from && edge.to === editor.value!.to) : undefined)
let loadVersion = 0
const message = (cause: unknown) => cause instanceof Error ? cause.message : '设置保存失败，请稍后重试'
function statusName(key: string) { const status = statuses.value.find(status => status.key === key); return status ? status.system && status.name === status.key ? t(status.name) : status.name : key }
function statusColor(key: string) { const color = statuses.value.find(status => status.key === key)?.color || ''; return /^#[0-9a-f]{6}$/i.test(color) ? color : '#9298a7' }
function edgeOf(from: string, to: string) { return form.transitions.find(edge => edge.from === from && edge.to === to) }
function roleName(key: string) { return t(roles.value.find(role => role.key === key)?.name || key) }
function edgeTitle(from: string, to: string) { const edge = edgeOf(from,to); return edge ? edge.roles.length ? edge.roles.map(roleName).join(', ') : t('尚未选择允许的角色') : t('禁止此流转') }
async function load(force = false) {
  if (saving.value || !scope.current()) return
  if (dirty.value && (!force || !window.confirm(t('重新加载将放弃本地工作流草稿，是否继续？')))) return
  const request = ++loadVersion; loading.value = true; error.value = ''
  try {
    const [workflow, state] = await Promise.all([scope.request<Workflow>('/requirement-workflow'), scope.request<{ items: Status[] }>('/requirement-statuses')])
    if (request !== loadVersion) return
    statuses.value = state.items; roles.value = workflow.roles; canManage.value = workflow.canManage
    Object.assign(form, { initialStatus: workflow.initialStatus, endStatuses: [...workflow.endStatuses], transitions: workflow.transitions.map(edge => ({ ...edge, roles: [...edge.roles] })), version: workflow.version })
    baseline.value = JSON.stringify(form); loaded.value = true; conflict.value = false; editor.value = null
  } catch (cause) { if (request === loadVersion && scope.current()) error.value = message(cause) }
  finally { if (request === loadVersion) loading.value = false }
}
function externalChanged() { if (dirty.value) { notice.value = '状态配置已变化，请先保存或重新加载工作流；本地修改仍保留。'; conflict.value = true } else void load() }
function toggleEdge(from: string, to: string) {
  if (!mutable.value || from === to) return
  if (edgeOf(from,to)) { form.transitions = form.transitions.filter(edge => edge.from !== from || edge.to !== to); if (editor.value?.from === from && editor.value?.to === to) editor.value = null }
  else if (statuses.value.some(status => status.key === to && status.enabled)) { form.transitions.push({ from, to, roles: [] }); editor.value = { from, to } }
}
function toggleRole(key: string) {
  if (!mutable.value || !selectedEdge.value || !permittedRoles.value.some(role => role.key === key)) return
  const edge = selectedEdge.value; edge.roles = edge.roles.includes(key) ? edge.roles.filter(role => role !== key) : [...edge.roles, key]
}
function toggleEnd(status: Status) {
  if (!mutable.value) return
  if (form.endStatuses.includes(status.key)) form.endStatuses = form.endStatuses.filter(key => key !== status.key)
  else if (status.enabled && ['done','cancelled'].includes(status.category)) form.endStatuses.push(status.key)
}
function validate() {
  if (!initialOptions.value.some(status => status.key === form.initialStatus)) return '请选择有效的待办或进行阶段作为起始状态'
  if (!form.endStatuses.length || form.endStatuses.some(key => !statuses.value.some(status => status.key === key && status.enabled && ['done','cancelled'].includes(status.category)))) return '请至少选择一个有效的完成或取消阶段作为结束状态'
  if (form.transitions.some(edge => edge.from === edge.to || !statuses.value.some(status => status.key === edge.from) || !statuses.value.some(status => status.key === edge.to && status.enabled))) return '请移除同状态或指向已停用状态的流转'
  if (form.transitions.some(edge => !edge.roles.length || edge.roles.some(key => !permittedRoles.value.some(role => role.key === key)))) return '每条已启用流转必须选择至少一个有效角色，只读角色不能流转'
  return ''
}
async function save() {
  if (!mutable.value || !dirty.value) return
  error.value = validate(); if (error.value) return
  const request = loadVersion; saving.value = true; error.value = ''; notice.value = ''
  try {
    const result = await scope.request<Workflow>('/requirement-workflow', { method: 'PUT', body: JSON.stringify(form) })
    if (request !== loadVersion) return
    Object.assign(form, { initialStatus: result.initialStatus, endStatuses: [...result.endStatuses], transitions: result.transitions.map(edge => ({ ...edge, roles: [...edge.roles] })), version: result.version })
    baseline.value = JSON.stringify(form); conflict.value = false; notice.value = '工作流已保存，后续流转按新规则执行'; editor.value = null
  } catch (cause) { if (request === loadVersion && scope.current()) { error.value = message(cause); conflict.value = (cause as { status?: number })?.status === 409 } }
  finally { if (request === loadVersion) saving.value = false }
}
function closeRole(event?: KeyboardEvent) { if (event?.key && event.key !== 'Escape') return; event?.preventDefault(); event?.stopImmediatePropagation(); if (!saving.value) editor.value = null }
onMounted(() => load())
defineExpose({ dirty, saving, externalChanged })
</script>

<template>
  <section class="workflow-settings" :aria-busy="loading || saving">
    <header class="settings-section-head"><div><h2>{{ t('需求工作流') }}</h2><p>{{ t('按行选择来源状态，按列选择目标状态；勾选流转后配置允许操作的角色。') }}</p></div><div class="settings-buttons"><button type="button" class="btn" :disabled="loading || saving || scope.locked.value" @click="load(true)">{{ t('重新加载') }}</button><button type="button" class="btn primary" :disabled="!mutable || !dirty" @click="save">{{ saving ? t('保存中…') : t('保存工作流') }}</button></div></header>
    <p v-if="scope.locked.value" class="settings-error" role="alert">{{ t('项目或账号已变化，请刷新页面后继续') }}</p><p v-if="!canManage && loaded" class="settings-note">{{ t('仅企业管理员和项目管理员可以修改配置。') }}</p>
    <p v-if="error" class="settings-error" role="alert">{{ t(error) }}</p><p v-if="notice" :class="conflict?'settings-warning':'settings-success'" role="status">{{ t(notice) }}</p><p v-if="conflict" class="settings-warning">{{ t('服务器配置已变化，本地草稿未清空。请记录修改后重新加载，再合并保存。') }}</p><p v-if="loading" role="status" class="settings-note">{{ t('正在加载…') }}</p>
    <div v-if="loaded" class="workflow-boundaries"><label><b>{{ t('起始状态') }}</b><select v-model="form.initialStatus" :disabled="!mutable" :aria-label="t('起始状态')"><option v-if="!initialOptions.some(status=>status.key===form.initialStatus)" :value="form.initialStatus">{{ statusName(form.initialStatus) }} · {{ t('原配置需调整') }}</option><option v-for="status in initialOptions" :key="status.key" :value="status.key">{{ statusName(status.key) }}</option></select><small>{{ t('新建需求使用此状态；显式保存草稿不受影响。') }}</small></label><fieldset :disabled="!mutable"><legend>{{ t('结束状态') }}</legend><div class="workflow-end-options"><label v-for="status in statuses.filter(status=>['done','cancelled'].includes(status.category)||form.endStatuses.includes(status.key))" :key="status.key"><input type="checkbox" :checked="form.endStatuses.includes(status.key)" :disabled="!mutable||(!status.enabled&&!form.endStatuses.includes(status.key))" @change="toggleEnd(status)"><i :style="{background:statusColor(status.key)}"></i>{{ statusName(status.key) }}<small v-if="!status.enabled">{{ t('已停用') }}</small></label></div><small>{{ t('至少一个完成或取消阶段；结束状态是否允许重新打开仍由下方矩阵控制。') }}</small></fieldset></div>
    <ScrollArea v-if="loaded" type="always" horizontal class="workflow-matrix-wrap workflow-scroll-area" tabindex="0" :aria-label="t('工作流状态流转矩阵')"><table class="workflow-matrix"><thead><tr><th class="workflow-source">{{ t('来源 ↓ / 目标 →') }}</th><th v-for="target in statuses" :key="target.key"><span><i :style="{background:statusColor(target.key)}"></i>{{ statusName(target.key) }}</span><small v-if="!target.enabled">{{ t('已停用') }}</small></th></tr></thead><tbody><tr v-for="from in statuses" :key="from.key"><th class="workflow-source"><i :style="{background:statusColor(from.key)}"></i>{{ statusName(from.key) }}<small v-if="!from.enabled">{{ t('历史来源') }}</small></th><td v-for="to in statuses" :key="to.key" :class="{'same-state':from.key===to.key,'allowed-state':!!edgeOf(from.key,to.key),'invalid-edge':edgeOf(from.key,to.key)&&!edgeOf(from.key,to.key)!.roles.length}"><span v-if="from.key===to.key" :title="t('相同状态不需要流转')">—</span><div v-else class="edge-cell"><input type="checkbox" :checked="!!edgeOf(from.key,to.key)" :disabled="!mutable||(!to.enabled&&!edgeOf(from.key,to.key))" :aria-label="t('允许从 {from} 流转到 {to}',{from:statusName(from.key),to:statusName(to.key)})" @change="toggleEdge(from.key,to.key)"><button v-if="edgeOf(from.key,to.key)" type="button" :disabled="!mutable" :title="edgeTitle(from.key,to.key)" :aria-label="t('配置 {from} 到 {to} 的角色权限',{from:statusName(from.key),to:statusName(to.key)})" @click="editor={from:from.key,to:to.key}">♙ <span>{{ edgeOf(from.key,to.key)!.roles.length }}</span></button></div></td></tr></tbody></table></ScrollArea>
    <div v-if="loaded" class="workflow-bottom"><p class="settings-note">{{ t('角色权限按当前项目角色校验；只读角色不能执行状态流转。') }}<br>{{ t('停用状态保留为历史来源，可配置恢复到有效状态。') }}</p><span :class="dirty?'settings-warning':'settings-note'">{{ dirty ? t('有尚未保存的工作流修改') : t('配置已同步') }} · {{ t('版本 {version}',{version:form.version}) }}</span></div>
    <div v-if="editor && selectedEdge" class="settings-modal-shade" @click.self="closeRole()" @keydown="closeRole"><section ref="modal" tabindex="-1" class="settings-modal workflow-role-modal" role="dialog" aria-modal="true" :aria-label="t('流转角色权限')"><header><div><h2>{{ t('流转角色权限') }}</h2><p>{{ statusName(editor.from) }} → {{ statusName(editor.to) }}</p></div><button type="button" :disabled="saving" :aria-label="t('关闭')" @click="closeRole()">×</button></header><div class="workflow-role-list"><label v-for="role in permittedRoles" :key="role.key"><input type="checkbox" :disabled="!mutable" :checked="selectedEdge.roles.includes(role.key)" @change="toggleRole(role.key)">{{ t(role.name) }}<code>{{ role.key }}</code></label></div><p class="settings-note">{{ t('至少选择一个角色。此处修改加入工作流草稿，点击“保存工作流”后生效。') }}</p><p v-if="!selectedEdge.roles.length" class="settings-warning">{{ t('尚未选择允许的角色') }}</p><footer><button type="button" class="btn primary" :disabled="saving" @click="closeRole()">{{ t('完成配置') }}</button></footer></section></div>
  </section>
</template>

<style scoped>
.workflow-scroll-area{height:clamp(300px,calc(100dvh - 390px),580px);overflow:hidden!important;padding-bottom:12px;padding-right:12px}.workflow-scroll-area:focus-visible{outline:2px solid var(--primary);outline-offset:2px}
.workflow-boundaries{display:grid;grid-template-columns:minmax(200px,1fr) 2fr;gap:24px;padding:20px;background:var(--surface,#fff);border:1px solid var(--line,#e6e9f0);border-radius:10px;margin:20px 0}.workflow-boundaries>label{display:grid;gap:10px;align-content:start}.workflow-boundaries b,.workflow-boundaries legend{font-size:12px;font-weight:600}.workflow-boundaries small{font-size:11px;line-height:1.6;color:var(--muted,#7a8597)}.workflow-boundaries select{border:1px solid var(--line,#d5dae3);border-radius:6px;padding:9px;background:var(--surface,#fff);font:inherit;font-size:12px}.workflow-boundaries fieldset{padding:0;border:0;min-width:0}.workflow-end-options{display:flex;flex-wrap:wrap;gap:12px 18px;margin:10px 0}.workflow-end-options label{display:flex;align-items:center;gap:6px;font-size:12px}.workflow-end-options i,.workflow-source i,.workflow-matrix th i{display:inline-block;width:6px;height:6px;border-radius:50%;flex:none}.workflow-matrix-wrap{max-width:100%;overflow:auto;border:1px solid var(--line,#e6e9f0);border-radius:9px;max-height:640px;background:var(--surface,#fff)}.workflow-matrix{border-collapse:separate;border-spacing:0;min-width:100%;font-size:11px}.workflow-matrix th,.workflow-matrix td{border-right:1px solid var(--line,#ecedf2);border-bottom:1px solid var(--line,#ecedf2);padding:11px;text-align:center;min-width:100px;height:49px}.workflow-matrix thead th{position:sticky;top:0;z-index:2;background:var(--surface-soft,#f7f8fb);font-weight:500;max-width:160px}.workflow-matrix th small{display:block;font-weight:400;font-size:10px;color:var(--muted,#8e98a8);margin-top:4px}.workflow-matrix th span{display:flex;align-items:center;justify-content:center;gap:5px}.workflow-matrix .workflow-source{position:sticky;left:0;z-index:1;text-align:left;min-width:190px;background:var(--surface,#fff);font-weight:500;box-shadow:2px 0 4px #132a4d05}.workflow-matrix .workflow-source i{margin-right:6px}.workflow-matrix thead .workflow-source{z-index:3;background:var(--surface-soft,#f7f8fb)}.workflow-matrix input{accent-color:#7068e8;width:14px;height:14px}.same-state{background:var(--surface-soft,#f5f6f9);color:#a2acbd}.allowed-state{background:#736aea08}.invalid-edge{background:#f59e0b0a}.edge-cell{display:flex;align-items:center;justify-content:center;gap:5px}.edge-cell button{display:flex;align-items:center;gap:3px;border:0;background:transparent;color:#6b64d9;font-size:14px;padding:0}.edge-cell button span{font-size:9px}.workflow-bottom{display:flex;align-items:center;justify-content:space-between;gap:15px;flex-wrap:wrap;margin:14px 0}.workflow-role-list{display:grid;gap:8px;padding:20px}.workflow-role-list label{display:flex;align-items:center;gap:9px;padding:9px;border:1px solid var(--line,#e6e9f0);border-radius:6px;font-size:12px}.workflow-role-list code{margin-left:auto;color:var(--muted,#929bad);font-size:10px}.workflow-role-modal>.settings-note,.workflow-role-modal>.settings-warning{padding:0 20px}@media(max-width:900px){.workflow-boundaries{grid-template-columns:1fr}.workflow-matrix .workflow-source{min-width:160px}}
</style>

<style scoped>
/* 工作流矩阵在暗色模式下需要区分“可流转”与“无效权限”，但不使用浅色硬编码底。 */
.workflow-boundaries,.workflow-matrix-wrap{background:var(--card);border-color:var(--border)}
.workflow-boundaries small,.workflow-matrix th small,.workflow-role-list code{color:var(--muted-foreground)}
.workflow-boundaries select{background:var(--background);color:var(--foreground);border-color:var(--input)}
.workflow-matrix th,.workflow-matrix td{border-color:var(--border)}
.workflow-matrix thead th,.workflow-matrix thead .workflow-source,.workflow-matrix .same-state{background:var(--secondary);color:var(--muted-foreground)}
.workflow-matrix .workflow-source{background:var(--card);color:var(--foreground);box-shadow:2px 0 4px color-mix(in srgb,var(--foreground) 8%,transparent)}
.workflow-matrix .allowed-state{background:color-mix(in srgb,var(--accent) 58%,var(--card))}
.workflow-matrix .invalid-edge{background:var(--warning-background)}
.edge-cell button{color:var(--primary)}
.workflow-role-list label{background:var(--background);border-color:var(--border)}
</style>

<style scoped>
.application-settings .workflow-role-list label{display:flex;align-items:center}.application-settings .workflow-role-list code{margin-left:auto}
</style>
<style scoped>
@media(max-width:820px){
 .edge-cell{gap:10px}.edge-cell button{min-width:44px;min-height:44px;justify-content:center}
 .workflow-matrix input{width:24px;height:24px;flex:none}.workflow-end-options label,.workflow-role-list label{min-height:44px}
 .workflow-role-list label{flex-wrap:wrap}.workflow-role-list code{overflow-wrap:anywhere}
 .workflow-boundaries select{font-size:16px;min-height:44px;min-width:0;max-width:100%}
}
</style>
