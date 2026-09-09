<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { t } from '../i18n'
import { workflowStyle } from '../requirementWorkflow'
import AppSelect from './AppSelect.vue'
import { useSettingsScope } from './settingsScope'

type DependencyEndpoint = {
  tenantId: string
  projectId: string
  projectName: string
  requirementId: number
  code: string
  title: string
  status: string
  statusName?: string
  statusColor?: string
  statusCategory?: string
  statusSystem?: boolean
  isEnd?: boolean
}

type DependencyItem = {
  id: number
  relationType: 'blocks' | 'blocked_by' | 'relates_to'
  counterpart: DependencyEndpoint
  crossProject: boolean
  createdAt: string
  updatedAt: string
}

type DependencyList = { items: DependencyItem[]; page: number; pageSize: number; hasMore: boolean }
type CandidateList = { items: DependencyEndpoint[]; page: number; pageSize: number; hasMore: boolean }

const props = defineProps<{ requirementId: number; canEdit: boolean }>()
const emit = defineEmits<{ (event: 'open', id: number): void }>()
const scope = useSettingsScope()

const items = ref<DependencyItem[]>([])
const candidates = ref<DependencyEndpoint[]>([])
const loading = ref(false)
const loadingMore = ref(false)
const searching = ref(false)
const saving = ref(false)
const error = ref('')
const searchError = ref('')
const picker = ref(false)
const query = ref('')
const pickerScope = ref<'current' | 'all'>('current')
const newRelation = ref<DependencyItem['relationType']>('blocks')
const removeTarget = ref<DependencyItem | null>(null)
const page = ref(1)
const hasMore = ref(false)

const relationOptions = computed(() => [
  { value: 'blocks', label: t('阻塞') },
  { value: 'blocked_by', label: t('被阻塞') },
  { value: 'relates_to', label: t('关联') },
])
const candidateScopeOptions = computed(() => [
  { value: 'current', label: t('当前项目') },
  { value: 'all', label: t('可访问的全部项目') },
])
const eligibleCandidates = computed(() => candidates.value
  .filter(item => Number.isSafeInteger(item.requirementId) && item.requirementId > 0)
  .filter(item => !(item.projectId === scope.project && item.requirementId === props.requirementId))
  .filter(item => !items.value.some(dependency => dependency.counterpart.projectId === item.projectId && dependency.counterpart.requirementId === item.requirementId))
)
// 影响摘要只从已按当前权限过滤后的依赖读取，不能因“统计”而额外泄露跨项目目标。
// blocked_by 表示当前需求尚被前置工作阻塞；blocks 表示当前需求未完成时会影响下游交付。
const incomingBlockers = computed(() => items.value.filter(item => item.relationType === 'blocked_by' && !item.counterpart.isEnd))
const downstreamBlocked = computed(() => items.value.filter(item => item.relationType === 'blocks' && !item.counterpart.isEnd))
const relatedDependencies = computed(() => items.value.filter(item => item.relationType === 'relates_to'))

let epoch = 0
let readVersion = 0
let searchVersion = 0
let disposed = false
let timer: ReturnType<typeof setTimeout> | undefined
const requestTarget = () => ({ epoch, id: props.requirementId, project: scope.project })
const current = (request: ReturnType<typeof requestTarget>) => !disposed && scope.current() && request.epoch === epoch && request.id === props.requirementId && request.project === scope.project
const message = (cause: unknown) => cause instanceof Error ? cause.message : t('依赖操作失败，请重试')

function relationLabel(relation: DependencyItem['relationType']) {
  if (relation === 'blocked_by') return t('被阻塞')
  if (relation === 'relates_to') return t('关联')
  return t('阻塞')
}

function relationClass(relation: DependencyItem['relationType']) {
  return 'dependency-relation-' + relation.replace('_', '-')
}

async function load(nextPage = 1) {
  if (!scope.current()) return
  const request = requestTarget()
  const version = ++readVersion
  if (nextPage === 1) loading.value = true
  else loadingMore.value = true
  error.value = ''
  try {
    const data = await scope.request<DependencyList>(`/requirements/${request.id}/dependencies?` + new URLSearchParams({ page: String(nextPage), pageSize: '20' }))
    if (!current(request) || version !== readVersion) return
    if (!Array.isArray(data.items)) throw Error(t('依赖数据格式不正确'))
    items.value = nextPage === 1 ? data.items : [...items.value, ...data.items.filter(item => !items.value.some(existing => existing.id === item.id))]
    page.value = data.page || nextPage
    hasMore.value = !!data.hasMore
  } catch (cause) {
    if (current(request) && version === readVersion) error.value = message(cause)
  } finally {
    if (current(request) && version === readVersion) {
      loading.value = false
      loadingMore.value = false
    }
  }
}

