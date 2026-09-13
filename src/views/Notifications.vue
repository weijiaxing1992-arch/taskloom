<script setup lang="ts">
import { t, locale, formatDate } from '../i18n'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api'
import AppSelect from '../components/AppSelect.vue'
import UserWecomWebhook from '../components/UserWecomWebhook.vue'
import DesktopNotifications from '../components/DesktopNotifications.vue'
import { notificationBody } from '../notificationDisplay'

type InboxNotice = { id: number; projectId: string; projectName: string; actor: string; eventType: string; subjectType?: string; subjectId?: number; title: string; body: string; readAt: string; createdAt: string; url: string }
type NoticeStateResult = { updated: number; unread: number }
const items = ref<InboxNotice[]>([])
const selectedIDs = ref<number[]>([])
const pageIDs = computed(() => items.value.map(item => item.id))
const allPageSelected = computed(() => pageIDs.value.length > 0 && pageIDs.value.every(id => selectedIDs.value.includes(id)))
const somePageSelected = computed(() => selectedIDs.value.length > 0 && !allPageSelected.value)
const selectedSummary = computed(() => t('已选 {count} 条', { count: selectedIDs.value.length }))
const stateNotice = ref('')
const identityBlocked = ref(false)
const unread = ref(0)
const read = ref('')
const project = ref('')
const eventType = ref('')
const filtersExpanded = ref(false)
const filterCount = computed(() => [eventType.value, project.value].filter(Boolean).length)
const group = ref(''), offset = ref(0), total = ref(0), hasMore = ref(false)
const groupUnread = ref<Record<string, number>>({})
const groups = [{key:'',name:'全部'}, {key:'handoffs',name:'分配与交接'}, {key:'mentions',name:'提及与回复'}, {key:'changes',name:'需求与状态变更'}, {key:'activity',name:'其他动态'}]
function chooseGroup(key: string) { if (saving.value) return; eventType.value = ''; group.value = key }
function changePage(direction: number) { if (loading.value || saving.value) return; selectedIDs.value = []; offset.value = Math.max(0, offset.value + direction * 40); void load() }
const projects = ref<any[]>([])
// 详情链接可直接定位自己的机器人投递记录，不携带账号或发送凭据。
const route = useRoute()
const tab = ref(route.query.tab === 'robot' ? 'robot' : 'station')
watch(() => route.query.tab, value => { tab.value = value === 'robot' ? 'robot' : 'station' })
const outbox = ref<any>({ items: [], mode: 'mock' })
const session = ref<any>(null)
const writeLocked = computed(() => identityBlocked.value || !!session.value?.impersonation || !!session.value?.user?.operationDisabled)
const loading = ref(true)
const error = ref('')
const saving = ref(false)
const opening = ref(false)
let loadVersion = 0
let identityVersion = 0
let navigationVersion = 0
let outboxVersion = 0
let refreshTimer: ReturnType<typeof setInterval> | undefined
let backgroundLoading = false
// 业务变更立即同步；正常轮询兜底，避免同时与角标和系统提醒重复高频查询。
const notificationRefreshInterval = 10_000
let nextBackgroundRefresh = 0, backgroundFailures = 0
const message = (cause: unknown) => cause instanceof Error ? cause.message : '操作失败，请稍后重试'
// 仅列出会写入站内通知表的稳定事件值。筛选仍向服务端传稳定 key，展示文案可随语言切换。
// 未知或未来事件在列表中保留原始 key，不能因前端版本较旧而让通知不可读。
const eventNames: Record<string, string> = {
  'requirement.assigned': '需求分配', 'requirement.owner_assigned': '需求负责人分配', 'requirement.role_assigned': '需求职能分配',
  'requirement.mentioned': '评论提及', 'requirement.description_mentioned': '正文提及', 'requirement.remarks_mentioned': '备注提及', 'requirement.replied': '需求评论回复',
  'requirement.status_changed': '需求状态变更', 'requirement.updated': '需求更新', 'requirement.backend_completed': '后端完成交接', 'requirement.frontend_completed': '前端完成交接', 'requirement.created': '创建需求',
  'defect.created': '创建缺陷', 'defect.assigned': '缺陷分配', 'defect.assignee_changed': '缺陷处理人变更', 'defect.verification': '缺陷验证', 'defect.verifier_assigned': '指定缺陷验证人', 'defect.verifier_changed': '缺陷验证人变更',
  'defect.status_changed': '缺陷状态变更', 'defect.created_from_execution': '测试失败生成缺陷', 'defect.mentioned': '缺陷评论提及', 'defect.replied': '缺陷评论回复',
  'sprint.started': '迭代开始', 'sprint.completed': '迭代完成', 'sprint.created': '创建迭代', 'sprint.status_changed': '迭代状态变更',
  'test.failed': '测试失败', 'test_case.owner_assigned': '测试用例负责人分配', 'test_case.owner_changed': '测试用例负责人变更', 'test_case.mentioned': '测试用例评论提及', 'test_case.replied': '测试用例评论回复',
  'test_plan.owner_assigned': '测试计划负责人分配', 'test_plan.owner_changed': '测试计划负责人变更', 'test_plan.executor_assigned': '测试计划执行人分配', 'test_plan.executor_changed': '测试计划执行人变更', 'test_plan.mentioned': '测试计划评论提及', 'test_plan.replied': '测试计划评论回复',
  'test_execution.mentioned': '测试执行评论提及', 'test_execution.replied': '测试执行评论回复', 'automation.requirement_status_changed': '自动化规则通知', 'user.password_changed': '密码已更新',
}
// 演示种子和旧记录仍可能包含 eventNames 中的历史 key；它们可以正常显示，但不应
// 伪装成当前用户能够触发的筛选项。此数组只包含后端当前业务路径会写入的事件。
const filterEventTypes = [
  'requirement.assigned', 'requirement.owner_assigned', 'requirement.role_assigned', 'requirement.mentioned', 'requirement.description_mentioned', 'requirement.remarks_mentioned', 'requirement.replied', 'requirement.status_changed', 'requirement.updated', 'requirement.backend_completed', 'requirement.frontend_completed',
  'defect.assigned', 'defect.assignee_changed', 'defect.verifier_assigned', 'defect.verifier_changed', 'defect.mentioned', 'defect.replied', 'defect.status_changed', 'defect.created_from_execution',
  'sprint.status_changed', 'sprint.completed',
  'test.failed', 'test_case.owner_assigned', 'test_case.owner_changed', 'test_case.mentioned', 'test_case.replied', 'test_plan.owner_assigned', 'test_plan.owner_changed', 'test_plan.executor_assigned', 'test_plan.executor_changed', 'test_plan.mentioned', 'test_plan.replied', 'test_execution.mentioned', 'test_execution.replied',
  'automation.requirement_status_changed', 'user.password_changed',
] as const
const subjectNames: Record<string, string> = { requirement: '需求', defect: '缺陷', sprint: '迭代', test_case: '测试用例', test_plan: '测试计划', test_execution: '测试执行' }
function noticeKind(item: InboxNotice) {
  const event = String(item.eventType || '')
  if (event.startsWith('defect.')) return { key: 'defect', label: '缺陷' }
  if (event.startsWith('sprint.')) return { key: 'sprint', label: '迭代' }
  if (event.startsWith('test')) return { key: 'test', label: '测试' }
  if (event.startsWith('requirement.')) return { key: 'requirement', label: '需求' }
  return { key: 'system', label: '系统' }
}
const deliveryNames: Record<string, string> = { queued: '待发送', pending: '待发送', sent: '已发送', failed: '发送失败', mock: '模拟发送' }
const readOptions = computed(() => [
  { value: '', label: t('全部消息') }, { value: 'unread', label: t('仅未读') }, { value: 'read', label: t('仅已读') },
])
const eventOptions = computed(() => [
  { value: '', label: t('全部事件') },
  ...filterEventTypes.map(value => ({ value, label: t(eventNames[value]) })),
])
const projectOptions = computed(() => [
  { value: '', label: t('全部项目') },
  ...projects.value.filter(item => item?.id != null).map(item => ({ value: String(item.id), label: String(item.name || item.id) })),
])
// unread 是当前账号在所有仍有权限项目中的未读总数；items.length 仅代表本次最多 40 条返回结果。
// 绝不相互推导，以免筛选后把“当前列表为空”误报为“没有未读通知”。
const unreadSummary = computed(() => t('未读 {count} 条', { count: unread.value }))
const resultSummary = computed(() => t('当前列表 {count} 条', { count: items.value.length }))

