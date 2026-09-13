<script setup lang="ts">
import Icon from '../components/Icon.vue'
import { t, locale, formatDate } from '../i18n'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api'
import { workflowStyle, statusLabel } from '../requirementWorkflow'
import RequirementCode from '../components/RequirementCode.vue'
import RequirementListExport from '../components/RequirementListExport.vue'
import { layoutScope } from '../layoutScope'
import { clearMyWorkPreferences, compareWorkAttention, personalWorkKey, readMyWorkPreferences, saveMyWorkPreferences } from '../myWorkEfficiency'

const router = useRouter()
const scopeIdentity = layoutScope.value
const workRoot = ref<HTMLElement | null>(null)
const selectedKey = ref('')
const navigationKey = ref('')
const composing = ref(false)
const items = ref<any[]>([])
const counts = ref<any>({})
const projects = ref<any[]>([])
const category = ref('active')
const q = ref('')
const type = ref('')
const project = ref('all')
const view = ref<'assigned' | 'favorites'>('assigned')
const status = ref('')
// 仅收纳低频筛选控件；收起后保留筛选条件，用数量提示当前生效范围。
const filtersExpanded = ref(false)
const filterCount = computed(() => [type.value, project.value !== 'all', status.value, sprintId.value, sort.value !== (view.value === 'favorites' ? 'updatedAt' : 'attention'), sort.value !== 'attention' && order.value !== 'desc'].filter(Boolean).length)
const sprintId=ref(''),sprints=ref<{id:number;name:string;projectId:string;projectName:string;status:string}[]>([])
const activeSprints=computed(()=>sprints.value.filter(item=>item.status==='进行中'))
function sprintLabel(item:typeof sprints.value[number]){return (project.value==='all'?item.projectName+' · ':'')+item.name}
function selectSprint(id:string){if(disposed||identityInvalid)return;sprintId.value=sprintId.value===id?'':id}