async function search() {
  if (!scope.current() || !picker.value || !props.canEdit) return
  const request = requestTarget()
  const version = ++searchVersion
  searching.value = true
  searchError.value = ''
  try {
    const data = await scope.request<CandidateList>('/requirement-dependency-candidates?' + new URLSearchParams({
      requirementId: String(request.id),
      scope: pickerScope.value,
      q: query.value.trim(),
      page: '1',
      pageSize: '30',
    }))
    if (!current(request) || version !== searchVersion) return
    if (!Array.isArray(data.items)) throw Error(t('依赖候选数据格式不正确'))
    candidates.value = data.items
  } catch (cause) {
    if (current(request) && version === searchVersion) searchError.value = message(cause)
  } finally {
    if (current(request) && version === searchVersion) searching.value = false
  }
}

function showPicker() {
  if (!props.canEdit || saving.value || loading.value || !scope.current()) return
  picker.value = true
  void search()
}

function hidePicker(force = false) {
  if (saving.value && !force) return
  picker.value = false
  query.value = ''
  candidates.value = []
  searchError.value = ''
}

async function create(candidate: DependencyEndpoint) {
  if (!props.canEdit || saving.value || !scope.current() || !Number.isSafeInteger(candidate.requirementId) || candidate.requirementId <= 0) return
  const request = requestTarget()
  saving.value = true
  error.value = ''
  try {
    await scope.request(`/requirements/${request.id}/dependencies`, {
      method: 'POST',
      body: JSON.stringify({
        targetTenantId: candidate.tenantId,
        targetProjectId: candidate.projectId,
        targetRequirementId: candidate.requirementId,
        relationType: newRelation.value,
      }),
    })
    if (!current(request)) return
    hidePicker(true)
    await load()
  } catch (cause) {
    if (current(request)) error.value = message(cause)
  } finally {
    if (current(request)) saving.value = false
  }
}

async function update(dependency: DependencyItem, relationType: DependencyItem['relationType']) {
  if (!props.canEdit || saving.value || !scope.current() || relationType === dependency.relationType) return
  const request = requestTarget()
  saving.value = true
  error.value = ''
  try {
    const data = await scope.request<{ item?: DependencyItem }>(`/requirements/${request.id}/dependencies/${dependency.id}`, {
      method: 'PATCH', body: JSON.stringify({ relationType }),
    })
    if (!current(request)) return
    if (data.item && Number.isSafeInteger(data.item.id)) items.value = items.value.map(item => item.id === data.item?.id ? data.item : item)
    else await load()
  } catch (cause) {
    if (current(request)) error.value = message(cause)
  } finally {
    if (current(request)) saving.value = false
  }
}

async function remove(dependency: DependencyItem) {
  if (!props.canEdit || saving.value || !scope.current()) return
  const request = requestTarget()
  saving.value = true
  error.value = ''
  try {
    await scope.request(`/requirements/${request.id}/dependencies/${dependency.id}`, { method: 'DELETE' })
    if (!current(request)) return
    removeTarget.value = null
    items.value = items.value.filter(item => item.id !== dependency.id)
    if (!items.value.length && page.value > 1) await load(page.value - 1)
  } catch (cause) {
    if (current(request)) error.value = message(cause)
  } finally {
    if (current(request)) saving.value = false
  }
}

function open(item: DependencyEndpoint) {
  if (saving.value || !scope.current()) return
  if (item.projectId === scope.project) {
    emit('open', item.requirementId)
    return
  }
  // 跨项目需求只从已授权的 API 结果中取得 projectId；完整跳转会让 App 重新校验项目上下文。
  window.location.assign(`/requirements?req=${encodeURIComponent(String(item.requirementId))}&project=${encodeURIComponent(item.projectId)}`)
}

