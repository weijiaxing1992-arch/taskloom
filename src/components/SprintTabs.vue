<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { t } from '../i18n'
import { moveSprintTab, normalizeSprintTabs } from '../sprintTabs'
const props=defineProps<{modelValue:string;order:string[];disabled?:boolean}>()
const emit=defineEmits<{(event:'update:modelValue',value:string):void;(event:'update:order',value:string[]):void}>()
const dragging=ref(''),over=ref(''),message=ref(''),buttons=new Map<string,HTMLElement>()
function buttonRef(name:string,element:unknown){if(element)buttons.set(name,element as HTMLElement);else buttons.delete(name)}
function finish(){dragging.value='';over.value=''}
function start(event:DragEvent,name:string){if(props.disabled){event.preventDefault();return}dragging.value=name;event.dataTransfer?.setData('text/plain',name);if(event.dataTransfer)event.dataTransfer.effectAllowed='move'}
function dragover(event:DragEvent,name:string){if(props.disabled||!dragging.value)return;event.preventDefault();over.value=name;if(event.dataTransfer)event.dataTransfer.dropEffect='move'}
function reorder(name:string,target:number){if(props.disabled)return;const before=normalizeSprintTabs(props.order),next=moveSprintTab(before,name,target);if(JSON.stringify(next)===JSON.stringify(before))return;emit('update:order',next);message.value='页签顺序已调整';void nextTick(()=>buttons.get(name)?.focus({preventScroll:true}))}
function drop(event:DragEvent,name:string){if(props.disabled||!dragging.value)return;event.preventDefault();reorder(dragging.value,props.order.indexOf(name));finish()}
function select(name:string){if(!props.disabled&&!dragging.value)emit('update:modelValue',name)}
function keydown(event:KeyboardEvent,name:string){
  if(props.disabled)return
  const order=normalizeSprintTabs(props.order),index=order.indexOf(name)
  if(event.key==='Escape'){finish();return}
  if(event.altKey&&['ArrowLeft','ArrowRight'].includes(event.key)){event.preventDefault();event.stopPropagation();reorder(name,index+(event.key==='ArrowLeft'?-1:1));return}
  const target=event.key==='ArrowLeft'?(index-1+order.length)%order.length:event.key==='ArrowRight'?(index+1)%order.length:event.key==='Home'?0:event.key==='End'?order.length-1:-1
  if(target<0)return;event.preventDefault();emit('update:modelValue',order[target]!);void nextTick(()=>buttons.get(order[target]!)?.focus({preventScroll:true}))
}
</script>
<template>
  <div class="sprint-tabs-wrap"><nav class="iteration-tabs reorderable-sprint-tabs" role="tablist" :aria-label="t('迭代页签，可拖动排序')" :title="t('拖动页签调整顺序，或按 Alt + ← / →')">
    <button v-for="name in order" :key="name" :ref="element=>buttonRef(name,element)" type="button" role="tab" :tabindex="modelValue===name?0:-1" :aria-selected="modelValue===name" :disabled="disabled" :draggable="!disabled" :class="{active:modelValue===name,dragging:dragging===name,'drop-target':over===name&&dragging!==name}" @click="select(name)" @dragstart="start($event,name)" @dragover="dragover($event,name)" @drop="drop($event,name)" @dragend="finish" @keydown="keydown($event,name)"><span class="tab-grip" aria-hidden="true">⠿</span>{{t(name)}}<slot :name="name" /></button>
  </nav><span class="sprint-tab-hint">{{t('拖动排序 · Alt + ← / →')}}</span><span class="sr-only" role="status" aria-live="polite">{{t(message)}}</span></div>
</template>
<style scoped>
.sprint-tabs-wrap{position:relative;border-bottom:1px solid var(--line);display:flex;align-items:center;min-width:0;gap:10px;padding-right:20px}.reorderable-sprint-tabs{min-width:0;overflow-x:auto;white-space:nowrap;flex:1;border-bottom:0;scrollbar-width:thin}.reorderable-sprint-tabs button{flex:none;display:inline-flex;align-items:center;gap:6px;cursor:grab}.reorderable-sprint-tabs button:active{cursor:grabbing}.reorderable-sprint-tabs button:focus-visible{outline:2px solid #1677ff;outline-offset:-3px}.reorderable-sprint-tabs button.dragging{opacity:.45}.reorderable-sprint-tabs button.drop-target{box-shadow:inset 3px 0 #1677ff;background:var(--primary-soft)}.tab-grip{font-size:13px;opacity:.45}.sprint-tab-hint{font-size:10px;white-space:nowrap;color:var(--muted)}.sr-only{position:absolute;width:1px;height:1px;padding:0;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}@media(max-width:1250px){.sprint-tab-hint{display:none}}
</style>