const sort = ref('attention'), order = ref<'asc' | 'desc'>('desc')
const loading = ref(true)
const error = ref('')
const tabs = [['active', '尚未结束'], ['', '全部'], ['todo', '待处理'], ['doing', '进行中'], ['due', '即将到期'], ['overdue', '已逾期'], ['completed', '已完成'], ['cancelled', '已终止']]
const roleNames: Record<string, string> = { frontend: '前端', backend: '后端', algorithm: '算法', ui: 'UI 设计', product: '产品', qa: '测试', frontend_lead: '前端组长', backend_lead: '后端组长', owner: '负责人', assignee: '负责人', verifier: '验证人', executor: '执行人', favorite: '收藏的需求' }
const requirementStatusDefinitions=ref<any[]>([])
const requirementStatuses=computed(()=>[...new Set(requirementStatusDefinitions.value.map(item=>String(item.key)))])
const statusOptions = computed(() => view.value === 'favorites' ? requirementStatuses.value : [...new Set([...requirementStatuses.value, '进行中', '已关闭', '新建', '已确认', '修复中', '待验证', '已解决', '重新打开', '未执行', '执行中', '通过', '失败', '阻塞', '跳过', '有效', '已废弃'])])
function statusOptionLabel(key:string){const names=[...new Set(requirementStatusDefinitions.value.filter(item=>item.key===key).map(item=>item.name))];return names.length?names.map(name=>{const def=requirementStatusDefinitions.value.find(item=>item.key===key&&item.name===name);return def?.system&&key===name?t(name):name}).join(' / '):t(key)}
// The favorites API sorts the entire accessible set, including sub-millisecond
// favorite times that JavaScript Date would round off. Keep that server order.
const sortedItems = computed(() => view.value === 'favorites' ? items.value : [...items.value].sort((left, right) => {
  if (sort.value === 'attention') return compareWorkAttention(left, right)
  const a = sort.value === 'code' ? Number(left.id) : String(left[sort.value] || '').toLocaleLowerCase(locale.value)
  const b = sort.value === 'code' ? Number(right.id) : String(right[sort.value] || '').toLocaleLowerCase(locale.value)
  if ((a === '') !== (b === '')) return a === '' ? 1 : -1
  const comparison = a < b ? -1 : a > b ? 1 : 0
  return (order.value === 'asc' ? comparison : -comparison) || String(left.projectId).localeCompare(String(right.projectId)) || Number(left.id) - Number(right.id)
}))
let loadVersion = 0, navigationVersion = 0, disposed = false, identityInvalid = false, restoring = false
let restoreScrollTop: number | null = null
let loadController: AbortController | undefined
let assignedCategory = 'active'
const currentProject = () => localStorage.getItem('devflow-project') || 'prj_orbit'
function workStorage() { try { return typeof window === 'undefined' ? undefined : window.sessionStorage } catch { return undefined } }
// module-page 自身是滚动容器，不是外层 main；记错节点会让返回位置始终为零。
function scrollContainer() { return workRoot.value }
function rememberPosition() {
  if (disposed || identityInvalid || scopeIdentity !== layoutScope.value) return
  saveMyWorkPreferences(workStorage(), scopeIdentity, { category: category.value, q: q.value, type: type.value,
    project: project.value, view: view.value, status: status.value, sprintId: sprintId.value, sort: sort.value,
    order: order.value, filtersExpanded: filtersExpanded.value, assignedCategory, selectedKey: selectedKey.value,
    scrollTop: scrollContainer()?.scrollTop || 0 })
}
function restorePreferences() {
  const previous = readMyWorkPreferences(workStorage(), scopeIdentity)
  if (!previous) return
  restoring = true
  category.value = previous.category; q.value = previous.q; type.value = previous.type; project.value = previous.project
  view.value = previous.view; status.value = previous.status; sprintId.value = previous.sprintId; sort.value = previous.sort
  order.value = previous.order; filtersExpanded.value = previous.filtersExpanded; assignedCategory = previous.assignedCategory
  selectedKey.value = previous.selectedKey; restoreScrollTop = previous.scrollTop
  restoring = false
}
function resetFilters() { type.value = ''; project.value = 'all'; status.value = ''; sprintId.value = ''; sort.value = view.value === 'favorites' ? 'updatedAt' : 'attention'; order.value = 'desc' }
const lastViewed = computed(() => sortedItems.value.find(item => personalWorkKey(item) === selectedKey.value))
function locateLastViewed() {
  const row = [...(workRoot.value?.querySelectorAll<HTMLElement>('[data-work-key]') || [])].find(element => element.dataset.workKey === selectedKey.value)
  row?.scrollIntoView({ block: 'center', behavior: 'auto' }); row?.focus({ preventScroll: true })
}
function attentionReason(item: any) { return ({ overdue: '已逾期', due: '即将到期' } as Record<string, string>)[item.category] || '' }
function selectView(next: 'assigned' | 'favorites') {
  if (view.value === next) return
  if (next === 'favorites') { assignedCategory = category.value; category.value = ''; type.value = ''; if (sort.value === 'attention') sort.value = 'updatedAt'; if (status.value && !requirementStatuses.value.includes(status.value)) status.value = '' }
  else { category.value = assignedCategory; if (sort.value === 'favoritedAt') sort.value = 'updatedAt' }
  view.value = next
}

async function load() {
  if (disposed || identityInvalid || composing.value) return
  clearTimeout(timer)
  const version = ++loadVersion
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  const scope = currentProject()
  loading.value = true
  error.value = ''
  try {
    const params = new URLSearchParams({ category: category.value, q: q.value, type: view.value === 'favorites' ? '需求' : type.value, project: project.value, view: view.value, status: status.value, sort: sort.value === 'attention' ? 'updatedAt' : sort.value, order: order.value, sprintId:sprintId.value })
    const data = await api<any>(`/my-work?${params}`, { headers: { 'X-TaskLoom-Project': scope }, signal: controller.signal })
    if (version !== loadVersion || scope !== currentProject() || disposed || identityInvalid) return
    items.value = data.items || []
    sprints.value=Array.isArray(data.sprints)?data.sprints:[]
    requirementStatusDefinitions.value=data.requirementStatuses||[]
    counts.value = data.counts || {}
    if (counts.value.all === undefined) counts.value.all = ['todo', 'doing', 'due', 'overdue', 'completed', 'cancelled'].reduce((sum, key) => sum + Number(counts.value[key] || 0), 0)
  } catch (e: any) { if (version === loadVersion && scope === currentProject() && !disposed && !identityInvalid) error.value = e.message || '操作失败，请稍后重试' }
  finally {
    if (version === loadVersion && scope === currentProject() && !disposed && !identityInvalid) {
      loading.value = false
      if (!error.value && restoreScrollTop !== null) {
        const top = restoreScrollTop
        await nextTick()
        if (version === loadVersion && !disposed && !identityInvalid && scope === currentProject()) {
          const container = scrollContainer(); if (container) container.scrollTop = top
          restoreScrollTop = null
        }
      }
    }
  }
}

