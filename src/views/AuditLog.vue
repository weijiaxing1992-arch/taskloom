<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { formatDate, locale, t } from '../i18n'
import AppSelect from '../components/AppSelect.vue'

type AuditChange = { field: string; before: string; after: string }
type AuditItem = {
  id: number
  actorId: string
  actorName: string
  objectType: string
  objectId: string
  action: string
  createdAt: string
  changes: AuditChange[]
  changesTrimmed: boolean
}

const items = ref<AuditItem[]>([])
const loading = ref(true)
const loadingMore = ref(false)
const error = ref('')
const nextCursor = ref('')
const page = ref(1)
const objectType = ref('')
const action = ref('')
const actorId = ref('')
const from = ref('')
const to = ref('')
let loadVersion = 0
let disposed = false
let controller: AbortController | undefined

const objectNames: Record<string, string> = {
  requirement: '需求', defect: '缺陷', sprint: '迭代', test_case: '测试用例', test_plan: '测试计划', test_execution: '测试执行',
  project: '项目', user: '成员', automation_rule: '自动化规则', field_definition: '自定义字段', ai_settings: 'AI 服务配置',
  requirement_category: '需求分类', requirement_status: '需求状态', requirement_workflow: '需求工作流',
}
const objectOptions = computed(() => [
  { value: '', label: t('全部对象') },
  ...Object.entries(objectNames).map(([value, label]) => ({ value, label: t(label) })),
])

function objectLabel(value: string) { return t(objectNames[value] || value) }
function actionLabel(value: string) { return value || '—' }
function actorLabel(item: AuditItem) { return item.actorName || item.actorId || t('系统事件') }
function objectLink(item: AuditItem) {
  if (!/^[1-9]\d*$/.test(item.objectId)) return ''
  const id = encodeURIComponent(item.objectId)
  const paths: Record<string, string> = {
    requirement: `/requirements?req=${id}`,
    defect: `/defects?bug=${id}`,
    sprint: `/iterations?sprint=${id}`,
    test_case: `/tests?tab=cases&case=${id}`,
    test_plan: `/tests?tab=plans&plan=${id}`,
    test_execution: `/tests?tab=executions&execution=${id}`,
  }
  return paths[item.objectType] || ''
}

function query(cursor = '') {
  const params = new URLSearchParams({ limit: '25' })
  if (objectType.value) params.set('objectType', objectType.value)
  if (action.value.trim()) params.set('action', action.value.trim())
  if (actorId.value.trim()) params.set('actorId', actorId.value.trim())
  if (from.value) params.set('from', from.value)
  if (to.value) params.set('to', to.value)
  if (cursor) params.set('cursor', cursor)
  return params
}

async function load(append = false) {
  if (append && (!nextCursor.value || loadingMore.value)) return
  const version = ++loadVersion
  // 只读请求也要中止旧连接，避免快速切换项目或筛选后让无用响应占住浏览器与服务端槽位。
  controller?.abort()
  const request = new AbortController()
  controller = request
  if (append) loadingMore.value = true
  else { loading.value = true; error.value = ''; nextCursor.value = ''; page.value = 1 }
  try {
    const data = await api<{ items: AuditItem[]; nextCursor?: string }>(`/audit-logs?${query(append ? nextCursor.value : '')}`, { signal: request.signal })
    if (disposed || version !== loadVersion) return
    items.value = append ? [...items.value, ...(data.items || [])] : (data.items || [])
    nextCursor.value = data.nextCursor || ''
    page.value = append ? page.value + 1 : 1
  } catch (cause) {
    if (!disposed && version === loadVersion) error.value = cause instanceof Error ? cause.message : t('操作失败，请稍后重试')
  } finally {
    if (controller === request) controller = undefined
    if (!disposed && version === loadVersion) { loading.value = false; loadingMore.value = false }
  }
}

function submitFilters() { nextCursor.value = ''; void load() }
function resetFilters() {
  objectType.value = ''; action.value = ''; actorId.value = ''; from.value = ''; to.value = ''
  nextCursor.value = ''
  void load()
}
function projectChanged() { controller?.abort(); nextCursor.value = ''; void load() }
function identityChanged() { controller?.abort(); disposed = true; loadVersion++; items.value = []; nextCursor.value = ''; loading.value = false; error.value = t('账号身份已在其他页面切换，请刷新后继续') }

watch(locale, () => { if (!disposed) void load() })
onMounted(() => {
  window.addEventListener('devflow-project-changed', projectChanged)
  window.addEventListener('devflow-identity-changed', identityChanged)
  window.addEventListener('devflow-auth-expired', identityChanged)
  void load()
})
onBeforeUnmount(() => {
  controller?.abort()
  disposed = true
  loadVersion++
  window.removeEventListener('devflow-project-changed', projectChanged)
  window.removeEventListener('devflow-identity-changed', identityChanged)
  window.removeEventListener('devflow-auth-expired', identityChanged)
})
</script>

