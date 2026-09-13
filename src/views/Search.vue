<script setup lang="ts">
import Icon from '../components/Icon.vue'
import { t, locale, formatDate } from '../i18n'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api'
import { workflowStyle, statusLabel as requirementStatusLabel } from '../requirementWorkflow'
import RequirementCode from '../components/RequirementCode.vue'
import RequirementListExport from '../components/RequirementListExport.vue'
import { requirementExportLimit } from '../requirementPaging'
import { readRecentSearches, rememberRecentSearch } from '../recentSearches'
import { searchDisplayCode } from '../topSearch'

const route = useRoute()
const q = ref(String(route.query.q || ''))
const type = ref('')
const project = ref('')
const items = ref<any[]>([])
const projects = ref<any[]>([])
const total = ref(0)
const loading = ref(false)
const searched = ref(false)
const error = ref('')
const searchFocused = ref(false)
const recentQueries = ref<string[]>([])
const searchScope = ref('')
let identityVersion = 0, identityBlocked = false
let searchVersion = 0
let searchController: AbortController | undefined
let disposed = false
const composing = ref(false)
const statusNames: Record<string, string> = { active: '进行中', archived: '已归档' }
const groups = computed(() => Object.entries(items.value.reduce((all: any, item: any) => { (all[item.type] || (all[item.type] = [])).push(item); return all }, {})))
// 搜索接口只返回用于列表展示的轻量摘要。导出时必须重新以每条记录所属项目读取
// 完整需求，不能借用当前项目或只导出首屏数据，否则跨项目搜索会导出失败或漏项。
const matchingRequirements = computed(() => items.value.filter(item => item?.type === '需求'))
const canExportRequirements = computed(() => {
  if (loading.value || error.value || (type.value && type.value !== '需求')) return false
  // 若首屏恰好没有需求，但服务端还有后续页，仍允许导出器按同一查询拉取完整结果。
  return matchingRequirements.value.length > 0 || total.value > items.value.length
})
const requirementExportTotal = computed(() => canExportRequirements.value ? Math.max(1, matchingRequirements.value.length) : 0)
const statusLabel = (value: string) => t(statusNames[value] || value)

async function search() {
  if (disposed || identityBlocked || composing.value) return
  clearTimeout(timer)
  const version = ++searchVersion
  searchController?.abort()
  const controller = new AbortController()
  searchController = controller
  loading.value = true
  error.value = ''
  try {
    if (q.value.trim()) recentQueries.value = rememberRecentSearch(typeof window === 'undefined' ? undefined : window.localStorage, searchScope.value, q.value)
    const params = new URLSearchParams({ q: q.value, type: type.value, project: project.value })
    const data = await api<any>(`/search?${params}`, { signal: controller.signal })
    if (version !== searchVersion) return
    if (!Array.isArray(data?.items) || !Number.isSafeInteger(data.total) || data.total < data.items.length) throw Error('搜索结果格式不正确，请重试')
    items.value = data.items
    total.value = data.total
    searched.value = true
  } catch (cause: any) { if (version === searchVersion) error.value = cause.message || '操作失败，请稍后重试' }
  finally { if (version === searchVersion) loading.value = false }
}

function go(item: any) {
  if (loading.value || error.value) return
  localStorage.setItem('devflow-project', item.projectId)
  location.href = item.url
}

let timer: any
function schedule() {
  if (disposed || identityBlocked) return
  // 输入一变就令旧请求失效，避免防抖等待期间把旧结果当成新结果展示。
  clearTimeout(timer); searchVersion++; searchController?.abort()
  loading.value = true; error.value = ''
  if (!composing.value) timer = setTimeout(search, 250)
}
function compositionStart() { composing.value = true; schedule() }
function compositionEnd() { composing.value = false; schedule() }
function submitSearch(event: KeyboardEvent) { if (!event.isComposing && !composing.value) void search() }
function openRecentSearches() { searchFocused.value = true; recentQueries.value = readRecentSearches(typeof window === 'undefined' ? undefined : window.localStorage, searchScope.value) }
function chooseRecentSearch(value: string) { q.value = value; void search() }
async function loadSearchScope() {
  const identity = identityVersion
  try {
    const session = await api<any>('/session')
    if (disposed || identityBlocked || identity !== identityVersion) return
    searchScope.value = [session?.tenant?.id || '', session?.user?.id || ''].join('\0')
    recentQueries.value = readRecentSearches(typeof window === 'undefined' ? undefined : window.localStorage, searchScope.value)
  } catch { if (identity === identityVersion) searchScope.value = '' }
}

