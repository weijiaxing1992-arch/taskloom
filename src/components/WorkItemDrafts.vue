<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { APIError, api } from '../api'
import { t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import { MAX_WORK_ITEM_DRAFTS_PER_SCOPE, cloneDraftObject, draftBytes, draftExportFilename, draftPreview, draftReadableHTML, draftTitle, newWorkItemDraftId, validatedDraftObject, workItemDraftScope, type DraftObject, type WorkItemDraftKind } from '../workItemDrafts'
import { useSettingsDialog } from './settingsScope'

/**
 * 需求和缺陷共用的私有草稿箱。
 *
 * 自动保存只写浏览器 IndexedDB 与 /api/drafts，绝不会调用创建/更新需求或缺陷的接口。
 * 草稿范围用已验证会话的 tenant/user/project 组合隔离；切换账号、项目、代访问状态或
 * 禁用账号后会立即停止同步并清空内存列表，避免把 A 的草稿展示给 B。
 */
const props = withDefaults(defineProps<{
  kind: WorkItemDraftKind
  targetId?: string
  context?: Record<string, unknown>
  payload: Record<string, unknown>
  ready: boolean
  dirty: boolean
  busy?: boolean
  restoreId?: string
  compact?: boolean
}>(), { targetId: '', context: () => ({}), busy: false, restoreId: '', compact: false })
const emit = defineEmits<{ (event: 'restore', payload: DraftObject, context: DraftObject, draft: { id: string; version: number; kind: WorkItemDraftKind; targetId: string }): void }>()

type DraftState = 'synced' | 'local' | 'conflict' | 'metadata'
type DraftRecord = {
  localKey: string
  scope: string
  id: string
  kind: WorkItemDraftKind
  targetId: string
  title: string
  context: DraftObject
  payload: DraftObject
  version: number
  createdAt: string
  updatedAt: string
  state: DraftState
  hasPayload: boolean
}
type DraftListResponse = { items?: Array<Partial<DraftRecord> & { id?: string; kind?: WorkItemDraftKind; updatedAt?: string; createdAt?: string; version?: number; targetId?: string; title?: string }> }
type DraftDetailResponse = { draft?: Partial<DraftRecord> & { id?: string; payload?: unknown; context?: unknown }; id?: string; payload?: unknown; context?: unknown }

const DB_NAME = 'devflow-workitem-drafts-v1', STORE_NAME = 'drafts', REMOTE_BODY_LIMIT = 29 * 1024 * 1024
const workspace = useWorkspaceStore()
const records = ref<DraftRecord[]>([]), inboxOpen = ref(false), selectedId = ref(''), pendingDeleteId = ref('')
const loading = ref(false), saving = ref(false), remoteSyncing = ref(false), message = ref(''), error = ref(''), localUnavailable = ref(false)
const activeId = ref(''), activeVersion = ref(0), activeCreatedAt = ref(''), disposed = ref(false)
const scopeDraftCount = ref(0)
let saveTimer: ReturnType<typeof setTimeout> | undefined, periodicTimer: ReturnType<typeof setInterval> | undefined, sequence = 0, database: Promise<IDBDatabase> | null = null

const session = computed(() => workspace.session)
const scopeKey = computed(() => {
  const context = session.value
  if (!context || context.impersonation || workspace.identityConflict) return ''
  try { return workItemDraftScope(context.tenant.id, context.user.id, context.project.id) } catch { return '' }
})
const canRead = computed(() => !!scopeKey.value && props.ready && !workspace.mustChangePassword && !workspace.operationDisabled)
const canWrite = computed(() => canRead.value && session.value?.user.role !== 'viewer')
const currentTargetId = computed(() => {
  const value = String(props.targetId || '').trim()
  return /^[1-9]\d*$/.test(value) ? value : ''
})
const selected = computed(() => records.value.find(record => record.id === selectedId.value) || null)
const activeRecord = computed(() => records.value.find(record => record.id === activeId.value) || null)
const countLabel = computed(() => records.value.length ? String(records.value.length) : '')
const statusText = computed(() => {
  if (localUnavailable.value) return t('浏览器本地草稿不可用；网络正常时仍会尝试保存到私有草稿箱')
  if (remoteSyncing.value) return t('正在同步私有草稿箱…')
  if (activeRecord.value?.state === 'conflict') return t('检测到另一页面的草稿版本，已保留两个版本')
  if (activeRecord.value?.state === 'local') return t('已保存到本机，等待私有草稿箱同步')
  if (activeRecord.value?.state === 'synced') return t('已保存到本机和私有草稿箱')
  return t('编辑后将每 30 秒自动保存')
})

function normalRecord(value: Partial<DraftRecord>, scope = scopeKey.value): DraftRecord | null {
  if (!scope || (value.kind !== 'requirement' && value.kind !== 'defect') || typeof value.id !== 'string' || !/^[0-9a-f-]{16,80}$/i.test(value.id)) return null
  const now = new Date().toISOString()
  let context: DraftObject = Object.create(null) as DraftObject, payload: DraftObject = Object.create(null) as DraftObject
  try {
    if (value.context && typeof value.context === 'object') context = cloneDraftObject(value.context)
    if (value.payload && typeof value.payload === 'object') payload = cloneDraftObject(value.payload)
  } catch { return null }
  const targetId = typeof value.targetId === 'string' && /^[1-9]\d*$/.test(value.targetId) ? value.targetId : ''
  const version = Number.isSafeInteger(value.version) && Number(value.version) >= 0 ? Number(value.version) : 0
  const hasPayload = value.hasPayload === true || Object.keys(payload).length > 0
  return {
    localKey: `${scope}:${value.kind}:${value.id}`, scope, id: value.id, kind: value.kind, targetId,
    title: typeof value.title === 'string' && value.title.trim() ? value.title.trim().slice(0, 120) : draftTitle(payload),
    context, payload, version, createdAt: typeof value.createdAt === 'string' ? value.createdAt : now,
    updatedAt: typeof value.updatedAt === 'string' ? value.updatedAt : now,
    state: value.state === 'synced' || value.state === 'conflict' || value.state === 'metadata' ? value.state : 'local', hasPayload,
  }
}
function isCurrent(scope = scopeKey.value, token = sequence) { return !disposed.value && !!scope && scope === scopeKey.value && token === sequence && !workspace.identityConflict && !session.value?.impersonation }
function replaceRecord(next: DraftRecord) { records.value = [...records.value.filter(record => record.id !== next.id), next].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)) }
function removeRecord(id: string) { records.value = records.value.filter(record => record.id !== id); if (selectedId.value === id) selectedId.value = ''; if (activeId.value === id) { activeId.value = ''; activeVersion.value = 0; activeCreatedAt.value = '' } }
function report(cause: unknown, fallback: string) { error.value = cause instanceof Error ? cause.message : t(fallback) }
function resetMessages() { error.value = ''; message.value = '' }

