<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, provide, ref, shallowRef, toRaw, useId, watch } from 'vue'
import { Node, type JSONContent } from '@tiptap/core'
import { Editor, EditorContent, VueNodeViewRenderer } from '@tiptap/vue-3'
import CodeBlockLowlight from '@tiptap/extension-code-block-lowlight'
import RichCodeBlock from './RichCodeBlock.vue'
import { codeHighlighter, codeLanguages } from '../codeHighlight'
import StarterKit from '@tiptap/starter-kit'
import RichTextAsset from './RichTextAsset.vue'
import { t } from '../i18n'
import { analyzePaste } from '../editorPaste'
import { importMarkdown } from '../markdownImport'
import { mentionAtCaret, mentionRoleLabels, normalizeMentionIds, type MentionMember } from '../mentions'
import { useRecentMentions } from '../useRecentMentions'
import { normalizeRichDocument, pendingRichBytes, readRichFiles, richAssetContextKey, richEmoji, richMentionState, richTextPlain, safeRichLink, sanitizeRichHTML, type RichDocument } from '../richText'

const props = defineProps<{ modelValue: string; document?: JSONContent | null; mentionUserIds?: string[]; mentionNames?: Record<string, string>; savedMentionUserIds?: string[]; members: MentionMember[]; requirementId?: number; disabled?: boolean; readonly?: boolean; label?: string; inputId?: string; placeholder?: string; mode?: 'description' | 'comment'; allowFullscreen?: boolean; fullscreen?: boolean }>()
const emit = defineEmits<{ (event: 'update:modelValue', value: string): void; (event: 'update:document', value: RichDocument): void; (event: 'update:mentionUserIds', value: string[]): void; (event: 'update:mentionNames', value: Record<string, string>): void; (event: 'update:fullscreen', value: boolean): void; (event: 'busy', value: boolean): void; (event: 'submit'): void }>()
const editor = shallowRef<Editor | null>(null), reading = ref(false), error = ref('')
const wrapper = ref<HTMLElement | null>(null), mentionStyle = ref<Record<string, string>>({})
const fileInput = ref<HTMLInputElement | null>(null), imageInput = ref<HTMLInputElement | null>(null), mentionList = ref<HTMLElement | null>(null), linkInput = ref<HTMLInputElement | null>(null)
const instanceID = useId(), mentionListID = 'rich-mentions-' + instanceID, emojiID = 'rich-emoji-' + instanceID
const focused = ref(false), mentionDismissed = ref(false), mentionIndex = ref(0), revision = ref(0), emojiOpen = ref(false), linkOpen = ref(false), linkValue = ref('')
const mention = ref<{ from: number; to: number; query: string } | null>(null)
const recentMentions = useRecentMentions(() => props.members, () => mention.value?.query || '')
const matches = recentMentions.matches
const editable = computed(() => !props.disabled && !props.readonly && !reading.value)
const markdownOpen = ref(false), markdownSource = ref(''), markdownFile = ref<HTMLInputElement|null>(null)
const markdownPreview = computed(() => {
  try { return {...importMarkdown(markdownSource.value),error:''} }
  catch (cause) { return {document:null,warnings:[],error:cause instanceof Error?cause.message:'Markdown 无法解析'} }
})
function beginMarkdown() { if (editable.value) { markdownOpen.value=true; emit('busy',true) } }
function closeMarkdown() { if (reading.value) return; markdownOpen.value=false; markdownSource.value=''; emit('busy',false) }
async function readMarkdown(event: Event) {
  const input=event.target as HTMLInputElement, file=input.files?.[0];input.value=''
  if (!file || !editable.value || !markdownOpen.value) return
  if (file.size>400000) { error.value='Markdown 文件过大，请拆分后导入';return }
  const version=++readVersion;reading.value=true
  try { const source=await file.text();if (!disposed&&version===readVersion&&markdownOpen.value) markdownSource.value=source }
  catch { if (!disposed&&version===readVersion) error.value='Markdown 文件读取失败' }
  finally { if (!disposed&&version===readVersion) reading.value=false }
}
function insertMarkdown() {
  if (!editable.value || !markdownSource.value.trim() || !markdownPreview.value.document) return
  editor.value?.chain().focus().insertContent(markdownPreview.value.document.content || []).run()
  closeMarkdown()
}
const highlightedCode = CodeBlockLowlight.extend({addNodeView(){ return VueNodeViewRenderer(RichCodeBlock) }}).configure({lowlight:codeHighlighter,defaultLanguage:'auto',enableTabIndentation:true,tabSize:2})
const codeMessage = ref('')
const codeLanguage = computed(() => { revision.value; return editor.value?.getAttributes('codeBlock').language || 'plaintext' })
function toggleCode() { if (editable.value) { editor.value?.chain().focus().toggleCodeBlock({ language: 'auto' }).run(); codeMessage.value = '' } }
function setCodeLanguage(event: Event) {
  const language = (event.target as HTMLSelectElement).value
  if (editable.value && codeLanguages.includes(language)) editor.value?.chain().focus().updateAttributes('codeBlock', { language }).run()
}
async function copyCode() {
  const instance = editor.value
  if (!instance) return
  const { $from } = instance.state.selection
  // 从文档节点复制，保留缩进和尾部换行，不从渲染后的 HTML 提取代码。
  for (let depth = $from.depth; depth > 0; depth--) {
    const node = $from.node(depth)
    if (node.type.name !== 'codeBlock') continue
    try { await navigator.clipboard.writeText(node.textContent); codeMessage.value = '代码已复制' }
    catch { codeMessage.value = '复制失败，请选中代码后使用系统复制' }
    return
  }
}
const fullscreen = computed(() => !!props.fullscreen)
const mentionVisible = computed(() => focused.value && editable.value && recentMentions.usable.value && !mentionDismissed.value && !!mention.value)
const assetContext = computed(() => ({ requirementId: props.requirementId, readonly: !!props.readonly, disabled: !editable.value }))
provide(richAssetContextKey, assetContext)
let disposed = false, readVersion = 0, lastEmittedDocument: JSONContent | null = null, linkSelection: { from: number; to: number } | null = null
const formatButtons = [
  { name: 'bold', label: '加粗', symbol: 'B' }, { name: 'italic', label: '斜体', symbol: 'I' },
  { name: 'underline', label: '下划线', symbol: 'U' }, { name: 'strike', label: '删除线', symbol: 'S' },
  { name: 'bulletList', label: '无序列表', symbol: '≡' }, { name: 'orderedList', label: '有序列表', symbol: '1.' },
  { name: 'blockquote', label: '引用', symbol: '❝' },
]
const paragraphType = computed(() => { revision.value; for (const level of [1, 2, 3, 4, 5, 6]) if (editor.value?.isActive('heading', { level })) return 'h' + level; return 'paragraph' })
function isActive(name: string) { revision.value; return !!editor.value?.isActive(name) }
function roleLabel(member: MentionMember) { const role = member.projectRole || member.role || ''; return t(mentionRoleLabels[role] || role || '项目成员') }
function currentDocument() { return normalizeRichDocument(props.document, props.modelValue, props.mentionUserIds, props.mentionNames, props.members) }

