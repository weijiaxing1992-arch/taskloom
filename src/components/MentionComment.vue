<script setup lang="ts">
import CodeInsertTools from './CodeInsertTools.vue'
import { clipboardCodeText, insideCodeFence } from '../editorPaste'
import { fencedCode } from '../codeHighlight'
import { t } from '../i18n'
import { computed, nextTick, ref, useId, watch } from 'vue'
import { containsMention, insertMention, mentionAtCaret, mentionRoleLabels, normalizeMentionIds, retainMentionIds, unavailableNewMentionIds, type MentionMember } from '../mentions'
import { useRecentMentions } from '../useRecentMentions'

const props = defineProps<{ modelValue: string; members: MentionMember[]; mentionUserIds?: string[]; mentionNames?: Record<string, string>; savedMentionUserIds?: string[]; disabled?: boolean; mode?: 'comment' | 'field'; inputId?: string; label?: string; placeholder?: string; rows?: number; maxlength?: number }>()
const emit = defineEmits<{ (event: 'update:modelValue', body: string): void; (event: 'update:mentionUserIds', ids: string[]): void; (event: 'update:mentionNames', names: Record<string, string>): void; (event: 'submit', value: { body: string; mentionUserIds: string[] }): void }>()
const textarea = ref<HTMLTextAreaElement | null>(null)
const list = ref<HTMLElement | null>(null)
const caret = ref(0), focused = ref(false), dismissed = ref(false), activeIndex = ref(0)
const internalMentionIds = ref<string[]>([])
const internalMentionNames = ref<Record<string, string>>({})
const selectedMentionIds = computed(() => normalizeMentionIds(props.mentionUserIds ?? internalMentionIds.value))
const selectedMentionNames = computed(() => props.mentionNames ?? internalMentionNames.value)
const fieldMode = computed(() => props.mode === 'field')
const listID = 'mention-options-' + useId()
const token = computed(() => mentionAtCaret(props.modelValue, caret.value))
const recentMentions = useRecentMentions(() => props.members, () => token.value?.query || '')
const matches = recentMentions.matches
const visible = computed(() => focused.value && !dismissed.value && !props.disabled && recentMentions.usable.value && token.value !== null)
const mentions = computed(() => selectedMentionIds.value.map((id): MentionMember | undefined => {
  const member = props.members.find(member => member.id === id)
  const name = selectedMentionNames.value[id] || member?.name
  return name ? { ...member, id, name, active: member?.active !== false && !!member } : undefined
}).filter((member): member is MentionMember => !!member && (fieldMode.value || member.active !== false) && containsMention(props.modelValue, member.name)))
const unavailableIds = computed(() => selectedMentionIds.value.filter(id => !props.members.some(member => member.id === id && member.active !== false)))
const blockingMentionIds = computed(() => unavailableNewMentionIds(selectedMentionIds.value, props.members, fieldMode.value ? props.savedMentionUserIds : []))
const hasSameNameMembers = computed(() => new Set(props.members.map(member => member.name)).size !== props.members.length)
function hasDuplicateName(name: string) { return props.members.filter(member => member.name === name).length > 1 }
function updateMentionIds(ids: string[]) { internalMentionIds.value = [...new Set(ids)]; emit('update:mentionUserIds', internalMentionIds.value) }
function updateMentionNames(names: Record<string, string>) { internalMentionNames.value = names; emit('update:mentionNames', names) }
function reconcileMentions(body: string) {
  const ids = retainMentionIds(body, selectedMentionIds.value, props.members, selectedMentionNames.value)
  if (ids.join('\0') !== selectedMentionIds.value.join('\0')) updateMentionIds(ids)
  if (!body) { caret.value = 0; dismissed.value = false }
}
watch([() => props.modelValue, () => props.members, () => props.mentionNames], ([body]) => reconcileMentions(body), { immediate: true })
watch(() => token.value?.query, () => { activeIndex.value = 0 })
watch(() => matches.value.length, length => { activeIndex.value = Math.max(0, Math.min(activeIndex.value, length - 1)) })