function openDatabase(): Promise<IDBDatabase> {
  if (database) return database
  if (typeof indexedDB === 'undefined') return Promise.reject(new Error('当前浏览器不支持本地草稿存储'))
  const opening: Promise<IDBDatabase> = new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1)
    request.onupgradeneeded = () => {
      const db = request.result
      const store = db.objectStoreNames.contains(STORE_NAME) ? request.transaction!.objectStore(STORE_NAME) : db.createObjectStore(STORE_NAME, { keyPath: 'localKey' })
      if (!store.indexNames.contains('scope')) store.createIndex('scope', 'scope', { unique: false })
    }
    request.onsuccess = () => { request.result.onversionchange = () => request.result.close(); resolve(request.result) }
    request.onerror = () => reject(request.error || new Error('无法打开本地草稿箱'))
    request.onblocked = () => reject(new Error('本地草稿箱正被其他旧页面占用，请关闭旧页面后重试'))
  }).catch((cause): never => { database = null; localUnavailable.value = true; throw cause })
  database = opening
  return opening
}
function requestValue<T>(request: IDBRequest<T>) { return new Promise<T>((resolve, reject) => { request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error || new Error('本地草稿箱操作失败')) }) }
function transactionDone(transaction: IDBTransaction) { return new Promise<void>((resolve, reject) => { transaction.oncomplete = () => resolve(); transaction.onabort = () => reject(transaction.error || new Error('本地草稿箱操作被取消')); transaction.onerror = () => reject(transaction.error || new Error('本地草稿箱操作失败')) }) }
async function localRecords(scope: string): Promise<DraftRecord[]> {
  const db = await openDatabase(), transaction = db.transaction(STORE_NAME, 'readonly'), store = transaction.objectStore(STORE_NAME)
  const index = store.index('scope'), found = await requestValue(index.getAll(IDBKeyRange.only(scope)))
  await transactionDone(transaction)
  return (found as unknown[]).map(item => normalRecord(item as Partial<DraftRecord>, scope)).filter((item): item is DraftRecord => !!item)
}
async function putLocal(record: DraftRecord) {
  const db = await openDatabase(), transaction = db.transaction(STORE_NAME, 'readwrite')
  // IndexedDB 只存 JSON 副本，避免保留 Vue Proxy 或可变引用。
  transaction.objectStore(STORE_NAME).put(JSON.parse(JSON.stringify(record)))
  await transactionDone(transaction)
}
async function deleteLocal(record: DraftRecord) {
  const db = await openDatabase(), transaction = db.transaction(STORE_NAME, 'readwrite')
  transaction.objectStore(STORE_NAME).delete(record.localKey)
  await transactionDone(transaction)
}

