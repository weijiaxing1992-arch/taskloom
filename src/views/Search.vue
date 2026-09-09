<script setup lang="ts">
import Icon from '../components/Icon.vue'
import { t, locale, formatDate } from '../i18n'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api'
import { workflowStyle, statusLabel as requirementStatusLabel } from '../requirementWorkflow'
import RequirementCode from '../components/RequirementCode.vue'
import RequirementListExport from '../components/RequirementListExport.vue'

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
let searchVersion = 0
let searchController: AbortController | undefined
let disposed = false
const composing = ref(false)
const statusNames: Record<string, string> = { active: '进行中', archived: '已归档' }
const groups = computed(() => Object.entries(items.value.reduce((all: any, item: any) => { (all[item.type] || (all[item.type] = [])).push(item); return all }, {})))
const statusLabel = (value: string) => t(statusNames[value] || value)

async function search() {
  if (disposed || composing.value) return
  clearTimeout(timer)
  const version = ++searchVersion
  searchController?.abort()
  const controller = new AbortController()
  searchController = controller
  loading.value = true
  error.value = ''
  try {
    const params = new URLSearchParams({ q: q.value, type: type.value, project: project.value })
    const data = await api<any>(`/search?${params}`, { signal: controller.signal })
    if (version !== searchVersion) return
    items.value = data.items || []
    total.value = data.total
    searched.value = true
  } catch (cause: any) { if (version === searchVersion) error.value = cause.message || '操作失败，请稍后重试' }
  finally { if (version === searchVersion) loading.value = false }
}

function go(item: any) {
  localStorage.setItem('devflow-project', item.projectId)
  location.href = item.url
}

let timer: any
function schedule() {
  // 输入一变就令旧请求失效，避免防抖等待期间把旧结果当成新结果展示。
  clearTimeout(timer); searchVersion++; searchController?.abort()
  loading.value = true; error.value = ''
  if (!composing.value) timer = setTimeout(search, 250)
}
function compositionStart() { composing.value = true; schedule() }
function compositionEnd() { composing.value = false; schedule() }
function submitSearch(event: KeyboardEvent) { if (!event.isComposing && !composing.value) void search() }
watch([q, type, project], schedule, { flush: 'sync' })
watch(() => route.query.q, value => { q.value = String(value || '') })
function projectChanged() { void search() }
onMounted(async () => { window.addEventListener('devflow-project-changed', projectChanged); void search(); try { const data = await api<any>('/projects'); if (!disposed) projects.value = data.items || [] } catch (cause: any) { if (!disposed) error.value = cause.message || '操作失败，请稍后重试' } })
onBeforeUnmount(() => { disposed = true; clearTimeout(timer); searchVersion++; searchController?.abort(); window.removeEventListener('devflow-project-changed', projectChanged) })
</script>

<template>
  <div class="module-page search-page">
    <div class="search-hero"><span class="eyebrow">{{ t("跨项目检索") }}</span><div class="search-heading"><h1>{{ t("全局搜索") }}</h1><RequirementListExport :items="loading?[]:items.filter(item=>item.type==='需求')" :project-id="project||'prj_orbit'" :label="t('导出需求')"/></div><div class="global-search-box"><Icon name="search"/><input :aria-label="t('搜索内容、负责人、开发人员或部门')" v-model="q" type="search" enterkeyhint="search" autocomplete="off" @compositionstart="compositionStart" @compositionend="compositionEnd" @keydown.enter="submitSearch" :placeholder="t('搜索内容、负责人、开发人员或部门')"><kbd>⌘ K</kbd></div><div class="search-filters"><select :aria-label="t('全部类型')" v-model="type"><option value="">{{ t("全部类型") }}</option><option value="项目">{{ t("项目") }}</option><option value="需求">{{ t("需求") }}</option><option value="迭代">{{ t("迭代") }}</option><option value="缺陷">{{ t("缺陷") }}</option><option value="测试用例">{{ t("测试用例") }}</option><option value="测试计划">{{ t("测试计划") }}</option></select><select :aria-label="t('全部可访问项目')" v-model="project"><option value="">{{ t("全部可访问项目") }}</option><option v-for="x in projects" :key="x.id" :value="x.id">{{ x.name }}</option></select><span>{{ total }} {{ t("条结果") }}</span></div></div>
    <div v-if="loading" class="state"><span class="spinner"></span>{{ t("正在检索…") }}</div>
    <div v-else-if="error" class="state error" role="alert">{{ t(error) }} <button class="btn" @click="search">{{ t("重试") }}</button></div><div v-else-if="searched && !items.length" class="empty-work"><div>⌕</div><h2>{{ t("没有找到匹配内容") }}</h2><p>{{ t("试试更短的关键词，或清除类型与项目筛选。") }}</p></div>
    <section v-else class="search-results"><div v-for="group in groups" :key="group[0]" class="search-group"><header><h2>{{ t(group[0]) }}</h2><span>{{ (group[1] as any[]).length }}</span></header><article v-for="x in group[1] as any[]" :key="`${x.projectId}-${x.type}-${x.id}`" tabindex="0" role="link" @keydown.enter.self="go(x)" @click="go(x)"><span :class="['object-mark', x.type]">{{ t(x.type).slice(0, 1) }}</span><div><div><RequirementCode v-if="x.type==='需求'" class="code" :requirement="x" :project-id="x.projectId" @open="go(x)"/><span v-else class="code">{{ x.code }}</span><b>{{ x.title }}</b><span :class="['status','workflow-color',x.status]" :style="workflowStyle(x)">{{x.statusName?requirementStatusLabel(x,[],t):statusLabel(x.status)}}</span></div><p>{{ x.snippet || t('暂无摘要') }}</p><small>{{ x.projectName }} {{ t("· 更新于") }} {{ formatDate(x.updatedAt) }}</small></div><span class="chevron">›</span></article></div></section>
  </div>
</template>

<style scoped>
.search-heading{display:flex;align-items:center;justify-content:center;gap:16px;flex-wrap:wrap}.search-heading h1{margin:10px 0}
@media(max-width:820px){
 .search-page{min-width:0}.search-hero{padding:22px 16px}.global-search-box{min-width:0;padding:0 11px;gap:8px}.global-search-box input{min-width:0;width:0;font-size:14px}.global-search-box kbd{display:none}
 .search-filters{flex-wrap:wrap;gap:8px}.search-filters select{min-width:0;max-width:100%;flex:1 1 125px}.search-filters span{margin-left:0;flex-basis:100%}
 .search-results{padding:16px 0}.search-group{min-width:0}.search-group article{grid-template-columns:28px minmax(0,1fr) 16px;padding:14px 12px;gap:9px;align-items:start}.search-group article>div{min-width:0}.search-group article>div>div{flex-wrap:wrap;gap:6px 9px}.search-group article b,.search-group article p,.search-group article small{overflow-wrap:anywhere;line-height:1.7}.search-group article .status{max-width:100%}
}
</style>
