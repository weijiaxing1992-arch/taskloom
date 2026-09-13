<script setup lang="ts">
import { t } from '../i18n'
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { api } from '../api'
import MemberMultiSelect from './MemberMultiSelect.vue'
import DatePicker from './DatePicker.vue'
import { fieldMemberRoles, memberCandidates, type MentionMember } from '../mentions'

type FieldDefinition = { id: number; key: string; name: string; type: string; departmentId?: string; memberRoles?: string[]; description?: string; required?: boolean; enabled?: boolean; defaultValue?: any; options?: string[] }
const props = defineProps<{ objectType: string; modelValue: Record<string, any>; inline?: boolean; disabled?: boolean; visibleKeys?: string[] | null; excludedKeys?: string[]; excludedNames?: string[]; visibleTypes?: string[]; excludedTypes?: string[] }>()
const emit = defineEmits<{ (event: 'update:modelValue', value: Record<string, any>): void; (event: 'validity-change', valid: boolean): void }>()
const dateValidity = ref<Record<string, boolean>>({})
const defs = ref<FieldDefinition[]>([]), members = ref<MentionMember[]>([])
const loading = ref(false), error = ref(''), memberError = ref('')
// 编辑权限和保存锁传入自定义组件，不能只依赖外围 fieldset 禁用原生输入。
const locked = computed(() => !!props.disabled || loading.value || !!error.value)
const instanceID = useId()
const activeMembers = computed(() => members.value.filter(member => member.active === true))
// 同一份字段定义可被编辑器拆到“核心字段”和“更多字段”；排除仅影响展示，不删除已有数据。
const visibleDefs = computed(() => {
  const candidates = props.visibleKeys == null ? defs.value : props.visibleKeys.map(key=>defs.value.find(d=>d.key===key)).filter((d):d is FieldDefinition=>!!d)
  const keys = new Set(props.excludedKeys || []), names = new Set(props.excludedNames || [])
  return candidates.filter(definition => !keys.has(definition.key) && !names.has(definition.name) && (!props.visibleTypes || props.visibleTypes.includes(definition.type)) && !props.excludedTypes?.includes(definition.type))
})
watch(() => visibleDefs.value.filter(field => field.type === 'date').every(field => dateValidity.value[field.key] !== false), valid => emit('validity-change', valid), { immediate: true })
let loadVersion = 0
// 异步加载用版本号与项目双重校验；身份冲突后冻结本实例，等待外壳重新建立会话。
let identityInvalid = false
function projectID() { try { return localStorage.getItem('devflow-project') || '' } catch { return '' } }