function currentPayload(): { payload: DraftObject; context: DraftObject } {
  const payload = validatedDraftObject(props.payload, '草稿内容'), context = validatedDraftObject(props.context || {}, '草稿上下文')
  // 上传体含元数据，预留 1 MiB 给 JSON 转义、版本和将来扩展字段，防止服务器请求上限临界失败。
  if (draftBytes({ kind: props.kind, targetId: currentTargetId.value, payload, context, baseVersion: activeVersion.value }) > REMOTE_BODY_LIMIT) throw new Error('草稿内容接近 30 MB 上限，请先移除部分图片或附件后重试')
  return { payload, context }
}
async function buildRecord(): Promise<DraftRecord | null> {
  const scope = scopeKey.value
  if (!isCurrent(scope) || !canWrite.value || !props.dirty) return null
  const { payload, context } = currentPayload(), now = new Date().toISOString()
  let id = activeId.value
  if (!id) {
    if (scopeDraftCount.value >= MAX_WORK_ITEM_DRAFTS_PER_SCOPE) throw new Error(`当前项目的私有草稿已达 ${MAX_WORK_ITEM_DRAFTS_PER_SCOPE} 条，请先在草稿箱确认并删除不需要的草稿`)
    id = newWorkItemDraftId(); activeId.value = id; activeVersion.value = 0; activeCreatedAt.value = now
  }
  const before = records.value.find(record => record.id === id)
  return {
    localKey: `${scope}:${props.kind}:${id}`, scope, id, kind: props.kind, targetId: currentTargetId.value,
    title: draftTitle(payload, props.kind === 'requirement' ? '未命名需求草稿' : '未命名缺陷草稿'), context, payload,
    version: before?.version ?? activeVersion.value, createdAt: before?.createdAt || activeCreatedAt.value || now, updatedAt: now,
    state: before?.state === 'conflict' ? 'conflict' : 'local', hasPayload: true,
  }
}
async function writeLocal(record: DraftRecord) {
  try { const existed = records.value.some(item => item.id === record.id); await putLocal(record); if (!isCurrent(record.scope)) return false; replaceRecord(record); if (!existed) scopeDraftCount.value++; localUnavailable.value = false; return true }
  catch (cause) { localUnavailable.value = true; report(cause, '本地草稿保存失败'); return false }
}
function online() { return typeof navigator === 'undefined' || navigator.onLine !== false }
async function syncRemote(record: DraftRecord) {
  if (!online() || !isCurrent(record.scope) || !canWrite.value || record.state === 'conflict' || remoteSyncing.value) return
  const token = sequence, scope = record.scope; remoteSyncing.value = true
  try {
    const result = await api<any>(`/drafts/${encodeURIComponent(record.id)}`, { method: 'PUT', headers: { 'X-TaskLoom-Project': session.value!.project.id }, body: JSON.stringify({ kind: record.kind, targetId: record.targetId, context: record.context, payload: record.payload, baseVersion: record.version }) })
    if (!isCurrent(scope, token) || activeId.value !== record.id) return
    const detail = result?.draft || result || {}, version = Number(detail.version)
    if (!Number.isSafeInteger(version) || version < 1) throw new Error('私有草稿箱返回的数据不完整，请稍后刷新草稿箱确认')
    const synced = { ...record, version, updatedAt: typeof detail.updatedAt === 'string' ? detail.updatedAt : record.updatedAt, state: 'synced' as const }
    activeVersion.value = version; replaceRecord(synced); await writeLocal(synced); message.value = t('草稿已同步到私有草稿箱')
  } catch (cause) {
    if (!isCurrent(scope, token) || activeId.value !== record.id) return
    if (cause instanceof APIError && (cause.status === 409 || cause.code === 'draft_conflict')) { await forkConflict(record); return }
    // 网络、权限或服务端暂不可用时保留本地版本，不报告成“已同步”。
    const local = { ...record, state: 'local' as const }; replaceRecord(local); await writeLocal(local)
    if (!(cause instanceof APIError && cause.status === 0)) report(cause, '私有草稿箱同步失败，本机草稿已保留')
  } finally { if (isCurrent(scope, token)) remoteSyncing.value = false }
}
async function forkConflict(record: DraftRecord) {
  if (!isCurrent(record.scope)) return
  const conflicted = { ...record, state: 'conflict' as const }; replaceRecord(conflicted); await writeLocal(conflicted)
  const now = new Date().toISOString(), forkId = newWorkItemDraftId(), fork: DraftRecord = { ...record, id: forkId, localKey: `${record.scope}:${record.kind}:${forkId}`, version: 0, createdAt: now, updatedAt: now, state: 'local', title: `${record.title}（冲突副本）` }
  activeId.value = forkId; activeVersion.value = 0; activeCreatedAt.value = now; await writeLocal(fork)
  error.value = t('检测到另一页面已更新此草稿；两个版本均已保留，请在草稿箱中确认后继续')
  // 新 UUID 使用 baseVersion 0，可作为独立私有草稿同步，不覆盖原草稿。
  await syncRemote(fork)
}
async function saveNow(manual = true) {
  if (saving.value || !canWrite.value || !props.ready) return false
  resetMessages(); saving.value = true
  try {
    const record = await buildRecord()
    if (!record) { if (manual && !props.dirty) message.value = t('当前没有需要保存的修改'); return false }
    const saved = await writeLocal(record)
    if (saved) { message.value = t('草稿已保存到本机'); void syncRemote(record) }
    return saved
  } catch (cause) { report(cause, '草稿保存失败') ; return false }
  finally { saving.value = false }
}
function queueSave() {
  if (saveTimer) clearTimeout(saveTimer)
  if (!canWrite.value || !props.dirty) return
  saveTimer = setTimeout(() => { saveTimer = undefined; void saveNow(false) }, 1000)
}
async function mergeRemote() {
  const scope = scopeKey.value, token = sequence
  if (!canRead.value || !isCurrent(scope, token)) return
  try {
    const result = await api<DraftListResponse>('/drafts', { headers: { 'X-TaskLoom-Project': session.value!.project.id } })
    if (!isCurrent(scope, token) || !Array.isArray(result.items)) return
    scopeDraftCount.value = Math.max(scopeDraftCount.value, result.items.length)
    const localById = new Map(records.value.map(record => [record.id, record]))
    for (const item of result.items) {
      // 当前编辑器只恢复同类工作项，避免把缺陷字段误灌入需求表单（反之亦然）。
      if (item.kind !== props.kind) continue
      const existing = typeof item.id === 'string' ? localById.get(item.id) : undefined
      const remote = normalRecord({ ...item, kind: item.kind, state: 'metadata', hasPayload: false }, scope)
      if (!remote) continue
      if (existing) {
        const newer = remote.version > existing.version || remote.updatedAt > existing.updatedAt
        if (newer) replaceRecord({ ...existing, version: remote.version, title: remote.title || existing.title, updatedAt: remote.updatedAt, state: existing.hasPayload ? existing.state : 'metadata' })
      } else replaceRecord(remote)
    }
  } catch (cause) { if (isCurrent(scope, token) && !(cause instanceof APIError && cause.status === 0)) report(cause, '私有草稿箱读取失败') }
}
async function loadInbox() {
  if (!canRead.value || loading.value) return
  resetMessages(); loading.value = true
  const scope = scopeKey.value, token = ++sequence
  try {
    const local = await localRecords(scope)
    if (!isCurrent(scope, token)) return
    scopeDraftCount.value = local.length; records.value = local.filter(record => record.kind === props.kind).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)); localUnavailable.value = false
    await mergeRemote()
  } catch (cause) { if (isCurrent(scope, token)) report(cause, '本地草稿箱读取失败') }
  finally { if (isCurrent(scope, token)) loading.value = false }
}
async function ensurePayload(record: DraftRecord): Promise<DraftRecord | null> {
  if (record.hasPayload) return record
  const scope = scopeKey.value, token = sequence
  try {
    const result = await api<DraftDetailResponse>(`/drafts/${encodeURIComponent(record.id)}`, { headers: { 'X-TaskLoom-Project': session.value!.project.id } })
    if (!isCurrent(scope, token)) return null
    const detail: any = result.draft || result, loaded = normalRecord({ ...record, ...detail, id: record.id, kind: record.kind, state: 'synced', hasPayload: true }, scope)
    if (!loaded || !loaded.hasPayload) throw new Error('草稿内容数据不完整，无法恢复')
    await writeLocal(loaded); return loaded
  } catch (cause) { report(cause, '草稿内容读取失败'); return null }
}
async function restore(record: DraftRecord) {
  if (saving.value || props.busy || !canRead.value) return
  resetMessages(); saving.value = true
  try {
    const source = await ensurePayload(record)
    if (!source || !isCurrent(source.scope)) return
    activeId.value = source.id; activeVersion.value = source.version; activeCreatedAt.value = source.createdAt
    emit('restore', cloneDraftObject(source.payload), cloneDraftObject(source.context), { id: source.id, version: source.version, kind: source.kind, targetId: source.targetId })
    await nextTick()
    message.value = t('草稿已恢复到编辑器；完成正式保存后会自动清理该草稿')
  } catch (cause) { report(cause, '草稿恢复失败') }
  finally { saving.value = false }
}
async function destroy(record: DraftRecord, completed = false) {
  // 正式工作项已经由外层确认保存时，外层 busy 仍会短暂为 true；此时允许只清理
  // 对应草稿，不能因此遗留“已成功发布”的重复恢复项。
  if (!canWrite.value || saving.value || (!completed && props.busy)) return
  resetMessages(); saving.value = true
  try {
    if (record.version > 0) {
      const result = await api<any>(`/drafts/${encodeURIComponent(record.id)}`, { method: 'DELETE', headers: { 'X-TaskLoom-Project': session.value!.project.id }, body: JSON.stringify({ version: record.version }) })
      if (result?.deleted !== true) throw new Error('私有草稿删除结果未确认，请刷新草稿箱核实')
    }
    await deleteLocal(record); removeRecord(record.id); scopeDraftCount.value = Math.max(0, scopeDraftCount.value - 1); pendingDeleteId.value = ''; message.value = t('草稿已删除')
  } catch (cause) { report(cause, '草稿删除失败，本机草稿已保留') }
  finally { saving.value = false }
}
async function complete() {
  const record = activeRecord.value
  if (!record || saving.value || !isCurrent(record.scope)) return
  await destroy(record, true)
}
function download(name: string, content: string, type: string) {
  if (typeof document === 'undefined' || typeof URL === 'undefined' || typeof Blob === 'undefined') { error.value = t('当前环境不能下载本地草稿文件'); return }
  const href = URL.createObjectURL(new Blob([content], { type })), anchor = document.createElement('a')
  anchor.href = href; anchor.download = name; anchor.style.display = 'none'; document.body.appendChild(anchor); anchor.click(); anchor.remove(); setTimeout(() => URL.revokeObjectURL(href), 1000)
}
async function exportDraft(record: DraftRecord, kind: 'json' | 'html') {
  const source = await ensurePayload(record)
  if (!source) return
  if (kind === 'json') download(draftExportFilename(source.kind, 'json'), JSON.stringify({ schemaVersion: 1, exportedAt: new Date().toISOString(), kind: source.kind, targetId: source.targetId, context: source.context, payload: source.payload }, null, 2), 'application/json;charset=utf-8')
  else download(draftExportFilename(source.kind, 'html'), draftReadableHTML({ kind: source.kind, targetId: source.targetId, payload: source.payload, updatedAt: source.updatedAt }), 'text/html;charset=utf-8')
}
function openInbox() { inboxOpen.value = true; pendingDeleteId.value = ''; void loadInbox() }
function closeInbox() { if (!saving.value) { inboxOpen.value = false; pendingDeleteId.value = '' } }
const dialog = useSettingsDialog(inboxOpen, closeInbox)

