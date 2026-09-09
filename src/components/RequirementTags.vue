<script setup lang="ts">
import { t } from '../i18n'
import { computed, ref, useId, watch } from 'vue'
import { splitTags, tagPalette, tagStyle, validTagColor, type RequirementTagOption } from '../requirementFields'
const props = defineProps<{ modelValue: string | undefined; colors?: Record<string, string>; options?: RequirementTagOption[]; disabled?: boolean; readonly?: boolean }>()
const emit = defineEmits<{ (event: 'update:modelValue', value: string): void; (event: 'update:colors', value: Record<string, string>): void }>()
const tags = computed(() => splitTags(props.modelValue))
const name = ref(''), color = ref(tagPalette[0]!), editing = ref(''), error = ref('')
const opened=ref(false), activeIndex=ref(0), listID='requirement-tags-'+useId()
const suggestions=computed(()=>[...new Map((props.options||[]).filter(option=>option.name&&!tags.value.includes(option.name)).map(option=>[option.name,option])).values()].filter(option=>option.name.toLocaleLowerCase().includes(name.value.trim().toLocaleLowerCase())))
watch(() => props.modelValue, () => { if (editing.value && !tags.value.includes(editing.value)) reset() })
watch(suggestions,()=>{activeIndex.value=Math.max(0,Math.min(activeIndex.value,suggestions.value.length-1))})
function reset() { name.value = ''; editing.value = ''; error.value = ''; opened.value=false }
function editTag(tag: string) { if (props.disabled || props.readonly) return; editing.value = tag; name.value = tag; color.value = validTagColor(props.colors?.[tag]); error.value = ''; opened.value=false }
function choose(option:RequirementTagOption){if(props.disabled||props.readonly)return;name.value=option.name;color.value=validTagColor(option.color);saveTag()}
function keydown(event:KeyboardEvent){
 if(event.isComposing||event.keyCode===229)return
 if(event.key==='Escape'&&opened.value){event.preventDefault();opened.value=false;return}
 if(!editing.value&&['ArrowDown','ArrowUp'].includes(event.key)){event.preventDefault();opened.value=true;if(suggestions.value.length)activeIndex.value=(activeIndex.value+(event.key==='ArrowDown'?1:-1)+suggestions.value.length)%suggestions.value.length;return}
 if(event.key==='Enter'){event.preventDefault();if(opened.value&&!editing.value&&suggestions.value[activeIndex.value])choose(suggestions.value[activeIndex.value]!);else if(name.value.trim())saveTag()}
}
function saveTag() {
  if(props.disabled||props.readonly)return
  error.value = ''
  const next = name.value.trim()
  if (!next) { error.value = '请输入标签名称'; return }
  if (next.length > 30) { error.value = '标签名称最多 30 个字符'; return }
  if (/[,，\n]/.test(next)) { error.value = '每次添加一个标签，名称不能包含逗号'; return }
  if (tags.value.includes(next) && next !== editing.value) { error.value = '该标签已存在'; return }
  const nextTags = editing.value ? tags.value.map(tag => tag === editing.value ? next : tag) : [...tags.value, next]
  const nextColors = { ...(props.colors || {}) }
  if (editing.value && next !== editing.value) delete nextColors[editing.value]
  nextColors[next] = validTagColor(color.value)
  emit('update:colors', nextColors); emit('update:modelValue', nextTags.join(',')); reset()
}
function removeTag(tag: string) { if(props.disabled||props.readonly)return;const nextColors = { ...(props.colors || {}) }; delete nextColors[tag]; emit('update:colors', nextColors); emit('update:modelValue', tags.value.filter(value => value !== tag).join(',')); if (editing.value === tag) reset() }
</script>
<template>
  <div class="requirement-tags" @focusout="!($event.relatedTarget && $el.contains($event.relatedTarget)) && (opened = false)">
    <div class="tag-chips" :aria-label="t('需求标签')"><div v-for="tag in tags" :key="tag" class="requirement-tag" :style="tagStyle(colors?.[tag])"><span class="tag-swatch" :style="{ backgroundColor: validTagColor(colors?.[tag]) }"></span><span v-if="readonly">{{ tag }}</span><button v-else class="tag-name" type="button" :disabled="disabled" :aria-label="t('编辑标签 {name}',{name:tag})" @click="editTag(tag)">{{ tag }}</button><button v-if="!readonly" type="button" class="remove-tag" :disabled="disabled" :aria-label="t('移除标签 {name}',{name:tag})" @click="removeTag(tag)">×</button></div><span v-if="!tags.length" class="no-tags">{{ readonly ? t('暂无标签') : t('添加标签，快速识别需求主题') }}</span></div>
    <div v-if="!readonly" class="tag-builder"><div class="tag-entry-wrap"><div class="tag-entry"><input v-model="name" :disabled="disabled" maxlength="30" :aria-label="editing ? t('修改标签名称') : t('标签名称')" :placeholder="t('搜索已有标签，或输入新标签')" role="combobox" aria-autocomplete="list" :aria-expanded="opened&&!editing&&!disabled" :aria-controls="listID" :aria-activedescendant="opened&&suggestions[activeIndex]?listID+'-'+activeIndex:undefined" @focus="opened=!editing;activeIndex=0" @input="opened=!editing;activeIndex=0" @keydown="keydown"><button type="button" class="tag-save" :disabled="disabled || !name.trim()" @click="saveTag">{{ editing ? t('更新') : t('添加') }}</button><button v-if="editing||name" type="button" class="tag-cancel" :disabled="disabled" @click="reset">{{ t('取消') }}</button></div><div v-if="opened&&!editing&&!disabled" :id="listID" class="tag-suggestions" role="listbox" :aria-label="t('当前项目已有标签')"><button v-for="(option,index) in suggestions" :id="listID+'-'+index" :key="option.name" type="button" role="option" :aria-selected="activeIndex===index" :class="{active:activeIndex===index}" @mousedown.prevent @mouseenter="activeIndex=index" @click="choose(option)"><span class="tag-option-swatch" :style="{backgroundColor:validTagColor(option.color)}"></span><span>{{option.name}}</span><small v-if="option.count">{{t('{count} 条需求',{count:option.count})}}</small></button><p v-if="!suggestions.length">{{t(name?'暂无匹配标签，点击添加创建':'当前项目暂无其他标签')}}</p></div></div><div class="tag-palette" :aria-label="t('标签色块')"><button v-for="swatch in tagPalette" :key="swatch" type="button" :class="{ selected: color === swatch }" :style="{ backgroundColor: swatch }" :disabled="disabled" :aria-label="t('选择标签颜色 {color}',{color:swatch})" :aria-pressed="color === swatch" @click="color = swatch"><span v-if="color === swatch">✓</span></button><input v-model="color" type="color" :disabled="disabled" :aria-label="t('自定义标签颜色')" :title="t('自定义颜色')"></div><p v-if="error" class="tag-error" role="alert">{{ t(error) }}</p><small v-else class="tag-hint">{{ editing ? t('正在编辑「{name}」，修改后点击更新',{name:editing}) : t('选择已有标签立即添加；输入新标签后点击添加，未添加内容不会保存。') }}</small></div>
  </div>
