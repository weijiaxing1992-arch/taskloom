<script setup lang="ts">
import AIButton from './AIButton.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { requirementTitleError, titleDescriptionError } from '../requirementTitle'

type Capability = { configured:boolean; enabled:boolean; model:string; canGenerate:boolean; maxDescriptionLength:number; maxTitleLength:number; baseUrl?:string }
const props = defineProps<{ title:string; description:string; document?:unknown; requirementId?:number; projectId:string; userId:string; actorKey?:string; disabled?:boolean; canConfigure?:boolean }>()
const emit = defineEmits<{ (event:'generated',title:string):void; (event:'busy',value:boolean):void }>()
const capability = ref<Capability|null>(null), loading = ref(false), generating = ref(false), error = ref(''), notice = ref(''), locked = ref(false)
let disposed = false, readVersion = 0, generationVersion = 0
let readController:AbortController|null = null, generationController:AbortController|null = null
const context = () => JSON.stringify([props.projectId, props.userId, props.actorKey || '', props.requirementId || null])
const input = () => JSON.stringify([props.title, props.description])
const available = computed(() => !!capability.value?.configured && !!capability.value.enabled && !!capability.value.canGenerate)
const inputError = computed(() => titleDescriptionError(props.description, props.document, capability.value?.maxDescriptionLength || 100000))
const canStart = computed(() => !props.disabled && !locked.value && !loading.value && !generating.value && !props.title.trim() && available.value && !inputError.value)
function current(key:string) {
  const activeProject = localStorage.getItem('devflow-project') || 'prj_orbit'
  return !disposed && !locked.value && !!props.userId && !!props.projectId && activeProject === props.projectId && key === context()
}
function cancel(showNotice = false) {
  const wasGenerating = generating.value
  generationVersion++; generationController?.abort(); generationController = null; generating.value = false
  if (showNotice && wasGenerating) notice.value = '已停止等待 AI 标题，可继续手动输入；已发出的请求可能仍产生费用'
}
function invalidate() {
  locked.value = true; readVersion++; readController?.abort(); readController = null; loading.value = false; capability.value = null; cancel(); error.value = ''; notice.value = ''
}
function storage(event:StorageEvent) { if (event.key === 'devflow-project' && !current(context())) invalidate() }
async function load() {
  const key = context()
  if (generating.value || !current(key)) return
  const version = ++readVersion
  readController?.abort(); const controller = new AbortController(); readController = controller
  loading.value = true; error.value = ''
  try {
    const data = await api<Capability>('/ai/requirement-title', { headers:{'X-TaskLoom-Project':props.projectId}, signal:controller.signal })
    if (!current(key) || version !== readVersion) return
    if (!data || typeof data.configured !== 'boolean' || typeof data.enabled !== 'boolean' || typeof data.canGenerate !== 'boolean' || typeof data.model !== 'string' || !Number.isSafeInteger(data.maxDescriptionLength) || data.maxDescriptionLength < 1 || data.maxDescriptionLength > 1000000 || data.maxTitleLength !== 80) throw Error('AI 标题能力返回格式不正确，请重试或手动填写标题')
    capability.value = data
  } catch (cause) { if (current(key) && version === readVersion && !controller.signal.aborted) { capability.value = null; error.value = cause instanceof Error ? cause.message : 'AI 标题功能暂不可用，请手动填写标题或重试' } }
  finally { if (current(key) && version === readVersion) loading.value = false }
}
async function generate():Promise<boolean> {
  const key = context()
  if (props.disabled || generating.value || !current(key) || props.title.trim()) return false
  error.value = ''; notice.value = ''
  if (loading.value) { error.value = '正在检查 AI 标题功能，请稍后重试或手动填写标题'; return false }
  if (!capability.value) { error.value = 'AI 标题功能暂不可用，请手动填写标题或重试'; return false }
  if (!capability.value.canGenerate) { error.value = '当前身份没有 AI 标题生成权限，请手动填写标题'; return false }
  if (!capability.value.configured || !capability.value.enabled) { error.value = '企业尚未配置或启用 AI，请手动填写标题或联系管理员'; return false }
  if (inputError.value) { error.value = inputError.value; return false }
  if (!window.confirm(t('本次仅将需求描述的纯文本发送给企业配置的 AI 服务，用于生成标题；不发送图片、附件文件、人员字段或其他字段。描述中手动写入的姓名等文字仍会发送。可能产生模型费用。生成后需您确认并再次保存，是否继续？'))) return false
  if (!current(key) || props.disabled || props.title.trim()) return false
  const revision = ++generationVersion, originalInput = input(), description = props.description, requirementId = props.requirementId
  const controller = new AbortController(); generationController = controller; generating.value = true
  try {
    const result = await api<{title:string;model:string}>('/ai/requirement-title', { method:'POST', headers:{'X-TaskLoom-Project':props.projectId}, signal:controller.signal, body:JSON.stringify({description,confirmed:true,...(requirementId ? {requirementId} : {})}) })
    if (!current(key) || revision !== generationVersion || input() !== originalInput || props.disabled || controller.signal.aborted) return false
    if (!result || requirementTitleError(result.title, capability.value.maxTitleLength) || typeof result.model !== 'string' || !result.model) throw Error('AI 返回的标题不符合 2–80 字符单行规范，请重试或手动填写')
    generating.value = false; generationController = null
    emit('generated', result.title.trim())
    return true
  } catch (cause) { if (current(key) && revision === generationVersion && !controller.signal.aborted) error.value = cause instanceof Error ? cause.message : 'AI 标题生成失败，需求尚未保存，请重试或手动填写标题'; return false }
  finally { if (revision === generationVersion) { generating.value = false; generationController = null } }
}
watch(generating, value => emit('busy', value), {flush:'sync'})
watch(() => [props.title, props.description], () => { if (generating.value) cancel(true) }, {flush:'sync'})
watch(() => props.disabled, value => { if (value) cancel() }, {flush:'sync'})
watch(() => context(), () => { cancel(); readVersion++; readController?.abort(); capability.value = null; loading.value = false; error.value = ''; notice.value = ''; void load() }, {flush:'sync'})
onMounted(() => { void load(); for (const name of ['devflow-identity-changed','devflow-auth-expired','devflow-account-disabled','devflow-project-changed']) window.addEventListener(name,invalidate); window.addEventListener('storage',storage) })
onBeforeUnmount(() => { disposed = true; readVersion++; readController?.abort(); cancel(); for (const name of ['devflow-identity-changed','devflow-auth-expired','devflow-account-disabled','devflow-project-changed']) window.removeEventListener(name,invalidate); window.removeEventListener('storage',storage) })
defineExpose({ generate, cancel, generating })
</script>
<template>
 <section class="title-assistant" :aria-label="t('AI 需求标题')" :aria-busy="loading||generating">
  <div class="title-assistant-actions"><AIButton :busy="generating" :disabled="!canStart" @click="generate">{{t(generating?'正在总结标题…':'AI 生成标题')}}</AIButton><button v-if="generating" type="button" class="link" @click="cancel(true)">{{t('取消生成')}}</button></div>
  <!-- 说明默认收起，保留完整的数据边界和人工确认提示，避免挤占需求正文编辑空间。 -->
  <details class="title-assistant-details"><summary :aria-label="t('AI 标题说明')" :title="t('AI 标题说明')"><span aria-hidden="true">ⓘ</span></summary><div><strong>{{t('AI 标题说明')}}</strong><p v-if="capability?.model">{{t('模型')}} · {{capability.model}}</p><p v-if="capability?.baseUrl">{{t('服务地址')}} · {{capability.baseUrl}}</p><AIButton v-if="!generating&&(!capability||!available)" :busy="loading" :disabled="disabled||locked" @click="load">{{t('重新检查 AI 配置')}}</AIButton><p>{{t('AI 标题为 2–80 个字符。标题留空提交时可从描述生成，已有标题不会被覆盖；生成后请确认并再次保存。')}}</p><p v-if="capability&&!available&&!locked">{{t(capability.canGenerate?'企业尚未配置或启用 AI，请手动填写标题或联系管理员':'当前身份没有 AI 标题生成权限，请手动填写标题')}} <router-link v-if="canConfigure" to="/settings/ai">{{t('配置 AI 服务')}}</router-link></p><p v-if="generating">{{t('可取消生成或继续修改标题、描述。取消只停止等待，已发出的请求可能仍产生费用。')}}</p></div></details>
  <p v-if="locked" class="title-assistant-error" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p><p v-else-if="error" class="title-assistant-error" role="alert">{{t(error)}}</p><p v-if="notice" role="status">{{t(notice)}}</p>
  <p v-if="loading&&!capability" role="status">{{t('正在检查 AI 标题功能…')}}</p>
 </section>