async function go(item: any) {
  if (disposed || identityInvalid || navigationKey.value) return
  const version = ++navigationVersion, scope = currentProject(), changed = item.projectId !== scope
  navigationKey.value = personalWorkKey(item)
  try {
    // Verify stale cross-project favorites before changing the app's project.
    if (item.favorited) await api(`/requirements/${item.id}/favorite`, { headers: { 'X-TaskLoom-Project': item.projectId } })
    else if (changed) await api(`/projects/${encodeURIComponent(item.projectId)}/summary`, { headers: { 'X-TaskLoom-Project': item.projectId } })
    if (disposed || identityInvalid || version !== navigationVersion || scope !== currentProject()) return
    selectedKey.value = personalWorkKey(item)
    rememberPosition()
    if (changed) { localStorage.setItem('devflow-project', item.projectId); location.href = item.url }
    else await router.push(item.url)
  } catch (cause: any) { if (!disposed && !identityInvalid && version === navigationVersion && scope === currentProject()) error.value = cause?.message || '操作失败，请稍后重试' }
  finally { if (!disposed && !identityInvalid && version === navigationVersion) navigationKey.value = '' }
}

let timer: any
function scheduleLoad() {
  if (restoring || disposed || identityInvalid) return
  restoreScrollTop = null; loadVersion++; loadController?.abort(); loading.value = true; clearTimeout(timer)
  if (!composing.value) timer = setTimeout(load, 250)
}
function compositionStart() { composing.value = true; scheduleLoad() }
function compositionEnd() { composing.value = false; scheduleLoad() }
function submitSearch(event: KeyboardEvent) { if (!event.isComposing && !composing.value) void load() }
watch(project,()=>{if(!restoring){sprintId.value='';sprints.value=[]}},{flush:'sync'})
watch([category, q, type, project, view, status, sprintId], scheduleLoad, { flush: 'sync' })
// 个人工作已取回当前筛选的完整集合，切换展示排序无需再次汇总请求。
// 收藏排序由服务端保证精度，仍保留它的请求语义。
watch([sort, order], () => { if (!restoring && view.value === 'favorites') scheduleLoad() }, { flush: 'sync' })
function projectChanged() { sprintId.value='';sprints.value=[];void load() }
function favoritesChanged() { if (view.value === 'favorites') void load() }
function identityChanged() { clearMyWorkPreferences(workStorage(), scopeIdentity); identityInvalid = true; clearTimeout(timer); loadController?.abort(); loadVersion++; navigationVersion++; navigationKey.value=''; selectedKey.value=''; items.value = []; counts.value = {}; sprints.value=[]; requirementStatusDefinitions.value = []; loading.value = false; error.value = '账号身份已在其他页面切换，请刷新后继续' }
watch(layoutScope, () => { if (scopeIdentity !== layoutScope.value) identityChanged() }, { flush: 'sync' })
onMounted(async () => { window.addEventListener('devflow-project-changed', projectChanged); window.addEventListener('devflow-favorites-changed', favoritesChanged); window.addEventListener('devflow-identity-changed', identityChanged); window.addEventListener('devflow-auth-expired', identityChanged); window.addEventListener('pagehide', rememberPosition); restorePreferences(); await load(); try { const data = await api<any>('/projects'); if (!disposed && !identityInvalid) projects.value = data.items || [] } catch (cause: any) { if (!disposed && !identityInvalid) error.value = cause.message || '操作失败，请稍后重试' } })
onBeforeUnmount(() => { rememberPosition(); disposed = true; clearTimeout(timer); loadController?.abort(); loadVersion++; navigationVersion++; window.removeEventListener('devflow-project-changed', projectChanged); window.removeEventListener('devflow-favorites-changed', favoritesChanged); window.removeEventListener('devflow-identity-changed', identityChanged); window.removeEventListener('devflow-auth-expired', identityChanged); window.removeEventListener('pagehide', rememberPosition) })
</script>