function onInput(event: Event) {
  const input = event.target as HTMLTextAreaElement
  caret.value = input.selectionStart
  dismissed.value = false
  emit('update:modelValue', input.value)
}
async function insertCode(language:string) {
  if(props.disabled||textarea.value?.closest('fieldset[disabled], [inert]')) return
  const start=textarea.value?.selectionStart??props.modelValue.length,end=textarea.value?.selectionEnd??start
  const code=props.modelValue.slice(start,end),block='\n'+fencedCode(code,language)+'\n'
  const body=props.modelValue.slice(0,start)+block+props.modelValue.slice(end)
  if(props.maxlength&&body.length>props.maxlength)return
  emit('update:modelValue',body);dismissed.value=true
  await nextTick();textarea.value?.focus();const position=start+block.indexOf('\n',1)+1;textarea.value?.setSelectionRange(position,position+code.length);caret.value=position
}
function updateCaret() { caret.value = textarea.value?.selectionStart || 0 }
function onFocus() { focused.value = true; updateCaret() }
function onClick() { updateCaret(); dismissed.value = false }
function roleLabel(member: MentionMember) { const role = member.projectRole || member.role || ''; return t(mentionRoleLabels[role] || role || '项目成员') }
async function choose(member: MentionMember) {
  if (!token.value || props.disabled || !recentMentions.accepts(member.id) || textarea.value?.closest('[inert], fieldset[disabled]')) return
  const insertion = insertMention(props.modelValue, token.value, member.name)
  recentMentions.remember(member.id)
  // 纯文本 @姓名无法区分同名账号；只保留用户最后明确选择的那个稳定 ID，
  // 不按显示名称向所有同名成员扩散通知。
  const nextIDs = [...selectedMentionIds.value.filter(id => id !== member.id && (selectedMentionNames.value[id] || props.members.find(item => item.id === id)?.name) !== member.name), member.id]
  updateMentionNames({ ...Object.fromEntries(nextIDs.filter(id => id !== member.id).map(id => [id, selectedMentionNames.value[id] || props.members.find(item => item.id === id)?.name || ''])), [member.id]: member.name })
  updateMentionIds(nextIDs)
  emit('update:modelValue', insertion.body)
  caret.value = insertion.caret; dismissed.value = true
  await nextTick()
  textarea.value?.focus(); textarea.value?.setSelectionRange(insertion.caret, insertion.caret)
}
function submit() {
  if (fieldMode.value || props.disabled || !props.modelValue.trim() || blockingMentionIds.value.length) return
  dismissed.value = true
  emit('submit', { body: props.modelValue.trim(), mentionUserIds: [...new Set(mentions.value.map(member => member.id))] })
}
async function onKeydown(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229) return
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') { if (!fieldMode.value) { event.preventDefault(); submit() }; return }
  if (visible.value) {
    if (event.key === 'Escape') { event.preventDefault(); dismissed.value = true; return }
    if (['ArrowDown', 'ArrowUp'].includes(event.key) && matches.value.length) {
      event.preventDefault()
      activeIndex.value = (activeIndex.value + (event.key === 'ArrowDown' ? 1 : -1) + matches.value.length) % matches.value.length
      await nextTick(); list.value?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
      return
    }
    if (event.key === 'Enter' && matches.value[activeIndex.value]) { event.preventDefault(); await choose(matches.value[activeIndex.value]!); return }
  }
  if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) { await nextTick(); updateCaret(); dismissed.value = false }
}
defineExpose({ focus: () => textarea.value?.focus() })
async function pasteCode(event:ClipboardEvent){
  if(props.disabled||textarea.value?.closest('fieldset[disabled], [inert]'))return
  const start=textarea.value?.selectionStart??props.modelValue.length,end=textarea.value?.selectionEnd??start
  if(insideCodeFence(props.modelValue.slice(0,start)))return
  const text=event.clipboardData?.getData('text/plain')||'',converted=clipboardCodeText(text)
  if(converted===text)return
  const insertion='\n'+converted+'\n',body=props.modelValue.slice(0,start)+insertion+props.modelValue.slice(end)
  if(props.maxlength&&body.length>props.maxlength)return
  event.preventDefault();emit('update:modelValue',body)
  await nextTick();textarea.value?.focus();textarea.value?.setSelectionRange(start+insertion.length,start+insertion.length)
}
</script>

