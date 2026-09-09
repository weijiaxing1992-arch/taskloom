<script setup lang="ts">
import Icon from '../components/Icon.vue'
import { t, formatDate } from '../i18n'
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { layoutScope } from '../layoutScope'
import ResizableDrawer from '../components/ResizableDrawer.vue'
import OrganizationModal from '../components/OrganizationModal.vue'
import ProjectMembers from '../components/ProjectMembers.vue'
import '../organization.css'

const route = useRoute(), router = useRouter()
const items = ref<any[]>([])
const q = ref('')
const status = ref('active')
const show = ref(false)
const editing = ref<any>(null)
const drawerWidth = ref(780)
const error = ref('')
const canCreate = ref(false)
const canManageMembers = ref(false), managingMembers = ref<any>(null), createdProject = ref<any>(null)
const canViewArchived = ref(false), notice = ref(''), locked = ref(false)
const loading = ref(false)
const recentLoading = ref(false), recentError = ref('')
const saving = ref(false)
const navigating = ref(false)
type ProjectAction = 'archive' | 'restore' | 'delete'
const action = ref<{ kind: ProjectAction; project: any } | null>(null), confirmation = ref('')
const controller = new AbortController()
let disposed = false, loadVersion = 0, recentLoadVersion = 0
const recentContent = ref<any[]>([])
const favorites = ref<string[]>([])
// 收藏仅用于当前账号的项目排序，不代表项目访问权限。旧版 `devflow-favorites`
// 没有租户/账号归属，无法安全判断属于谁；因此不读取或迁移，避免共享浏览器时串给下一位登录者。
function favoritesStorageKey() { return layoutScope.value ? `devflow-layout:v1:${layoutScope.value}:projects.favorites` : '' }
function savedFavorites(): string[] {
  const key = favoritesStorageKey()
  if (!key) return []
  try { const values = JSON.parse(localStorage.getItem(key) || '[]'); return Array.isArray(values) ? values.filter(x => typeof x === 'string') : [] } catch { return [] }
}
function restoreFavorites() { favorites.value = savedFavorites() }
watch(layoutScope, restoreFavorites, { immediate: true, flush: 'sync' })
const form = reactive<any>({ name: '', code: '', description: '', ownerUserId: '', icon: '', color: '#5B5BD6' })
const message = (cause: unknown) => cause instanceof Error ? cause.message : '操作失败，请稍后重试'
function openEdit(project: any) { if (locked.value || saving.value || !project.canManage || project.status !== 'active') return; editing.value = { ...project }; error.value = '' }