<template>
  <div ref="workRoot" class="module-page work-page">
    <div class="page-heading compact-page-heading"><div><h1>{{ t("我的工作") }}</h1><details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{ view === 'favorites' ? t('收藏包含其他成员负责的可访问需求。') : t("需求的任意人员字段、职能角色或明确的 @ 提及包含我，都会纳入；同时保留我负责的其他工作项。") }}</p></details></div><RequirementListExport :items="loading?[]:sortedItems.filter(item=>item.type==='需求')" :project-id="currentProject()" :label="t('导出需求')"/><div class="work-view-switch" :aria-label="t('工作视图')"><button type="button" :class="{active:view==='assigned'}" :aria-pressed="view==='assigned'" @click="selectView('assigned')">{{t('与我相关')}}</button><button type="button" :class="{active:view==='favorites'}" :aria-pressed="view==='favorites'" @click="selectView('favorites')"><span aria-hidden="true">☆</span> {{t('我的收藏')}}</button></div></div>
    <nav class="work-tabs" :aria-label="t('工作分类')"><button v-for="x in tabs" :key="x[0]" :aria-pressed="category === x[0]" :class="{ active: category === x[0] }" @click="category = x[0]"><span>{{ t(x[1]) }}</span><b>{{ counts[x[0] || 'all'] || 0 }}</b></button></nav>
    <div v-if="view==='assigned'" class="work-focus-bar">
      <button type="button" :class="{active:sort==='attention'}" :aria-pressed="sort==='attention'" @click="sort=sort==='attention'?'updatedAt':'attention'">{{t('优先关注')}}</button>
      <span>{{sort==='attention'?t('逾期与临近截止优先，再看待处理、进行中；同组按优先级排序。'):t('当前使用自选排序，不改变工作项状态。')}}</span>
      <button v-if="lastViewed&&!loading&&!error" type="button" class="work-resume" @click="locateLastViewed">{{t('定位上次查看')}}</button>
    </div>
    <nav v-if="activeSprints.length" class="work-sprints" :aria-label="t('进行中迭代筛选')"><span>{{t('进行中迭代')}}</span><button type="button" :aria-pressed="!sprintId" @click="sprintId=''">{{t('全部迭代')}}</button><button v-for="item in activeSprints" :key="item.id" type="button" :aria-pressed="sprintId===String(item.id)" @click="selectSprint(String(item.id))">{{sprintLabel(item)}}</button></nav>
    <div class="toolbar module-toolbar work-filters">
      <div class="search"><Icon name="search"/><input type="search" enterkeyhint="search" :aria-label="t('搜索我的工作项')" v-model="q" :placeholder="t('搜索我的工作项')" @compositionstart="compositionStart" @compositionend="compositionEnd" @keydown.enter="submitSearch"></div>
      <button type="button" class="btn mobile-filter-toggle" :aria-expanded="filtersExpanded" aria-controls="work-advanced-filters" @click="filtersExpanded=!filtersExpanded">{{t('筛选')}}<span v-if="filterCount"> · {{filterCount}}</span><span aria-hidden="true">{{filtersExpanded?'⌃':'⌄'}}</span></button>
      <div id="work-advanced-filters" class="mobile-advanced-filters" :class="{'is-expanded':filtersExpanded}">
      <select v-if="view !== 'favorites'" :aria-label="t('全部类型')" v-model="type"><option value="">{{ t("全部类型") }}</option><option value="需求">{{ t("需求") }}</option><option value="缺陷">{{ t("缺陷") }}</option><option value="迭代">{{ t("迭代") }}</option><option value="测试用例">{{ t("测试用例") }}</option><option value="测试执行">{{ t("测试执行") }}</option></select>
      <select :aria-label="t('全部项目')" v-model="project"><option value="all">{{ t("全部项目") }}</option><option v-for="x in projects" :key="x.id" :value="x.id">{{ x.name }}</option></select>
      <select v-model="sprintId" :aria-label="t('筛选迭代')"><option value="">{{t('全部迭代')}}</option><option v-for="item in sprints" :key="item.id" :value="String(item.id)">{{sprintLabel(item)}} · {{t(item.status)}}</option></select>
      <select :aria-label="t('全部状态')" v-model="status"><option value="">{{t('全部状态')}}</option><option v-for="value in statusOptions" :key="value" :value="value">{{statusOptionLabel(value)}}</option></select>
      <select :aria-label="t('排序字段')" v-model="sort"><option v-if="view!=='favorites'" value="attention">{{t('优先关注')}}</option><option v-if="view==='favorites'" value="favoritedAt">{{t('收藏时间')}}</option><option value="updatedAt">{{t('更新时间')}}</option><option value="title">{{t('标题')}}</option><option value="priority">{{t('优先级')}}</option><option value="code">{{t('编号')}}</option><option value="dueDate">{{t('截止日期')}}</option></select>
      <button v-if="sort!=='attention'" type="button" class="btn sort-order" :aria-label="t(order==='asc'?'升序':'降序')" @click="order=order==='asc'?'desc':'asc'">{{t(order==='asc'?'升序':'降序')}} {{order==='asc'?'↑':'↓'}}</button>
      <button v-if="filterCount" type="button" class="btn" @click="resetFilters">{{t('重置筛选')}}</button>
      </div>
      <span class="result-count">{{ items.length }} {{ t("项") }}</span>
    </div>
    <div v-if="loading" class="state"><span class="spinner"></span>{{ t("正在汇总个人工作…") }}</div>
    <div v-else-if="error" class="state error" role="alert">{{ t(error) }} <button class="btn" @click="load">{{ t("重试") }}</button></div>
    <div v-else-if="!items.length" class="empty-work"><div>{{view==='favorites'?'☆':'✓'}}</div><h2>{{ view==='favorites'?t('暂无符合条件的收藏需求'):t("这里已经清空了") }}</h2><p>{{ view==='favorites'?t('在需求详情中点击收藏，方便稍后跟进。'):t("当前分类下没有分配给你的工作项。") }}</p></div>
    <div v-else class="work-list"><article v-for="x in sortedItems" :key="personalWorkKey(x)" :data-work-key="personalWorkKey(x)" :class="{'last-viewed':selectedKey===personalWorkKey(x)}" :aria-busy="navigationKey===personalWorkKey(x)" tabindex="0" role="link" @keydown.enter.self="go(x)" @click="go(x)"><span :class="['object-mark', x.type]">{{ t(x.type).slice(0, 1) }}</span><div class="work-main"><div><RequirementCode v-if="x.type==='需求'" class="code" :requirement="x" :project-id="x.projectId" @open="go(x)"/><span v-else class="code">{{ x.code }}</span><b>{{ x.title }}</b><span v-if="x.favorited" class="favorite-mark" :aria-label="t('已收藏')">★</span></div><p>{{ x.projectName }}<template v-if="x.sprint && x.sprint !== '待规划'"><span>·</span>{{ x.sprint }}</template><template v-if="x.role"><span>·</span>{{ t(roleNames[x.role] || x.role) }}</template><span v-if="view==='assigned'&&attentionReason(x)" :class="['work-attention',x.category]">{{t(attentionReason(x))}}</span><span v-if="selectedKey===personalWorkKey(x)" class="work-last-label">{{t('上次查看')}}</span></p></div><span v-if="x.priority" :class="['priority', x.priority]">{{ x.priority }}</span><span :class="['status','workflow-color',x.status]" :style="workflowStyle(x)">{{ statusLabel(x,[],t) }}</span><time :title="t(view==='favorites'?'收藏时间':x.dueDate?'截止日期':'更新时间')"><small>{{t(view==='favorites'?'收藏':x.dueDate?'截止':'更新')}}</small>{{ formatDate(view==='favorites'?x.favoritedAt:x.dueDate || x.updatedAt) }}</time><span class="chevron" :aria-label="navigationKey===personalWorkKey(x)?t('正在打开…'):undefined">{{navigationKey===personalWorkKey(x)?'…':'›'}}</span></article></div>
  </div>
