<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { formatDate, t } from '../i18n'
import { Button } from '../components/ui/button'
import { useSettingsScope } from '../components/settingsScope'
import { codexIntegrationConfig, contextMarkdown, contextReadScopes, credentialRequest, integrationContextPath, integrationScopeLabels, integrationTokenStatus, readIntegrationContext, readIntegrationLogs, readIntegrationSnapshot, readIntegrationToken, type IntegrationLog, type IntegrationSnapshot, type IntegrationToken } from '../integrationPlatform'

const scope = useSettingsScope()
const snapshot = ref<IntegrationSnapshot | null>(null), logs = ref<IntegrationLog[]>([])
const loading = ref(false), saving = ref(false), downloading = ref(false), logsLoading = ref(false)
const error = ref(''), logsError = ref(''), notice = ref(''), secret = ref(''), secretId = ref(''), uncertainCreation = ref(false)
const formOpen = ref(false), name = ref(''), expiresInDays = ref(30), selectedScopes = ref<string[]>([])
const contextKind = ref<'project' | 'requirement' | 'sprint'>('project'), contextId = ref('')
const activeTab = ref<'connect' | 'credentials' | 'context' | 'logs'>('connect')
let disposed = false, generation = 0, logsGeneration = 0, leaveApproved = false
const busy = computed(() => loading.value || saving.value || downloading.value || scope.locked.value)
const disabled = computed(() => busy.value || !snapshot.value?.canManage)
const optionalScopes = computed(() => snapshot.value?.scopes.filter(item => !(contextReadScopes as readonly string[]).includes(item.key) && (snapshot.value?.canWrite || !item.key.endsWith(':write'))) || [])
const config = computed(() => snapshot.value ? codexIntegrationConfig(window.location.origin, snapshot.value.mcpPath) : '')
const apiURL = computed(() => snapshot.value ? window.location.origin + snapshot.value.apiPath : '')
const localEndpoint = ['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname)
function current(version: number) { return !disposed && version === generation && scope.current() }
function scrub() {
  generation++; logsGeneration++; snapshot.value = null; logs.value = []; secret.value = ''; secretId.value = ''
  name.value = ''; selectedScopes.value = []; formOpen.value = false; contextId.value = ''; error.value = ''; notice.value = ''; logsError.value = ''
  loading.value = false; saving.value = false; downloading.value = false; logsLoading.value = false; uncertainCreation.value = false; leaveApproved = false
}
async function loadLogs() {
  if (logsLoading.value || !scope.current() || disposed) return
  const version = ++logsGeneration; logsLoading.value = true; logsError.value = ''
  try { const value = await scope.request<unknown>('/integrations/logs'); if (!disposed && version === logsGeneration && scope.current()) logs.value = readIntegrationLogs(value, scope.project) }
  catch (cause) { if (!disposed && version === logsGeneration && scope.current()) logsError.value = cause instanceof Error ? cause.message : '调用记录暂时无法加载，请重试' }
  finally { if (!disposed && version === logsGeneration && scope.current()) logsLoading.value = false }
}
async function load() {
  if (busy.value || disposed || !scope.current()) return
  const version = ++generation; loading.value = true; error.value = ''
  try {
    const value = await scope.request<unknown>('/integrations')
    if (!current(version)) return
    snapshot.value = readIntegrationSnapshot(value, scope.project); uncertainCreation.value = false
    expiresInDays.value = Math.min(expiresInDays.value, snapshot.value.maxExpiryDays)
    selectedScopes.value = selectedScopes.value.filter(key => optionalScopes.value.some(item => item.key === key))
    if (secretId.value && !snapshot.value.tokens.some(item => item.id === secretId.value && !item.revokedAt)) hideSecret()
  } catch (cause) { if (current(version)) error.value = cause instanceof Error ? cause.message : '集成平台暂时无法加载，请重试' }
  finally { if (current(version)) loading.value = false }
}
async function createCredential() {
  if (disabled.value || uncertainCreation.value || secret.value || !snapshot.value || !scope.current()) return
  error.value = ''; notice.value = ''
  let body
  try { body = credentialRequest(name.value, selectedScopes.value, Number(expiresInDays.value), snapshot.value) }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '请检查凭据配置'; return }
  const version = generation; saving.value = true
  try {
    const result = await scope.request<{ token: string; credential: unknown }>('/integrations/tokens', { method: 'POST', body: JSON.stringify(body) })
    if (!current(version)) return
    const credential = readIntegrationToken(result?.credential)
    if (typeof result.token !== 'string' || !result.token || result.token.length > 1024 || /\s/.test(result.token)) throw Error('集成平台返回格式不正确，请刷新后重试')
    snapshot.value.tokens = [credential, ...snapshot.value.tokens.filter(item => item.id !== credential.id)]
    secret.value = result.token; secretId.value = credential.id; name.value = ''; selectedScopes.value = []; formOpen.value = false
    activeTab.value = 'credentials'; notice.value = '凭据已创建，完整令牌仅显示这一次'
  } catch (cause) {
    if (current(version)) { uncertainCreation.value = true; error.value = cause instanceof Error ? cause.message : '创建凭据失败，请重试'; notice.value = '创建结果尚未确认，请先刷新凭据列表；遗失的令牌可撤销后重新创建。' }
  } finally { if (current(version)) saving.value = false }
}
async function revokeCredential(token: IntegrationToken) {
  if (disabled.value || !scope.current() || !snapshot.value?.tokens.some(item => item.id === token.id && !item.revokedAt)) return
  if (!window.confirm(t('撤销凭据「{name}」？使用它的集成将立即失去访问权限。', { name: token.name }))) return
  const version = generation; saving.value = true; error.value = ''; notice.value = ''
  try {
    await scope.request('/integrations/tokens/' + encodeURIComponent(token.id), { method: 'DELETE' })
    if (!current(version)) return
    // Only a confirmed server revocation changes the displayed credential state.
    token.revokedAt = new Date().toISOString()
    if (secretId.value === token.id) hideSecret()
    notice.value = '凭据已撤销'
  } catch (cause) { if (current(version)) error.value = cause instanceof Error ? cause.message : '撤销凭据失败，请重试' }
  finally { if (current(version)) saving.value = false }
}
function hideSecret() { secret.value = ''; secretId.value = ''; notice.value = '' }
async function copy(value: string) {
  if (!value || !scope.current() || disposed) return
  const version = generation
  try { await navigator.clipboard.writeText(value); if (current(version)) notice.value = '已复制' }
  catch { if (current(version)) error.value = '复制失败，请手动选中并复制' }
}
function downloadText(content: string, filename: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type })), link = document.createElement('a')
  try { link.href = url; link.download = filename; document.body.appendChild(link); link.click() }
  finally { link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000) }
}
async function downloadContext(format: 'json' | 'md') {
  if (downloading.value || busy.value || !snapshot.value || !scope.current()) return
  error.value = ''; notice.value = ''
  let path
  try { path = integrationContextPath(contextKind.value, contextId.value) } catch (cause) { error.value = cause instanceof Error ? cause.message : '请输入有效的需求或迭代 ID'; return }
  const version = generation; downloading.value = true
  try {
    const response = await scope.request<unknown>(path)
    if (!current(version)) return
    const context = readIntegrationContext(response, scope.project), filename = 'devflow-context-' + context.generatedAt.slice(0, 10) + '.' + format
    downloadText(format === 'json' ? JSON.stringify(context, null, 2) : contextMarkdown(context), filename, format === 'json' ? 'application/json;charset=utf-8' : 'text/markdown;charset=utf-8')
    notice.value = context.limits.truncated ? '已导出部分上下文；数据达到上限，请缩小范围或通过 API 分页读取。' : '上下文已下载'
  } catch (cause) { if (current(version)) error.value = cause instanceof Error ? cause.message : '上下文下载失败，请重试' }
  finally { if (current(version)) downloading.value = false }
}
async function downloadOpenAPI() {
  if (downloading.value || busy.value || !snapshot.value || !scope.current()) return
  const version = generation; downloading.value = true; error.value = ''
  try {
    const spec = await scope.request<Record<string, unknown>>('/integrations/openapi')
    if (!current(version)) return
    if (!spec || typeof spec.openapi !== 'string' || !spec.paths || typeof spec.paths !== 'object') throw Error('集成平台返回格式不正确，请刷新后重试')
    downloadText(JSON.stringify(spec, null, 2), 'devflow-openapi.json', 'application/json;charset=utf-8'); notice.value = '接口文档已下载'
  } catch (cause) { if (current(version)) error.value = cause instanceof Error ? cause.message : '接口文档下载失败，请重试' }
  finally { if (current(version)) downloading.value = false }
}
function canLeave() { return !saving.value && (leaveApproved || !secret.value || window.confirm(t('完整令牌只显示一次，离开后无法再次查看。确认已经保存并离开？'))) }
function beforeProjectChange(event: Event) { leaveApproved = false; if (!canLeave()) event.preventDefault(); else leaveApproved = true }
function cancelProjectChange() { leaveApproved = false }
function beforeUnload(event: BeforeUnloadEvent) { if (saving.value || (secret.value && !leaveApproved)) { event.preventDefault(); event.returnValue = '' } }
watch(scope.locked, value => { if (value) scrub() }, { flush: 'sync' })
watch(activeTab, value => { if (value === 'logs') void loadLogs() })
onBeforeRouteLeave(canLeave)
onMounted(() => { void load(); window.addEventListener('devflow-before-project-change', beforeProjectChange); window.addEventListener('devflow-project-change-cancelled', cancelProjectChange); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { disposed = true; scrub(); window.removeEventListener('devflow-before-project-change', beforeProjectChange); window.removeEventListener('devflow-project-change-cancelled', cancelProjectChange); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <div class="module-page integrations-page">
    <header class="integration-header"><div><h1>{{ t('API 与 AI 集成') }}</h1><p>{{ t('让 Codex 和 AI 助手在授权范围内读取与更新当前项目的研发工作。') }}</p></div><Button variant="outline" :disabled="busy" @click="load">{{ t(loading ? '加载中…' : '刷新') }}</Button></header>
    <p v-if="scope.locked.value" class="integration-message error" role="alert">{{ t('项目或账号已变化，请刷新页面后继续') }}</p>
    <p v-if="error" class="integration-message error" role="alert">{{ t(error) }}</p><p v-if="notice" class="integration-message" role="status">{{ t(notice) }}</p>
    <p v-if="loading && !snapshot" role="status">{{ t('正在加载集成平台…') }}</p>
    <template v-if="snapshot && !scope.locked.value">
      <nav class="integration-tabs" :aria-label="t('集成功能')"><Button v-for="tab in ([['connect', '快速接入'], ['credentials', '我的凭据'], ['context', '导出上下文'], ['logs', '调用记录']] as const)" :key="tab[0]" variant="ghost" :aria-current="activeTab === tab[0] ? 'page' : undefined" @click="activeTab = tab[0]">{{ t(tab[1]) }}</Button></nav>
      <section v-if="secret" class="integration-secret" aria-labelledby="secret-title"><div class="integration-section-title"><h2 id="secret-title">{{ t('保存完整令牌') }}</h2><Button variant="outline" @click="hideSecret">{{ t('已保存，隐藏令牌') }}</Button></div><p>{{ t('完整令牌仅显示这一次；请保存到安全的凭据管理器，并在运行 Codex 的环境中设置 DEVFLOW_API_TOKEN。') }}</p><div class="integration-copy"><input :value="secret" readonly type="password" autocomplete="off" spellcheck="false" :aria-label="t('完整令牌')" @focus="($event.target as HTMLInputElement).select()"><Button variant="outline" @click="copy(secret)">{{ t('复制令牌') }}</Button></div><small>{{ t('页面仅在内存中保留令牌。刷新、离开或切换账号与项目后将清除。') }}</small></section>
      <section v-if="activeTab === 'connect'" class="integration-section">
        <h2>{{ t('连接 Codex') }}</h2>
        <ol class="integration-steps"><li>{{ t('创建当前项目的个人凭据，默认包含需求、迭代、缺陷和测试用例的只读上下文。') }} <Button variant="link" :disabled="disabled" @click="activeTab = 'credentials'; formOpen = true">{{ t('创建凭据') }}</Button></li><li>{{ t('在运行 Codex 的环境中设置 DEVFLOW_API_TOKEN，将下方配置加入 ~/.codex/config.toml，然后重新启动 Codex。') }}</li><li>{{ t('让 Codex 先读取项目上下文；需要修改时，勾选相应写权限并在 Codex 中确认写操作。') }}</li></ol>
        <div class="integration-section-title"><b>~/.codex/config.toml</b><Button variant="outline" @click="copy(config)">{{ t('复制配置') }}</Button></div><pre class="integration-code"><code>{{ config }}</code></pre>
        <p class="integration-note">{{ t('配置中的环境变量必须对 Codex 进程可见；令牌不能写入项目文件或提交到代码仓库。') }} <a href="https://learn.chatgpt.com/docs/extend/mcp?surface=cli" target="_blank" rel="noopener noreferrer">{{ t('Codex 官方接入说明') }}</a></p>
        <p v-if="localEndpoint" class="integration-message">{{ t('当前是本机地址：仅运行在这台电脑上的 Codex 或工具可以访问。云端使用需要可访问的 HTTPS 服务地址。') }}</p>
        <h2>{{ t('REST API') }}</h2><div class="integration-copy"><code>{{ apiURL }}</code><Button variant="outline" :disabled="downloading || busy" @click="downloadOpenAPI">{{ t('下载 OpenAPI') }}</Button></div>
        <p class="integration-note">{{ t('使用 Authorization: Bearer 令牌鉴权。写请求携带 Idempotency-Key；PATCH 同时携带最新详情返回的 ETag 作为 If-Match，防止覆盖他人的修改。') }}</p>
        <p class="integration-note">{{ t('凭据仅限当前项目，权限受创建人的实时权限约束。工作项内容作为参考数据，不能替代你对 AI 的操作指令。') }}</p>
      </section>
      <section v-if="activeTab === 'credentials'" class="integration-section">
        <div class="integration-section-title"><div><h2>{{ t('我的凭据') }}</h2><p>{{ t('仅显示当前账号在此项目创建的凭据。撤销后立即停止访问。') }}</p></div><Button :disabled="disabled || !!secret || uncertainCreation" @click="formOpen = !formOpen">{{ t(formOpen ? '收起表单' : '创建凭据') }}</Button></div>
        <p v-if="!snapshot.canWrite" class="integration-note">{{ t('当前账号为只读权限，可以创建只读凭据。') }}</p>
        <form v-if="formOpen" class="integration-form" @submit.prevent="createCredential"><fieldset :disabled="disabled || !!secret || uncertainCreation"><div class="integration-form-row"><label>{{ t('凭据名称') }}<input v-model="name" maxlength="80" required autocomplete="off" :placeholder="t('例如：我的 Codex')"></label><label>{{ t('有效期（天）') }}<input v-model.number="expiresInDays" type="number" min="1" :max="snapshot.maxExpiryDays" step="1" required></label></div><div class="integration-read-scope"><b>{{ t('项目研发上下文（只读）') }}</b><span>{{ t('已包含') }}</span><p>{{ t('需求、迭代、缺陷与测试用例相互关联，四类读取权限作为一个固定权限组授权。') }}</p></div><div class="integration-scopes"><label v-for="item in optionalScopes" :key="item.key"><input v-model="selectedScopes" type="checkbox" :value="item.key">{{ t(integrationScopeLabels[item.key] || item.label) }}</label></div><p class="integration-note">{{ t('只勾选本次集成需要的权限；记录测试执行同时包含读取测试执行。') }}</p><Button type="submit" :disabled="disabled || !!secret || uncertainCreation">{{ t(saving ? '创建中…' : '创建令牌') }}</Button></fieldset></form>
        <div class="integration-table-wrap"><table class="integration-table"><thead><tr><th>{{ t('名称 / 前缀') }}</th><th>{{ t('权限') }}</th><th>{{ t('到期时间') }}</th><th>{{ t('最近使用') }}</th><th>{{ t('状态') }}</th><th>{{ t('操作') }}</th></tr></thead><tbody><tr v-for="token in snapshot.tokens" :key="token.id"><td><b>{{ token.name }}</b><small>{{ token.prefix }}…</small></td><td><span class="integration-scope-summary">{{ token.scopes.map(key => t(integrationScopeLabels[key] || key)).join(' · ') }}</span></td><td>{{ formatDate(token.expiresAt) }}</td><td>{{ formatDate(token.lastUsedAt) }}</td><td>{{ t(integrationTokenStatus(token)) }}</td><td><Button v-if="!token.revokedAt" variant="ghost" :disabled="disabled" @click="revokeCredential(token)">{{ t('撤销') }}</Button><span v-else>—</span></td></tr><tr v-if="!snapshot.tokens.length"><td colspan="6" class="integration-empty">{{ t('尚未创建凭据') }}</td></tr></tbody></table></div>
      </section>
      <section v-if="activeTab === 'context'" class="integration-section"><h2>{{ t('导出上下文') }}</h2><p>{{ t('下载当前项目的需求、迭代、缺陷和测试用例，供 AI 阅读或离线核对。可按需求或迭代缩小范围。') }}</p><div class="integration-context-controls"><label>{{ t('导出范围') }}<select v-model="contextKind" :disabled="downloading"><option value="project">{{ t('当前项目') }}</option><option value="requirement">{{ t('指定需求') }}</option><option value="sprint">{{ t('指定迭代') }}</option></select></label><label v-if="contextKind !== 'project'">{{ t(contextKind === 'requirement' ? '需求 ID' : '迭代 ID') }}<input v-model="contextId" :disabled="downloading" inputmode="numeric" pattern="[1-9][0-9]*" :placeholder="t('输入数字 ID')"></label><Button variant="outline" :disabled="downloading || busy" @click="downloadContext('json')">{{ t('下载 JSON') }}</Button><Button variant="outline" :disabled="downloading || busy" @click="downloadContext('md')">{{ t('下载 Markdown') }}</Button></div><p class="integration-note">{{ t('每类数据最多导出 100 条；达到上限会明确标记。文件包含业务内容，请仅交给有权限访问该项目的工具或人员。') }}</p><p v-if="downloading" role="status">{{ t('正在准备下载…') }}</p></section>
      <section v-if="activeTab === 'logs'" class="integration-section"><div class="integration-section-title"><div><h2>{{ t('最近调用记录') }}</h2><p>{{ t('查看个人凭据的请求结果；记录不展示令牌、请求正文或响应正文。') }}</p></div><Button variant="outline" :disabled="logsLoading || busy" @click="loadLogs">{{ t('刷新记录') }}</Button></div><p v-if="logsError" class="integration-message error" role="alert">{{ t(logsError) }}</p><p v-if="logsLoading" role="status">{{ t('正在加载调用记录…') }}</p><div class="integration-table-wrap"><table class="integration-table"><thead><tr><th>{{ t('时间') }}</th><th>{{ t('凭据') }}</th><th>{{ t('请求') }}</th><th>{{ t('结果') }}</th></tr></thead><tbody><tr v-for="item in logs" :key="item.id"><td>{{ formatDate(item.createdAt) }}</td><td>{{ item.tokenName }}</td><td><code>{{ item.method }} {{ item.path }}</code></td><td>{{ item.status === 0 ? t('结果待确认') : item.status }} <small v-if="item.replayed">{{ t('幂等重放') }}</small></td></tr><tr v-if="!logs.length && !logsLoading"><td colspan="4" class="integration-empty">{{ t('暂无调用记录') }}</td></tr></tbody></table></div></section>
    </template>
  </div>
</template>

<style scoped>
.integrations-page{max-width:1200px;margin:auto;min-width:0;color:var(--ink);font-size:var(--ui-font-body)}
.integration-header,.integration-section-title{display:flex;align-items:center;justify-content:space-between;gap:16px;min-width:0}.integration-header{align-items:flex-start;margin-bottom:16px}.integration-header h1{font-size:var(--ui-font-title);margin:0 0 6px}.integration-header p,.integration-section-title p{margin:0;color:var(--muted)}.integration-header>div,.integration-section-title>div{min-width:0}
.integration-tabs{display:flex;gap:6px;overflow:auto;border-bottom:1px solid var(--line);padding-bottom:8px}.integration-tabs [aria-current]{background:var(--primary-soft);color:var(--primary)}.integration-section{padding:22px 0;min-width:0}.integration-section h2,.integration-secret h2{font-size:var(--ui-font-section);margin:0 0 10px}.integration-section p,.integration-secret p{line-height:1.7;overflow-wrap:anywhere}.integration-section>h2:not(:first-child){margin-top:26px}.integration-note,.integration-secret small{font-size:var(--ui-font-caption);color:var(--muted);line-height:1.8}.integration-note a{color:var(--primary)}.integration-steps{padding-left:23px;line-height:1.9}.integration-steps li{padding:3px 0}.integration-steps :deep(button){vertical-align:middle}
.integration-code{padding:14px;background:var(--surface-soft,var(--surface));border:1px solid var(--line);border-radius:var(--ui-control-radius);overflow:auto;line-height:1.8;font-size:var(--ui-font-body)}.integration-message{padding:10px 12px;background:var(--surface-soft,var(--surface));border-left:3px solid var(--primary);font-size:var(--ui-font-body);line-height:1.7;overflow-wrap:anywhere}.integration-message.error{border-color:var(--danger);color:var(--danger)}
.integration-secret{padding:16px 0;border-bottom:1px solid var(--line)}.integration-secret .integration-copy{margin-bottom:8px}.integration-copy{display:flex;align-items:center;gap:10px;min-width:0}.integration-copy>code{flex:1;overflow-wrap:anywhere;min-width:0}.integration-copy input{flex:1;min-width:0}.integration-form{margin:16px 0;border-block:1px solid var(--line);padding:16px 0}.integration-form fieldset{border:0;margin:0;padding:0;min-width:0}.integration-form-row{display:grid;grid-template-columns:minmax(200px,1fr) 180px;gap:16px;max-width:680px}.integration-form-row label,.integration-context-controls label{display:grid;gap:7px}.integration-form input:not([type=checkbox]),.integration-copy input,.integration-context-controls input,.integration-context-controls select{width:100%;min-width:0;box-sizing:border-box;border:1px solid var(--line);background:var(--surface);color:var(--ink);font:inherit;padding:0 9px;height:var(--ui-control-height);border-radius:var(--ui-control-radius)}.integration-read-scope{margin:18px 0 12px}.integration-read-scope>span{margin-left:10px;color:var(--primary);font-size:var(--ui-font-caption)}.integration-read-scope p{margin:5px 0;color:var(--muted);font-size:var(--ui-font-caption)}.integration-scopes{display:flex;gap:12px 20px;flex-wrap:wrap}.integration-scopes label{display:flex;align-items:center;gap:7px;cursor:pointer}.integration-scopes input{width:16px;height:16px;accent-color:var(--primary)}
.integration-table-wrap{overflow:auto;border-block:1px solid var(--line);margin-top:16px}.integration-table{width:100%;border-collapse:collapse;text-align:left;font-size:var(--ui-font-body);min-width:700px}.integration-table th{font-weight:500;color:var(--muted);background:var(--surface-soft,var(--surface));white-space:nowrap}.integration-table th,.integration-table td{padding:11px 12px;border-bottom:1px solid var(--line);vertical-align:middle}.integration-table tr:last-child td{border-bottom:0}.integration-table b{font-weight:500;overflow-wrap:anywhere}.integration-table small{display:block;color:var(--muted);font-size:var(--ui-font-caption);margin-top:4px}.integration-table td>code{overflow-wrap:anywhere}.integration-scope-summary{display:block;max-width:260px;min-width:140px;line-height:1.6;font-size:var(--ui-font-caption);color:var(--muted)}.integration-empty{text-align:center;color:var(--muted);height:90px}.integration-context-controls{display:flex;gap:12px;align-items:flex-end;flex-wrap:wrap;margin:20px 0}.integration-context-controls label{min-width:150px;max-width:260px}.integration-context-controls label input{width:180px}
@media(max-width:820px){.integration-header,.integration-section-title{gap:10px}.integration-section-title{flex-wrap:wrap}.integration-form-row{grid-template-columns:minmax(0,1fr)}.integration-copy{flex-wrap:wrap}.integration-copy input{flex-basis:220px}.integration-context-controls{align-items:stretch}.integration-context-controls label{max-width:none;flex:1 1 180px}.integration-context-controls label input{width:100%}.integration-context-controls :deep(button){flex:1 1 160px}.integration-form input:not([type=checkbox]),.integration-copy input,.integration-context-controls input,.integration-context-controls select{font-size:16px}.integration-header p{font-size:var(--ui-font-caption)}}
</style>
