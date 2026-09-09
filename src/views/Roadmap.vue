<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { t } from '../i18n'
import { workflowStyle } from '../requirementWorkflow'
import { useSettingsScope } from '../components/settingsScope'
import AppSelect from '../components/AppSelect.vue'

type DependencyStatus = {
  state: 'clear' | 'blocked' | 'blocking' | 'related'
  blockedByCount: number
  blockingCount: number
  relatedCount: number
}

type RoadmapItem = {
  id: number
  code: string
  title: string
  tenantId: string
  projectId: string
  projectName: string
  sprint: string
  startDate: string
  endDate: string
  status: string
  statusName?: string
  statusColor?: string
  statusCategory?: string
  statusSystem?: boolean
  isEnd?: boolean
  priority: string
  dependencyStatus: DependencyStatus
}

type RoadmapResponse = { items: RoadmapItem[]; page: number; pageSize: number; hasMore: boolean; scope: 'current' | 'all' }
type RoadmapGroup = { key: string; label: string; items: RoadmapItem[] }

const scope = useSettingsScope()
const viewScope = ref<'current' | 'all'>('current')
const items = ref<RoadmapItem[]>([])
const page = ref(1)
const hasMore = ref(false)
const loading = ref(false)
const error = ref('')
let epoch = 0
let disposed = false

const scopeOptions = computed(() => [
  { value: 'current', label: t('当前项目路线图') },
  { value: 'all', label: t('我的可访问项目') },
])
const requestTarget = () => ({ epoch, project: scope.project, scope: viewScope.value })
const current = (request: ReturnType<typeof requestTarget>) => !disposed && scope.current() && request.epoch === epoch && request.project === scope.project && request.scope === viewScope.value
const message = (cause: unknown) => cause instanceof Error ? cause.message : t('路线图暂时无法读取，请稍后重试')

function groupFor(item: RoadmapItem): Pick<RoadmapGroup, 'key' | 'label'> {
  const sprint = item.sprint.trim()
  if (sprint && sprint !== '待规划') return { key: `sprint:${sprint}`, label: t('迭代 · {sprint}', { sprint }) }
  const start = item.startDate.trim(), end = item.endDate.trim()
  if (start || end) return { key: `date:${start}:${end}`, label: t('计划 {start} 至 {end}', { start: start || '—', end: end || '—' }) }
  return { key: 'unscheduled', label: t('尚未排期') }
}

const groups = computed<RoadmapGroup[]>(() => {
  const result: RoadmapGroup[] = []
  const byKey = new Map<string, RoadmapGroup>()
  for (const item of items.value) {
    const info = groupFor(item)
    let group = byKey.get(info.key)
    if (!group) {
      group = { ...info, items: [] }
      byKey.set(info.key, group)
      result.push(group)
    }
    group.items.push(item)
  }
  return result
})

function dependencyLabel(status: DependencyStatus) {
  if (status.state === 'blocked') return t('被阻塞 · {count} 项前置未完成', { count: status.blockedByCount })
  if (status.state === 'blocking') return t('阻塞 {count} 项待完成需求', { count: status.blockingCount })
  if (status.state === 'related') return t('关联 {count} 项协作需求', { count: status.relatedCount })
  return t('无依赖阻塞')
}

async function load(nextPage = page.value) {
  if (!scope.current()) return
  const request = requestTarget()
  loading.value = true
  error.value = ''
  try {
    const data = await scope.request<RoadmapResponse>('/roadmap?' + new URLSearchParams({ scope: request.scope, page: String(nextPage), pageSize: '50' }))
    if (!current(request)) return
    if (!Array.isArray(data.items)) throw Error(t('路线图数据格式不正确'))
    items.value = data.items
    page.value = data.page || nextPage
    hasMore.value = !!data.hasMore
  } catch (cause) {
    if (current(request)) error.value = message(cause)
  } finally {
    if (current(request)) loading.value = false
  }
}

function changeScope(value: string | number) {
  const next = value === 'all' ? 'all' : 'current'
  if (next === viewScope.value) return
  viewScope.value = next
}