</template>

<style scoped>
@media(min-width:821px){.work-list article{grid-template-columns:30px minmax(0,1fr) 44px minmax(70px,110px) 125px 16px;gap:10px}.work-list article>.priority{grid-column:3}.work-list article>.status{grid-column:4;white-space:normal;overflow-wrap:anywhere}.work-list article>time{grid-column:5}.work-list article>.chevron{grid-column:6}.work-list .work-main{min-width:0}.work-main>div{flex-wrap:wrap;gap:6px 9px}.work-main b{min-width:0;overflow-wrap:anywhere}}
.work-focus-bar{display:flex;align-items:center;gap:8px;flex-wrap:wrap;padding:7px 0 10px;min-width:0;font-size:12px;color:var(--muted)}.work-focus-bar>span{flex:1 1 280px;line-height:1.6}.work-focus-bar button{flex:none;border:1px solid var(--line);background:var(--surface,#fff);color:var(--muted);border-radius:7px;padding:6px 10px;font:inherit}.work-focus-bar button.active{color:var(--primary);background:var(--primary-soft);border-color:var(--primary)}.work-focus-bar button:focus-visible{outline:2px solid var(--primary);outline-offset:2px}.work-resume{margin-left:auto}.work-list article.last-viewed{box-shadow:inset 3px 0 var(--primary);background:var(--primary-soft)}.work-list article[aria-busy=true]{cursor:progress}.work-main p{display:flex;align-items:center;gap:6px;flex-wrap:wrap}.work-main p>span{margin:0}.work-main .work-attention,.work-main .work-last-label{padding:1px 6px;border-radius:5px;white-space:nowrap;font-size:11px}.work-main .work-attention.overdue{color:#b42318;background:#fef3f2}.work-main .work-attention.due{color:#93370d;background:#fffaeb}.work-main .work-last-label{color:var(--primary);background:var(--surface,#fff)}.work-list article>time{display:flex;gap:5px;align-items:baseline;white-space:nowrap}.work-list article>time small{color:var(--muted);font-size:10px}
.work-sprints{display:flex;align-items:center;gap:6px;overflow-x:auto;max-width:100%;padding:5px 0 9px}.work-sprints>span{font-size:12px;color:var(--muted,#667085);flex:none}.work-sprints button{font:inherit;font-size:12px;white-space:nowrap;flex:none;padding:5px 10px;background:var(--surface,#fff);color:var(--ink,#344054);border:1px solid var(--line,#e4e7ec);border-radius:5px}.work-sprints button[aria-pressed=true]{background:var(--primary-soft,#eef3ff);border-color:var(--primary,#4268df);color:var(--primary,#4268df)}.work-sprints button:focus-visible{outline:2px solid var(--primary,#4268df);outline-offset:2px}
.work-view-switch{display:flex;gap:4px;padding:4px;border:1px solid #e5e7ef;border-radius:9px;background:#f5f6fa;align-self:center}.work-view-switch button{padding:8px 14px;border:0;border-radius:6px;background:transparent;color:#667085;font-size:12px;white-space:nowrap}.work-view-switch button.active{background:#fff;color:#5751c9;box-shadow:0 2px 5px #10182812}.work-view-switch button:focus-visible,.work-tabs button:focus-visible,.work-list article:focus-visible{outline:2px solid #7168ec;outline-offset:3px}.work-filters{flex-wrap:wrap}.work-page .work-filters>select{display:block}.favorite-mark{font-size:14px;color:#c28b15;margin-left:8px}.sort-order{white-space:nowrap}.work-tabs{overflow-x:auto}@media(max-width:760px){.page-heading{flex-wrap:wrap;gap:12px}.work-view-switch{width:100%}.work-view-switch button{flex:1}.work-filters select{max-width:150px}}
</style>

<style scoped>
@media(max-width:820px){
 .work-page{min-width:0}.page-heading{flex-wrap:wrap;gap:12px}.work-view-switch{width:100%;max-width:100%}.work-view-switch button{flex:1;min-width:0;min-height:40px;padding:8px}
 .work-tabs{display:flex;overflow:auto;max-width:100%;gap:7px}.work-tabs button{flex:none;min-width:125px;gap:15px;padding:12px}
 .work-filters{padding:10px 0;gap:8px}.work-filters>.search{flex:1 1 100%;width:100%;min-width:0}.work-filters>select{max-width:100%;min-width:0;flex:1 1 130px}
 .work-list article{grid-template-columns:30px minmax(0,1fr) 22px;gap:9px;padding:15px 12px;align-items:start}
 .work-list article>.object-mark{grid-column:1;grid-row:1/4}.work-list .work-main{grid-column:2;min-width:0}.work-main>div{flex-wrap:wrap;gap:5px 9px}.work-main b,.work-main p{overflow-wrap:anywhere;line-height:1.7}
 .work-list article>.priority{grid-column:2;justify-self:start}.work-list article>.status{grid-column:2;justify-self:start}.work-list article>time{grid-column:2}.work-list article>.chevron{grid-column:3;grid-row:1;justify-self:end}
}
</style>