// 项目管理属于企业，不绑定当前选中的业务项目。身份失效则中止请求与待确认操作；
// 删除始终使用打开确认框时的稳定 ID / 编码，不受后来排序、搜索或同名项目影响。
async function request<T = any>(path: string, options: RequestInit = {}): Promise<T> {
  if (disposed || locked.value) throw new Error('账号已变化，请刷新后继续')
  const result = await api<T>(path, { ...options, signal: controller.signal })
  if (disposed || locked.value) throw new Error('账号已变化，请刷新后继续')
  return result
}
function invalidate() { locked.value = true; loadVersion++; controller.abort(); action.value = null; confirmation.value = ''; editing.value = null; show.value = false; managingMembers.value = null; createdProject.value = null; canManageMembers.value = false }
function openMembers(project: any) {
  if (locked.value || saving.value || navigating.value || !canManageMembers.value || project.status !== 'active' || managingMembers.value || action.value || show.value || editing.value) return
  managingMembers.value = { id: project.id, name: project.name, code: project.code }
}
function membersChanged(projectId: string) {
  if (locked.value || disposed || managingMembers.value?.id !== projectId) return
  void load()
  window.dispatchEvent(new CustomEvent('devflow-project-list-changed'))
}
function actionAllowed(project: any, kind: ProjectAction) {
  return !locked.value && (kind === 'archive' ? project.status === 'active' && project.canManage === true : project.status === 'archived' && (kind === 'restore' ? project.canRestore === true : project.canDelete === true))
}
function beginAction(project: any, kind: ProjectAction) {
  if (saving.value || !actionAllowed(project, kind)) return
  action.value = { kind, project: { ...project } }; confirmation.value = ''; error.value = ''; notice.value = ''
}
function closeAction() { if (!saving.value) { action.value = null; confirmation.value = '' } }
async function performAction() {
  const current = action.value
  if (!current || saving.value || !actionAllowed(current.project, current.kind)) return
  if (current.kind === 'delete' && confirmation.value !== current.project.code) { error.value = '请输入完整项目代号以确认删除'; return }
  saving.value = true; error.value = ''
  try {
    const path = '/projects/' + encodeURIComponent(current.project.id)
    const result = await request(current.kind === 'delete' ? path : path + '/' + current.kind, { method: current.kind === 'delete' ? 'DELETE' : 'POST', body: JSON.stringify(current.kind === 'delete' ? { confirmCode: confirmation.value } : {}) })
    const expected = current.kind === 'restore' ? 'active' : current.kind === 'archive' ? 'archived' : 'deleted'
    if (result.status !== expected) throw new Error('操作结果未确认，请刷新项目列表核实')
    if (editing.value?.id === current.project.id) editing.value = null
    action.value = null; confirmation.value = ''
    notice.value = current.kind === 'restore' ? '项目已恢复，可重新进入协作' : current.kind === 'archive' ? '项目已归档，可在已归档中管理' : '项目已删除，历史业务数据已保留'
    await load()
    await loadRecent()
    window.dispatchEvent(new CustomEvent('devflow-project-list-changed'))
  } catch (cause) { if (!disposed && !locked.value) error.value = message(cause) }
  finally { saving.value = false }
}
const actionTitle = computed(() => action.value?.kind === 'delete' ? '删除归档项目' : action.value?.kind === 'restore' ? '恢复项目' : '归档项目')
function selectStatus(value: string) {
  if (value === 'archived' && !canViewArchived.value) return
  status.value = value
  void router.replace({ query: { ...route.query, status: value || undefined } })
}

const filtered = computed(() => items.value
  .filter(x => (!q.value || (`${x.name} ${x.code} ${x.description}`).toLowerCase().includes(q.value.toLowerCase())) && (!status.value || x.status === status.value))
  .sort((a, b) => Number(favorites.value.includes(b.id)) - Number(favorites.value.includes(a.id))))

async function load() {
  if (locked.value || disposed) return
  const version = ++loadVersion
  loading.value = true
  try {
    const data = await request('/projects')
    if (version !== loadVersion) return
    items.value = (data.items || []).filter((item: any) => item.status === 'active' || (data.canViewArchived === true && item.status === 'archived'))
    canCreate.value = data.canCreate === true; canViewArchived.value = data.canViewArchived === true
    canManageMembers.value = data.canManageMembers === true
    if (status.value === 'archived' && !canViewArchived.value) status.value = 'active'
  }
  catch (cause) { if (!disposed && !locked.value && version === loadVersion) error.value = message(cause) }
  finally { if (version === loadVersion) loading.value = false }
}

// 首页动态只展示服务端已按项目成员关系裁剪过的最小摘要。前端仍校验跳转路径，
// 防止异常响应把“最近内容”变成外部跳转入口。
async function loadRecent() {
  if (locked.value || disposed) return
  const version = ++recentLoadVersion
  recentLoading.value = true
  recentError.value = ''
  try {
    const data = await request('/home/recent-content?limit=8')
    if (version !== recentLoadVersion) return
    recentContent.value = Array.isArray(data.items) ? data.items : []
  } catch (cause) {
    if (!disposed && !locked.value && version === recentLoadVersion) recentError.value = message(cause)
  } finally {
    if (version === recentLoadVersion) recentLoading.value = false
  }
}

function recentTypeLabel(item: any) {
  return item.objectType === 'defect' ? t('缺陷') : item.objectType === 'sprint' ? t('迭代') : t('需求')
}

async function openRecent(item: any) {
  const projectID = typeof item?.projectId === 'string' ? item.projectId : ''
  const target = typeof item?.url === 'string' ? item.url : ''
  if (navigating.value || locked.value || !projectID || !/^\/(?!\/)/.test(target)) return
  navigating.value = true
  error.value = ''
  try {
    await request(`/projects/${encodeURIComponent(projectID)}/visit`, { method: 'POST', headers: { 'X-DevFlow-Project': projectID } })
    localStorage.setItem('devflow-project', projectID)
    location.href = target
  } catch (cause) {
    if (!disposed && !locked.value) error.value = message(cause)
  } finally { navigating.value = false }
}

