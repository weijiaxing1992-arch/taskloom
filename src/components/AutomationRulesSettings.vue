<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { t } from '../i18n'
import AppSelect from './AppSelect.vue'
import { useSettingsDialog, useSettingsScope } from './settingsScope'

type Status = { key: string; name: string; color: string; enabled: boolean }
type Member = { id: string; name: string }
type Rule = { id: number; name: string; enabled: boolean; trigger: string; fromStatus: string; toStatus: string; action: string; recipientModes: string[]; recipientUserIds: string[]; version: number; createdAt: string; updatedAt: string }
type Preview = { dryRun: boolean; matchingSampleCount: number; fromStatusEvaluatedAtRuntime: boolean; sampleItems: { id: number; code: string; title: string; status: string }[] }
type Execution = { id: number; ruleId: number; ruleName: string; subjectId: number; subjectCode: string; subjectTitle: string; status: 'notified' | 'skipped_no_recipient'; recipients: string[]; createdAt: string }
type RuleForm = Pick<Rule, 'name' | 'enabled' | 'fromStatus' | 'toStatus' | 'recipientModes' | 'recipientUserIds'>

const scope = useSettingsScope()
const items = ref<Rule[]>([]), statuses = ref<Status[]>([]), members = ref<Member[]>([]), canManage = ref(false)
const loading = ref(false), saving = ref(false), error = ref(''), notice = ref(''), preview = ref<Preview | null>(null)
const executions = ref<Execution[]>([]), executionsLoading = ref(false), executionsError = ref('')
const opened = ref(false), editing = ref<Rule | null>(null), baseline = ref(''), memberSearch = ref('')
const modal = useSettingsDialog(opened, () => close())
const form = reactive<RuleForm>({ name: '', enabled: false, fromStatus: '', toStatus: '', recipientModes: [], recipientUserIds: [] })
const dirty = computed(() => opened.value && JSON.stringify(form) !== baseline.value)
const mutable = computed(() => canManage.value && !loading.value && !saving.value && !scope.locked.value)
const stateOptions = computed(() => [{ value: '', label: t('不限状态') }, ...statuses.value.map(status => ({ value: status.key, label: status.name || status.key, disabled: !status.enabled && status.key !== form.fromStatus && status.key !== form.toStatus }))])
// 模式只表达“从当前需求的哪一项绑定取人”；不能选择整个项目的某个角色。
// 后端会在每次真正执行时重新校验绑定人员是否仍可访问当前项目。
const recipientModes = [
  { value: 'assignee', label: '处理人' },
  { value: 'owner', label: '负责人' },
  { value: 'frontend', label: '前端工程师（需求职能）' },
  { value: 'backend', label: '后端工程师（需求职能）' },
  { value: 'algorithm', label: '算法工程师（需求职能）' },
  { value: 'ui', label: 'UI 设计师（需求职能）' },
  { value: 'product', label: '产品负责人（需求职能）' },
  { value: 'frontend_lead', label: '前端负责人（字段）' },
  { value: 'backend_lead', label: '后端负责人（字段）' },
  { value: 'tester', label: '测试人员（字段）' },
]
const selectableMembers = computed(() => {
  const listed = new Map(members.value.map(member => [member.id, member]))
  // 历史规则中的成员可能在编辑期间已离开项目；保留可见且可移除的占位项，
  // 不能把无效成员静默当作有效成员继续保存。
  for (const id of form.recipientUserIds) if (!listed.has(id)) listed.set(id, { id, name: t('已不可用成员') + ' · ' + id })
  return [...listed.values()].sort((left, right) => left.name.localeCompare(right.name, 'zh-CN'))
})
const filteredMembers = computed(() => {
  const query = memberSearch.value.trim().toLocaleLowerCase()
  return selectableMembers.value.filter(member => !query || (member.name + ' ' + member.id).toLocaleLowerCase().includes(query))
})
const matchingPreview = computed(() => preview.value?.sampleItems || [])
const executionRows = computed(() => executions.value.map(item => ({
  ...item,
  ruleLabel: item.ruleName || t('已删除规则') + ' #' + item.ruleId,
  subjectLabel: [item.subjectCode, item.subjectTitle].filter(Boolean).join(' · ') || t('已删除需求') + ' #' + item.subjectId,
  recipientLabel: item.recipients.length ? item.recipients.map(id => members.value.find(member => member.id === id)?.name || id).join('、') : t('无有效接收人'),
  statusLabel: item.status === 'notified' ? t('已通知') : t('已跳过（无有效接收人）'),
})))
let requestVersion = 0, executionVersion = 0