<template>
  <section class="module-page audit-page">
    <header class="page-heading audit-heading compact-page-heading">
      <div>

        <h1>{{ t('变更历史') }}</h1>
        <details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{ t('查看当前项目的重要配置与协作操作；敏感信息始终脱敏。') }}</p></details>
      </div>
      <button type="button" class="btn" :disabled="loading || loadingMore" @click="load()">{{ t('刷新') }}</button>
    </header>

    <form class="audit-filters card" @submit.prevent="submitFilters">
      <div class="audit-filter-title"><b>{{ t('筛选条件') }}</b><small>{{ t('当前项目管理员可查看；记录按时间倒序并以游标稳定翻页。') }}</small></div>
      <div class="audit-filter-grid">
        <AppSelect v-model="objectType" :options="objectOptions" :label="t('对象类型')" class="audit-object-select" />
        <label><span>{{ t('操作动作') }}</span><input v-model="action" :placeholder="t('例如 requirement.updated')" maxlength="100"></label>
        <label><span>{{ t('操作人 ID') }}</span><input v-model="actorId" :placeholder="t('例如 u_admin')" maxlength="100"></label>
        <label><span>{{ t('开始日期') }}</span><input v-model="from" type="date"></label>
        <label><span>{{ t('结束日期') }}</span><input v-model="to" type="date"></label>
        <div class="audit-filter-actions"><button type="submit" class="btn primary" :disabled="loading || loadingMore">{{ t('查询') }}</button><button type="button" class="btn" :disabled="loading || loadingMore" @click="resetFilters">{{ t('清空筛选') }}</button></div>
      </div>
    </form>

    <p v-if="error" class="inline-notice audit-error" role="alert">{{ t(error) }} <button type="button" class="link" @click="load()">{{ t('重试') }}</button></p>
    <div v-if="loading" class="state"><span class="spinner"></span>{{ t('正在读取审计记录…') }}</div>
    <div v-else-if="!items.length" class="empty-work audit-empty"><div>◷</div><h2>{{ t('暂无符合条件的审计记录') }}</h2><p>{{ t('调整筛选条件，或稍后再试。') }}</p></div>
    <div v-else class="audit-stream">
      <p class="audit-count">{{ t('已加载 {count} 条', { count: items.length }) }} · {{ t('第 {page} 页', { page }) }}</p>
      <article v-for="item in items" :key="item.id" class="audit-entry">
        <div class="audit-entry-head">
          <div class="audit-identity"><span class="audit-avatar">{{ actorLabel(item).slice(0, 1) }}</span><div><b>{{ actorLabel(item) }}</b><small>{{ item.actorId || t('系统事件') }}</small></div></div>
          <time>{{ formatDate(item.createdAt) }}</time>
        </div>
        <div class="audit-event"><span class="audit-object">{{ objectLabel(item.objectType) }}</span><span class="audit-object-id">#{{ item.objectId }}</span><b>{{ actionLabel(item.action) }}</b><router-link v-if="objectLink(item)" class="audit-open-link" :to="objectLink(item)" :aria-label="t('打开关联对象')">↗</router-link></div>
        <details class="audit-diff">
          <summary>{{ t('查看 {count} 项变更', { count: item.changes.length }) }}</summary>
          <dl>
            <div v-for="change in item.changes" :key="`${item.id}-${change.field}`">
              <dt>{{ t(change.field) }}</dt>
              <dd><span><small>{{ t('变更前') }}</small>{{ t(change.before) }}</span><span><small>{{ t('变更后') }}</small>{{ t(change.after) }}</span></dd>
            </div>
          </dl>
          <p v-if="item.changesTrimmed" class="audit-trimmed">{{ t('更多变更未展示') }}</p>
        </details>
      </article>
      <div class="audit-more"><button v-if="nextCursor" type="button" class="btn" :disabled="loadingMore" @click="load(true)">{{ loadingMore ? t('正在读取审计记录…') : t('继续加载') }}</button><span v-else>{{ t('没有更多记录') }}</span></div>
    </div>
  </section>
</template>