watch(query, () => {
  searchVersion++
  clearTimeout(timer)
  if (picker.value) {
    searching.value = true
    timer = setTimeout(() => void search(), 180)
  }
}, { flush: 'sync' })
watch(pickerScope, () => {
  searchVersion++
  candidates.value = []
  if (picker.value) void search()
})
watch(() => props.requirementId, () => {
  epoch++
  readVersion++
  searchVersion++
  clearTimeout(timer)
  items.value = []
  candidates.value = []
  query.value = ''
  picker.value = false
  removeTarget.value = null
  saving.value = false
  loading.value = false
  loadingMore.value = false
  searching.value = false
  page.value = 1
  hasMore.value = false
  void load()
}, { immediate: true, flush: 'sync' })
watch(scope.locked, value => {
  if (!value) return
  epoch++
  clearTimeout(timer)
  loading.value = false
  loadingMore.value = false
  searching.value = false
  saving.value = false
  items.value = []
  candidates.value = []
}, { flush: 'sync' })
onBeforeRouteLeave(() => !saving.value)
onBeforeRouteUpdate(() => !saving.value)
onBeforeUnmount(() => { disposed = true; epoch++; clearTimeout(timer) })
defineExpose({ saving })
</script>

<template>
  <section class="requirement-dependencies" :aria-label="t('需求依赖')">
    <header>
      <div>
        <h3>{{ t('需求依赖') }}</h3>
        <p>{{ t('在交付前明确前置阻塞与协作关系；跨项目链接仅展示你可访问的项目。') }}</p>
      </div>
      <button v-if="canEdit" class="btn primary compact" type="button" :disabled="loading || saving || scope.locked.value" @click="showPicker">{{ t('添加依赖') }}</button>
    </header>

    <p v-if="scope.locked.value" class="field-error" role="alert">{{ t('项目或账号已变化，请刷新页面后继续') }}</p>
    <p v-if="error" class="field-error" role="alert">{{ t(error) }} <button type="button" class="link" :disabled="loading || saving" @click="load()">{{ t('重试') }}</button></p>
    <p v-if="loading" class="dependencies-empty" role="status">{{ t('加载依赖中…') }}</p>
    <section v-if="!loading && items.length" class="dependency-impact" :aria-label="t('交付影响摘要')">
      <div><span>{{ t('当前阻塞') }}</span><strong>{{ incomingBlockers.length }}</strong><small>{{ incomingBlockers.length ? t('前置需求未完成') : t('没有未完成前置阻塞') }}</small></div>
      <div><span>{{ t('下游影响') }}</span><strong>{{ downstreamBlocked.length }}</strong><small>{{ downstreamBlocked.length ? t('当前需求会影响的未完成需求') : t('没有待跟进的下游需求') }}</small></div>
      <div><span>{{ t('协作关联') }}</span><strong>{{ relatedDependencies.length }}</strong><small>{{ t('不构成交付阻塞') }}</small></div>
      <p v-if="incomingBlockers.length" class="dependency-impact-alert">{{ t('请先处理未完成的前置依赖，再推进当前需求，避免交付状态与实际条件不一致。') }}</p>
    </section>

    <div v-if="picker" class="dependency-picker">
      <div class="dependency-picker-controls">
        <label>
          <span>{{ t('依赖类型') }}</span>
          <AppSelect :model-value="newRelation" :options="relationOptions" :label="t('依赖类型')" :disabled="saving || scope.locked.value" @update:model-value="newRelation=$event as DependencyItem['relationType']" />
        </label>
        <label>
          <span>{{ t('查看范围') }}</span>
          <AppSelect :model-value="pickerScope" :options="candidateScopeOptions" :label="t('查看范围')" :disabled="saving || scope.locked.value" @update:model-value="pickerScope=$event as 'current'|'all'" />
        </label>
        <label class="dependency-search">
          <span>{{ t('搜索可依赖的需求') }}</span>
          <input v-model="query" type="search" :disabled="saving || scope.locked.value" :placeholder="t('搜索编号或标题')">
        </label>
        <button class="btn compact dependency-picker-close" type="button" :disabled="saving" @click="() => hidePicker()">{{ t('取消') }}</button>
      </div>
      <p v-if="searchError" role="alert" class="field-error">{{ t(searchError) }} <button type="button" class="link" :disabled="saving" @click="search">{{ t('重试') }}</button></p>
      <p v-if="searching" role="status">{{ t('搜索中…') }}</p>
      <ul v-else class="dependency-candidates">
        <li v-for="candidate in eligibleCandidates" :key="candidate.projectId + ':' + candidate.requirementId">
          <button type="button" class="dependency-candidate-copy" :disabled="saving || scope.locked.value" @click="create(candidate)">
            <span class="dependency-candidate-main"><b>{{ candidate.code }}</b><span>{{ candidate.title }}</span></span>
            <span class="dependency-candidate-meta"><small v-if="candidate.projectId !== scope.project">{{ candidate.projectName }}</small><small>{{ candidate.statusName || candidate.status }}</small></span>
          </button>
          <button class="btn compact" type="button" :disabled="saving || scope.locked.value" :aria-label="t('添加对 {code} 的依赖',{code:candidate.code})" @click="create(candidate)">{{ t('添加') }}</button>
        </li>
        <li v-if="!eligibleCandidates.length">{{ t('没有可添加的需求') }}</li>
      </ul>
      <small>{{ t('候选仅包含你有权限访问的活动项目；跨项目依赖会在两端重新校验权限。') }}</small>
    </div>

    <div v-if="removeTarget" class="dependency-remove-confirm" role="alertdialog" :aria-label="t('解除依赖')">
      <p>{{ t('确定解除与「{title}」的依赖关系？需求本身不会被删除。',{title:removeTarget.counterpart.title}) }}</p>
      <button class="btn compact" :disabled="saving" type="button" @click="removeTarget=null">{{ t('取消') }}</button>
      <button class="btn compact" :disabled="saving || scope.locked.value" type="button" @click="remove(removeTarget)">{{ t('确认解除') }}</button>
    </div>

    <article v-for="item in items" :key="item.id" class="dependency-item">
      <div class="dependency-item-main">
        <span :class="['dependency-relation', relationClass(item.relationType)]">{{ relationLabel(item.relationType) }}</span>
        <button type="button" class="link dependency-title" :disabled="saving || scope.locked.value" @click="open(item.counterpart)"><b>{{ item.counterpart.code }}</b><span>{{ item.counterpart.title }}</span></button>
        <small v-if="item.crossProject" class="dependency-project">{{ t('跨项目') }} · {{ item.counterpart.projectName }}</small>
      </div>
      <span class="status workflow-color dependency-status" :style="workflowStyle(item.counterpart)">{{ item.counterpart.statusName || item.counterpart.status }}</span>
      <AppSelect v-if="canEdit" class="dependency-relation-select" :model-value="item.relationType" :options="relationOptions" :label="t('更新依赖关系')" :disabled="saving || scope.locked.value" @update:model-value="update(item, $event as DependencyItem['relationType'])" />
      <button v-if="canEdit" type="button" class="link dependency-remove" :disabled="saving || scope.locked.value" :aria-label="t('解除与需求 {code} 的依赖',{code:item.counterpart.code})" @click="removeTarget=item">{{ t('解除依赖') }}</button>
    </article>

    <button v-if="hasMore" class="btn compact dependency-load-more" type="button" :disabled="loadingMore || saving || scope.locked.value" @click="load(page + 1)">{{ loadingMore ? t('加载中…') : t('加载更多') }}</button>
    <p v-if="!loading && !items.length && !error" class="dependencies-empty">{{ t('暂无依赖关系') }}</p>
  </section>