async function load(background = false) {
  if (identityBlocked.value || background && backgroundLoading) return
  if (background) backgroundLoading = true
  const version = ++loadVersion
  if (!background) { loading.value = true; error.value = '' }
  try {
    const params = new URLSearchParams({ read: read.value, project: project.value, eventType: eventType.value, group: group.value, offset: String(offset.value) })
    const data = await api<any>(`/notifications?${params}`)
    if (version !== loadVersion) return
    items.value = Array.isArray(data?.items) ? data.items : []
    total.value = Number(data?.total) || 0
    selectedIDs.value = selectedIDs.value.filter(id => items.value.some(item => item.id === id))
    hasMore.value = data?.hasMore === true
    groupUnread.value = data?.groupUnread || {}
    backgroundFailures = 0; nextBackgroundRefresh = Date.now() + notificationRefreshInterval
    const nextUnread = Number(data?.unread)
    unread.value = Number.isFinite(nextUnread) ? Math.max(0, Math.floor(nextUnread)) : 0
    window.dispatchEvent(new CustomEvent('devflow-unread', { detail: unread.value }))
    // 标记后筛选结果可能少一整页，自动退回仍存在的最后一页。
    if (!items.value.length && offset.value > 0 && offset.value >= total.value) {
      selectedIDs.value = []
      offset.value = Math.max(0, Math.floor((total.value - 1) / 40) * 40)
      await load()
    }
  } catch (cause) { if (version === loadVersion) { backgroundFailures++; nextBackgroundRefresh = Date.now() + Math.min(60_000, notificationRefreshInterval * 2 ** Math.min(backgroundFailures, 3)); if (!background) error.value = message(cause) } }
  finally { if (background) backgroundLoading = false; if (version === loadVersion && !background) loading.value = false }
}