function toggleFav(id: string) {
  favorites.value = favorites.value.includes(id) ? favorites.value.filter(x => x !== id) : [...favorites.value, id]
  const key = favoritesStorageKey()
  if (!key) return
  try { localStorage.setItem(key, JSON.stringify(favorites.value)) } catch { error.value = '收藏偏好无法保存，请检查浏览器存储权限' }
}

async function enter(project: any) {
  if (navigating.value || locked.value || managingMembers.value || project.status === 'archived' || project.status === 'deleted') return
  navigating.value = true
  error.value = ''
  try {
    await request(`/projects/${encodeURIComponent(project.id)}/visit`, { method: 'POST', headers: { 'X-DevFlow-Project': project.id } })
    localStorage.setItem('devflow-project', project.id)
    location.href = '/requirements'
  } catch (cause) {
    if (!disposed && !locked.value) error.value = message(cause)
  } finally { navigating.value = false }
}

async function create() {
  if (saving.value || locked.value || !canCreate.value) return
  saving.value = true
  error.value = ''
  try {
    const project = await request('/projects', { method: 'POST', body: JSON.stringify(form) })
    show.value = false
    createdProject.value = { ...project, status: 'active' }
    notice.value = '项目已创建，可先管理成员或进入项目'
    await load()
    window.dispatchEvent(new CustomEvent('devflow-project-list-changed'))
  } catch (cause) { error.value = message(cause) }
  finally { saving.value = false }
}

async function patch(project: any, values: any) {
  if (saving.value || locked.value || !project.canManage || project.status !== 'active') return
  saving.value = true; error.value = ''
  try {
    await request(`/projects/${encodeURIComponent(project.id)}`, { method: 'PATCH', body: JSON.stringify(values) })
    if (editing.value?.id === project.id) editing.value = null
    await load()
  } catch (cause) { error.value = message(cause) }
  finally { saving.value = false }
}

function refreshHome() { void load(); void loadRecent() }

onMounted(async () => {
  for (const event of ['devflow-auth-expired', 'devflow-identity-changed', 'devflow-account-disabled', 'devflow-password-change-required']) window.addEventListener(event, invalidate)
  if (route.query.status === 'archived') status.value = 'archived'
  await Promise.all([load(), loadRecent()])
  if (route.query.project) { const project = items.value.find(x => x.id === route.query.project && x.canManage); if (project) openEdit(project) }
  window.addEventListener('devflow-project-changed', refreshHome)
})
watch(() => route.query.status, value => { status.value = value === 'archived' && canViewArchived.value ? 'archived' : value === '' ? '' : 'active' })
onBeforeUnmount(() => { disposed = true; loadVersion++; recentLoadVersion++; controller.abort(); window.removeEventListener('devflow-project-changed', refreshHome); for (const event of ['devflow-auth-expired', 'devflow-identity-changed', 'devflow-account-disabled', 'devflow-password-change-required']) window.removeEventListener(event, invalidate) })
</script>