</template>

<style scoped>
.requirement-dependencies>header{display:flex;justify-content:space-between;align-items:flex-start;gap:16px}.requirement-dependencies h3{margin:0;font-size:16px}.requirement-dependencies p,.requirement-dependencies small{font-size:12px;line-height:1.7;color:var(--muted)}.requirement-dependencies button{cursor:pointer}.requirement-dependencies,.requirement-dependencies>header>div{min-width:0}.field-error{color:var(--error-fg,#b42318)!important}.dependency-impact{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:1px;margin:16px 0;border:1px solid var(--line);border-radius:10px;overflow:hidden;background:var(--line)}.dependency-impact>div{display:grid;gap:3px;padding:12px;background:var(--surface);min-width:0}.dependency-impact span,.dependency-impact small{color:var(--muted);font-size:11px}.dependency-impact strong{font-size:21px;line-height:1.15;font-variant-numeric:tabular-nums}.dependency-impact-alert{grid-column:1/-1;margin:0;padding:9px 12px;background:color-mix(in srgb,var(--danger,#b42318) 7%,var(--surface));color:var(--danger,#b42318)!important;font-size:11px!important}.dependency-picker,.dependency-remove-confirm{border:1px solid var(--line);border-radius:10px;padding:15px;background:var(--surface-soft);margin:16px 0}.dependency-picker-controls{display:grid;grid-template-columns:auto auto minmax(180px,1fr) auto;gap:10px;align-items:end}.dependency-picker label{display:grid;gap:6px;min-width:0;font-size:12px;color:var(--muted)}.dependency-picker .app-select-trigger{min-width:145px}.dependency-search input{box-sizing:border-box;min-width:0;width:100%;height:36px;padding:8px 10px;border:1px solid var(--line);border-radius:8px;background:var(--surface);color:var(--ink);font:inherit;font-size:13px}.dependency-search input:focus-visible{outline:2px solid color-mix(in srgb,var(--primary) 28%,transparent);outline-offset:1px;border-color:var(--primary)}.dependency-candidates{list-style:none;padding:0;margin:12px 0;max-height:min(310px,42dvh);overflow:auto;border-top:1px solid var(--line);overscroll-behavior:contain}.dependency-candidates li{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 0;border-bottom:1px solid var(--line);font-size:12px}.dependency-candidate-copy{display:block;min-width:0;flex:1;padding:1px 0;border:0;background:transparent;color:var(--ink);text-align:left}.dependency-candidate-copy:enabled:hover .dependency-candidate-main span{text-decoration:underline;color:var(--primary)}.dependency-candidate-main,.dependency-candidate-meta{display:flex;gap:7px;align-items:baseline;min-width:0}.dependency-candidate-main b{font-variant-numeric:tabular-nums;color:var(--primary);flex:none}.dependency-candidate-main span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.dependency-candidate-meta{margin-top:4px;color:var(--muted)}.dependency-candidate-meta small{font-size:11px}.dependency-remove-confirm button+button{margin-left:8px}.dependency-item{display:grid;grid-template-columns:minmax(0,1fr) auto auto auto;align-items:center;gap:10px;border:1px solid var(--line);border-radius:10px;background:var(--surface);padding:12px;margin-top:10px}.dependency-item-main{min-width:0;display:flex;align-items:center;gap:8px;flex-wrap:wrap}.dependency-relation{display:inline-flex;align-items:center;min-height:22px;padding:2px 7px;border:1px solid;border-radius:999px;font-size:11px;font-weight:650;white-space:nowrap}.dependency-relation-blocks{color:#a15c08;background:#fff7e8;border-color:#f2cf96}.dependency-relation-blocked-by{color:#b42318;background:#fff0f0;border-color:#f5b8b4}.dependency-relation-relates-to{color:#325db6;background:#eff5ff;border-color:#bdd1fa}.dependency-title{display:inline-flex;gap:7px;min-width:0;padding:0;text-align:left;color:var(--ink);font-size:13px}.dependency-title b{color:var(--primary);font-variant-numeric:tabular-nums;flex:none}.dependency-title span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.dependency-project{max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.dependency-status{max-width:150px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.dependency-relation-select{min-width:118px}.dependency-remove{white-space:nowrap;font-size:12px;color:var(--muted)}.dependency-remove:hover:enabled{color:var(--danger,#b42318)}.dependency-load-more{display:block;margin:14px auto 0}.dependencies-empty{padding:24px 0;text-align:center}.dependency-picker-close{align-self:end}
@media(max-width:760px){.requirement-dependencies>header{flex-wrap:wrap}.dependency-impact{grid-template-columns:1fr}.dependency-impact-alert{grid-column:auto}.dependency-picker,.dependency-remove-confirm{padding:12px}.dependency-picker-controls{grid-template-columns:1fr 1fr}.dependency-search{grid-column:1/-1}.dependency-picker-close{justify-self:end}.dependency-candidates li{gap:9px}.dependency-candidate-main span{white-space:normal;overflow-wrap:anywhere}.dependency-item{grid-template-columns:minmax(0,1fr) auto;gap:8px}.dependency-item-main{grid-column:1/-1}.dependency-status{justify-self:start}.dependency-relation-select{justify-self:end;min-width:105px}.dependency-remove{justify-self:end}.requirement-dependencies .btn{min-height:40px;white-space:normal}.dependency-search input{min-height:42px;font-size:16px}}
</style>