function message(cause: unknown) { return cause instanceof Error ? cause.message : t('设置保存失败，请稍后重试') }
function statusName(key: string) { return statuses.value.find(status => status.key === key)?.name || key || t('不限状态') }
function recipientText(rule: Rule) {
  const values = rule.recipientModes.map(mode => recipientModes.find(item => item.value === mode)?.label || mode)
  const explicit = rule.recipientUserIds.map(id => members.value.find(member => member.id === id)?.name || id)
  return [...values, ...explicit].join('、') || t('未配置接收人')
}
function currentPayload(): RuleForm & { trigger: string; action: string } {
  return { name: form.name.trim(), enabled: form.enabled, trigger: 'requirement.status_changed', fromStatus: form.fromStatus, toStatus: form.toStatus, action: 'notify', recipientModes: [...form.recipientModes], recipientUserIds: [...form.recipientUserIds] }
}
function validate() {
  if (!form.name.trim()) return t('请填写规则名称')
  if (!form.recipientModes.length && !form.recipientUserIds.length) return t('请至少选择一位通知接收人')
  if (form.recipientUserIds.length > 50) return t('最多选择 50 位指定成员')
  return ''
}
async function load() {
  if (saving.value || !scope.current()) return
  const request = ++requestVersion
  loading.value = true; error.value = ''
  try {
    const response = await scope.request<{ items: Rule[]; statuses: Status[]; members: Member[]; canManage: boolean }>('/automation-rules')
    if (request !== requestVersion || !scope.current()) return
    items.value = response.items
    statuses.value = response.statuses
    members.value = response.members
    canManage.value = response.canManage
    if (canManage.value) void loadExecutions()
  } catch (cause) {
    if (request === requestVersion && scope.current()) error.value = message(cause)
  } finally {
    if (request === requestVersion) loading.value = false
  }
}
async function loadExecutions() {
  if (!canManage.value || !scope.current()) return
  const request = ++executionVersion
  executionsLoading.value = true; executionsError.value = ''
  try {
    const response = await scope.request<{ items: Execution[] }>('/automation-rules/executions')
    if (request !== executionVersion || !scope.current()) return
    if (!Array.isArray(response.items)) throw Error(t('自动化执行记录格式不正确'))
    executions.value = response.items
  } catch (cause) {
    if (request === executionVersion && scope.current()) executionsError.value = message(cause)
  } finally {
    if (request === executionVersion) executionsLoading.value = false
  }
}
function open(rule?: Rule) {
  if (!mutable.value) return
  editing.value = rule || null
  Object.assign(form, rule ? { name: rule.name, enabled: rule.enabled, fromStatus: rule.fromStatus, toStatus: rule.toStatus, recipientModes: [...rule.recipientModes], recipientUserIds: [...rule.recipientUserIds] } : { name: '', enabled: false, fromStatus: '', toStatus: '', recipientModes: [], recipientUserIds: [] })
  baseline.value = JSON.stringify(form); error.value = ''; preview.value = null; memberSearch.value = ''; opened.value = true
}
function close() {
  if (saving.value) return false
  if (dirty.value && !window.confirm(t('放弃尚未保存的设置修改？'))) return false
  opened.value = false
  return true
}
function toggleMode(mode: string) {
  if (!mutable.value) return
  form.recipientModes = form.recipientModes.includes(mode) ? form.recipientModes.filter(value => value !== mode) : [...form.recipientModes, mode]
}
function toggleMember(id: string) {
  if (!mutable.value) return
  form.recipientUserIds = form.recipientUserIds.includes(id) ? form.recipientUserIds.filter(value => value !== id) : [...form.recipientUserIds, id]
}
async function previewCurrent() {
  if (!mutable.value) return
  const validation = validate()
  if (validation) { error.value = validation; return }
  const request = requestVersion; saving.value = true; error.value = ''; preview.value = null
  try {
    const result = await scope.request<Preview>('/automation-rules/preview', { method: 'POST', body: JSON.stringify(currentPayload()) })
    if (request === requestVersion && scope.current()) {
      preview.value = result
      notice.value = t('演练完成：当前条件可匹配 {count} 条需求，未产生任何通知。', { count: result.matchingSampleCount })
    }
  } catch (cause) {
    if (request === requestVersion && scope.current()) error.value = message(cause)
  } finally {
    if (request === requestVersion) saving.value = false
  }
}
async function previewSaved(rule: Rule) {
  if (!canManage.value || saving.value || !scope.current()) return
  const request = requestVersion; saving.value = true; error.value = ''; preview.value = null
  try {
    const result = await scope.request<Preview>('/automation-rules/' + rule.id + '/preview', { method: 'POST', body: '{}' })
    if (request === requestVersion && scope.current()) {
      preview.value = result
      notice.value = t('演练完成：当前条件可匹配 {count} 条需求，未产生任何通知。', { count: result.matchingSampleCount })
    }
  } catch (cause) {
    if (request === requestVersion && scope.current()) error.value = message(cause)
  } finally {
    if (request === requestVersion) saving.value = false
  }
}
async function save() {
  if (!mutable.value || !opened.value) return
  const validation = validate()
  if (validation) { error.value = validation; return }
  const request = requestVersion; saving.value = true; error.value = ''
  try {
    const payload = currentPayload()
    const result = await scope.request<Rule>(editing.value ? '/automation-rules/' + editing.value.id : '/automation-rules', { method: editing.value ? 'PATCH' : 'POST', body: JSON.stringify(editing.value ? { ...payload, version: editing.value.version } : payload) })
    if (request !== requestVersion || !scope.current()) return
    items.value = [result, ...items.value.filter(item => item.id !== result.id)]
    opened.value = false; preview.value = null
    notice.value = t('自动化规则已保存；默认保持关闭，启用后才会执行。')
    void loadExecutions()
  } catch (cause) {
    if (request === requestVersion && scope.current()) error.value = message(cause)
  } finally {
    if (request === requestVersion) saving.value = false
  }
}
async function toggle(rule: Rule) {
  if (!mutable.value || opened.value) return
  const request = requestVersion; saving.value = true; error.value = ''
  try {
    const result = await scope.request<Rule>('/automation-rules/' + rule.id, { method: 'PATCH', body: JSON.stringify({ enabled: !rule.enabled, version: rule.version }) })
    if (request === requestVersion && scope.current()) items.value = items.value.map(item => item.id === result.id ? result : item)
  } catch (cause) {
    if (request === requestVersion && scope.current()) error.value = message(cause)
  } finally {
    if (request === requestVersion) saving.value = false
  }
}
async function remove(rule: Rule) {
  if (!mutable.value || opened.value || !window.confirm(t('删除规则“{name}”？已执行记录将保留用于审计。', { name: rule.name }))) return
  const request = requestVersion; saving.value = true; error.value = ''
  try {
    const result = await scope.request<{ deleted: boolean }>('/automation-rules/' + rule.id + '?version=' + rule.version, { method: 'DELETE' })
    if (request === requestVersion && scope.current() && result.deleted) {
      items.value = items.value.filter(item => item.id !== rule.id)
      notice.value = t('自动化规则已删除；历史执行记录仍保留。')
      void loadExecutions()
    }
  } catch (cause) {
    if (request === requestVersion && scope.current()) error.value = message(cause)
  } finally {
    if (request === requestVersion) saving.value = false
  }
}