async function loadOutbox() { const version = ++outboxVersion; error.value = ''; try { const data = await api('/notifications/outbox'); if (version === outboxVersion) outbox.value = data } catch (cause) { if (version === outboxVersion) error.value = message(cause) } }
async function action(run: () => Promise<unknown>) { if (saving.value) return false; saving.value = true; error.value = ''; try { await run(); return true } catch (cause) { error.value = message(cause); return false } finally { saving.value = false } }
function toggleSelection(id: number) {
  if (loading.value || saving.value || writeLocked.value || !pageIDs.value.includes(id)) return
  selectedIDs.value = selectedIDs.value.includes(id) ? selectedIDs.value.filter(value => value !== id) : [...selectedIDs.value, id]
}
function togglePageSelection() {
  if (loading.value || saving.value || writeLocked.value) return
  selectedIDs.value = allPageSelected.value ? [] : [...pageIDs.value]
}
async function mutateState(path: string, method: string, value: boolean, ids: number[] | null, refresh = true) {
  if (writeLocked.value) return false
  const identity = identityVersion
  const succeeded = await action(async () => {
    ++loadVersion // 在途轮询不得用修改前的状态覆盖已提交的状态。
    stateNotice.value = ''
    const result = await api<NoticeStateResult>(path, { method, ...(ids === null ? {} : { body: JSON.stringify(path.endsWith('/bulk-read') ? { ids, read: value } : { read: value }) }) })
    if (identity !== identityVersion) return
    selectedIDs.value = []
    items.value = items.value.map(item => ids === null || ids.includes(item.id) ? { ...item, readAt: value ? item.readAt || new Date().toISOString() : '' } : item)
    const nextUnread = Number(result?.unread)
    if (Number.isFinite(nextUnread)) {
      unread.value = Math.max(0, Math.floor(nextUnread))
      window.dispatchEvent(new CustomEvent('devflow-unread', { detail: unread.value }))
    }
    stateNotice.value = t('已更新 {count} 条通知', { count: Number(result?.updated) || 0 })
    window.dispatchEvent(new CustomEvent('devflow-notifications-changed'))
    if (refresh) await load()
  })
  return succeeded && identity === identityVersion
}
async function mark(item: InboxNotice, value = true) { return mutateState(`/notifications/${item.id}`, 'PATCH', value, [item.id]) }
async function all() { await mutateState('/notifications/read-all', 'POST', true, null) }
async function markSelected(value: boolean) {
  const ids = selectedIDs.value.filter(id => pageIDs.value.includes(id))
  if (!ids.length || loading.value || saving.value) return
  await mutateState('/notifications/bulk-read', 'POST', value, ids)
}
function notificationTarget(item: any): { projectID: string; url: string } | null {
  const projectID = typeof item?.projectId === 'string' ? item.projectId.trim() : ''
  const rawURL = typeof item?.url === 'string' ? item.url.trim() : ''
  if (!projectID || !rawURL) return null
  // 通知记录在点击前也可能因退项目失效。只接受同源相对地址，避免历史脏数据把用户带往外部站点。
  const origin = typeof location !== 'undefined' && typeof location.origin === 'string' && location.origin ? location.origin : 'http://devflow.local'
  try {
    const target = new URL(rawURL, origin)
    if (target.origin !== origin || !target.pathname.startsWith('/')) return null
    return { projectID, url: `${target.pathname}${target.search}${target.hash}` }
  } catch { return null }
}
async function open(item: any) {
  if (opening.value || saving.value || identityBlocked.value) return
  const identity = identityVersion
  const navigation = navigationVersion
  const target = notificationTarget(item)
  if (!target) { error.value = '通知链接无效，请刷新后重试'; return }
  opening.value = true
  try {
    // 先复验项目权限，再记已读；通知标记失败不阻止查看仍有权访问的详情。
    // 目标详情接口仍独立鉴权；账号切换或撤权绝不能由本地状态放行。
    const entered = await action(() => api(`/projects/${encodeURIComponent(target.projectID)}/visit`, {
      method: 'POST', headers: { 'X-TaskLoom-Project': target.projectID },
    }))
    if (!entered || identity !== identityVersion || navigation !== navigationVersion || identityBlocked.value) return
    if (!writeLocked.value && !item.readAt) {
      // 即将离开列表，不再等待整页重查，减少一次请求与页面闪动。
      await mutateState(`/notifications/${item.id}`, 'PATCH', true, [item.id], false)
    }
    if (identity !== identityVersion || navigation !== navigationVersion || identityBlocked.value) return
    localStorage.setItem('devflow-project', target.projectID)
    location.href = target.url
  } finally { opening.value = false }
}
async function retry(item: any) { await action(async () => { await api(`/notifications/outbox/${item.id}/retry`, { method: 'POST' }); await loadOutbox() }) }

