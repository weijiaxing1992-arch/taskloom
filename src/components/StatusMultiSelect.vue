<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { t } from '../i18n'
import { normalizeStatusSelection, statusOptionLabel, workflowStyle, type StatusOption } from '../requirementWorkflow'
const props=defineProps<{modelValue:string[];options:StatusOption[];disabled?:boolean;label?:string}>()
const emit=defineEmits<{(event:'update:modelValue',value:string[]):void}>()
const root=ref<HTMLElement|null>(null),trigger=ref<HTMLButtonElement|null>(null),open=ref(false),query=ref('')
const id='status-multi-'+useId()
const selected=computed(()=>normalizeStatusSelection(props.modelValue))
const options=computed(()=>{const values=[...props.options];for(const value of selected.value)if(!values.some(item=>item.value===value))values.push({value,label:value,custom:true});return values})
const filtered=computed(()=>options.value.filter(item=>statusOptionLabel(item,t).toLowerCase().includes(query.value.trim().toLowerCase())))
function toggle(value:string){if(!props.disabled)emit('update:modelValue',selected.value.includes(value)?selected.value.filter(item=>item!==value):[...selected.value,value])}
function dismiss(event:Event){if(root.value&&!event.composedPath().includes(root.value))open.value=false}
function escape(event:KeyboardEvent){if(event.key==='Escape'&&open.value){event.preventDefault();event.stopPropagation();open.value=false;trigger.value?.focus()}}
onMounted(()=>document.addEventListener('pointerdown',dismiss))
onBeforeUnmount(()=>document.removeEventListener('pointerdown',dismiss))
</script>
<template>
  <div ref="root" class="status-filter" @keydown="escape" @focusout="!($event.relatedTarget && $el.contains($event.relatedTarget)) && (open=false)">
    <button ref="trigger" type="button" class="status-filter-trigger" :disabled="disabled" :aria-label="label||t('工作状态（多选）')" :aria-expanded="open" :aria-controls="id" @click="open=!open"><span>{{selected.length?t('已选 {count} 个状态',{count:selected.length}):t('全部状态')}}</span><span aria-hidden="true">⌄</span></button>
    <section v-if="open&&!disabled" :id="id" class="status-filter-menu" :aria-label="label||t('工作状态（多选）')">
      <input v-model="query" :aria-label="t('查找状态')" :placeholder="t('查找状态')">
      <div class="status-filter-actions"><small>{{t('多选状态之间为“或”关系')}}</small><button type="button" @click="emit('update:modelValue',[])">{{t('清空')}}</button></div>
      <div class="status-filter-options"><label v-for="option in filtered" :key="option.value"><input type="checkbox" :checked="selected.includes(option.value)" :aria-label="statusOptionLabel(option,t)" @change="toggle(option.value)"><span class="workflow-color" :style="workflowStyle({status:option.value,statusColor:option.color,statusSystem:!option.custom})">{{statusOptionLabel(option,t)}}</span></label><p v-if="!filtered.length">{{t('没有匹配的状态')}}</p></div>
      <button type="button" class="status-filter-done" @click="open=false;trigger?.focus()">{{t('完成选择')}}</button>
    </section>
  </div>
</template>
<style scoped>
.status-filter{position:relative;flex:none;min-width:155px}.status-filter-trigger{display:flex;align-items:center;justify-content:space-between;gap:15px;width:100%;min-height:34px;border:1px solid var(--line);border-radius:8px;background:var(--surface,#fff);color:var(--ink);padding:6px 10px;font-size:12px;box-shadow:none}.status-filter-menu{position:absolute;left:0;top:calc(100% + 6px);width:300px;max-width:calc(100vw - 32px);padding:10px;background:var(--surface,#fff);border:1px solid var(--line);border-radius:8px;box-shadow:none;z-index:90}.status-filter-menu>input{width:100%;padding:8px;border:1px solid var(--line);border-radius:6px;background:var(--surface,#fff);color:var(--ink);font-size:12px}.status-filter-actions{display:flex;justify-content:space-between;align-items:center;margin:9px 0;color:var(--muted)}.status-filter-actions small{font-size:10px}.status-filter-actions button{border:0;background:transparent;color:var(--primary);font-size:11px}.status-filter-options{max-height:290px;overflow:auto;display:grid;gap:3px}.status-filter-options label{display:flex;align-items:center;gap:8px;margin:0;padding:7px 4px;font-size:12px;cursor:pointer}.status-filter-options input{width:15px;height:15px;flex:none;accent-color:var(--primary);margin:0}.workflow-color{padding:3px 7px;border:1px solid;border-radius:5px}.status-filter-options p{font-size:12px;color:var(--muted)}.status-filter-done{width:100%;margin-top:10px;border:0;background:var(--primary);color:white;border-radius:6px;padding:8px;font-size:12px}.status-filter-trigger:focus-visible,.status-filter-menu button:focus-visible{outline:2px solid var(--primary);outline-offset:2px}
</style>
<style scoped>
@media(max-width:820px){
 .status-filter{flex-basis:100%;width:100%;min-width:0}
 .status-filter-menu{width:100%;max-width:100%}
 .status-filter-trigger,.status-filter-done,.status-filter-options label{min-height:44px}
 .status-filter-actions{gap:8px;flex-wrap:wrap}.status-filter-actions button{min-height:36px}
 .status-filter-menu>input{font-size:16px}
}
</style>