async function load() {
  const version = ++loadVersion
  const project = projectID()
  if (identityInvalid) return
  const options = { headers: { 'X-TaskLoom-Project': project } }
  loading.value = true; error.value = ''; memberError.value = ''
  try {
    const data = await api<{ items: FieldDefinition[] }>('/field-definitions?objectType=' + encodeURIComponent(props.objectType), options)
    if (version !== loadVersion || project !== projectID()) return
    if (!Array.isArray(data.items)) throw new Error('自定义字段数据格式不正确')
    defs.value = data.items.filter(definition => definition.enabled)
    if (defs.value.some(definition => definition.type === 'user' || definition.type === 'users')) {
      try {
        const data = await api<{ items: MentionMember[] }>('/members', options)
        if (version !== loadVersion || project !== projectID()) return
        if (!Array.isArray(data.items)) throw new Error('项目成员数据格式不正确')
        members.value = data.items
      } catch (cause) {
        if (version === loadVersion) memberError.value = message(cause)
      }
    } else members.value = []
  } catch (cause) {
    if (version === loadVersion) error.value = message(cause)
  } finally {
    if (version === loadVersion) loading.value = false
  }
}
function message(cause: unknown) { return cause instanceof Error ? cause.message : '请稍后重试' }
function fieldID(definition: FieldDefinition) { return 'custom-field-' + instanceID + '-' + definition.id }
function labelID(definition: FieldDefinition) { return fieldID(definition) + '-label' }
function valueOf(definition: FieldDefinition) {
  // “未设置”才使用默认值；显式 null、false、0 或空字符串都是用户输入，不能被默认值覆盖。
  return Object.prototype.hasOwnProperty.call(props.modelValue || {}, definition.key) ? props.modelValue[definition.key] : definition.defaultValue
}
function optionsOf(definition: FieldDefinition) { return Array.isArray(definition.options) ? definition.options : [] }
function selectedOptions(definition: FieldDefinition): string[] { const value = valueOf(definition); return Array.isArray(value) ? value : [] }
function rolesFor(definition: FieldDefinition) { return fieldMemberRoles(definition, props.objectType) }
function candidates(definition: FieldDefinition) { return memberCandidates(activeMembers.value, rolesFor(definition), definition.departmentId) }
// 候选范围变更后历史绑定仍展示；提交时由后端区分保留旧值与新增越权绑定。
function memberLabel(value: unknown) { return members.value.find(member => member.id === value)?.name || String(value ?? '') }
function unavailableMember(definition: FieldDefinition) {
  const value = valueOf(definition)
  return typeof value === 'string' && value !== '' && !candidates(definition).some(member => member.id === value)
}
function set(key: string, value: any) { if (locked.value || identityInvalid) return; emit('update:modelValue', { ...(props.modelValue || {}), [key]: value }) }
function setNumber(definition: FieldDefinition, event: Event) {
  const input = event.target as HTMLInputElement
  const value = input.value.trim() === '' ? null : Number(input.value)
  if (!input.validity?.badInput && (value === null || Number.isFinite(value))) set(definition.key, value)
}
function toggleOption(definition: FieldDefinition, option: string, event: Event) {
  const selected = selectedOptions(definition)
  set(definition.key, (event.target as HTMLInputElement).checked ? [...new Set([...selected, option])] : selected.filter(value => value !== option))
}
watch(() => props.objectType, () => { defs.value = []; members.value = []; void load() }, { immediate: true })
function projectChanged() { defs.value = []; members.value = []; void load() }
function identityChanged() { identityInvalid = true; ++loadVersion; members.value = []; defs.value = []; loading.value = false }
onMounted(() => { window.addEventListener('devflow-project-changed', projectChanged); window.addEventListener('devflow-identity-changed', identityChanged); window.addEventListener('devflow-auth-expired', identityChanged) })
onBeforeUnmount(() => { emit('validity-change', true); ++loadVersion; window.removeEventListener('devflow-project-changed', projectChanged); window.removeEventListener('devflow-identity-changed', identityChanged); window.removeEventListener('devflow-auth-expired', identityChanged) })
</script>