onMounted(load)
defineExpose({ dirty, saving })
</script>

<template>
  <section class="automation-settings" :aria-busy="loading || saving">
    <header class="settings-section-head">
      <div><h2>{{ t('自动化规则') }}</h2><p>{{ t('当需求状态变更时自动发送站内通知；已启用的成员机器人会同步推送。') }}</p></div>
      <div class="settings-buttons"><button type="button" class="btn" :disabled="loading || saving || scope.locked.value" @click="load">{{ t('重新加载') }}</button><button type="button" class="btn primary" :disabled="!mutable" @click="open()">＋ {{ t('创建规则') }}</button></div>
    </header>
    <p v-if="scope.locked.value" class="settings-error" role="alert">{{ t('项目或账号已变化，请刷新页面后继续') }}</p>
    <p v-if="!canManage && !loading" class="settings-note">{{ t('仅企业管理员和项目管理员可以修改配置。') }}</p>
    <p class="automation-safety"><span>●</span>{{ t('站内通知试运行：规则默认关闭；每次执行都会留下可追溯记录。') }}</p>
    <p v-if="error && !opened" class="settings-error" role="alert">{{ t(error) }} <button type="button" class="link" :disabled="loading || saving" @click="load">{{ t('重新加载') }}</button></p>
    <p v-if="notice" class="settings-success" role="status">{{ notice }}</p>

    <div class="settings-table-wrap automation-table-wrap">
      <table class="settings-table automation-table"><thead><tr><th>{{ t('规则') }}</th><th>{{ t('触发条件') }}</th><th>{{ t('通知接收人') }}</th><th>{{ t('状态') }}</th><th>{{ t('操作') }}</th></tr></thead><tbody>
        <tr v-if="loading"><td colspan="5">{{ t('正在加载…') }}</td></tr>
        <tr v-for="rule in items" :key="rule.id">
          <td><b>{{ rule.name }}</b><small><code>{{ rule.trigger }}</code> · {{ t('站内通知') }}</small></td>
          <td><span class="automation-condition"><i :style="{ background: statuses.find(status=>status.key===rule.fromStatus)?.color || '#94A3B8' }"></i>{{ statusName(rule.fromStatus) }} → <i :style="{ background: statuses.find(status=>status.key===rule.toStatus)?.color || '#94A3B8' }"></i>{{ statusName(rule.toStatus) }}</span></td>
          <td class="automation-recipients">{{ recipientText(rule) }}</td>
          <td><button type="button" :class="['automation-switch', { active: rule.enabled }]" :aria-pressed="rule.enabled" :disabled="!mutable" @click="toggle(rule)"><i></i><span>{{ rule.enabled ? t('已启用') : t('已关闭') }}</span></button></td>
          <td class="automation-actions"><button type="button" class="link" :disabled="!canManage || saving" @click="previewSaved(rule)">{{ t('演练') }}</button><button type="button" class="link" :disabled="!mutable" @click="open(rule)">{{ t('编辑') }}</button><button type="button" class="link danger-link" :disabled="!mutable" @click="remove(rule)">{{ t('删除') }}</button></td>
        </tr>
        <tr v-if="!loading && !items.length"><td colspan="5" class="settings-empty">{{ t('暂无自动化规则。创建后默认关闭，建议先使用演练确认匹配范围。') }}</td></tr>
      </tbody></table>
    </div>
    <div v-if="preview && !opened" class="automation-preview"><b>{{ t('最近一次演练') }}</b><span>{{ t('匹配 {count} 条需求', { count: preview.matchingSampleCount }) }}</span><small v-if="preview.fromStatusEvaluatedAtRuntime">{{ t('来源状态会在真实状态变更时校验，演练不猜测历史状态。') }}</small><ul v-if="matchingPreview.length"><li v-for="item in matchingPreview" :key="item.id"><code>{{ item.code }}</code> {{ item.title }}</li></ul></div>

    <details v-if="canManage" class="automation-executions">
      <summary><span>{{ t('执行记录') }}</span><small>{{ t('保留最近 100 条，规则删除后历史记录仍可审计。') }}</small></summary>
      <div class="automation-execution-actions"><span>{{ t('仅展示已真实执行的规则；演练不会写入此处。') }}</span><button type="button" class="btn compact" :disabled="executionsLoading || saving || scope.locked.value" @click="loadExecutions">{{ executionsLoading ? t('加载中…') : t('刷新记录') }}</button></div>
      <p v-if="executionsError" class="settings-error" role="alert">{{ t(executionsError) }} <button type="button" class="link" :disabled="executionsLoading" @click="loadExecutions">{{ t('重试') }}</button></p>
      <div class="settings-table-wrap automation-execution-table-wrap">
        <table class="settings-table automation-execution-table"><thead><tr><th>{{ t('时间') }}</th><th>{{ t('规则') }}</th><th>{{ t('受影响需求') }}</th><th>{{ t('结果') }}</th><th>{{ t('接收人') }}</th></tr></thead><tbody>
          <tr v-if="executionsLoading"><td colspan="5">{{ t('正在加载…') }}</td></tr>
          <tr v-for="item in executionRows" :key="item.id"><td><time>{{ item.createdAt }}</time></td><td>{{ item.ruleLabel }}</td><td><code>{{ item.subjectLabel }}</code></td><td><span :class="['automation-execution-status', item.status]">{{ item.statusLabel }}</span></td><td>{{ item.recipientLabel }}</td></tr>
          <tr v-if="!executionsLoading && !executionRows.length"><td colspan="5" class="settings-empty">{{ t('暂无执行记录') }}</td></tr>
        </tbody></table>
      </div>
    </details>

    <div v-if="opened" class="settings-modal-shade" @click.self="close">
      <form ref="modal" tabindex="-1" class="settings-modal automation-modal" role="dialog" aria-modal="true" :aria-label="t(editing ? '编辑自动化规则' : '创建自动化规则')" @submit.prevent="save">
        <header><div><h2>{{ t(editing ? '编辑自动化规则' : '创建自动化规则') }}</h2><p>{{ t('仅需求状态变更可触发；动作固定为站内通知。') }}</p></div><button type="button" :disabled="saving" :aria-label="t('关闭')" @click="close">×</button></header>
        <fieldset :disabled="saving || scope.locked.value">
          <label>{{ t('规则名称') }} *<input v-model="form.name" maxlength="100" required :placeholder="t('例如：研发完成后提醒验证人')"></label>
          <div class="automation-form-grid"><label>{{ t('来源状态（可选）') }}<AppSelect v-model="form.fromStatus" :options="stateOptions" :label="t('来源状态（可选）')" :disabled="!mutable" /></label><label>{{ t('目标状态（可选）') }}<AppSelect v-model="form.toStatus" :options="stateOptions" :label="t('目标状态（可选）')" :disabled="!mutable" /></label></div>
          <div class="automation-form-grid"><label>{{ t('触发事件') }}<input :value="t('需求状态已变更')" disabled></label><label>{{ t('执行动作') }}<input :value="t('发送站内通知')" disabled></label></div>
          <fieldset class="automation-recipients-field"><legend>{{ t('通知接收人') }} *</legend><div class="automation-mode-grid"><label v-for="mode in recipientModes" :key="mode.value" class="settings-check"><input type="checkbox" :checked="form.recipientModes.includes(mode.value)" @change="toggleMode(mode.value)">{{ t(mode.label) }}</label></div><label>{{ t('指定成员（可选）') }}<input v-model="memberSearch" type="search" :placeholder="t('搜索成员')"></label><div class="automation-member-list"><label v-for="member in filteredMembers" :key="member.id" class="settings-check"><input type="checkbox" :checked="form.recipientUserIds.includes(member.id)" @change="toggleMember(member.id)"><span>{{ member.name }}</span><code>{{ member.id }}</code></label><p v-if="!filteredMembers.length">{{ t('暂无匹配成员') }}</p></div><small>{{ t('处理人、负责人、需求职能成员及已绑定的前后端负责人/测试人员都会在执行时按当前项目可见性重新校验；无有效接收人时仅记录跳过。') }}</small></fieldset>
          <label class="settings-check"><input v-model="form.enabled" type="checkbox">{{ t('保存后立即启用规则') }}<small>{{ t('新规则默认关闭；建议先点击“演练”核对范围。') }}</small></label>
        </fieldset>
        <p v-if="error" class="settings-error" role="alert">{{ t(error) }}</p>
        <div v-if="preview" class="automation-preview modal-preview"><b>{{ t('演练结果') }}</b><span>{{ t('匹配 {count} 条需求', { count: preview.matchingSampleCount }) }}</span><ul v-if="matchingPreview.length"><li v-for="item in matchingPreview" :key="item.id"><code>{{ item.code }}</code> {{ item.title }}</li></ul></div>
        <footer><button type="button" class="btn" :disabled="saving" @click="close">{{ t('取消') }}</button><button type="button" class="btn" :disabled="!mutable" @click="previewCurrent">{{ t('演练') }}</button><button type="submit" class="btn primary" :disabled="!mutable">{{ saving ? t('保存中…') : t('保存规则') }}</button></footer>
      </form>
    </div>
  </section>
