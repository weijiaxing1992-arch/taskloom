<script setup lang="ts">
import Icon from '../components/Icon.vue'
import { t, locale, formatDate } from '../i18n'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api'
import { workflowStyle, statusLabel } from '../requirementWorkflow'
import RequirementCode from '../components/RequirementCode.vue'
import RequirementListExport from '../components/RequirementListExport.vue'

const router = useRouter()
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
const filterCount = computed(() => [type.value, project.value !== 'all', status.value, sprintId.value, sort.value !== 'updatedAt', order.value !== 'desc'].filter(Boolean).length)
const sprintId=ref(''),sprints=ref<{id:number;name:string;projectId:string;projectName:string;status:string}[]>([])
const activeSprints=computed(()=>sprints.value.filter(item=>item.status==='进行中'))
function sprintLabel(item:typeof sprints.value[number]){return (project.value==='all'?item.projectName+' · ':'')+item.name}
function selectSprint(id:string){if(disposed||identityInvalid)return;sprintId.value=sprintId.value===id?'':id}

const sort = ref('updatedAt'), order = ref<'asc' | 'desc'>('desc')
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
  const a = sort.value === 'code' ? Number(left.id) : String(left[sort.value] || '').toLocaleLowerCase(locale.value)
  const b = sort.value === 'code' ? Number(right.id) : String(right[sort.value] || '').toLocaleLowerCase(locale.value)
  if ((a === '') !== (b === '')) return a === '' ? 1 : -1
  const comparison = a < b ? -1 : a > b ? 1 : 0
  return (order.value === 'asc' ? comparison : -comparison) || String(left.projectId).localeCompare(String(right.projectId)) || Number(left.id) - Number(right.id)
}))
let loadVersion = 0, navigationVersion = 0, disposed = false, identityInvalid = false
let assignedCategory = 'active'
const currentProject = () => localStorage.getItem('devflow-project') || 'prj_orbit'
function selectView(next: 'assigned' | 'favorites') {
  if (view.value === next) return
  if (next === 'favorites') { assignedCategory = category.value; category.value = ''; type.value = ''; if (status.value && !requirementStatuses.value.includes(status.value)) status.value = '' }
  else { category.value = assignedCategory; if (sort.value === 'favoritedAt') sort.value = 'updatedAt' }
  view.value = next
}

async function load() {
  if (disposed || identityInvalid) return
  const version = ++loadVersion
  const scope = currentProject()
  loading.value = true
  error.value = ''
  try {
    const params = new URLSearchParams({ category: category.value, q: q.value, type: view.value === 'favorites' ? '需求' : type.value, project: project.value, view: view.value, status: status.value, sort: sort.value, order: order.value, sprintId:sprintId.value })
    const data = await api<any>(`/my-work?${params}`, { headers: { 'X-DevFlow-Project': scope } })
    if (version !== loadVersion || scope !== currentProject() || disposed || identityInvalid) return
    items.value = data.items || []
    sprints.value=Array.isArray(data.sprints)?data.sprints:[]
    requirementStatusDefinitions.value=data.requirementStatuses||[]
    counts.value = data.counts || {}
    if (counts.value.all === undefined) counts.value.all = ['todo', 'doing', 'due', 'overdue', 'completed', 'cancelled'].reduce((sum, key) => sum + Number(counts.value[key] || 0), 0)
  } catch (e: any) { if (version === loadVersion && scope === currentProject() && !disposed && !identityInvalid) error.value = e.message || '操作失败，请稍后重试' }
  finally { if (version === loadVersion && scope === currentProject() && !disposed && !identityInvalid) loading.value = false }
}

async function go(item: any) {
  if (disposed || identityInvalid) return
  const version = ++navigationVersion, scope = currentProject(), changed = item.projectId !== scope
  try {
    // Verify stale cross-project favorites before changing the app's project.
    if (item.favorited) await api(`/requirements/${item.id}/favorite`, { headers: { 'X-DevFlow-Project': item.projectId } })
    else if (changed) await api(`/projects/${encodeURIComponent(item.projectId)}/summary`, { headers: { 'X-DevFlow-Project': item.projectId } })
    if (disposed || identityInvalid || version !== navigationVersion || scope !== currentProject()) return
    if (changed) { localStorage.setItem('devflow-project', item.projectId); location.href = item.url }
    else await router.push(item.url)
  } catch (cause: any) { if (!disposed && !identityInvalid && version === navigationVersion && scope === currentProject()) error.value = cause?.message || '操作失败，请稍后重试' }
}