</template>
<style scoped>
.tag-entry-wrap{position:relative}.tag-suggestions{position:absolute;z-index:75;top:100%;left:0;right:0;max-height:210px;overflow:auto;padding:5px;background:white;border:1px solid #e4e7ec;border-radius:7px;box-shadow:0 12px 30px #1720331f}.tag-suggestions button{display:flex;align-items:center;gap:8px;width:100%;border:0;border-radius:5px;background:white;padding:9px 7px;text-align:left;color:#344054;font-size:12px}.tag-suggestions button.active{background:#f1f0ff}.tag-suggestions button>span:nth-child(2){flex:1;overflow-wrap:anywhere}.tag-suggestions small,.tag-suggestions p{color:#98a2b3;font-size:10px}.tag-suggestions p{padding:8px;line-height:1.6}.tag-option-swatch{width:9px;height:9px;border-radius:3px;flex:none}
.requirement-tags{min-width:0}.tag-chips{display:flex;flex-wrap:wrap;gap:6px;min-height:30px;align-items:center}.requirement-tag{display:inline-flex;align-items:center;gap:5px;border:1px solid transparent;border-radius:5px;padding:4px 6px;max-width:100%;font-size:11px;line-height:1.5}.tag-swatch{height:6px;width:6px;border-radius:2px;flex:none}.tag-name,.remove-tag{border:0;background:transparent;color:inherit;padding:0;line-height:1.5;font-size:11px}.tag-name{max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.remove-tag{padding:0 2px;font-size:14px;opacity:.65}.tag-name:hover{text-decoration:underline}.remove-tag:hover{opacity:1}.no-tags{color:#98a2b3;font-size:11px}.tag-builder{margin-top:10px}.tag-entry{display:flex;gap:6px}.tag-entry input{width:100%;min-width:0;flex:1;border:1px solid #dfe3eb;border-radius:6px;padding:8px 9px;font-size:12px;background:#fff;color:#344054;outline:none}.tag-entry input:focus{border-color:#7771ed;box-shadow:0 0 0 3px #706aec18}.tag-save,.tag-cancel{padding:6px 10px;white-space:nowrap;border:1px solid #dfe3eb;border-radius:6px;color:#5b5ce2;font-size:11px;background:#fff}.tag-save:disabled{opacity:.5;cursor:not-allowed}.tag-palette{display:flex;flex-wrap:wrap;gap:6px;align-items:center;margin-top:9px}.tag-palette button{display:grid;place-items:center;width:20px;height:20px;padding:0;border:2px solid #fff;border-radius:5px;box-shadow:0 0 0 1px #e4e7ec;color:white;font-size:10px}.tag-palette button.selected{box-shadow:0 0 0 2px #5b5ce2}.tag-palette input[type=color]{width:24px;height:24px;min-height:0;border:1px solid #dfe3eb;padding:2px;border-radius:5px;background:white;cursor:pointer;box-shadow:none}.tag-hint{display:block;margin-top:8px;color:#98a2b3;font-size:10px;line-height:1.5}.tag-error{margin:7px 0 0;color:#b42318;font-size:11px}
</style>