<template>
  <div class="module-page projects-page">
    <div class="page-heading compact-page-heading">
      <div><h1>{{ t("项目空间") }}</h1><details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{ t("切换你参与的产品研发空间，数据与成员权限按项目严格隔离。") }}</p></details></div>
      <button v-if="canCreate" class="btn primary" :disabled="locked || saving" @click="show = true">{{ t("＋ 创建项目") }}</button>
    </div>
    <p v-if="locked" class="field-error" role="alert">{{ t('账号已变化，请刷新后继续') }}</p>
    <p v-if="error" class="field-error" role="alert">{{ t(error) }}</p><p v-if="notice" class="project-notice" role="status">{{ t(notice) }}</p><div v-if="createdProject" class="project-created-actions"><b>{{ createdProject.name }}</b><button v-if="canManageMembers" type="button" class="btn" :disabled="locked || saving" @click="openMembers(createdProject)">{{ t('管理项目成员') }}</button><button type="button" class="btn primary" :disabled="locked || saving || navigating" @click="enter(createdProject)">{{ t('进入项目') }}</button><button type="button" class="link" :aria-label="t('关闭创建结果')" @click="createdProject = null">×</button></div><p v-if="loading" role="status">{{ t("正在加载…") }}</p>
    <div class="project-status-tabs" :aria-label="t('项目状态')"><button type="button" :class="{ active: status === 'active' }" :aria-pressed="status === 'active'" @click="selectStatus('active')">{{ t('进行中') }}<span>{{ items.filter(item => item.status === 'active').length }}</span></button><button v-if="canViewArchived" type="button" :class="{ active: status === 'archived' }" :aria-pressed="status === 'archived'" @click="selectStatus('archived')">{{ t('已归档') }}<span>{{ items.filter(item => item.status === 'archived').length }}</span></button><small v-if="canViewArchived">{{ t('归档项目仅管理员可见和管理') }}</small></div>
    <div v-if="status === 'archived'" class="project-archive-hint">{{ t('归档后暂停项目协作。恢复将保留原有成员、需求、迭代、缺陷及附件。') }}</div>
    <div class="toolbar module-toolbar">
      <div class="search"><Icon name="search"/><input type="search" enterkeyhint="search" :aria-label="t('搜索项目名称、代号或描述')" v-model="q" :placeholder="t('搜索项目名称、代号或描述')"></div>
      <span class="result-count">{{ filtered.length }} {{ t("个项目") }}</span>
    </div>
    <div class="project-card-grid">
      <article v-for="x in filtered" :key="x.id" class="project-card" :class="{ 'archived-project': x.status === 'archived' }" :tabindex="x.status === 'active' ? 0 : -1" :role="x.status === 'active' ? 'link' : undefined" :aria-label="x.name" @keydown.enter.self="enter(x)" @click="enter(x)">
        <header>
          <span class="project-logo" :style="{ background: x.color }">{{ x.icon || x.name.slice(0, 1) }}</span>
          <div><div class="project-title"><h2>{{ x.name }}</h2><span>{{ x.code }}</span></div><p>{{ x.description || t("暂无项目描述") }}</p></div>
          <button class="favorite" :class="{ active: favorites.includes(x.id) }" @click.stop="toggleFav(x.id)" :aria-label="t(favorites.includes(x.id) ? '取消收藏' : '收藏项目')">{{ favorites.includes(x.id) ? '★' : '☆' }}</button>
        </header>
        <div class="project-metrics"><span><b>{{ x.requirements }}</b>{{ t("需求") }}</span><span><b>{{ x.defects }}</b>{{ t("缺陷") }}</span><span><b>{{ x.sprints }}</b>{{ t("迭代") }}</span><span><b>{{ x.testCases }}</b>{{ t("用例") }}</span><span><b>{{ x.members }}</b>{{ t("成员") }}</span></div>
        <footer><span><i :class="['dot', x.status === 'active' ? 'green' : 'gray']"></i>{{ x.status === 'active' ? t("进行中") : t("已归档") }}</span><span>{{ t("负责人") }} {{ x.owner || t("未设置") }}</span><span>{{ x.status === 'archived' ? t('归档于 {date}', {date: formatDate(x.archivedAt)}) : x.lastVisitedAt ? t("最近访问 {date}", { date: formatDate(x.lastVisitedAt) }) : t("尚未访问") }}</span><div class="project-card-actions"><button v-if="x.status === 'active' && canManageMembers" type="button" class="link" :disabled="locked || saving" @click.stop="openMembers(x)">{{ t('管理成员') }}</button><button v-if="x.status === 'active' && x.canManage" class="link" :disabled="locked || saving" @click.stop="openEdit(x)">{{ t("管理") }}</button><button v-if="x.canRestore" type="button" class="btn compact project-restore" :disabled="locked || saving" @click.stop="beginAction(x, 'restore')">{{ t('恢复项目') }}</button><button v-if="x.canDelete" type="button" class="btn compact danger-outline" :disabled="locked || saving" @click.stop="beginAction(x, 'delete')">{{ t('删除') }}</button></div></footer>
      </article>
      <div v-if="!loading && !filtered.length" class="state compact-state"><b>{{ t("没有符合条件的项目") }}</b><p>{{ t("调整搜索或状态筛选后再试。") }}</p></div>
    </div>

    <section v-if="status !== 'archived'" class="recent-content" :aria-busy="recentLoading" aria-labelledby="recent-content-title">
      <header class="recent-content-head"><div><span class="eyebrow">{{ t('回到工作上下文') }}</span><h2 id="recent-content-title">{{ t('最近内容') }}</h2><p>{{ t('按你有权访问项目的最近更新排列；点击即可进入对应项目。') }}</p></div><button type="button" class="btn compact" :disabled="recentLoading || locked" @click="loadRecent">{{ t(recentLoading ? '正在刷新…' : '刷新内容') }}</button></header>
      <p v-if="recentError" class="field-error" role="alert">{{ t(recentError) }}</p>
      <div v-else-if="recentLoading && !recentContent.length" class="recent-loading" role="status">{{ t('正在加载最近内容…') }}</div>
      <div v-else-if="recentContent.length" class="recent-content-grid">
        <button v-for="item in recentContent" :key="item.objectType + ':' + item.projectId + ':' + item.id" type="button" class="recent-content-item" :disabled="navigating || locked" @click="openRecent(item)">
          <span class="recent-content-type" :class="item.objectType">{{ recentTypeLabel(item) }}</span>
          <span class="recent-content-copy"><b>{{ item.title }}</b><small>{{ t(item.event) }} · {{ item.projectName }}<template v-if="item.projectCode"> · {{ item.projectCode }}</template> · {{ formatDate(item.updatedAt) }}</small></span>
          <span class="recent-content-arrow" aria-hidden="true">›</span>
        </button>
      </div>
      <div v-else class="recent-empty"><b>{{ t('暂时没有最近内容') }}</b><p>{{ t('创建、更新或进入项目后，相关内容会出现在这里。') }}</p></div>
    </section>

    <div v-if="show" class="modal-shade" @click.self="!saving && (show = false)"><div class="modal">
      <header><h2>{{ t("创建项目") }}</h2><button :disabled="saving" @click="show = false" :aria-label="t('关闭')">×</button></header>
      <div class="modal-body form-grid"><label>{{ t("项目名称 *") }}</label><input :aria-label="t('项目名称 *')" v-model="form.name"><label>{{ t("项目代号 *") }}</label><input :aria-label="t('项目代号 *')" v-model="form.code" maxlength="12" :placeholder="t('例如 CORE')"><label>{{ t("项目描述") }}</label><textarea :aria-label="t('项目描述')" v-model="form.description"></textarea><label>{{ t("图标文字") }}</label><input :aria-label="t('图标文字')" v-model="form.icon" maxlength="1"><label>{{ t("主题色") }}</label><input :aria-label="t('主题色')" v-model="form.color" type="color"><p v-if="error" class="field-error">{{ t(error) }}</p></div>
      <footer><button class="btn" :disabled="saving" @click="show = false">{{ t("取消") }}</button><button class="btn primary" :disabled="saving" @click="create">{{ t(saving ? '创建中…' : '创建项目') }}</button></footer>
    </div></div>

    <div v-if="editing" class="drawer-shade" @click.self="!saving && (editing = null)"><ResizableDrawer v-model:width="drawerWidth" storage-key="projects.settings.width" :initial-width="780" :min-width="480" :label="t('项目设置')" class="module-detail-drawer">
      <header class="drawer-head"><div class="drawer-title"><h2>{{ t("项目设置") }}</h2><button :disabled="saving" @click="editing = null" :aria-label="t('关闭')">×</button></div><p>{{ editing.code }}</p></header>
      <div class="drawer-body"><section class="detail-main form-stack"><p v-if="error" class="field-error" role="alert">{{ t(error) }}</p><label>{{ t("名称") }}<input v-model="editing.name" :disabled="saving"></label><label>{{ t("描述") }}<textarea v-model="editing.description" :disabled="saving"></textarea></label><button class="btn primary" :disabled="saving || locked" @click="patch(editing, { name: editing.name, description: editing.description })">{{ t("保存基础信息") }}</button><button class="btn danger-outline" :disabled="saving || locked" @click="beginAction(editing, 'archive')">{{ t("归档项目") }}</button></section></div>
    </ResizableDrawer></div>

    <ProjectMembers v-if="managingMembers" :project="managingMembers" @close="managingMembers = null" @changed="membersChanged" />

    <OrganizationModal v-if="action" :title="t(actionTitle)" :busy="saving" @close="closeAction">
      <form id="project-lifecycle-confirm" class="project-confirm-form" @submit.prevent="performAction">
        <div class="project-confirm-summary"><b>{{ action.project.name }}</b><code>{{ action.project.code }}</code></div>
        <p v-if="action.kind === 'restore'">{{ t('恢复后，原项目成员可继续访问和编辑，历史数据保持不变。') }}</p>
        <p v-else-if="action.kind === 'archive'">{{ t('归档后，普通成员将无法进入此项目。企业管理员可随时在已归档中恢复。') }}</p>
        <template v-else><p class="project-delete-warning">{{ t('删除后不再显示该项目，不能在页面中恢复。此操作保留历史数据和审计，不执行物理清理。') }}</p><p>{{ t('项目包含 {requirements} 条需求、{defects} 条缺陷、{sprints} 次迭代。', {requirements: action.project.requirements, defects: action.project.defects, sprints: action.project.sprints}) }}</p><label>{{ t('输入项目代号 {code} 确认删除', {code: action.project.code}) }}<input v-model="confirmation" :aria-label="t('项目代号确认')" :disabled="saving" autocomplete="off" spellcheck="false" required></label></template>
        <p v-if="error" role="alert" class="field-error">{{ t(error) }}</p>
      </form>
      <template #footer><button type="button" class="btn" :disabled="saving" @click="closeAction">{{ t('取消') }}</button><button type="submit" form="project-lifecycle-confirm" class="btn" :class="action.kind === 'delete' ? 'project-delete-button' : 'primary'" :disabled="saving || locked || (action.kind === 'delete' && confirmation !== action.project.code)">{{ t(saving ? '处理中…' : action.kind === 'delete' ? '确认删除项目' : action.kind === 'archive' ? '确认归档' : '确认恢复') }}</button></template>
    </OrganizationModal>
  </div>