function resetScope() {
  sequence++; if (saveTimer) clearTimeout(saveTimer); saveTimer = undefined
  records.value = []; scopeDraftCount.value = 0; selectedId.value = ''; pendingDeleteId.value = ''; activeId.value = ''; activeVersion.value = 0; activeCreatedAt.value = ''; remoteSyncing.value = false; loading.value = false; inboxOpen.value = false
}
function pageHide() { if (props.dirty) void saveNow(false) }
function onlineSync() { const record = activeRecord.value; if (record?.state === 'local') void syncRemote(record) }
watch([scopeKey, () => props.ready], () => { resetScope(); if (canRead.value) void loadInbox() }, { immediate: true })
watch(() => props.restoreId, id => { if (id && records.value.some(record => record.id === id)) { const record = records.value.find(item => item.id === id); if (record) void restore(record) } })
watch(() => props.payload, queueSave, { deep: true })
watch(() => props.dirty, dirty => { if (dirty) queueSave() })
onMounted(() => {
  periodicTimer = setInterval(() => { if (props.dirty) void saveNow(false) }, 30_000)
  window.addEventListener('pagehide', pageHide); window.addEventListener('online', onlineSync)
})
onBeforeUnmount(() => {
  disposed.value = true; sequence++; if (saveTimer) clearTimeout(saveTimer); if (periodicTimer) clearInterval(periodicTimer)
  window.removeEventListener('pagehide', pageHide); window.removeEventListener('online', onlineSync)
})
defineExpose({ saveNow, complete })
</script>