function refreshVisible(event?: Event) {
  const changed = event?.type === 'devflow-notifications-changed' || event?.type === 'devflow-unread' && Number((event as CustomEvent).detail) !== unread.value
  if (document.visibilityState !== 'hidden' && tab.value === 'station' && !loading.value && !saving.value && (changed || Date.now() >= nextBackgroundRefresh)) void load(true)
}
onMounted(() => window.addEventListener('devflow-unread', refreshVisible))
onBeforeUnmount(() => window.removeEventListener('devflow-unread', refreshVisible))
watch([read, project, eventType, group], () => { selectedIDs.value = []; stateNotice.value = ''; offset.value = 0; void load() }, { flush: 'sync' })
watch(tab, value => { selectedIDs.value = []; if (value === 'outbox') loadOutbox() })
watch(locale, () => { stateNotice.value = ''; void load(); if (tab.value === 'outbox') void loadOutbox() })
function projectChanged() { navigationVersion++; selectedIDs.value = []; void load(); if (tab.value === 'outbox') void loadOutbox() }
const identityEvents = ['devflow-auth-session-ended', 'devflow-auth-expired', 'devflow-identity-changed', 'devflow-account-disabled']
function invalidateIdentity() { identityVersion++; loadVersion++; outboxVersion++; identityBlocked.value = true; selectedIDs.value = []; items.value = []; groupUnread.value = {}; unread.value = 0; stateNotice.value = ''; loading.value = false }
onMounted(async () => { identityEvents.forEach(event => window.addEventListener(event, invalidateIdentity)); window.addEventListener('devflow-project-changed', projectChanged); window.addEventListener('focus', refreshVisible); document.addEventListener('visibilitychange', refreshVisible); window.addEventListener('devflow-notifications-changed', refreshVisible); refreshTimer = setInterval(refreshVisible, notificationRefreshInterval); await load(); if (identityBlocked.value) return; try { const [p, s] = await Promise.all([api<any>('/projects'), api('/session')]); if (!identityBlocked.value) { projects.value = p.items || []; session.value = s } } catch (cause) { if (!identityBlocked.value) error.value = message(cause) } })
onBeforeUnmount(() => { invalidateIdentity(); identityEvents.forEach(event => window.removeEventListener(event, invalidateIdentity)); clearInterval(refreshTimer); window.removeEventListener('focus', refreshVisible); if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', refreshVisible); window.removeEventListener('devflow-notifications-changed', refreshVisible); window.removeEventListener('devflow-project-changed', projectChanged) })
</script>

<template>
  <div class="module-page notifications-page">
    <div class="page-heading compact-page-heading"><div><h1>{{ t("通知中心") }}</h1><details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{ t("站内消息按账号隔离；外发队列仅向管理员提供运维视图。") }}</p></details></div><button v-if="tab === 'station' && unread" class="btn" :disabled="saving || loading || writeLocked" :title="t('将所有有权限项目的未读通知设为已读，不受当前筛选限制')" @click="all">{{ t("全部标为已读") }}</button></div>
    <p v-if="error" class="inline-notice" role="alert">{{ t(error) }} <button class="link" @click="tab === 'outbox' ? loadOutbox() : load()">{{ t("重试") }}</button></p><nav class="module-tabs"><button :class="{ active: tab === 'station' }" @click="tab = 'station'">{{ t("站内通知") }} <small>{{ unreadSummary }}</small></button><button v-if="['tenant_admin', 'project_admin'].includes(session?.user?.role)" :class="{ active: tab === 'outbox' }" @click="tab = 'outbox'">{{ t("外发队列") }}</button><button :class="{active:tab==='robot'}" @click="tab='robot'">{{t('机器人投递')}}</button></nav>
    <DesktopNotifications />
    <template v-if="tab === 'station'">
      <p v-if="session?.impersonation" class="inline-notice">{{ t('代访问期间仅查看通知，不修改已读状态') }}</p>
      <nav class="notice-groups" :aria-label="t('消息分类')"><button v-for="item in groups" :key="item.key" type="button" :aria-pressed="group===item.key" :class="{active:group===item.key}" @click="chooseGroup(item.key)">{{t(item.name)}}<small v-if="item.key && groupUnread[item.key]" :aria-label="t('未读')">{{groupUnread[item.key]}}</small></button><span>{{t('分类数字为所选项目的未读数；已读不代表交接完成')}}</span></nav>
      <div class="toolbar module-toolbar notice-filters"><AppSelect class="notification-filter-select" :model-value="read" :options="readOptions" :label="t('已读状态')" :disabled="saving" @update:model-value="read = String($event)"/><button type="button" class="btn mobile-filter-toggle" :aria-expanded="filtersExpanded" aria-controls="notice-advanced-filters" @click="filtersExpanded=!filtersExpanded">{{t('筛选')}}<span v-if="filterCount"> · {{filterCount}}</span><span aria-hidden="true">{{filtersExpanded?'⌃':'⌄'}}</span></button><div id="notice-advanced-filters" class="mobile-advanced-filters" :class="{'is-expanded':filtersExpanded}"><AppSelect class="notification-filter-select" :model-value="eventType" :options="eventOptions" :label="t('全部事件')" :disabled="saving" @update:model-value="eventType = String($event)"/><AppSelect class="notification-filter-select" :model-value="project" :options="projectOptions" :label="t('全部项目')" :disabled="saving" @update:model-value="project = String($event)"/><button v-if="filterCount" type="button" class="btn" :disabled="saving" @click="eventType='';project=''">{{t('重置筛选')}}</button></div><span class="result-count" aria-live="polite">{{ resultSummary }}</span><button class="btn compact" :disabled="loading || saving" @click="load()">{{ t('刷新') }}</button></div>
      <div v-if="items.length" class="notice-bulk-bar" :aria-label="t('批量通知操作')">
        <label class="notice-page-select"><input type="checkbox" :checked="allPageSelected" :indeterminate="somePageSelected" :disabled="loading || saving || writeLocked" @change="togglePageSelection" />{{ t('全选当前页') }}</label>
        <span class="notice-selection-count" aria-live="polite">{{ selectedSummary }}</span>
        <div class="notice-bulk-actions"><button type="button" class="btn compact" :disabled="!selectedIDs.length || loading || saving || writeLocked" @click="markSelected(true)">{{ t('设为已读') }}</button><button type="button" class="btn compact" :disabled="!selectedIDs.length || loading || saving || writeLocked" @click="markSelected(false)">{{ t('设为未读') }}</button><button v-if="selectedIDs.length" type="button" class="btn compact" :disabled="saving" @click="selectedIDs=[]">{{ t('取消选择') }}</button></div>
        <small>{{ t('仅操作当前页所选通知；切换筛选或分页会清空选择') }}</small>
      </div>
      <p v-if="stateNotice" class="notice-state-result" role="status">{{ stateNotice }}</p>
      <div v-if="loading" :class="items.length?'notice-refresh-status':'state'" role="status"><span class="spinner"></span>{{ t(items.length ? '正在更新通知…' : '正在载入通知…') }}</div>
      <div v-if="!loading && !error && !items.length" class="empty-work"><div>◷</div><h2>{{ t("暂无通知") }}</h2><p>{{ t("新的分配、状态变化和提及会出现在这里。") }}</p></div>
      <div v-if="items.length" class="notification-list" :aria-busy="loading" :inert="loading"><article v-for="x in items" :key="x.id" :class="{ unread: !x.readAt, read: !!x.readAt, selected: selectedIDs.includes(x.id) }" @click="open(x)"><span class="notice-dot" aria-hidden="true"></span><label class="notice-check" @click.stop><input type="checkbox" :checked="selectedIDs.includes(x.id)" :disabled="loading || saving || writeLocked" :aria-label="t('选择通知：{title}', { title: x.title })" @change="toggleSelection(x.id)" /></label><div class="notice-content" tabindex="0" role="link" @keydown.enter.stop.prevent="open(x)"><header><span :class="['notice-kind','notice-kind-'+noticeKind(x).key]">{{t(noticeKind(x).label)}}</span><span class="notice-read-status">{{ x.readAt ? t('已读') : t('未读') }}</span><b>{{ x.title }}</b><time>{{ formatDate(x.createdAt) }}</time></header><p>{{ notificationBody(x,t) }}</p><small>{{ t(eventNames[x.eventType] || x.eventType) }} · {{ x.projectName }} · {{ x.actor || t("系统事件") }}</small></div><button type="button" class="notice-state-action" :disabled="saving || loading || writeLocked" @click.stop="mark(x, !x.readAt)">{{ x.readAt ? t("设为未读") : t("设为已读") }}</button></article></div>
      <div v-if="total || offset" class="notice-pagination"><span>{{t('共 {count} 条',{count:total})}} · {{t('第 {count} 页',{count:Math.floor(offset/40)+1})}}</span><button type="button" class="btn compact" :disabled="!offset||loading||saving" @click="changePage(-1)">{{t('上一页')}}</button><button type="button" class="btn compact" :disabled="!hasMore||loading||saving" @click="changePage(1)">{{t('下一页')}}</button></div>
    </template>
    <UserWecomWebhook v-else-if="tab==='robot'" read-only />
    <template v-else>
      <div class="outbox-note"><b>{{ t(outbox.mode === 'mock' ? "Mock 外发模式" : "企业微信外发模式") }}</b><span>{{ t("队列与站内通知分别存储；失败项可安全重试。") }}</span></div>
      <div class="card table-card"><table><thead><tr><th>{{ t("事件") }}</th><th>{{ t("主题") }}</th><th>{{ t("渠道") }}</th><th>{{ t("状态") }}</th><th>{{ t("重试") }}</th><th>{{ t("失败原因") }}</th><th>{{ t("更新时间") }}</th><th>{{ t("操作") }}</th></tr></thead><tbody><tr v-if="!outbox.items.length"><td colspan="8" class="empty-mini">{{ t("暂无外发记录") }}</td></tr><tr v-for="x in outbox.items" :key="x.id"><td><b>{{ t(eventNames[x.eventType] || x.eventType) }}</b></td><td>{{ t(subjectNames[x.subjectType] || x.subjectType) }} #{{ x.subjectId }}</td><td>{{ t(x.channel === 'wecom' ? '企业微信' : x.channel === 'mock' ? '模拟渠道' : x.channel) }}</td><td><span class="status">{{ t(deliveryNames[x.status] || x.status) }}</span></td><td>{{ x.retryCount }}</td><td>{{ x.lastError || '—' }}</td><td>{{ formatDate(x.updatedAt || x.createdAt) }}</td><td><button v-if="x.status === 'failed'" class="link" :disabled="saving" @click="retry(x)">{{ t("重试") }}</button><span v-else>—</span></td></tr></tbody></table></div>
    </template>
  </div>
</template>

<style scoped>
.notice-refresh-status{display:flex;align-items:center;gap:8px;padding:10px 0;color:var(--muted);font-size:12px}.notification-list[aria-busy="true"]{opacity:.65}.notification-list .notice-content p{white-space:pre-line}.notification-list .notice-content header>.notice-read-status{flex:none;margin-right:0}
.notice-kind{display:inline-flex;align-items:center;justify-content:center;flex:none;min-height:22px;padding:2px 7px;border:1px solid transparent;border-radius:999px;font-size:11px;font-weight:700;line-height:1.2;letter-spacing:.01em;white-space:nowrap}.notice-kind-requirement{color:#1d4ed8;background:#eff6ff;border-color:#bfdbfe}.notice-kind-defect{color:#c2410c;background:#fff7ed;border-color:#fed7aa}.notice-kind-sprint{color:#6d28d9;background:#f5f3ff;border-color:#ddd6fe}.notice-kind-test{color:#0f766e;background:#f0fdfa;border-color:#99f6e4}.notice-kind-system{color:#475569;background:#f8fafc;border-color:#e2e8f0}.notification-list .notice-content header{display:flex;align-items:center;gap:7px;min-width:0}.notification-list .notice-content header>b{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.notification-list .notice-content header time{margin-left:auto;flex:none;white-space:nowrap;font-variant-numeric:tabular-nums}
.notice-bulk-bar{display:flex;align-items:center;flex-wrap:wrap;gap:8px 12px;padding:10px 12px;background:var(--primary-soft);border:1px solid var(--line);border-radius:8px;margin-bottom:10px;font-size:13px}.notice-page-select{display:flex;align-items:center;gap:8px;cursor:pointer;white-space:nowrap}.notice-bulk-bar input,.notice-check input{width:17px;height:17px;flex:none;margin:0;accent-color:var(--primary);cursor:pointer}.notice-selection-count{color:var(--muted);white-space:nowrap}.notice-bulk-actions{display:flex;flex-wrap:wrap;gap:6px;margin-left:auto}.notice-bulk-actions button{white-space:nowrap;min-height:32px}.notice-bulk-bar>small{flex-basis:100%;color:var(--muted);font-size:12px;line-height:1.5}.notice-state-result{font-size:13px;color:var(--primary);margin:8px 0}.notification-list article{grid-template-columns:8px 38px minmax(0,1fr) auto}.notice-check{display:flex;align-items:flex-start;justify-content:center;padding-top:3px;min-height:28px;cursor:pointer}.notification-list .notice-content{min-width:0;border-radius:6px}.notification-list .notice-content:focus-visible{outline:2px solid var(--ring);outline-offset:3px}.notification-list article.read{background:var(--surface,#fff)}.notification-list article.unread{background:var(--primary-soft,#f1efff);box-shadow:inset 3px 0 0 var(--primary)}.notification-list article.selected{outline:1px solid var(--primary);outline-offset:-1px}.notification-list article.read b{font-weight:500;color:var(--muted)}.notification-list article.unread b{font-weight:700;color:var(--text)}.notification-list article>.notice-state-action{align-self:center;white-space:nowrap;line-height:1.3;min-height:32px;padding:6px 10px;font-size:12px;border:1px solid var(--line);border-radius:6px;color:var(--primary);background:var(--surface,#fff);flex-shrink:0}.notification-list article>.notice-state-action:disabled{opacity:.5;cursor:not-allowed}.notice-read-status{display:inline-block;padding:1px 6px;margin-right:7px;border-radius:4px;background:var(--line);color:var(--muted);font-size:11px;white-space:nowrap}.unread .notice-read-status{background:var(--primary);color:#fff}.notification-list .notice-content header{flex-wrap:wrap;gap:5px 16px}.notification-list .notice-content header b{overflow-wrap:anywhere}.notification-list .notice-content time{white-space:nowrap}.notification-list .notice-content p,.notification-list .notice-content small{overflow-wrap:anywhere}
.notice-groups{display:flex;align-items:center;flex-wrap:wrap;gap:6px;padding:8px 0;border-bottom:1px solid var(--line)}.notice-groups button{display:flex;gap:6px;align-items:center;min-height:32px;padding:5px 10px;border:1px solid transparent;border-radius:6px;background:transparent;color:var(--muted);font:inherit;font-size:13px;cursor:pointer;white-space:nowrap}.notice-groups button.active{background:var(--primary-soft);color:var(--primary);border-color:var(--line)}.notice-groups small{font-size:12px;font-variant-numeric:tabular-nums}.notice-groups>span{flex:1 0 100%;min-width:0;padding-top:2px;font-size:12px;color:var(--muted);line-height:1.5;overflow-wrap:anywhere}.notice-filters{display:flex;align-items:center;gap:8px;min-width:0}.notice-filters :deep(.notification-filter-select){flex:0 1 210px;min-width:150px;max-width:250px}.notice-filters .result-count{margin-left:auto;min-width:0;white-space:nowrap}.notice-pagination{display:flex;align-items:center;justify-content:flex-end;gap:8px;flex-wrap:wrap;padding:12px 0;font-size:13px;color:var(--muted)}
@media(max-width:820px){
 .notice-bulk-actions{margin-left:0;flex-basis:100%}.notice-bulk-actions button{min-height:36px}.notice-check{padding-top:3px}.notification-list article>.notice-state-action{grid-column:3;justify-self:start;min-height:36px;padding:7px 10px}
 .notifications-page{min-width:0}.page-heading{flex-wrap:wrap;gap:12px}.module-tabs{max-width:100%;overflow:auto;white-space:nowrap}.module-tabs button{flex:none;min-height:40px}.module-toolbar{flex-wrap:wrap;padding:10px 0;gap:8px}.notice-filters :deep(.notification-filter-select),.module-toolbar :deep(.notification-filter-select){flex:1 1 135px;min-width:0;max-width:none}.notice-filters .result-count,.module-toolbar .result-count{margin-left:0}
 .notification-list article{grid-template-columns:6px 28px minmax(0,1fr);gap:9px;padding:14px 12px}.notification-list article>.avatar{width:28px;height:28px;font-size:11px}.notification-list article>div{min-width:0}.notification-list article>div header{flex-direction:column;gap:5px}.notification-list b,.notification-list p,.notification-list small{overflow-wrap:anywhere;line-height:1.7}.notification-list article>button{grid-column:3;justify-self:end;min-height:40px;padding:8px 4px}
 .outbox-note{flex-wrap:wrap;gap:7px;line-height:1.7}.table-card{max-width:100%;overflow:auto}.table-card table{min-width:740px}
 .notification-list .notice-content header{flex-direction:row;align-items:center;gap:5px 7px}.notification-list .notice-content header>b{flex:1 0 100%;white-space:normal;line-height:1.5}.notification-list .notice-content header time{margin-left:0;font-size:11px}
}
</style>