</template>

<style scoped>
.project-created-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin:10px 0 16px;padding:14px;border:1px solid var(--line);border-radius:10px;background:var(--surface)}.project-created-actions>b{margin-right:auto;overflow-wrap:anywhere;font-size:14px}.project-created-actions>button{min-height:38px}
.project-status-tabs{display:flex;align-items:center;gap:8px;flex-wrap:wrap;border-bottom:1px solid var(--line);margin:20px 0 12px;padding-bottom:10px}.project-status-tabs button{display:flex;align-items:center;gap:8px;padding:9px 14px;border:0;border-radius:8px;background:transparent;color:var(--muted);font:inherit;font-size:13px;cursor:pointer}.project-status-tabs button.active{color:var(--primary);background:color-mix(in srgb,var(--primary) 10%,var(--surface))}.project-status-tabs button span{border-radius:6px;padding:1px 6px;background:color-mix(in srgb,var(--muted) 10%,transparent);font-size:11px;font-variant-numeric:tabular-nums}.project-status-tabs button:focus-visible{outline:2px solid var(--primary);outline-offset:2px}.project-status-tabs small{margin-left:auto;color:var(--muted);font-size:12px}.project-archive-hint,.project-notice{padding:12px 14px;font-size:13px;line-height:1.7;border-radius:8px;background:var(--surface);border:1px solid var(--line);color:var(--muted)}.project-notice{color:var(--ink);border-left:3px solid var(--primary)}.project-card.archived-project{cursor:default;border-style:dashed}.project-card.archived-project:hover{transform:none}.project-card footer{flex-wrap:wrap;gap:10px}.project-card-actions{display:flex;align-items:center;gap:8px;margin-left:auto}.project-restore{color:var(--primary);border-color:color-mix(in srgb,var(--primary) 40%,var(--line))}.project-confirm-form{display:grid;gap:16px;font-size:13px;line-height:1.8;color:var(--ink)}.project-confirm-summary{display:flex;flex-wrap:wrap;gap:10px;align-items:center;overflow-wrap:anywhere}.project-confirm-summary code{color:var(--muted);background:var(--surface-soft);padding:2px 8px;border-radius:5px}.project-confirm-form p{margin:0}.project-confirm-form label{display:grid;gap:8px}.project-confirm-form input{min-width:0;width:100%;border:1px solid var(--line);border-radius:6px;background:var(--surface);color:var(--ink);padding:10px 12px;font:inherit;box-sizing:border-box}.project-delete-warning{color:var(--danger,#c24145);padding:12px;background:color-mix(in srgb,var(--danger,#c24145) 8%,var(--surface));border-radius:6px}.project-delete-button{background:var(--danger,#c24145);color:#fff;border-color:var(--danger,#c24145)}
.recent-content{margin-top:28px;padding:22px;border:1px solid var(--line);border-radius:14px;background:var(--surface)}.recent-content-head{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:16px}.recent-content-head h2{margin:3px 0 6px;font-size:18px;letter-spacing:-.01em}.recent-content-head p{margin:0;color:var(--muted);font-size:12px;line-height:1.7}.recent-content-head .btn{flex:none}.recent-content-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}.recent-content-item{display:flex;align-items:center;min-width:0;gap:10px;width:100%;padding:12px;border:1px solid var(--line);border-radius:10px;background:var(--surface);color:var(--ink);font:inherit;text-align:left;cursor:pointer;transition:border-color .16s ease,box-shadow .16s ease,transform .16s ease}.recent-content-item:hover:not(:disabled){border-color:color-mix(in srgb,var(--primary) 42%,var(--line));box-shadow:0 6px 18px color-mix(in srgb,var(--primary) 12%,transparent);transform:translateY(-1px)}.recent-content-item:focus-visible{outline:2px solid var(--primary);outline-offset:2px}.recent-content-item:disabled{cursor:wait;opacity:.7}.recent-content-type{display:grid;place-items:center;flex:none;min-width:34px;height:24px;padding:0 6px;border-radius:7px;background:var(--primary-soft);color:var(--primary);font-size:11px;font-weight:700}.recent-content-type.defect{background:#fff0ef;color:#d35443}.recent-content-type.sprint{background:#eefaf5;color:#16855f}.recent-content-copy{display:grid;min-width:0;gap:4px;flex:1}.recent-content-copy b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:13px}.recent-content-copy small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--muted);font-size:11px}.recent-content-arrow{flex:none;color:var(--muted);font-size:20px;line-height:1}.recent-loading,.recent-empty{padding:22px 12px;text-align:center;color:var(--muted);font-size:13px}.recent-empty b{display:block;color:var(--ink);margin-bottom:5px}.recent-empty p{margin:0;font-size:12px;line-height:1.7}
@media(max-width:820px){
 .project-status-tabs small{flex-basis:100%;margin-left:0}.project-status-tabs button,.project-card-actions button{min-height:44px}.project-confirm-form input{font-size:16px}.project-card-actions{flex-wrap:wrap}
 .projects-page{min-width:0}.page-heading{flex-wrap:wrap;gap:12px}.project-card-grid{grid-template-columns:minmax(0,1fr);max-width:100%}.project-card{min-width:0;padding:16px}.project-card header{gap:10px}.project-card header>div,.project-title{min-width:0}.project-title,.project-card header p{overflow-wrap:anywhere}.project-card footer{flex-wrap:wrap;gap:10px}.project-card .favorite{min-width:38px;min-height:38px;flex:none}.project-metrics{gap:10px}.project-metrics>*{min-width:0}
 .module-toolbar{padding:10px 0;flex-wrap:wrap}.module-toolbar .search{width:100%;min-width:0;flex:1 1 100%}.module-toolbar select{min-width:0;max-width:100%}
 .recent-content{padding:16px}.recent-content-head{align-items:center}.recent-content-head p{display:none}.recent-content-grid{grid-template-columns:minmax(0,1fr)}.recent-content-item{min-height:58px}.recent-content-copy b,.recent-content-copy small{white-space:normal;overflow-wrap:anywhere}
 .module-detail-drawer .drawer-head{padding:16px;flex:none}.drawer-title{gap:10px;flex-wrap:wrap}.drawer-title h2{min-width:0;overflow-wrap:anywhere}.drawer-title button{min-height:40px}.drawer-body{overflow:auto;display:block}.detail-main{padding:18px 16px;width:100%;min-width:0;overflow:visible}.form-stack{min-width:0}
 .form-grid{grid-template-columns:minmax(0,1fr);gap:9px}.form-grid>input,.form-grid>textarea{min-width:0;width:100%}
}
</style>