function open(item: RoadmapItem) {
  if (loading.value || !scope.current()) return
  if (item.projectId === scope.project) {
    window.location.assign(`/requirements?req=${encodeURIComponent(String(item.id))}`)
    return
  }
  // 跨项目入口只接受已由路线图受权查询返回的 scope；App 会再次校验 project 参数。
  window.location.assign(`/requirements?req=${encodeURIComponent(String(item.id))}&project=${encodeURIComponent(item.projectId)}`)
}

watch(viewScope, () => {
  epoch++
  page.value = 1
  hasMore.value = false
  items.value = []
  void load(1)
})
watch(scope.locked, value => {
  if (!value) return
  epoch++
  loading.value = false
  items.value = []
})
watch(() => scope.project, () => {
  epoch++
  page.value = 1
  hasMore.value = false
  items.value = []
  void load(1)
})
void load(1)
onBeforeUnmount(() => { disposed = true; epoch++ })
</script>

<template>
  <main class="roadmap-page page-shell" :aria-label="t('交付路线图')">
    <header class="roadmap-hero compact-page-heading">
      <div>

        <h1>{{ t('交付路线图') }}</h1>
        <details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{ t('按迭代和计划日期汇总可访问需求，优先处理被阻塞的交付项。') }}</p></details>
      </div>
      <div class="roadmap-actions">
        <AppSelect :model-value="viewScope" :options="scopeOptions" :label="t('查看范围')" :disabled="loading || scope.locked.value" @update:model-value="changeScope" />
        <button class="btn" type="button" :disabled="loading || scope.locked.value" @click="load()">{{ t('刷新路线图') }}</button>
      </div>
    </header>

    <p v-if="scope.locked.value" class="field-error" role="alert">{{ t('项目或账号已变化，请刷新页面后继续') }}</p>
    <section v-else class="roadmap-board">
      <p v-if="error" class="field-error" role="alert">{{ t(error) }} <button type="button" class="link" :disabled="loading" @click="load()">{{ t('重试') }}</button></p>
      <p v-if="loading" class="roadmap-empty" role="status">{{ t('加载路线图中…') }}</p>
      <template v-else>
        <section v-for="group in groups" :key="group.key" class="roadmap-group">
          <header><h2>{{ group.label }}</h2><span>{{ t('{count} 条需求',{count:group.items.length}) }}</span></header>
          <div class="roadmap-items">
            <article v-for="item in group.items" :key="item.projectId + ':' + item.id" class="roadmap-item" :class="'dependency-' + item.dependencyStatus.state">
              <button type="button" class="roadmap-item-main" :aria-label="t('打开需求 {code}',{code:item.code})" @click="open(item)">
                <span class="roadmap-rail" aria-hidden="true"></span>
                <span class="roadmap-copy"><small class="roadmap-code">{{ item.code }}</small><b>{{ item.title }}</b><small v-if="viewScope === 'all'" class="roadmap-project">{{ t('项目：{project}', {project:item.projectName}) }}</small></span>
              </button>
              <div class="roadmap-item-meta">
                <span class="status workflow-color" :style="workflowStyle(item)">{{ item.statusName || item.status }}</span>
                <span class="roadmap-priority">{{ item.priority || '—' }}</span>
                <span class="roadmap-dependency" :class="'is-' + item.dependencyStatus.state">{{ dependencyLabel(item.dependencyStatus) }}</span>
              </div>
            </article>
          </div>
        </section>
        <p v-if="!groups.length" class="roadmap-empty">{{ t('没有匹配的路线图需求') }}</p>
        <nav v-if="items.length || page > 1" class="roadmap-pagination" :aria-label="t('路线图分页')">
          <button class="btn compact" type="button" :disabled="loading || page <= 1" @click="load(page - 1)">{{ t('上一页') }}</button>
          <span>{{ t('第 {page} 页',{page}) }}</span>
          <button class="btn compact" type="button" :disabled="loading || !hasMore" @click="load(page + 1)">{{ t('下一页') }}</button>
        </nav>
      </template>
    </section>
  </main>
</template>