</template>
<style scoped>
.title-assistant{padding:7px 10px;margin:8px 0 10px;border:1px solid var(--line);border-radius:8px;background:var(--surface-soft,var(--surface));color:var(--ink);min-width:0}.title-assistant-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.title-assistant-actions span{font-size:10px;color:var(--muted);overflow-wrap:anywhere}.title-assistant .btn{font-size:11px;min-height:30px}.title-assistant .link{font:inherit;font-size:11px;min-height:30px;border:0;background:transparent;color:var(--primary);cursor:pointer}.title-assistant-details{display:inline-block;margin:0 0 0 2px;vertical-align:middle;color:var(--muted);font-size:11px}.title-assistant-details summary{cursor:pointer;color:var(--muted);list-style:none;white-space:nowrap}.title-assistant-details summary::-webkit-details-marker{display:none}.title-assistant-details summary::before{content:'⌄';display:inline-block;margin-right:4px;transition:transform .16s ease}.title-assistant-details[open] summary::before{transform:rotate(180deg)}.title-assistant-details>div{max-width:780px}.title-assistant p{font-size:11px;line-height:1.7;color:var(--muted);margin:7px 0 0;overflow-wrap:anywhere}.title-assistant p.title-assistant-error{color:var(--danger,#b83c42)}.title-assistant a{color:var(--primary);white-space:nowrap}.title-assistant button:disabled{opacity:.5;cursor:not-allowed}.title-assistant button:focus-visible,.title-assistant-details summary:focus-visible{outline:2px solid var(--primary);outline-offset:2px;border-radius:3px}@media(max-width:640px){.title-assistant{padding:9px}.title-assistant-actions{gap:8px}.title-assistant .btn,.title-assistant .link{min-height:40px;white-space:normal}.title-assistant-actions span{flex-basis:100%}.title-assistant-details{display:block;margin:5px 0 0}}
</style>
<style scoped>
/* 高频操作与输入框对齐；模型、配置重试放入可访问的说明浮层，不占第二行。 */
.title-assistant{display:grid;grid-template-columns:auto 32px;align-items:start;gap:4px;position:relative}
.title-assistant-actions{flex-wrap:nowrap;gap:4px;min-height:32px}
.title-assistant-details{position:relative;margin:0;font-size:var(--ui-font-body)}
.title-assistant-details summary{display:grid;place-items:center;width:32px;height:32px;border:1px solid var(--line);border-radius:6px;background:var(--surface);color:var(--muted)}
.title-assistant-details summary::before{display:none}
.title-assistant-details>div{position:absolute;z-index:30;right:0;top:calc(100% + 6px);width:min(340px,calc(100vw - 40px));padding:14px;border:1px solid var(--line);border-radius:6px;background:var(--surface);box-shadow:none;white-space:normal}
.title-assistant>p{grid-column:1/-1;max-width:320px;margin:0}
@media(max-width:640px){.title-assistant-details{margin:0}.title-assistant-details summary{height:40px}.title-assistant-actions{min-height:40px}.title-assistant-details>div{position:fixed;left:16px;right:16px;top:25dvh;width:auto;max-height:65dvh;overflow:auto}}
</style>
