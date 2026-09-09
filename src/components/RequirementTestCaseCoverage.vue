<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../api'
import { formatDate, t } from '../i18n'
import RequirementCaseActions from './RequirementCaseActions.vue'

type CaseSummary = {
  id: number
  code: string
  title: string
  status: string
  priority: string
  caseType: string
  category: string
  owner: string
  ownerUserId?: string
  enabled: boolean
  updatedAt: string
  requirement?: { id: number; code: string; title: string; status: string } | null
}
type CaseStats = { total: number; enabled: number; draft: number; pendingReview: number; approved: number; deprecated: number }
type CaseResponse = { items: CaseSummary[]; stats: CaseStats; nextCursor?: string; hasMore?: boolean }

const props = withDefaults(defineProps<{ requirementId: number; disabled?: boolean; limit?: number }>(), { limit: 6 })
const emit = defineEmits<{ (event: 'loaded', value: { total: number; items: CaseSummary[] }): void }>()

const items = ref<CaseSummary[]>([])
const stats = ref<CaseStats>({ total: 0, enabled: 0, draft: 0, pendingReview: 0, approved: 0, deprecated: 0 })
const loading = ref(false)
const error = ref('')
const hasMore = ref(false)
let version = 0
let disposed = false
const actions=ref<InstanceType<typeof RequirementCaseActions>|null>(null)

const libraryPath = computed(() => `/tests?tab=cases&requirement=${props.requirementId}`)
const statItems = computed(() => [
  { key: 'draft', label: '草稿', value: stats.value.draft, tone: 'draft' },
  { key: 'pendingReview', label: '待评审', value: stats.value.pendingReview, tone: 'review' },
  { key: 'approved', label: '已通过', value: stats.value.approved, tone: 'approved' },
])

function message(cause: unknown) { return cause instanceof Error ? cause.message : '关联用例列表加载失败，请重试' }
function validCase(value: unknown): value is CaseSummary {
  if (!value || typeof value !== 'object') return false
  const item = value as Record<string, unknown>
  return Number.isSafeInteger(item.id) && (item.id as number) > 0 && typeof item.code === 'string' && typeof item.title === 'string' && typeof item.status === 'string' && typeof item.priority === 'string' && typeof item.caseType === 'string' && typeof item.updatedAt === 'string'
}
function validStats(value: unknown): value is CaseStats {
  if (!value || typeof value !== 'object') return false
  const stat = value as Record<string, unknown>
  return ['total', 'enabled', 'draft', 'pendingReview', 'approved', 'deprecated'].every(key => Number.isSafeInteger(stat[key]) && (stat[key] as number) >= 0)
}
function statusTone(status: string) { return ({ '草稿': 'draft', '待评审': 'review', '已通过': 'approved', '已废弃': 'deprecated' } as Record<string, string>)[status] || 'neutral' }
// 需求侧覆盖与用例库共享同一套类型语义色，避免性能/自动化这两种有效类型
// 在追溯视图中退化为无色标签，影响质量状态的快速扫读。
function typeTone(type: string) { return ({ '功能测试': 'functional', '接口测试': 'api', '兼容性测试': 'compatibility', '安全测试': 'security', '性能测试': 'performance', '自动化测试': 'automation' } as Record<string, string>)[type] || 'neutral' }

async function load() {
  const id = props.requirementId
  if (!Number.isSafeInteger(id) || id < 1 || disposed) return
  const request = ++version
  loading.value = true
  error.value = ''
  try {
    const data = await api<CaseResponse>(`/requirements/${id}/test-cases?limit=${Math.min(20, Math.max(1, props.limit))}`)
    if (disposed || request !== version || id !== props.requirementId) return
    if (!data || !Array.isArray(data.items) || !validStats(data.stats) || data.items.some(item => !validCase(item))) throw Error('关联用例列表返回格式不正确，请重试')
    items.value = data.items
    stats.value = data.stats
    hasMore.value = data.hasMore === true
    emit('loaded', { total: data.stats.total, items: data.items })
  } catch (cause) {
    if (!disposed && request === version) error.value = message(cause)
  } finally {
    if (!disposed && request === version) loading.value = false
  }
}