let timer: any
watch(project,()=>{sprintId.value='';sprints.value=[]},{flush:'sync'})
watch([category, q, type, project, view, status, sort, order, sprintId], () => { loadVersion++; loading.value = true; clearTimeout(timer); timer = setTimeout(load, 120) }, { flush: 'sync' })
function projectChanged() { sprintId.value='';sprints.value=[];void load() }
function favoritesChanged() { if (view.value === 'favorites') void load() }
function identityChanged() { identityInvalid = true; loadVersion++; navigationVersion++; items.value = []; counts.value = {}; sprints.value=[]; requirementStatusDefinitions.value = []; loading.value = false; error.value = '账号身份已在其他页面切换，请刷新后继续' }
onMounted(async () => { window.addEventListener('devflow-project-changed', projectChanged); window.addEventListener('devflow-favorites-changed', favoritesChanged); window.addEventListener('devflow-identity-changed', identityChanged); window.addEventListener('devflow-auth-expired', identityChanged); await load(); try { const data = await api<any>('/projects'); if (!disposed && !identityInvalid) projects.value = data.items || [] } catch (cause: any) { if (!disposed && !identityInvalid) error.value = cause.message || '操作失败，请稍后重试' } })
onBeforeUnmount(() => { disposed = true; clearTimeout(timer); loadVersion++; navigationVersion++; window.removeEventListener('devflow-project-changed', projectChanged); window.removeEventListener('devflow-favorites-changed', favoritesChanged); window.removeEventListener('devflow-identity-changed', identityChanged); window.removeEventListener('devflow-auth-expired', identityChanged) })
</script>