<style scoped>
.roadmap-page{max-width:1440px;margin:0 auto;padding:30px 32px 52px}.roadmap-hero{display:flex;justify-content:space-between;align-items:flex-end;gap:24px;margin-bottom:22px}.roadmap-hero h1{margin:3px 0 8px;font-size:28px;letter-spacing:-.03em}.roadmap-hero p{margin:0;color:var(--muted);line-height:1.65}.eyebrow{font-size:12px;color:var(--primary)!important;font-weight:700}.roadmap-actions{display:flex;gap:10px;align-items:center;flex:none}.roadmap-board{border:1px solid var(--line);border-radius:14px;background:var(--surface);padding:18px}.field-error{color:var(--error-fg,#b42318)}.roadmap-group+.roadmap-group{margin-top:26px}.roadmap-group>header{display:flex;align-items:center;justify-content:space-between;gap:12px;padding-bottom:10px;border-bottom:1px solid var(--line)}.roadmap-group h2{margin:0;font-size:15px}.roadmap-group header span{font-size:12px;color:var(--muted)}.roadmap-items{position:relative;padding-top:11px}.roadmap-items::before{content:"";position:absolute;top:17px;bottom:17px;left:17px;width:1px;background:var(--line)}.roadmap-item{display:flex;align-items:center;gap:14px;min-width:0;padding:11px 10px;border:1px solid transparent;border-radius:10px}.roadmap-item:hover{background:var(--surface-soft);border-color:var(--line)}.roadmap-item-main{display:flex;align-items:flex-start;gap:11px;min-width:0;flex:1;border:0;padding:0;background:transparent;color:var(--ink);text-align:left;cursor:pointer}.roadmap-rail{position:relative;z-index:1;display:block;flex:none;width:14px;height:14px;margin:4px 0 0;border:3px solid var(--surface);border-radius:50%;background:var(--muted)}.dependency-blocked .roadmap-rail{background:#d92d20}.dependency-blocking .roadmap-rail{background:#d97706}.dependency-related .roadmap-rail{background:var(--primary)}.roadmap-copy{display:grid;gap:3px;min-width:0}.roadmap-code{color:var(--primary);font-variant-numeric:tabular-nums}.roadmap-copy b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:14px}.roadmap-project{color:var(--muted);font-size:11px}.roadmap-item-meta{display:flex;align-items:center;justify-content:flex-end;gap:8px;flex-wrap:wrap;min-width:300px}.roadmap-priority{font-size:11px;color:var(--muted);font-variant-numeric:tabular-nums}.roadmap-dependency{max-width:190px;padding:4px 7px;border-radius:999px;background:var(--surface-soft);color:var(--muted);font-size:11px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.roadmap-dependency.is-blocked{background:#fff0f0;color:#b42318}.roadmap-dependency.is-blocking{background:#fff7e8;color:#a15c08}.roadmap-dependency.is-related{background:#eff5ff;color:#325db6}.roadmap-empty{padding:44px 12px;text-align:center;color:var(--muted);font-size:13px}.roadmap-pagination{display:flex;align-items:center;justify-content:flex-end;gap:10px;padding-top:18px;margin-top:18px;border-top:1px solid var(--line);font-size:12px;color:var(--muted)}
@media(max-width:760px){.roadmap-page{padding:20px 14px 36px}.roadmap-hero{align-items:stretch;flex-direction:column;margin-bottom:16px}.roadmap-hero h1{font-size:24px}.roadmap-actions{justify-content:space-between;flex-wrap:wrap}.roadmap-actions .app-select-trigger{flex:1;min-width:0}.roadmap-board{padding:12px}.roadmap-item{align-items:flex-start;flex-direction:column;gap:8px;padding:11px 5px 11px 0}.roadmap-item-main{width:100%}.roadmap-copy b{white-space:normal;overflow-wrap:anywhere}.roadmap-item-meta{justify-content:flex-start;min-width:0;padding-left:25px}.roadmap-dependency{max-width:min(78vw,300px)}.roadmap-pagination{justify-content:space-between}.roadmap-pagination .btn{min-height:38px}}
</style>