<template>
 <section v-if="canRead" class="work-item-drafts" :aria-label="t('草稿箱')">
  <div :class="['draft-tools-bar',{compact}]" :title="statusText"><p>{{statusText}}</p><div><button type="button" class="btn compact" :disabled="saving||busy||!canWrite||!dirty" @click="saveNow(true)">{{t(saving?'正在保存草稿…':'保存草稿')}}</button><button type="button" class="btn compact" :disabled="saving||busy" @click="openInbox">{{t('草稿箱')}}<span v-if="countLabel" class="draft-count">{{countLabel}}</span></button></div></div>
  <p v-if="error&&!inboxOpen" class="draft-error" role="alert">{{error}}</p><p v-else-if="message&&!inboxOpen" class="draft-message" role="status">{{message}}</p>
 </section>
 <div v-if="inboxOpen" class="draft-shade" @click.self="closeInbox"><section ref="dialog" class="draft-dialog" tabindex="-1" role="dialog" aria-modal="true" :aria-label="t('草稿箱')"><header><div><span class="eyebrow">{{t('本机与私有草稿')}}</span><h2>{{t('草稿箱')}}</h2><p>{{t('草稿仅属于当前账号和项目；正式保存成功后才会创建或修改工作项。')}}</p></div><button type="button" :disabled="saving" :aria-label="t('关闭')" @click="closeInbox">×</button></header>
   <p v-if="error" class="draft-error dialog-message" role="alert">{{error}}</p><p v-else-if="message" class="draft-message dialog-message" role="status">{{message}}</p>
   <div class="draft-dialog-body"><aside><div class="draft-list-header"><strong>{{t('可恢复草稿')}}</strong><button type="button" class="link" :disabled="loading||saving" @click="loadInbox">{{t(loading?'刷新中…':'刷新')}}</button></div><p v-if="loading" class="draft-empty" role="status">{{t('正在读取草稿…')}}</p><p v-else-if="!records.length" class="draft-empty">{{t('暂无草稿。开始编辑后，系统会每 30 秒自动保存。')}}</p><ul v-else class="draft-list"><li v-for="record in records" :key="record.id"><button type="button" :class="{selected:selectedId===record.id}" @click="selectedId=record.id"><strong>{{record.title}}</strong><small>{{record.kind==='requirement'?t('需求'):t('缺陷')}} · {{record.state==='synced'?t('已同步'):record.state==='conflict'?t('冲突副本'):record.state==='metadata'?t('私有草稿'):t('仅本机')}} · {{new Date(record.updatedAt).toLocaleString()}}</small></button></li></ul></aside>
    <main><template v-if="selected"><span class="draft-kind">{{selected.kind==='requirement'?t('需求草稿'):t('缺陷草稿')}}</span><h3>{{selected.title}}</h3><p class="draft-meta">{{selected.targetId?t('编辑对象 #{id}',{id:selected.targetId}):t('新建对象草稿')}} · {{t('最后保存于 {time}',{time:new Date(selected.updatedAt).toLocaleString()})}}</p><pre>{{selected.hasPayload?draftPreview(selected.payload,12000)||t('（草稿尚未填写正文）'):t('为保护流量，正文将在恢复或导出时读取。')}}</pre><p class="draft-meta">{{t('本机 HTML 可直接双击查看；JSON 用于完整备份，可能包含未上传图片和附件。')}}</p></template><p v-else class="draft-empty">{{t('从左侧选择一个草稿以预览、恢复或导出。')}}</p></main></div>
   <footer><template v-if="selected"><button type="button" class="link" :disabled="saving" @click="exportDraft(selected,'html')">{{t('下载可查看 HTML')}}</button><button type="button" class="link" :disabled="saving" @click="exportDraft(selected,'json')">{{t('下载完整 JSON')}}</button><span></span><button v-if="pendingDeleteId!==selected.id" type="button" class="btn danger" :disabled="saving||busy||!canWrite" @click="pendingDeleteId=selected.id">{{t('删除草稿')}}</button><button v-else type="button" class="btn danger" :disabled="saving||busy||!canWrite" @click="destroy(selected)">{{t('确认删除草稿')}}</button><button type="button" class="btn" :disabled="saving||busy" @click="closeInbox">{{t('取消')}}</button><button type="button" class="btn primary" :disabled="saving||busy" @click="restore(selected)">{{t('恢复到编辑器')}}</button></template><button v-else type="button" class="btn" :disabled="saving" @click="closeInbox">{{t('关闭')}}</button></footer>
  </section></div>