<template>
  <div :class="['custom-fields', inline ? 'inline' : '']" :aria-busy="loading">
    <p v-if="loading && !defs.length" class="custom-field-state" role="status">{{ t('正在载入自定义字段…') }}</p>
    <div v-if="error || memberError" class="custom-field-error" role="alert"><span>{{error?t('自定义字段加载失败：{error}',{error:t(error)}):t('项目成员加载失败：{error}',{error:t(memberError)})}}</span><button type="button" :disabled="loading" @click="load">{{ loading ? t('重试中…') : t('重新加载') }}</button></div>
    <template v-for="definition in visibleDefs" :key="definition.id">
      <label :id="labelID(definition)" :for="definition.type === 'multi_select' ? undefined : fieldID(definition)" :title="definition.description">{{ definition.name }} <b v-if="definition.required">*</b></label>
      <select v-if="definition.type === 'single_select'" :id="fieldID(definition)" :value="valueOf(definition) ?? ''" :disabled="locked" :aria-label="definition.name" :aria-required="!!definition.required" @change="set(definition.key, ($event.target as HTMLSelectElement).value)">
        <option value="">{{ t('请选择') }}</option><option v-for="option in optionsOf(definition)" :key="option" :value="option">{{ option }}</option>
      </select>
      <div v-else-if="definition.type === 'multi_select'" class="option-checks" role="group" :aria-labelledby="labelID(definition)">
        <label v-for="(option, index) in optionsOf(definition)" :key="option" :for="fieldID(definition) + '-option-' + index"><input :id="fieldID(definition) + '-option-' + index" type="checkbox" :disabled="locked" :aria-label="definition.name + '：' + option" :checked="selectedOptions(definition).includes(option)" @change="toggleOption(definition, option, $event)"> {{ option }}</label>
        <span v-if="!optionsOf(definition).length" class="custom-field-empty">{{ t('暂无可选项') }}</span>
      </div>
      <label v-else-if="definition.type === 'boolean'" class="check" :for="fieldID(definition)"><input :id="fieldID(definition)" type="checkbox" :disabled="locked" :aria-label="definition.name" :aria-required="!!definition.required" :checked="valueOf(definition) === true" @change="set(definition.key, ($event.target as HTMLInputElement).checked)"> {{ t('是') }}</label>
      <input v-else-if="definition.type === 'number'" :id="fieldID(definition)" type="number" step="any" inputmode="decimal" :disabled="locked" :aria-label="definition.name" :aria-required="!!definition.required" :value="valueOf(definition) ?? ''" @input="setNumber(definition, $event)">
      <DatePicker v-else-if="definition.type === 'date'" :id="fieldID(definition)" :label="definition.name" :disabled="locked" :required="!!definition.required" :model-value="valueOf(definition) ?? ''" @update:model-value="set(definition.key, $event)" @validity-change="dateValidity[definition.key] = $event" />
      <MemberMultiSelect v-else-if="definition.type === 'user'" single :input-id="fieldID(definition)" :label="definition.name" :model-value="valueOf(definition)?[String(valueOf(definition))]:[]" :members="members" :department-id="definition.departmentId" :member-roles="rolesFor(definition)" :disabled="locked || !!memberError" :show-lead="false" @update:model-value="set(definition.key, $event[0]||'')" />
      <MemberMultiSelect v-else-if="definition.type === 'users'" :input-id="fieldID(definition)" :label="definition.name" :model-value="selectedOptions(definition)" :members="members" :department-id="definition.departmentId" :member-roles="rolesFor(definition)" :disabled="locked || !!memberError" :show-lead="false" :hint="t('可选多位成员；仅可新增当前项目与限定部门中的可用成员，历史绑定保留。')" @update:model-value="set(definition.key, $event)" />
      <textarea v-else-if="definition.type === 'textarea'" :id="fieldID(definition)" :disabled="locked" :aria-label="definition.name" :aria-required="!!definition.required" :value="valueOf(definition) ?? ''" @input="set(definition.key, ($event.target as HTMLTextAreaElement).value)"></textarea>
      <input v-else :id="fieldID(definition)" :disabled="locked" :aria-label="definition.name" :aria-required="!!definition.required" :value="valueOf(definition) ?? ''" @input="set(definition.key, ($event.target as HTMLInputElement).value)">
    </template>
  </div>
</template>

<style scoped>
.custom-field-state,.custom-field-error{grid-column:1/-1;margin:4px 0 10px;font-size:12px;line-height:1.6}.custom-field-state,.custom-field-empty{color:#98a2b3}.custom-field-error{display:flex;align-items:center;gap:8px;flex-wrap:wrap;color:#b42318}.custom-field-error button{border:0;background:none;padding:0;font:inherit;color:inherit;text-decoration:underline;cursor:pointer}.custom-field-error button:disabled{cursor:wait;opacity:.6}.custom-fields .option-checks input[type=checkbox],.custom-fields .check input[type=checkbox]{width:auto;flex:none}.custom-field-empty{font-size:11px}
</style>