function exportAbortError() {
  const cause = new Error('导出已取消')
  cause.name = 'AbortError'
  return cause
}

/**
 * 固定点击瞬间的关键词与项目筛选，逐页读取所有匹配需求，再以所属项目拉取完整记录。
 * 这样 CSV 可以保留人员、字段等完整信息，同时不会把一个项目的权限头误用于另一个项目。
 */
async function exportRequirements(signal: AbortSignal, progress: (done: number, total: number) => void) {
  const snapshot = { q: q.value, project: project.value }
  const records: any[] = [], seen = new Set<string>()
  let expectedTotal: number | undefined
  let offset = 0
  while (true) {
    if (signal.aborted) throw exportAbortError()
    const params = new URLSearchParams({ q: snapshot.q, type: '需求', project: snapshot.project, limit: '100', offset: String(offset) })
    const page = await api<any>(`/search?${params}`, { signal })
    if (signal.aborted) throw exportAbortError()
    const pageTotal = Number(page?.total)
    const pageItems = Array.isArray(page?.items) ? page.items : null
    if (!Number.isSafeInteger(pageTotal) || pageTotal < 0 || !pageItems || pageItems.length > 100 || offset + pageItems.length > pageTotal) throw Error('导出需求数据格式不正确，请重试')
    if (pageTotal > requirementExportLimit) throw Error('最多导出 20000 条需求，请缩小筛选范围后分批导出')
    if (expectedTotal === undefined) expectedTotal = pageTotal
    else if (expectedTotal !== pageTotal) throw Error('搜索结果已变化，请重新搜索后导出')
    if (!pageItems.length) {
      if (offset !== pageTotal) throw Error('搜索结果已变化，请重新搜索后导出')
      break
    }
    for (const item of pageItems) {
      const id = Number(item?.id), projectId = String(item?.projectId || '')
      if (item?.type !== '需求' || !Number.isSafeInteger(id) || id <= 0 || !projectId) throw Error('导出需求数据格式不正确，请重试')
      const key = `${projectId}:${id}`
      if (seen.has(key)) throw Error('搜索结果已变化，请重新搜索后导出')
      seen.add(key)
      const record = await api<any>(`/requirements/${id}`, { headers: { 'X-TaskLoom-Project': projectId }, signal })
      if (signal.aborted) throw exportAbortError()
      records.push({ ...record, projectId })
      progress(records.length, pageTotal)
    }
    offset += pageItems.length
    if (offset === pageTotal) break
  }
  if (!expectedTotal || !records.length) throw Error('当前筛选没有可导出的需求')
  if (records.length !== expectedTotal) throw Error('搜索结果已变化，请重新搜索后导出')
  return records
}
watch([q, type, project], schedule, { flush: 'sync' })
watch(() => route.query.q, value => { q.value = String(value || '') })
function projectChanged() { items.value = []; total.value = 0; searched.value = false; void search() }
function invalidateSearchIdentity() { identityVersion++; identityBlocked = true; clearTimeout(timer); searchVersion++; searchController?.abort(); items.value = []; projects.value = []; recentQueries.value = []; searchScope.value = ''; total.value = 0; loading.value = false; searched.value = false; error.value = ''; q.value = '' }
const identityEvents = ['devflow-identity-changed', 'devflow-auth-expired', 'devflow-auth-session-ended', 'devflow-account-disabled']
onMounted(async () => { window.addEventListener('devflow-project-changed', projectChanged); identityEvents.forEach(event => window.addEventListener(event, invalidateSearchIdentity)); void search(); void loadSearchScope(); try { const data = await api<any>('/projects'); if (!disposed && !identityBlocked) projects.value = data.items || [] } catch (cause: any) { if (!disposed && !identityBlocked) error.value = cause.message || '操作失败，请稍后重试' } })
onBeforeUnmount(() => { disposed = true; clearTimeout(timer); searchVersion++; searchController?.abort(); window.removeEventListener('devflow-project-changed', projectChanged); identityEvents.forEach(event => window.removeEventListener(event, invalidateSearchIdentity)) })
</script>