<template>
  <div class="module-page work-page">
    <div class="page-heading compact-page-heading"><div><h1>{{ t("我的工作") }}</h1><details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{ view === 'favorites' ? t('收藏包含其他成员负责的可访问需求。') : t("需求的任意人员字段、职能角色或明确的 @ 提及包含我，都会纳入；同时保留我负责的其他工作项。") }}</p></details></div><RequirementListExport :items="loading?[]:sortedItems.filter(item=>item.type==='需求')" :project-id="currentProject()" :label="t('导出需求')"/><div class="work-view-switch" :aria-label="t('工作视图')"><button type="button" :class="{active:view==='assigned'}" :aria-pressed="view==='assigned'" @click="selectView('assigned')">{{t('与我相关')}}</button><button type="button" :class="{active:view==='favorites'}" :aria-pressed="view==='favorites'" @click="selectView('favorites')"><span aria-hidden="true">☆</span> {{t('我的收藏')}}</button></div></div>
    <nav class="work-tabs" :aria-label="t('工作分类')"><button v-for="x in tabs" :key="x[0]" :aria-pressed="category === x[0]" :class="{ active: category === x[0] }" @click="category = x[0]"><span>{{ t(x[1]) }}</span><b>{{ counts[x[0] || 'all'] || 0 }}</b></button></nav>
    <nav v-if="activeSprints.length" class="work-sprints" :aria-label="t('进行中迭代筛选')"><span>{{t('进行中迭代')}}</span><button type="button" :aria-pressed="!sprintId" @click="sprintId=''">{{t('全部迭代')}}</button><button v-for="item in activeSprints" :key="item.id" type="button" :aria-pressed="sprintId===String(item.id)" @click="selectSprint(String(item.id))">{{sprintLabel(item)}}</button></nav>
    <div class="toolbar module-toolbar work-filters">
      <div class="search"><Icon name="search"/><input type="search" enterkeyhint="search" :aria-label="t('搜索我的工作项')" v-model="q" :placeholder="t('搜索我的工作项')"></div>
      <button type="button" class="btn mobile-filter-toggle" :aria-expanded="filtersExpanded" aria-controls="work-advanced-filters" @click="filtersExpanded=!filtersExpanded">{{t('筛选')}}<span v-if="filterCount"> · {{filterCount}}</span><span aria-hidden="true">{{filtersExpanded?'⌃':'⌄'}}</span></button>
      <div id="work-advanced-filters" class="mobile-advanced-filters" :class="{'is-expanded':filtersExpanded}">
      <select v-if="view !== 'favorites'" :aria-label="t('全部类型')" v-model="type"><option value="">{{ t("全部类型") }}</option><option value="需求">{{ t("需求") }}</option><option value="缺陷">{{ t("缺陷") }}</option><option value="迭代">{{ t("迭代") }}</option><option value="测试用例">{{ t("测试用例") }}</option><option value="测试执行">{{ t("测试执行") }}</option></select>
      <select :aria-label="t('全部项目')" v-model="project"><option value="all">{{ t("全部项目") }}</option><option v-for="x in projects" :key="x.id" :value="x.id">{{ x.name }}</option></select>
      <select v-model="sprintId" :aria-label="t('筛选迭代')"><option value="">{{t('全部迭代')}}</option><option v-for="item in sprints" :key="item.id" :value="String(item.id)">{{sprintLabel(item)}} · {{t(item.status)}}</option></select>
      <select :aria-label="t('全部状态')" v-model="status"><option value="">{{t('全部状态')}}</option><option v-for="value in statusOptions" :key="value" :value="value">{{statusOptionLabel(value)}}</option></select>
      <select :aria-label="t('排序字段')" v-model="sort"><option v-if="view==='favorites'" value="favoritedAt">{{t('收藏时间')}}</option><option value="updatedAt">{{t('更新时间')}}</option><option value="title">{{t('标题')}}</option><option value="priority">{{t('优先级')}}</option><option value="code">{{t('编号')}}</option><option value="dueDate">{{t('截止日期')}}</option></select>
      <button type="button" class="btn sort-order" :aria-label="t(order==='asc'?'升序':'降序')" @click="order=order==='asc'?'desc':'asc'">{{t(order==='asc'?'升序':'降序')}} {{order==='asc'?'↑':'↓'}}</button>
      <button v-if="filterCount" type="button" class="btn" @click="type='';project='all';status='';sprintId='';sort='updatedAt';order='desc'">{{t('重置筛选')}}</button>
      </div>
      <span class="result-count">{{ items.length }} {{ t("项") }}</span>
    </div>
    <div v-if="loading" class="state"><span class="spinner"></span>{{ t("正在汇总个人工作…") }}</div>
    <div v-else-if="error" class="state error" role="alert">{{ t(error) }} <button class="btn" @click="load">{{ t("重试") }}</button></div>
    <div v-else-if="!items.length" class="empty-work"><div>{{view==='favorites'?'☆':'✓'}}</div><h2>{{ view==='favorites'?t('暂无符合条件的收藏需求'):t("这里已经清空了") }}</h2><p>{{ view==='favorites'?t('在需求详情中点击收藏，方便稍后跟进。'):t("当前分类下没有分配给你的工作项。") }}</p></div>
    <div v-else class="work-list"><article v-for="x in sortedItems" :key="`${x.projectId}-${x.type}-${x.id}`" tabindex="0" role="link" @keydown.enter.self="go(x)" @click="go(x)"><span :class="['object-mark', x.type]">{{ t(x.type).slice(0, 1) }}</span><div class="work-main"><div><RequirementCode v-if="x.type==='需求'" class="code" :requirement="x" :project-id="x.projectId" @open="go(x)"/><span v-else class="code">{{ x.code }}</span><b>{{ x.title }}</b><span v-if="x.favorited" class="favorite-mark" :aria-label="t('已收藏')">★</span></div><p>{{ x.projectName }}<span>·</span>{{ x.sprint && x.sprint !== '待规划' ? x.sprint : t("未纳入迭代") }}<span>·</span>{{ t(roleNames[x.role] || x.role || "负责人") }}</p></div><span :class="['priority', x.priority]">{{ x.priority }}</span><span :class="['status','workflow-color',x.status]" :style="workflowStyle(x)">{{ statusLabel(x,[],t) }}</span><time :title="t(view==='favorites'?'收藏时间':'截止日期')">{{ formatDate(view==='favorites'?x.favoritedAt:x.dueDate || x.updatedAt) }}</time><span class="chevron">›</span></article></div>
  </div>
</template>

<style scoped>
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