</template>

<style scoped>
.automation-safety{display:inline-flex;align-items:center;gap:8px;margin:14px 0 18px;padding:7px 10px;border:1px solid color-mix(in srgb,var(--success) 28%,var(--border));border-radius:7px;background:color-mix(in srgb,var(--success) 9%,var(--card));color:var(--muted-foreground);font-size:11px}.automation-safety span{color:var(--success);font-size:11px}.automation-condition{display:inline-flex;align-items:center;gap:6px;white-space:nowrap}.automation-condition i{width:7px;height:7px;border-radius:50%;flex:none}.automation-recipients{max-width:270px;overflow:hidden;text-overflow:ellipsis}.automation-actions{white-space:nowrap}.automation-actions .link+.link{margin-left:10px}.danger-link{color:var(--danger)!important}.automation-switch{display:inline-flex;align-items:center;gap:7px;padding:0;border:0;background:transparent;color:var(--muted-foreground);font:inherit;font-size:11px}.automation-switch i{display:block;position:relative;width:28px;height:16px;border-radius:99px;background:var(--muted);transition:background .15s}.automation-switch i::after{content:'';position:absolute;top:2px;left:2px;width:12px;height:12px;border-radius:50%;background:#fff;box-shadow:0 1px 2px #0003;transition:transform .15s}.automation-switch.active{color:var(--success)}.automation-switch.active i{background:var(--success)}.automation-switch.active i::after{transform:translateX(12px)}.automation-preview{display:grid;grid-template-columns:auto auto 1fr;align-items:baseline;gap:8px 15px;margin-top:14px;padding:12px 14px;border:1px solid var(--border);border-radius:8px;background:var(--card);font-size:12px}.automation-preview>span{color:var(--primary);font-weight:600}.automation-preview small{color:var(--muted-foreground);font-size:11px}.automation-preview ul{grid-column:1 / -1;margin:2px 0 0;padding-left:18px;color:var(--muted-foreground);font-size:11px;line-height:1.8}.automation-preview code{margin-right:5px;color:var(--primary)}.automation-form-grid{display:grid;grid-template-columns:1fr 1fr;gap:14px}.automation-recipients-field{display:grid;gap:10px;margin:0;padding:0;border:0}.automation-recipients-field legend{font-size:12px;color:var(--text)}.automation-mode-grid{display:flex;gap:16px;flex-wrap:wrap}.automation-member-list{max-height:176px;overflow:auto;border:1px solid var(--input);border-radius:7px;padding:4px;background:var(--background)}.automation-member-list label{display:flex!important;align-items:center;gap:8px;padding:8px;border-radius:5px}.automation-member-list label:hover{background:var(--accent)}.automation-member-list code{margin-left:auto;color:var(--muted-foreground);font-size:10px}.automation-member-list p{margin:8px;color:var(--muted-foreground);font-size:11px;text-align:center}.automation-modal .settings-check small{display:block;margin-left:4px}.modal-preview{margin:0 23px 14px}.automation-table-wrap{min-height:100px}.automation-executions{margin-top:18px;border:1px solid var(--border);border-radius:8px;background:var(--card)}.automation-executions>summary{display:flex;align-items:baseline;gap:10px;padding:12px 14px;cursor:pointer;list-style:none}.automation-executions>summary::-webkit-details-marker{display:none}.automation-executions>summary::before{content:'›';color:var(--primary);font-size:17px;line-height:1;transition:transform .15s}.automation-executions[open]>summary::before{transform:rotate(90deg)}.automation-executions>summary span{font-size:13px;font-weight:600}.automation-executions>summary small,.automation-execution-actions{font-size:11px;color:var(--muted-foreground)}.automation-execution-actions{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:0 14px 12px}.automation-execution-table-wrap{max-height:360px;overflow:auto;border-top:1px solid var(--border)}.automation-execution-table{min-width:780px}.automation-execution-table td{vertical-align:top;line-height:1.6}.automation-execution-table time,.automation-execution-table code{font-size:11px;overflow-wrap:anywhere}.automation-execution-status{display:inline-flex;padding:2px 6px;border-radius:999px;font-size:11px;white-space:nowrap}.automation-execution-status.notified{background:color-mix(in srgb,var(--success) 12%,var(--card));color:var(--success)}.automation-execution-status.skipped_no_recipient{background:var(--accent);color:var(--muted-foreground)}@media(max-width:780px){.automation-table{min-width:760px}.automation-form-grid{grid-template-columns:1fr}.automation-preview{grid-template-columns:1fr}.automation-preview ul{grid-column:auto}.automation-member-list label{min-height:40px}.automation-switch{min-height:34px}.automation-executions>summary{align-items:flex-start;flex-wrap:wrap}.automation-execution-actions{align-items:flex-start;flex-wrap:wrap}.automation-execution-actions .btn{min-height:40px}}
</style>