<template>
  <div class="module-page search-page">
    <div class="search-hero"><div class="search-hero-inner">
      <div class="search-heading"><div class="search-title"><span class="eyebrow">{{ t("跨项目检索") }}</span><h1>{{ t("全局搜索") }}</h1></div><RequirementListExport :items="matchingRequirements" :project-id="project||'prj_orbit'" :total="requirementExportTotal" :records-provider="exportRequirements" :label="t('导出匹配需求')"/></div>
      <div class="search-query-row"><div class="search-input-stack"><div class="global-search-box"><Icon name="search"/><input :aria-label="t('搜索内容、负责人、开发人员或部门')" v-model="q" type="search" enterkeyhint="search" autocomplete="off" @focus="openRecentSearches" @blur="searchFocused=false" @compositionstart="compositionStart" @compositionend="compositionEnd" @keydown.enter="submitSearch" :placeholder="t('搜索内容、负责人、开发人员或部门')"><kbd>⌘ K</kbd></div><div v-if="searchFocused&&!q.trim()&&recentQueries.length" class="search-recent-panel"><span>{{t('最近搜索')}}</span><button v-for="item in recentQueries" :key="item" type="button" @mousedown.prevent @click="chooseRecentSearch(item)"><Icon name="search" :size="13"/>{{item}}</button></div></div><p class="search-result-count" role="status" aria-live="polite"><b>{{ total }}</b><span>{{ t("条结果") }}</span></p></div>
      <div class="search-filters"><select :aria-label="t('全部类型')" v-model="type"><option value="">{{ t("全部类型") }}</option><option value="项目">{{ t("项目") }}</option><option value="需求">{{ t("需求") }}</option><option value="迭代">{{ t("迭代") }}</option><option value="缺陷">{{ t("缺陷") }}</option><option value="测试用例">{{ t("测试用例") }}</option><option value="测试计划">{{ t("测试计划") }}</option></select><select :aria-label="t('全部可访问项目')" v-model="project"><option value="">{{ t("全部可访问项目") }}</option><option v-for="x in projects" :key="x.id" :value="x.id">{{ x.name }}</option></select></div>
    </div></div>
    <div v-if="loading" :class="items.length?'search-refresh-status':'state'" role="status"><span class="spinner"></span>{{ t(items.length ? '正在更新搜索结果…' : '正在检索…') }}</div>
    <div v-if="error" class="state error" role="alert">{{ t(error) }} <button class="btn" @click="search">{{ t("重试") }}</button></div>
    <div v-if="!loading && !error && searched && !items.length" class="empty-work"><div>⌕</div><h2>{{ t("没有找到匹配内容") }}</h2><p>{{ t("试试更短的关键词，或清除类型与项目筛选。") }}</p></div>
    <section v-if="items.length" class="search-results" :aria-busy="loading" :inert="loading || !!error"><div v-for="group in groups" :key="group[0]" class="search-group"><header><h2>{{ t(group[0]) }}</h2><span>{{ (group[1] as any[]).length }}</span></header><article v-for="x in group[1] as any[]" :key="`${x.projectId}-${x.type}-${x.id}`" tabindex="0" role="link" @keydown.enter.self="go(x)" @click="go(x)"><span :class="['object-mark', x.type]">{{ t(x.type).slice(0, 1) }}</span><div><div><RequirementCode v-if="x.type==='需求'" class="code" :requirement="x" :display-code="searchDisplayCode(x)" :disabled="loading || !!error" :project-id="x.projectId" @open="go(x)"/><span v-else class="code">{{ x.code }}</span><b>{{ x.title }}</b><span :class="['status','workflow-color',x.status]" :style="workflowStyle(x)">{{x.statusName?requirementStatusLabel(x,[],t):statusLabel(x.status)}}</span></div><p>{{ x.snippet || t('暂无摘要') }}</p><small>{{ x.projectName }} {{ t("· 更新于") }} {{ formatDate(x.updatedAt) }}</small></div><span class="chevron">›</span></article></div></section>
  </div>
</template>