const Mention = Node.create({
  name: 'mention', group: 'inline', inline: true, atom: true, selectable: false,
  addAttributes() { return { id: { default: '' }, label: { default: '' } } },
  // Clipboard HTML must not be able to fabricate project member identities.
  parseHTML() { return [] },
  renderHTML({ node }) { const member = props.members.find(item => item.id === node.attrs.id); return ['span', { class: 'rich-mention', title: member?.email || node.attrs.id }, '@' + node.attrs.label] },
  renderText({ node }) { return '@' + node.attrs.label },
})
function assetExtension(name: 'image' | 'attachment') {
  return Node.create({
    name, group: 'block', atom: true, draggable: true,
    addAttributes() { return { attachmentId: { default: null }, name: { default: '' }, alt: { default: '' }, category: { default: 'auto' }, data: { default: null } } },
    parseHTML() { return [] },
    renderHTML({ node }) { return ['figure', { 'data-rich-asset': name }, ['span', {}, node.attrs.alt || node.attrs.name || '']] },
    renderText({ node }) { return node.attrs.alt || node.attrs.name || '' },
    addNodeView() { return VueNodeViewRenderer(RichTextAsset) },
  })
}
// 表格只允许规则行列与受限单元格，不接受样式、脚本或任意 HTML 属性。
const tableNodes = [
  Node.create({name:'table',group:'block',content:'tableRow+',isolating:true,parseHTML:()=>[{tag:'table'}],renderHTML:()=>['table',['tbody',0]]}),
  Node.create({name:'tableRow',content:'(tableCell | tableHeader)+',parseHTML:()=>[{tag:'tr'}],renderHTML:()=>['tr',0]}),
  ...(['tableCell','tableHeader'] as const).map(name=>Node.create({name,content:'(paragraph | heading | bulletList | orderedList | blockquote | codeBlock | horizontalRule | image | attachment)+',isolating:true,addAttributes:()=>({textAlign:{default:null}}),parseHTML:()=>[{tag:name==='tableHeader'?'th':'td'}],renderHTML:({node})=>[name==='tableHeader'?'th':'td',(['left','center','right'].includes(node.attrs.textAlign)?{style:'text-align:'+node.attrs.textAlign}:{}),0]})),
]
function publish() {
  if (!editor.value || disposed || props.readonly || props.disabled) return
  const doc = normalizeRichDocument(editor.value.getJSON()), text = richTextPlain(doc), mentions = richMentionState(doc)
  // Directory loading must not silently clear unresolved legacy identities.
  // Normally snapshots resolve these when loading the existing plain text.
  for (const id of normalizeMentionIds(props.mentionUserIds)) if (text && !props.mentionNames?.[id] && !props.members.some(member => member.id === id) && !mentions.ids.includes(id)) mentions.ids.push(id)
  lastEmittedDocument = doc
  emit('update:modelValue', text); emit('update:document', doc); emit('update:mentionUserIds', mentions.ids); emit('update:mentionNames', mentions.names)
}
function updateMention() {
  const instance = editor.value
  if (!instance || instance.isActive('codeBlock') || !instance.state.selection.empty || !instance.state.selection.$from.parent.isTextblock) { mention.value = null; return }
  const { $from } = instance.state.selection
  const text = $from.parent.textBetween(0, $from.parent.content.size, '\n', '\uFFFC')
  const token = mentionAtCaret(text, $from.parentOffset)
  const previous = mention.value?.query
  mention.value = token ? { from: $from.start() + token.start, to: $from.start() + token.end, query: token.query } : null
  if (previous !== mention.value?.query) { mentionIndex.value = 0; mentionDismissed.value = false }
  if (token && wrapper.value) {
    try { const caret = instance.view.coordsAtPos($from.pos), box = wrapper.value.getBoundingClientRect(); mentionStyle.value = { left: Math.max(10, Math.min(caret.left - box.left, box.width - 420)) + 'px', top: Math.max(0, (window.innerHeight - caret.bottom < 280 ? caret.top - 275 : caret.bottom + 7) - box.top) + 'px', bottom: 'auto' } } catch { /* Layout can disappear while switching drawers. */ }
  }
}
function chooseMember(member: MentionMember) {
  const token = mention.value, instance = editor.value
  if (!token || !instance || !editable.value || !recentMentions.accepts(member.id) || wrapper.value?.closest('[inert], fieldset[disabled]')) return
  if (instance.chain().focus().insertContentAt({ from: token.from, to: token.to }, [{ type: 'mention', attrs: { id: member.id, label: member.name } }, { type: 'text', text: ' ' }]).run()) recentMentions.remember(member.id)
  mentionDismissed.value = true; mention.value = null
}
// 需求正文可在当前编辑器实例内放大，不复制草稿或重建 Tiptap，避免丢失光标、撤销栈和待上传附件。
function toggleFullscreen() {
  if (!props.allowFullscreen || props.readonly) return
  emit('update:fullscreen', !fullscreen.value)
  void nextTick(() => editor.value?.commands.focus())
}
function leaveFullscreen() {
  if (fullscreen.value) emit('update:fullscreen', false)
}
function wrapperKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !fullscreen.value) return
  event.preventDefault(); event.stopPropagation(); leaveFullscreen()
}
function onKeydown(event: KeyboardEvent): boolean {
  if (event.isComposing || event.keyCode === 229 || !editable.value) return false
  if (mentionVisible.value) {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); mentionDismissed.value = true; return true }
    if (['ArrowDown', 'ArrowUp'].includes(event.key) && matches.value.length) {
      event.preventDefault(); mentionIndex.value = (mentionIndex.value + (event.key === 'ArrowDown' ? 1 : -1) + matches.value.length) % matches.value.length
      void nextTick(() => mentionList.value?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })); return true
    }
    if (event.key === 'Enter' && !event.ctrlKey && !event.metaKey && matches.value[mentionIndex.value]) { event.preventDefault(); chooseMember(matches.value[mentionIndex.value]!); return true }
  }
  if (props.mode === 'comment' && (event.ctrlKey || event.metaKey) && event.key === 'Enter') { event.preventDefault(); emit('submit'); return true }
  return false
}
function clipboardFiles(data: DataTransfer | null): File[] {
  if (!data) return []
  if (data.files?.length) return Array.from(data.files)
  return Array.from(data.items || []).filter(item => item.kind === 'file').map(item => item.getAsFile()).filter((file): file is File => !!file)
}
async function insertFiles(files: File[], imagesOnly = false) {
  const instance = editor.value
  if (!instance || !editable.value || markdownOpen.value || !files.length) return
  const version = ++readVersion, requirementId = props.requirementId, selection = { from: instance.state.selection.from, to: instance.state.selection.to }
  reading.value = true; emit('busy', true); error.value = ''; emojiOpen.value = false
  try {
    const content = await readRichFiles(files, pendingRichBytes(instance.getJSON()), imagesOnly)
    if (disposed || version !== readVersion || requirementId !== props.requirementId || editor.value !== instance || props.disabled || props.readonly) return
    instance.chain().focus().insertContentAt(selection, content).run()
  } catch (cause) { if (!disposed && version === readVersion) error.value = cause instanceof Error ? cause.message : '文件读取失败，请重试' }
  finally { if (!disposed && version === readVersion) { reading.value = false; emit('busy', false) } }
}
function pickFiles(event: Event, imagesOnly = false) { const input = event.target as HTMLInputElement; const files = Array.from(input.files || []); input.value = ''; void insertFiles(files, imagesOnly) }
function openFilePicker(imagesOnly = false) { if (editable.value) (imagesOnly ? imageInput.value : fileInput.value)?.click() }
function format(name: string) {
  if (!editable.value || !editor.value) return
  const chain = editor.value.chain().focus()
  if (name === 'bold') chain.toggleBold().run()
  else if (name === 'italic') chain.toggleItalic().run()
  else if (name === 'underline') chain.toggleUnderline().run()
  else if (name === 'strike') chain.toggleStrike().run()
  else if (name === 'bulletList') chain.toggleBulletList().run()
  else if (name === 'orderedList') chain.toggleOrderedList().run()
  else if (name === 'blockquote') chain.toggleBlockquote().run()
}
function setParagraph(event: Event) {
  if (!editable.value || !editor.value) return
  const value = (event.target as HTMLSelectElement).value
  if (value === 'paragraph') editor.value.chain().focus().setParagraph().run()
  else if (['h1', 'h2', 'h3', 'h4', 'h5', 'h6'].includes(value)) editor.value.chain().focus().setHeading({ level: Number(value.slice(1)) as 1 | 2 | 3 | 4 | 5 | 6 }).run()
}
function history(action: 'undo' | 'redo') { if (editable.value && editor.value) editor.value.chain().focus()[action]().run() }
function addEmoji(value: string) { if (editable.value && editor.value) { editor.value.chain().focus().insertContent({ type: 'text', text: value }).run(); emojiOpen.value = false } }
async function openLink() {
  if (!editable.value || !editor.value) return
  linkSelection = { from: editor.value.state.selection.from, to: editor.value.state.selection.to }
  linkValue.value = editor.value.getAttributes('link').href || ''; linkOpen.value = true; emojiOpen.value = false; error.value = ''
  await nextTick(); linkInput.value?.focus()
}
function applyLink(remove = false) {
  if (!editable.value || !editor.value || !linkSelection) return
  const href = safeRichLink(linkValue.value)
  if (!remove && !href) { error.value = '请输入有效的 https、http 或 mailto 链接'; return }
  const chain = editor.value.chain().focus().setTextSelection(linkSelection).extendMarkRange('link')
  if (remove) chain.unsetLink().run()
  else if (linkSelection.from === linkSelection.to && !editor.value.isActive('link')) chain.insertContent({ type: 'text', text: href!, marks: [{ type: 'link', attrs: { href } }] }).run()
  else chain.setLink({ href: href! }).run()
  linkOpen.value = false; error.value = ''
}
function closeLink() { linkOpen.value = false; editor.value?.commands.focus() }
function invalidateRead() { ++readVersion; if (reading.value) { reading.value = false; emit('busy', markdownOpen.value) } }
function syncContent() {
  const instance = editor.value
  if (!instance || disposed) return
  if (props.document && toRaw(props.document) === lastEmittedDocument) return
  try {
    const doc = currentDocument(), node = instance.schema.nodeFromJSON(doc)
    // Echoed v-model updates do not replace the document: selection and undo
    // history belong to the user, not to the component render cycle.
    if (instance.state.doc.eq(node)) return
    invalidateRead(); lastEmittedDocument = null
    instance.commands.setContent(doc, { emitUpdate: false }); mention.value = null; linkOpen.value = false; emojiOpen.value = false; error.value = ''
  } catch { error.value = '富文本内容无法载入，请重新打开需求' }
}
const smartPaste=ref(true)
function handleClipboardPaste(event:ClipboardEvent) {
  const files=clipboardFiles(event.clipboardData)
  if(files.length){event.preventDefault();if(editable.value)void insertFiles(files);return true}
  const instance=editor.value, text=event.clipboardData?.getData?.('text/plain')||''
  if(!instance||!editable.value||!smartPaste.value||!text)return false
  // 已在代码块中时按原文粘贴，不能把 Markdown 符号再次转换成文档节点。
  if(instance.isActive('codeBlock')) return false
  // 保留编辑器内复制的富文本节点和附件，不能误降级成纯文本。
  if(event.clipboardData?.getData?.('text/html')?.includes('data-pm-slice'))return false
  let language=''
  try{language=JSON.parse(event.clipboardData?.getData?.('vscode-editor-data')||'{}').mode||''}catch{/* 非标准剪贴板元数据忽略。 */}
  try{
    const result=analyzePaste(text,typeof language==='string'?language:'')
    if(!result)return false
    event.preventDefault();instance.chain().focus().insertContent(result.document.content||[]).run()
    codeMessage.value=result.kind==='code'?t('已识别代码语言：{language}',{language:result.language||'auto'}):t('Markdown 已转换为文档结构')
    if(result.warnings.length)error.value=result.warnings.join('；')
    return true
  }catch(cause){event.preventDefault();error.value=cause instanceof Error?cause.message:'Markdown 无法解析';return true}
}
function createEditor() {
  editor.value = new Editor({
    extensions: [highlightedCode, StarterKit.configure({ codeBlock:false, heading: { levels: [1, 2, 3, 4, 5, 6] }, trailingNode: false, link: { openOnClick: false, autolink: false, linkOnPaste: false, isAllowedUri: url => !!safeRichLink(url), HTMLAttributes: { target: '_blank', rel: 'noopener noreferrer nofollow' } } }), Mention, assetExtension('image'), assetExtension('attachment'), ...tableNodes],
    content: currentDocument(), editable: editable.value,
    editorProps: {
      attributes: state => ({ id: props.inputId || 'rich-input-' + instanceID, class: 'rich-document', role: 'textbox', 'aria-label': props.label || t('富文本编辑器'), 'aria-multiline': 'true', 'aria-readonly': String(!!props.readonly || !editable.value), 'data-empty': String(state.doc.childCount === 1 && !!state.doc.firstChild?.isTextblock && state.doc.firstChild.content.size === 0), 'data-placeholder': props.placeholder || t('描述需求，或粘贴截图；输入 @ 选择协作成员…') }),
      transformPastedHTML: html => sanitizeRichHTML(html),
      handleKeyDown: (_view, event) => onKeydown(event),
      handlePaste: (_view, event) => handleClipboardPaste(event),
      handleDrop: (_view, event) => { const files = clipboardFiles(event.dataTransfer); if (!files.length) return false; event.preventDefault(); if (editable.value) void insertFiles(files); return true },
      handleClick: (_view, _pos, event) => { const link = (event.target as Element)?.closest?.('a'); if (link && !props.readonly) { event.preventDefault(); return true }; return false },
    },
    onUpdate: () => { publish(); updateMention(); revision.value++ },
    onSelectionUpdate: () => { updateMention(); revision.value++ },
    onFocus: () => { focused.value = true; mentionDismissed.value = false; updateMention() },
    onBlur: () => { focused.value = false },
  })
}
onMounted(createEditor)
watch([() => props.document, () => props.modelValue, () => props.mentionUserIds, () => props.mentionNames], syncContent)
watch(() => props.requirementId, () => {
  markdownOpen.value=false;markdownSource.value='';emit('busy',false)
  invalidateRead(); mention.value = null; linkOpen.value = false; emojiOpen.value = false; lastEmittedDocument = null
  // A different requirement is a new editing scope, even if both happen to
  // contain identical text. Its undo stack must never restore the old draft.
  if (editor.value) { const previous = editor.value; editor.value = null; previous.destroy(); createEditor() }
})
watch(editable, value => editor.value?.setEditable(value, false))
watch([() => props.disabled, () => props.readonly], ([disabled, readonly]) => { if (disabled || readonly) { markdownOpen.value=false; markdownSource.value=''; emit('busy',false); invalidateRead(); linkOpen.value = false; emojiOpen.value = false; mention.value = null } })
watch(() => matches.value.length, length => { mentionIndex.value = Math.max(0, Math.min(mentionIndex.value, length - 1)) })
watch([mentionVisible, mentionIndex, () => props.label], () => {
  const element = editor.value?.view.dom
  if (!element) return
  element.setAttribute('aria-autocomplete', 'list'); element.setAttribute('aria-expanded', String(mentionVisible.value))
  if (mentionVisible.value) { element.setAttribute('aria-controls', mentionListID); element.setAttribute('aria-activedescendant', mentionListID + '-' + mentionIndex.value) }
  else { element.removeAttribute('aria-controls'); element.removeAttribute('aria-activedescendant') }
})
watch([() => props.label, () => props.placeholder, () => t('富文本编辑器')], () => {
  const element = editor.value?.view.dom
  if (!element) return
  element.setAttribute('aria-label', props.label || t('富文本编辑器'))
  element.setAttribute('data-placeholder', props.placeholder || t('描述需求，或粘贴截图；输入 @ 选择协作成员…'))
})
onBeforeUnmount(() => { disposed = true; markdownOpen.value=false; emit('busy',false); invalidateRead(); editor.value?.destroy(); editor.value = null })
defineExpose({ focus: () => editor.value?.commands.focus(), reading, insertFiles })
</script>