<style scoped>
.audit-page{max-width:1240px;margin:0 auto;padding-bottom:42px}.audit-heading{align-items:flex-start}.audit-heading>.btn{margin-top:4px}.audit-filters{display:grid;gap:16px;padding:18px 20px;margin-bottom:16px;border:1px solid var(--border);background:var(--card)}.audit-filter-title{display:grid;gap:4px}.audit-filter-title b{font-size:14px;color:var(--foreground)}.audit-filter-title small{font-size:12px;line-height:1.65;color:var(--muted-foreground)}.audit-filter-grid{display:grid;grid-template-columns:minmax(138px,.8fr) minmax(170px,1.25fr) minmax(150px,1fr) minmax(140px,.82fr) minmax(140px,.82fr) auto;gap:10px;align-items:end}.audit-filter-grid label{display:grid;gap:6px;min-width:0;font-size:12px;color:var(--muted-foreground)}.audit-filter-grid input{width:100%;min-width:0;height:36px;padding:7px 10px;border:1px solid var(--input);border-radius:8px;background:var(--background);color:var(--foreground);font:inherit;font-size:12px}.audit-filter-grid input:focus-visible{outline:2px solid color-mix(in srgb,var(--ring) 55%,transparent);outline-offset:1px;border-color:var(--ring)}.audit-object-select{width:100%;min-width:0}.audit-object-select :deep(.app-select-trigger){width:100%}.audit-filter-actions{display:flex;gap:8px;align-items:center;min-height:36px;white-space:nowrap}.audit-error{margin-bottom:16px}.audit-stream{display:grid;gap:10px}.audit-count{margin:3px 0 0;color:var(--muted-foreground);font-size:12px}.audit-entry{border:1px solid var(--border);border-radius:12px;background:var(--card);padding:16px 18px;box-shadow:0 1px 2px color-mix(in srgb,var(--foreground) 5%,transparent)}.audit-entry-head{display:flex;align-items:center;justify-content:space-between;gap:14px}.audit-identity{display:flex;align-items:center;gap:9px;min-width:0}.audit-avatar{display:grid;place-items:center;width:30px;height:30px;flex:none;border-radius:9px;background:color-mix(in srgb,var(--primary) 12%,var(--card));color:var(--primary);font-size:13px;font-weight:700}.audit-identity div{display:grid;gap:2px;min-width:0}.audit-identity b{font-size:13px;color:var(--foreground);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.audit-identity small,.audit-entry time{font-size:11px;color:var(--muted-foreground)}.audit-event{display:flex;align-items:center;gap:7px;min-width:0;margin:13px 0 10px}.audit-object{padding:3px 7px;border-radius:6px;background:var(--secondary);color:var(--secondary-foreground);font-size:11px;font-weight:650;white-space:nowrap}.audit-object-id{font-size:12px;color:var(--muted-foreground);white-space:nowrap}.audit-event>b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--foreground);font-size:13px}.audit-open-link{display:grid;place-items:center;width:24px;height:24px;margin-left:auto;border-radius:6px;color:var(--primary);text-decoration:none}.audit-open-link:hover,.audit-open-link:focus-visible{background:var(--accent);outline:none}.audit-diff{border-top:1px solid var(--border);padding-top:10px}.audit-diff summary{cursor:pointer;width:max-content;max-width:100%;color:var(--primary);font-size:12px;font-weight:600;list-style:none}.audit-diff summary::-webkit-details-marker{display:none}.audit-diff summary::before{content:'›';display:inline-block;margin-right:6px;transform:rotate(0deg);transition:transform .15s ease}.audit-diff[open] summary::before{transform:rotate(90deg)}.audit-diff dl{display:grid;gap:9px;margin:12px 0 0}.audit-diff dl>div{display:grid;grid-template-columns:145px minmax(0,1fr);gap:12px;padding:10px;border-radius:8px;background:color-mix(in srgb,var(--secondary) 65%,transparent)}.audit-diff dt{overflow-wrap:anywhere;color:var(--muted-foreground);font-size:12px}.audit-diff dd{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:9px;margin:0}.audit-diff dd span{display:grid;gap:4px;min-width:0;overflow-wrap:anywhere;color:var(--foreground);font-size:12px;line-height:1.55}.audit-diff dd small{font-size:10px;color:var(--muted-foreground)}.audit-trimmed{margin:9px 0 0;color:var(--muted-foreground);font-size:11px}.audit-more{display:flex;justify-content:center;align-items:center;min-height:52px;color:var(--muted-foreground);font-size:12px}.audit-empty{margin-top:18px}
@media(max-width:1080px){.audit-filter-grid{grid-template-columns:repeat(3,minmax(0,1fr))}.audit-filter-actions{grid-column:span 3}}
@media(max-width:760px){.audit-page{padding:16px 12px 32px}.audit-heading{gap:12px}.audit-heading>.btn{width:100%}.audit-filters{padding:14px;margin-inline:0}.audit-filter-grid{grid-template-columns:1fr 1fr}.audit-filter-grid label:first-of-type{grid-column:span 2}.audit-filter-actions{grid-column:span 2;display:grid;grid-template-columns:1fr 1fr}.audit-filter-actions .btn{min-height:40px}.audit-entry{padding:14px}.audit-entry-head{align-items:flex-start}.audit-entry time{white-space:nowrap;padding-top:3px}.audit-event{flex-wrap:wrap}.audit-event>b{flex:1 1 calc(100% - 104px);white-space:normal;overflow:visible}.audit-open-link{margin-left:0}.audit-diff dl>div{grid-template-columns:1fr;gap:7px}.audit-diff dd{grid-template-columns:1fr}.audit-diff dd span{padding:7px 8px;border-radius:6px;background:var(--card)}}
</style>