<style scoped>
.search-refresh-status{display:flex;align-items:center;gap:8px;padding:12px 24px;color:var(--muted);font-size:12px}.search-results[aria-busy="true"]{opacity:.65}.search-group article>div>div{gap:7px;flex-wrap:wrap}.search-group article>div>div>b{flex:1 1 240px;line-height:1.5}
.search-group article>div>div{display:flex;align-items:center}.search-group article .workflow-color{order:-1;flex:none;min-height:22px;padding:2px 7px;border-radius:999px;font-size:11px;font-weight:700;line-height:1.2;white-space:nowrap}.search-group article .code{flex:none;font-variant-numeric:tabular-nums}.search-group article b{min-width:0}
.search-input-stack{position:relative;flex:1 1 520px;min-width:0}.search-input-stack .global-search-box{width:100%}.search-recent-panel{position:absolute;z-index:30;top:calc(100% + 6px);left:0;right:0;display:grid;gap:2px;padding:8px;border:1px solid var(--line);border-radius:8px;background:var(--surface);box-shadow:0 14px 34px #17203320}.search-recent-panel>span{padding:3px 7px 5px;color:var(--muted);font-size:11px}.search-recent-panel button{display:flex;align-items:center;gap:8px;min-width:0;border:0;border-radius:6px;background:transparent;padding:8px 7px;color:var(--ink);font:inherit;font-size:12px;text-align:left;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.search-recent-panel button:hover{background:var(--primary-soft)}
.search-page{padding:0;min-width:0}.search-hero{padding:24px max(24px,calc((100% - 1160px)/2));border-bottom:1px solid var(--line);background:linear-gradient(120deg,color-mix(in srgb,var(--surface) 96%,transparent),color-mix(in srgb,var(--primary-soft) 42%,var(--surface)))}.search-hero-inner{display:grid;gap:10px;max-width:1160px;margin:0 auto}.search-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;min-width:0}.search-title{display:flex;align-items:baseline;gap:9px;min-width:0}.search-title .eyebrow{color:var(--muted);font-size:12px;white-space:nowrap}.search-heading h1{margin:0;font-size:23px;line-height:32px;white-space:nowrap}.search-heading :deep(.list-export){justify-content:flex-end;min-width:0}.search-heading :deep(.list-export small){flex-basis:100%;text-align:right}.search-query-row{display:flex;align-items:center;gap:12px;min-width:0}.global-search-box{--ui-control-height:44px;flex:1 1 520px;width:auto;min-width:0!important;height:44px!important;border-radius:8px!important;box-shadow:none!important}.global-search-box input{font-size:14px!important}.global-search-box kbd{flex:none;border-radius:4px;padding:2px 5px;font-size:10px}.search-result-count{display:flex;align-items:baseline;gap:4px;flex:none;margin:0;color:var(--muted);font-size:12px;white-space:nowrap}.search-result-count b{font-size:15px;color:var(--ink);font-variant-numeric:tabular-nums}.search-filters{display:flex;align-items:center;gap:8px;margin:0}.search-filters select{min-width:132px;max-width:min(100%,220px);height:34px;padding:5px 28px 5px 10px;border:1px solid var(--input);border-radius:6px;background:var(--card);color:var(--ink);font-size:12px;box-shadow:none}
@media(max-width:820px){
 .search-hero{padding:18px 16px}.search-heading{align-items:flex-start}.search-title{align-items:flex-start;flex-direction:column;gap:2px}.search-query-row{flex-wrap:wrap;gap:8px}.global-search-box{flex-basis:100%}.global-search-box input{min-width:0}.search-result-count{margin-left:auto}.search-filters{flex-wrap:wrap;gap:8px}.search-filters select{min-width:0;max-width:100%;flex:1 1 125px}
 .search-results{padding:16px 0}.search-group{min-width:0}.search-group article{grid-template-columns:28px minmax(0,1fr) 16px;padding:14px 12px;gap:9px;align-items:start}.search-group article>div{min-width:0}.search-group article>div>div{flex-wrap:wrap;gap:6px 9px}.search-group article b,.search-group article p,.search-group article small{overflow-wrap:anywhere;line-height:1.7}.search-group article .status{max-width:100%}
}
@media(max-width:520px){.search-heading{flex-wrap:wrap}.search-heading :deep(.list-export){width:100%;justify-content:flex-start}.search-heading :deep(.list-export small){text-align:left}.search-result-count{width:100%;margin:0}.global-search-box kbd{display:none}}
</style>