<template>
  <div ref="wrapper" class="rich-editor" :class="{ 'rich-readonly': readonly, 'rich-comment': mode === 'comment', 'is-disabled': disabled, 'rich-fullscreen': fullscreen }" :aria-busy="reading" @keydown="wrapperKeydown">
    <div v-if="!readonly" class="rich-toolbar" role="toolbar" :aria-label="t('富文本格式工具')">
      <select :value="paragraphType" :disabled="!editable" :aria-label="t('段落样式')" @change="setParagraph"><option value="paragraph">{{ t('正文') }}</option><option value="h1">{{ t('一级标题') }}</option><option value="h2">{{ t('二级标题') }}</option><option value="h3">{{ t('三级标题') }}</option><option value="h4">{{t('四级标题')}}</option><option value="h5">{{t('五级标题')}}</option><option value="h6">{{t('六级标题')}}</option></select>
      <button v-for="item in formatButtons" :key="item.name" type="button" :class="[item.name, { active: isActive(item.name) }]" :disabled="!editable" :title="t(item.label)" :aria-label="t(item.label)" :aria-pressed="isActive(item.name)" @mousedown.prevent @click="format(item.name)">{{ item.symbol }}</button>
      <button type="button" :disabled="!editable" :aria-pressed="isActive('codeBlock')" :class="{active:isActive('codeBlock')}" @mousedown.prevent @click="toggleCode">{{ t('代码块') }}</button>
      <button type="button" :disabled="!editable" :aria-pressed="smartPaste" @mousedown.prevent @click="smartPaste=!smartPaste">{{t('智能粘贴')}}</button><button type="button" :disabled="!editable||markdownOpen" @mousedown.prevent @click="beginMarkdown">{{t('导入 Markdown')}}</button>
      <select v-if="isActive('codeBlock')" :value="codeLanguage" :disabled="!editable" :aria-label="t('代码语言')" @change="setCodeLanguage"><option v-for="language in codeLanguages" :key="language" :value="language">{{language==='plaintext'?t('纯文本'):language}}</option><option v-if="!codeLanguages.includes(codeLanguage)" :value="codeLanguage">{{codeLanguage}}</option></select>
      <button v-if="isActive('codeBlock')" type="button" @mousedown.prevent @click="copyCode">{{t('复制代码')}}</button>
      <span class="rich-tool-separator"></span><button type="button" :disabled="!editable" :class="{ active: isActive('link') }" :title="t('插入链接')" :aria-label="t('插入链接')" @mousedown.prevent @click="openLink">↗</button>
      <button type="button" :disabled="!editable" @mousedown.prevent @click="openFilePicker(true)">{{ t('图片') }}</button><button type="button" :disabled="!editable" @mousedown.prevent @click="openFilePicker()">{{ t('附件') }}</button>
      <button v-if="mode === 'comment'" type="button" :disabled="!editable" :aria-expanded="emojiOpen" :aria-controls="emojiID" :aria-label="t('表情与表情包')" @mousedown.prevent @click="emojiOpen = !emojiOpen">☺</button>
      <span class="rich-tool-separator"></span><button type="button" :disabled="!editable || !editor?.can().undo()" :title="t('撤销')" :aria-label="t('撤销')" @mousedown.prevent @click="history('undo')">↶</button><button type="button" :disabled="!editable || !editor?.can().redo()" :title="t('重做')" :aria-label="t('重做')" @mousedown.prevent @click="history('redo')">↷</button>
      <button v-if="allowFullscreen && !readonly" type="button" class="rich-fullscreen-button" :disabled="disabled" :aria-pressed="fullscreen" :title="t(fullscreen ? '退出全屏描述' : '全屏描述')" :aria-label="t(fullscreen ? '退出全屏描述' : '全屏描述')" @mousedown.prevent @click="toggleFullscreen">{{ fullscreen ? '↙' : '⤢' }}</button>
      <input ref="fileInput" class="rich-file-input" type="file" multiple :disabled="!editable" :aria-label="t('选择附件')" @change="pickFiles($event)"><input ref="imageInput" class="rich-file-input" type="file" accept="image/png,image/jpeg,image/gif" multiple :disabled="!editable" :aria-label="t('选择图片或表情包')" @change="pickFiles($event, true)">
    </div>
    <div v-if="linkOpen && !readonly" class="rich-link-dialog" role="dialog" :aria-label="t('插入链接')" @keydown.esc.stop.prevent="closeLink">
      <label :for="'rich-link-' + instanceID">{{ t('链接地址') }}</label><input :id="'rich-link-' + instanceID" ref="linkInput" v-model="linkValue" type="url" placeholder="https://" :disabled="!editable" @keydown.enter.prevent="applyLink()"><button type="button" :disabled="!editable" @click="applyLink()">{{ t('应用') }}</button><button type="button" :disabled="!editable" @click="applyLink(true)">{{ t('移除链接') }}</button><button type="button" @click="closeLink">{{ t('取消') }}</button>
    </div>
    <div v-if="emojiOpen && !readonly" :id="emojiID" class="rich-emoji-panel" role="region" :aria-label="t('表情与表情包')" @keydown.esc.stop.prevent="emojiOpen = false"><div><button v-for="emoji in richEmoji" :key="emoji" type="button" :disabled="!editable" :aria-label="emoji" @mousedown.prevent @click="addEmoji(emoji)">{{ emoji }}</button></div><button type="button" :disabled="!editable" @mousedown.prevent @click="openFilePicker(true)">{{ t('上传表情包') }}</button></div>
    <section v-if="markdownOpen&&!readonly" class="markdown-import" :aria-label="t('Markdown 导入预览')" @keydown.stop>
      <header><b>{{t('导入 Markdown')}}</b><button type="button" :disabled="reading" @click="markdownFile?.click()">{{t('选择 .md 文件')}}</button><input ref="markdownFile" type="file" accept=".md,.markdown,text/markdown,text/plain" hidden @change="readMarkdown"></header>
      <textarea v-model="markdownSource" :disabled="reading" maxlength="100000" :aria-label="t('Markdown 原文')" :placeholder="t('粘贴 AI 生成的 Markdown，预览后插入当前位置')" rows="7"></textarea>
      <p v-for="warning in markdownPreview.warnings" :key="warning">{{t(warning)}}</p><p v-if="markdownPreview.error" role="alert">{{t(markdownPreview.error)}}</p>
      <div v-if="markdownPreview.document&&markdownSource" class="markdown-preview"><RichTextEditor model-value="" :document="markdownPreview.document" :members="[]" readonly :label="t('Markdown 预览')" /></div>
      <footer><button type="button" :disabled="reading" @click="closeMarkdown">{{t('取消')}}</button><button type="button" :disabled="!editable||!markdownSource.trim()||!!markdownPreview.error" @click="insertMarkdown">{{t('插入正文')}}</button></footer>
    </section>
    <EditorContent :editor="editor || undefined" class="rich-content" :inert="markdownOpen || undefined" />
    <div v-if="mentionVisible" class="rich-mention-popup" :style="mentionStyle"><header><b>{{ t('提及项目成员') }}</b><span>{{ t('{count} 位匹配', { count: matches.length }) }}</span></header><div :id="mentionListID" ref="mentionList" role="listbox" :aria-label="t('选择提及成员')"><button v-for="(member, index) in matches" :id="mentionListID + '-' + index" :key="member.id" type="button" role="option" :aria-selected="index === mentionIndex" :class="{ active: index === mentionIndex }" @mousedown.prevent @mouseenter="mentionIndex = index" @click="chooseMember(member)"><span><b>{{ member.name }} <span v-if="recentMentions.badge(member.id)" class="mention-recency">{{ recentMentions.badge(member.id) }}</span></b><small>{{ member.email || member.id }}</small></span><em>{{ roleLabel(member) }}</em></button><p v-if="!matches.length">{{ t('没有匹配的成员，请尝试姓名、邮箱或角色。') }}</p></div><footer>{{ t('↑ ↓ 选择 · Enter 确认 · Esc 关闭') }}</footer></div>
    <p v-if="error" class="rich-error" role="alert">{{ t(error) }}</p>
    <p v-if="codeMessage" class="rich-footer" role="status">{{t(codeMessage)}}</p>
    <footer v-if="!readonly" class="rich-footer"><span v-if="reading" role="status">{{ t('正在读取文件，请稍候…') }}</span><span v-else>{{ mode === 'comment' ? t('支持表情、截图与附件 · Ctrl / ⌘ + Enter 发布') : t('支持粘贴截图 · 单文件 10 MiB，待保存文件合计 20 MiB') }}</span></footer>
  </div>