watch(() => props.requirementId, () => {
  items.value = []
  stats.value = { total: 0, enabled: 0, draft: 0, pendingReview: 0, approved: 0, deprecated: 0 }
  hasMore.value = false
  void load()
}, { immediate: true })
onBeforeUnmount(() => { disposed = true; version++ })
defineExpose({ refresh: load, canLeave:()=>actions.value?.canLeave()!==false })
</script>

<template>
  <section class="requirement-case-coverage" :aria-busy="loading">
    <header class="coverage-heading">
      <div>
        <span class="coverage-kicker">{{ t('质量覆盖') }}</span>
        <h3>{{ t('测试用例覆盖') }} <b>{{ stats.total }}</b></h3>
        <p>{{ t('用例关联需求后，可在这里追踪覆盖与评审状态。') }}</p>
      </div>
      <div class="coverage-actions">
        <router-link class="btn compact" :to="libraryPath">{{ t('查看用例库') }}</router-link>
        <button v-if="!disabled" type="button" class="btn compact" @click="actions?.associate()">{{t('关联已有用例')}}</button>
        <button v-if="!disabled" type="button" class="btn primary compact" @click="actions?.open()">{{ t('创建关联用例') }}</button>
      </div>
    </header>

    <p v-if="error" class="coverage-error" role="alert">{{ t(error) }} <button class="link" type="button" :disabled="loading" @click="load">{{ t('重试') }}</button></p>
    <p v-else-if="loading && !items.length" class="coverage-loading" role="status">{{ t('正在加载关联用例…') }}</p>

    <div v-if="!loading || items.length" class="coverage-stats" :aria-label="t('测试用例覆盖')">
      <span v-for="item in statItems" :key="item.key" :class="['coverage-stat', item.tone]"><b>{{ item.value }}</b><small>{{ t(item.label) }}</small></span>
      <span class="coverage-stat enabled"><b>{{ stats.enabled }}</b><small>{{ t('已启用') }}</small></span>
    </div>

    <div v-if="items.length" class="coverage-list">
      <button v-for="item in items" :key="item.id" type="button" class="coverage-case" @click="actions?.open(item.id)">
        <span class="coverage-case-main"><code>{{ item.code }}</code><b>{{ item.title }}</b><small v-if="item.category">{{ item.category }}</small></span>
        <span class="coverage-case-meta"><em :class="['coverage-type', typeTone(item.caseType)]">{{ t(item.caseType) }}</em><em :class="['coverage-status', statusTone(item.status)]">{{ t(item.status) }}</em><span class="coverage-owner">{{ item.owner || t('未分配') }}</span><time>{{ formatDate(item.updatedAt) }}</time></span>
      </button>
    </div>
    <p v-else-if="!loading && !error" class="coverage-empty">{{ t('暂无关联测试用例') }}</p>
    <footer v-if="hasMore" class="coverage-more"><span>{{ t('仅展示最新 {count} 条，可在用例库继续查看。', { count: items.length }) }}</span><router-link :to="libraryPath">{{ t('查看全部') }}</router-link></footer>
    <RequirementCaseActions :key="requirementId" ref="actions" :requirement-id="requirementId" :disabled="disabled" @saved="load"/>
  </section>
</template>