<template>
  <div class="mention-comment" :class="{ disabled, 'mention-field': fieldMode }">
    <div class="mention-input-wrap">
      <textarea :id="inputId" ref="textarea" :value="modelValue" :disabled="disabled" :rows="rows" :maxlength="maxlength" :aria-label="label || t('需求评论')" :placeholder="placeholder || t('输入评论，使用 @ 提及当前项目成员…')" :aria-controls="visible ? listID : undefined" :aria-expanded="visible" :aria-activedescendant="visible && matches.length ? listID + '-' + activeIndex : undefined" aria-autocomplete="list" @paste="pasteCode" @input="onInput" @focus="onFocus" @blur="focused = false" @click="onClick" @select="updateCaret" @keydown="onKeydown"></textarea>
      <CodeInsertTools :text="modelValue" :disabled="disabled" @insert="insertCode"/>
      <div v-if="visible" class="mention-popup">
        <header><b>{{ t('提及项目成员') }}</b><span>{{t('{count} 位匹配',{count:matches.length})}}</span></header>
        <div :id="listID" ref="list" class="mention-options" role="listbox" :aria-label="t('选择提及成员')">
          <button v-for="(member, index) in matches" :id="listID + '-' + index" :key="member.id" type="button" role="option" :aria-selected="index === activeIndex" :class="{ active: index === activeIndex }" @mousedown.prevent @mouseenter="activeIndex = index" @click="choose(member)"><span class="mention-avatar">{{ member.name.slice(0, 1) }}</span><span class="mention-person"><b>{{ member.name }} <span v-if="recentMentions.badge(member.id)" class="mention-recency">{{ recentMentions.badge(member.id) }}</span></b><small>{{ member.email || roleLabel(member) }}</small></span><em>{{ roleLabel(member) }}</em></button>
          <p v-if="!matches.length" class="mention-empty">{{ t('没有匹配的成员，请尝试姓名、邮箱或角色。') }}</p>
        </div>
        <footer>{{ t('↑ ↓ 选择 · Enter 确认 · Esc 关闭') }}</footer>
      </div>
    </div>
    <div v-if="mentions.length" class="mentioned-members" :aria-label="fieldMode ? t('正文或备注中的已选提及') : t('评论将通知的成员')"><span>{{ fieldMode ? t('已选提及') : t('将通知') }}</span><span v-for="member in mentions" :key="member.id" class="mentioned-chip" :title="member.email || member.id">@{{ member.name }}<small v-if="hasDuplicateName(member.name)"> · {{ member.email || member.id }}</small></span></div>
    <p v-if="hasSameNameMembers" class="mention-identity-hint">{{ t('同名成员仅通知最后选择的账号，请在下拉列表中核对邮箱。') }}</p>
    <div v-if="blockingMentionIds.length" class="mention-warning" role="alert"><span>{{t('{count} 位已选提及成员暂不可用。请等待成员加载，或移除后重新选择，以免遗漏通知。',{count:blockingMentionIds.length})}}</span><button type="button" :disabled="disabled" @click="updateMentionIds(selectedMentionIds.filter(id => !blockingMentionIds.includes(id)))">{{ t('移除不可用提及') }}</button></div>
    <p v-else-if="fieldMode && unavailableIds.length" class="mention-identity-hint">{{ t('历史提及成员暂不可用，原提及可保留，不会重复通知。') }}</p>
    <footer v-if="fieldMode" class="field-compose-footer"><small>{{ t('输入 @ 选择成员 · Enter 换行；保存后仅通知新增提及。') }}</small></footer>
    <footer v-else class="comment-compose-footer"><small>{{ t('输入 @ 选择成员 · Ctrl / ⌘ + Enter 发布') }}</small><button type="button" class="btn primary compact" :disabled="disabled || !modelValue.trim() || !!blockingMentionIds.length" @click="submit">{{ disabled ? t('发布中…') : t('发布评论') }}</button></footer>
  </div>