</template>

<style scoped>
.markdown-import{border-bottom:1px solid var(--line);padding:12px;display:grid;gap:8px;min-width:0;background:var(--surface)}.markdown-import header,.markdown-import footer{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.markdown-import header b{margin-right:auto}.markdown-import footer{justify-content:flex-end}.markdown-import textarea{width:100%;max-width:100%;box-sizing:border-box;font-family:ui-monospace,monospace;font-size:13px;border:1px solid var(--line);background:var(--surface);color:var(--ink);padding:10px;border-radius:6px;resize:vertical}.markdown-import button{min-height:32px;font:inherit;font-size:13px;border:1px solid var(--line);border-radius:6px;padding:4px 10px;background:var(--surface);color:var(--ink);cursor:pointer}.markdown-import p{font-size:12px;color:var(--muted);margin:0}.markdown-preview{max-height:300px;overflow:auto}.rich-content :deep(table){width:100%;border-collapse:collapse;table-layout:fixed;margin:12px 0}.rich-content :deep(th),.rich-content :deep(td){border:1px solid var(--line);padding:8px;min-width:0;overflow-wrap:anywhere;vertical-align:top}.rich-content :deep(th){background:var(--surface-subtle);font-weight:600}.rich-content :deep(pre){white-space:pre;overflow-x:auto;tab-size:4}
.rich-editor{position:relative;border:1px solid #dde2eb;border-radius:9px;background:#fff;min-width:0;color:#344054}.rich-editor:focus-within{border-color:#8780e9;box-shadow:0 0 0 3px #756dea0b}.rich-toolbar{display:flex;align-items:center;flex-wrap:wrap;gap:3px;padding:8px 10px;border-bottom:1px solid #e8ebf1;background:#fafbfd;border-radius:9px 9px 0 0}.rich-toolbar button,.rich-toolbar select{height:29px;border:0;border-radius:5px;background:transparent;color:#5b6577;min-width:29px;padding:3px 7px;font-size:12px;line-height:1.3;width:auto}.rich-toolbar select{max-width:105px;border:1px solid #e4e7ec;background:#fff;margin-right:6px}.rich-toolbar button:hover,.rich-toolbar button.active{background:#eceafe;color:#6559d4}.rich-toolbar button:disabled{opacity:.38;cursor:not-allowed;background:transparent}.rich-toolbar .bold{font-weight:800}.rich-toolbar .italic{font-style:italic}.rich-toolbar .underline{text-decoration:underline}.rich-toolbar .strike{text-decoration:line-through}.rich-tool-separator{height:18px;border-left:1px solid #e1e5ec;margin:0 4px}.rich-file-input{display:none}.rich-content :deep(.rich-document){outline:0;min-height:240px;padding:18px 20px;font-size:14px;line-height:1.85;overflow-wrap:anywhere;white-space:pre-wrap}.rich-comment .rich-content :deep(.rich-document){min-height:112px;padding:14px 16px}.rich-content :deep(.rich-document>p:first-child:last-child:empty::before){content:attr(data-placeholder);color:#98a2b3;pointer-events:none}.rich-content :deep(p){margin:0 0 12px;line-height:inherit;color:inherit}.rich-content :deep(h1){font-size:25px;line-height:1.45;margin:18px 0 12px}.rich-content :deep(h2){font-size:21px;line-height:1.5;margin:16px 0 10px}.rich-content :deep(h3){font-size:17px;line-height:1.6;margin:14px 0 8px}.rich-content :deep(ul),.rich-content :deep(ol){padding-left:26px;margin:10px 0}.rich-content :deep(li>p){margin:3px 0}.rich-content :deep(blockquote){border-left:3px solid #a9a3ed;background:#f8f7ff;margin:14px 0;padding:8px 16px;color:#667085}.rich-content :deep(blockquote p:last-child){margin-bottom:0}.rich-content :deep(a){color:#6258d8;text-decoration:underline;cursor:pointer}.rich-content :deep(pre){background:#f4f6fa;border:1px solid #e7eaf0;border-radius:7px;padding:12px 15px;white-space:pre-wrap}.rich-content :deep(code){font-family:ui-monospace,SFMono-Regular,monospace;background:#f1f3f7;padding:1px 3px;border-radius:3px;font-size:.9em}.rich-content :deep(.rich-mention){background:#edebff;color:#6257cf;border-radius:4px;padding:2px 4px;white-space:nowrap}.rich-content :deep(hr){border:0;border-top:1px solid #e2e6ef;margin:20px 0}.rich-readonly{border:0;border-radius:0;box-shadow:none!important}.rich-readonly .rich-content :deep(.rich-document){min-height:0;padding:0}.rich-readonly .rich-content :deep(.rich-document>p:last-child){margin-bottom:0}.rich-footer{border-top:1px solid #eef0f4;color:#98a2b3;font-size:10px;padding:8px 12px;line-height:1.6}.rich-error{margin:0;padding:9px 12px;color:#b42318;background:#fff5f5;font-size:11px;line-height:1.6}.rich-link-dialog{display:flex;align-items:center;flex-wrap:wrap;gap:7px;padding:11px 12px;background:#f5f4ff;border-bottom:1px solid #e4e1f6}.rich-link-dialog label{font-size:11px;margin:0;color:#667085}.rich-link-dialog input{min-width:160px;flex:1;width:auto;border:1px solid #d8d4ec;padding:6px 8px;border-radius:5px;font-size:12px}.rich-link-dialog button,.rich-emoji-panel>button{border:1px solid #ddd8f2;border-radius:5px;padding:6px 9px;font-size:11px;background:#fff;color:#655bd2}.rich-emoji-panel{padding:10px 12px;border-bottom:1px solid #e8e6f2;background:#fbfaff}.rich-emoji-panel>div{display:grid;grid-template-columns:repeat(8,32px);gap:4px;margin-bottom:8px}.rich-emoji-panel>div button{font-size:21px;border:0;border-radius:5px;background:transparent;padding:3px}.rich-emoji-panel>div button:hover{background:#ece9ff}.rich-mention-popup{position:absolute;z-index:70;left:18px;bottom:34px;width:min(410px,calc(100% - 36px));border:1px solid #e0e3ec;border-radius:8px;background:#fff;box-shadow:0 14px 38px #17203329;overflow:hidden}.rich-mention-popup header{display:flex;justify-content:space-between;gap:8px;padding:10px 12px;border-bottom:1px solid #eef0f4;font-size:11px}.rich-mention-popup header span{color:#98a2b3}.rich-mention-popup [role=listbox]{max-height:210px;overflow:auto;padding:4px}.rich-mention-popup [role=option]{display:flex;align-items:center;justify-content:space-between;gap:10px;width:100%;border:0;border-radius:5px;background:#fff;padding:8px 9px;color:#344054;text-align:left}.rich-mention-popup [role=option].active{background:#f0edff}.rich-mention-popup b{font-size:12px;font-weight:600}.rich-mention-popup small{display:block;font-size:10px;color:#98a2b3;margin-top:3px}.rich-mention-popup em{font-style:normal;font-size:10px;color:#8d95a5;white-space:nowrap}.rich-mention-popup footer,.rich-mention-popup p{font-size:10px;color:#98a2b3;padding:8px 12px;margin:0;border-top:1px solid #eef0f4}.is-disabled .rich-toolbar,.is-disabled .rich-content{background:#fafbfc}.rich-fullscreen{position:fixed;z-index:260;inset:max(14px,env(safe-area-inset-top)) max(14px,env(safe-area-inset-right)) max(14px,env(safe-area-inset-bottom)) max(14px,env(safe-area-inset-left));display:flex;flex-direction:column;width:auto;max-width:none;min-height:0;max-height:none;overflow:hidden;border-radius:12px;box-shadow:0 24px 78px color-mix(in srgb,#172033 28%,transparent);background:var(--card,#fff)}.rich-fullscreen .rich-toolbar{flex:none;border-radius:12px 12px 0 0}.rich-fullscreen .rich-content{flex:1;min-height:0;overflow:auto}.rich-fullscreen .rich-content :deep(.rich-document){min-height:100%;box-sizing:border-box;padding:24px 28px}.rich-fullscreen .rich-footer{flex:none}.rich-fullscreen-button{margin-left:auto!important;border:1px solid #dde2eb!important;background:#fff!important}.rich-fullscreen-button:hover:not(:disabled){background:#eceafe!important}@media(max-width:700px){.rich-content :deep(.rich-document){padding:15px;min-height:200px}.rich-toolbar{gap:2px;padding:7px}.rich-toolbar button{font-size:11px;padding:3px 5px}.rich-mention-popup em{display:none}.rich-fullscreen{inset:0;border-radius:0}.rich-fullscreen .rich-toolbar{border-radius:0}.rich-fullscreen .rich-content :deep(.rich-document){padding:18px;min-height:100%}}
</style>

<style scoped>
.rich-content :deep(.rich-document[data-empty="true"]::before){content:attr(data-placeholder);float:left;height:0;color:#98a2b3;pointer-events:none;font-weight:400}.rich-readonly .rich-content :deep(.rich-document[data-empty="true"]::before){display:none}
</style>
<style scoped>
@media(max-width:820px){
 .rich-toolbar button,.rich-toolbar select{min-width:44px;min-height:44px;height:auto;font-size:13px}
 .rich-toolbar select{font-size:16px;max-width:140px}.rich-toolbar{gap:4px}
 .rich-editor:not(.rich-readonly) .rich-content :deep(.rich-document){font-size:max(16px,1em)}
 .rich-link-dialog input{min-width:0;width:100%;flex-basis:100%;font-size:16px;min-height:44px}
 .rich-link-dialog button,.rich-emoji-panel>button{min-height:44px}
 .rich-emoji-panel>div{grid-template-columns:repeat(auto-fit,minmax(44px,1fr))}.rich-emoji-panel>div button{min-height:44px}
 .rich-mention-popup [role=option]{min-height:44px}.rich-mention-popup [role=option]>span{min-width:0;overflow-wrap:anywhere}
}
</style>
<style scoped>
/* 提及仍由正文中的 @ 触发；这里只统一候选菜单的紧凑扁平外观。 */
.rich-mention-popup{border-color:var(--line,#d9e0ea);border-radius:5px;background:var(--surface-raised,#fff);color:var(--ink,#26334a);box-shadow:none}.rich-mention-popup header{padding:6px 8px;border-color:var(--line,#d9e0ea)}.rich-mention-popup [role=listbox]{padding:3px;max-height:240px}.rich-mention-popup [role=option]{min-height:34px;padding:5px 6px;border-radius:3px;background:transparent;color:inherit}.rich-mention-popup [role=option].active{background:var(--primary-soft,#eef4ff)}.rich-mention-popup [role=option]:focus-visible{outline:2px solid var(--primary,#3370eb);outline-offset:-2px}.rich-mention-popup small{margin-top:1px}.rich-mention-popup small,.rich-mention-popup em,.rich-mention-popup header span,.rich-mention-popup footer,.rich-mention-popup p{color:var(--muted,#758298)}.rich-mention-popup footer,.rich-mention-popup p{padding:4px 8px;border-color:var(--line,#d9e0ea)}@media(max-width:820px){.rich-mention-popup [role=option]{min-height:44px}}
</style>