<style scoped>
.coverage-case{width:100%;background:transparent;border:0;border-radius:0;text-align:left;cursor:pointer}
.requirement-case-coverage{border:1px solid var(--line);border-radius:12px;background:var(--surface);overflow:hidden}.coverage-heading{display:flex;justify-content:space-between;gap:18px;padding:18px 19px 15px;border-bottom:1px solid var(--line)}.coverage-heading>div{min-width:0}.coverage-kicker{display:block;color:var(--primary);font-size:10px;font-weight:700;letter-spacing:.07em}.coverage-heading h3{display:flex;align-items:center;gap:8px;margin:5px 0 5px;color:var(--ink);font-size:16px}.coverage-heading h3 b{display:inline-grid;place-items:center;min-width:22px;height:22px;padding:0 6px;border-radius:999px;background:var(--primary-soft);color:var(--primary);font-size:11px;font-variant-numeric:tabular-nums}.coverage-heading p{margin:0;color:var(--muted);font-size:11px;line-height:1.7}.coverage-actions{display:flex;align-items:flex-start;gap:8px;flex-wrap:wrap}.coverage-actions .btn{white-space:nowrap;text-decoration:none}.coverage-error,.coverage-loading,.coverage-empty{margin:14px 19px;color:var(--muted);font-size:12px;line-height:1.7}.coverage-error{color:var(--danger,#bf3944)}.coverage-stats{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));border-bottom:1px solid var(--line);background:var(--surface-soft)}.coverage-stat{display:flex;align-items:baseline;gap:6px;padding:11px 15px;border-right:1px solid var(--line);min-width:0}.coverage-stat:last-child{border-right:0}.coverage-stat b{font-size:17px;font-variant-numeric:tabular-nums}.coverage-stat small{overflow:hidden;color:var(--muted);font-size:10px;text-overflow:ellipsis;white-space:nowrap}.coverage-stat.draft b{color:#7a6fce}.coverage-stat.review b{color:#b57617}.coverage-stat.approved b,.coverage-stat.enabled b{color:#25845f}.coverage-list{display:grid}.coverage-case{display:flex;align-items:center;justify-content:space-between;gap:15px;padding:12px 19px;border-bottom:1px solid var(--line);color:inherit;text-decoration:none}.coverage-case:hover{background:var(--surface-soft)}.coverage-case:focus-visible{outline:2px solid var(--ring);outline-offset:-2px}.coverage-case-main,.coverage-case-meta{display:flex;align-items:center;gap:8px;min-width:0}.coverage-case-main{flex:1}.coverage-case-main code{flex:none;color:var(--primary);font-size:11px}.coverage-case-main b{overflow:hidden;color:var(--ink);font-size:12px;text-overflow:ellipsis;white-space:nowrap}.coverage-case-main small{flex:none;max-width:100px;overflow:hidden;color:var(--muted);font-size:10px;text-overflow:ellipsis;white-space:nowrap}.coverage-case-meta{flex:none;color:var(--muted);font-size:10px}.coverage-case-meta em{font-style:normal;white-space:nowrap}.coverage-type,.coverage-status{padding:3px 6px;border:1px solid var(--line);border-radius:999px;background:var(--surface);font-size:10px}.coverage-type.functional{color:#3d6edb;background:#eef4ff;border-color:#d7e5ff}.coverage-type.api{color:#7d56bb;background:#f5efff;border-color:#e5d8ff}.coverage-type.compatibility{color:#31776d;background:#ebfbf6;border-color:#ccefe3}.coverage-type.security{color:#b15b2a;background:#fff3eb;border-color:#f7d8c1}.coverage-type.performance{color:#9a5f05;background:#fff8df;border-color:#f6dc9b}.coverage-type.automation{color:#276d8d;background:#eaf8ff;border-color:#bde7f7}.coverage-status.draft{color:#6d61b8;background:#f4f2ff;border-color:#ded9ff}.coverage-status.review{color:#9a6812;background:#fff8e8;border-color:#f5dea1}.coverage-status.approved{color:#1f7b57;background:#ebfaf3;border-color:#b9e8d0}.coverage-status.deprecated{color:#717987;background:#f5f6f8}.coverage-owner{max-width:90px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.coverage-case time{white-space:nowrap;color:var(--muted);font-variant-numeric:tabular-nums}.coverage-more{display:flex;justify-content:space-between;gap:12px;padding:11px 19px;color:var(--muted);font-size:11px}.coverage-more a{color:var(--primary);font-weight:600;text-decoration:none}@media(max-width:760px){.coverage-heading{align-items:flex-start;flex-direction:column}.coverage-actions{width:100%}.coverage-actions .btn{flex:1}.coverage-stats{grid-template-columns:repeat(2,minmax(0,1fr))}.coverage-stat:nth-child(2){border-right:0}.coverage-stat:nth-child(-n+2){border-bottom:1px solid var(--line)}.coverage-case{align-items:flex-start;flex-direction:column;gap:8px}.coverage-case-main,.coverage-case-meta{width:100%}.coverage-case-meta{flex-wrap:wrap}.coverage-case time{margin-left:auto}.coverage-owner{margin-left:auto}.coverage-case-main small{max-width:72px}}
</style>