</template>

<style scoped>
.work-item-drafts{min-width:0;border:1px solid var(--line);border-radius:9px;background:var(--surface-subtle,var(--surface));padding:10px 12px;margin:0 0 16px}.draft-tools-bar{display:flex;align-items:center;gap:12px;justify-content:space-between;min-width:0}.draft-tools-bar p{margin:0;min-width:0;color:var(--muted);font-size:12px;line-height:1.55;overflow-wrap:anywhere}.draft-tools-bar>div{display:flex;align-items:center;gap:8px;flex:none}.draft-tools-bar.compact p{display:none}.draft-tools-bar.compact{gap:0}.draft-count{display:inline-grid;min-width:16px;height:16px;margin-left:5px;padding:0 3px;border-radius:9px;background:var(--control-selected,#3370eb);color:#fff;place-items:center;font-size:10px}.draft-error,.draft-message{margin:8px 0 0;font-size:12px;line-height:1.6;overflow-wrap:anywhere}.draft-error{color:var(--danger,#b42318)}.draft-message{color:var(--success,#067647)}.draft-shade{position:fixed;inset:0;z-index:190;background:#17223970;display:grid;place-items:center;padding:20px}.draft-dialog{width:min(960px,100%);max-height:calc(100dvh - 40px);display:flex;flex-direction:column;overflow:hidden;border:1px solid var(--line);border-radius:13px;background:var(--surface);color:var(--ink);box-shadow:0 20px 70px #0004}.draft-dialog>header{display:flex;gap:20px;justify-content:space-between;padding:20px 24px;border-bottom:1px solid var(--line)}.draft-dialog h2{font-size:19px;margin:3px 0}.draft-dialog h3{margin:7px 0 5px;font-size:17px;overflow-wrap:anywhere}.draft-dialog header p,.draft-meta,.draft-empty{margin:0;color:var(--muted);font-size:12px;line-height:1.7;overflow-wrap:anywhere}.draft-dialog header>button{border:0;background:transparent;color:var(--muted);font-size:25px;line-height:1;cursor:pointer}.dialog-message{margin:10px 24px 0}.draft-dialog-body{display:grid;grid-template-columns:minmax(230px,34%) minmax(0,1fr);min-height:280px;overflow:hidden;border-top:0}.draft-dialog-body>aside{min-width:0;overflow:auto;border-right:1px solid var(--line);padding:14px}.draft-dialog-body>main{min-width:0;overflow:auto;padding:20px 24px}.draft-list-header{display:flex;justify-content:space-between;align-items:center;gap:8px;margin-bottom:9px;font-size:13px}.draft-list{list-style:none;padding:0;margin:0}.draft-list li+li{margin-top:4px}.draft-list button{width:100%;display:grid;gap:4px;text-align:left;padding:10px;border:1px solid transparent;border-radius:8px;color:var(--ink);background:transparent;cursor:pointer;min-width:0}.draft-list button:hover,.draft-list button.selected{background:var(--control-hover,#edf3ff);border-color:var(--control-selected,#3370eb)}.draft-list strong{font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.draft-list small{font-size:10px;color:var(--muted);line-height:1.5}.draft-kind{font-size:11px;color:var(--control-selected,#3370eb);font-weight:650}.draft-dialog pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:310px;overflow:auto;margin:16px 0;padding:14px;border:1px solid var(--line);border-radius:8px;background:var(--surface-subtle,#f7f9fc);font:13px/1.7 ui-monospace,SFMono-Regular,Menlo,monospace}.draft-dialog footer{display:flex;align-items:center;gap:8px;flex-wrap:wrap;padding:15px 24px;border-top:1px solid var(--line)}.draft-dialog footer>span{flex:1}.draft-dialog .danger{color:var(--danger,#b42318);border-color:color-mix(in srgb,var(--danger,#b42318) 40%,var(--line))}.draft-dialog .link{font-size:12px}@media(max-width:700px){.work-item-drafts{margin-bottom:12px}.draft-tools-bar{align-items:flex-start;flex-direction:column}.draft-tools-bar>div{width:100%}.draft-tools-bar .btn{flex:1;min-height:40px}.draft-shade{padding:10px}.draft-dialog{max-height:calc(100dvh - 20px)}.draft-dialog>header{padding:16px}.draft-dialog-body{display:block;overflow:auto}.draft-dialog-body>aside{max-height:220px;border-right:0;border-bottom:1px solid var(--line)}.draft-dialog-body>main{min-height:230px;padding:16px}.draft-dialog footer{padding:12px 16px}.draft-dialog footer .btn{min-height:42px}.draft-dialog footer>span{display:none}.draft-dialog footer .link:first-child{margin-right:auto}}
</style>