</template>

<style scoped>
.mention-comment{border:1px solid #dfe3eb;border-radius:8px;background:#fff;padding:11px;min-width:0}.mention-comment:focus-within{border-color:#8a85ed;box-shadow:0 0 0 3px #706aec0d}.mention-input-wrap{position:relative}.mention-comment textarea{display:block;resize:vertical;border:0;outline:0;width:100%;min-height:92px;padding:2px 2px 10px;font:inherit;font-size:12px;line-height:1.8;color:#344054;background:transparent;box-shadow:none}.mention-comment textarea:focus-visible{outline:0}.mention-comment textarea:disabled{color:#98a2b3}.comment-compose-footer{display:flex;align-items:center;justify-content:space-between;gap:12px;padding-top:7px}.comment-compose-footer small{color:#98a2b3;font-size:10px}.mentioned-members{display:flex;flex-wrap:wrap;align-items:center;gap:5px;padding:1px 0 5px;font-size:10px;color:#98a2b3}.mentioned-chip{padding:3px 6px;background:#efefff;color:#5b5ce2;border-radius:4px}.mention-popup{position:absolute;z-index:40;left:0;right:0;bottom:calc(100% + 7px);width:min(430px,100%);border:1px solid #e4e7ec;border-radius:9px;background:#fff;box-shadow:0 12px 36px #17203324;overflow:hidden}.mention-popup>header{display:flex;align-items:center;justify-content:space-between;padding:10px 12px;border-bottom:1px solid #eef0f4;background:#fafbfc}.mention-popup>header b{font-size:11px;color:#344054}.mention-popup>header span{font-size:10px;color:#98a2b3}.mention-options{max-height:230px;overflow:auto;padding:4px}.mention-options button{display:flex;width:100%;align-items:center;gap:9px;padding:9px 8px;border:0;border-radius:6px;background:#fff;color:#344054;text-align:left}.mention-options button.active{background:#f1f0ff}.mention-avatar{display:grid;place-items:center;flex:none;width:29px;height:29px;border-radius:8px;background:#e9e8ff;color:#625ce0;font-size:12px}.mention-person{flex:1;min-width:0}.mention-person b,.mention-person small{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.mention-person b{font-size:12px}.mention-person small{margin-top:3px;color:#98a2b3;font-size:10px}.mention-options em{font-size:9px;color:#8a93a3;white-space:nowrap;font-style:normal}.mention-empty{margin:0;padding:20px 12px;font-size:11px;color:#98a2b3;text-align:center;line-height:1.7}.mention-popup>footer{padding:7px 12px;font-size:9px;color:#98a2b3;border-top:1px solid #eef0f4}.mention-comment.disabled{background:#fafbfc}@media(max-width:800px){.comment-compose-footer{align-items:flex-end}.comment-compose-footer small{max-width:170px;line-height:1.6}.mention-options em{display:none}}
</style>
<style scoped>.mention-warning{display:flex;align-items:center;gap:8px;margin:4px 0;padding:8px;border:1px solid #f9deb2;border-radius:6px;background:#fffaeb;color:#a15c07;font-size:10px;line-height:1.6}.mention-warning span{flex:1}.mention-warning button{flex:none;border:0;background:transparent;color:inherit;text-decoration:underline;font-size:10px;padding:3px}.mention-identity-hint{margin:4px 0 6px;color:#8a93a3;font-size:10px;line-height:1.6}.mention-field textarea{min-height:0;font-size:13px}.field-compose-footer{border-top:1px solid #eef0f4;padding-top:8px;margin-top:5px;line-height:1.6}.field-compose-footer small{font-size:10px;color:#8a93a3}.mention-field .mention-popup{bottom:auto;top:calc(100% + 7px);z-index:60}.mention-field .mention-options{max-height:200px}</style>
