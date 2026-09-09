<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { t } from '../i18n'
import { layoutScope } from '../layoutScope'
import { useWorkspaceStore } from '../stores/workspace'
import { clearRecentMembers, memberPanelGeometry, readRecentMembers, recentMemberScope, rememberMembers } from '../recentMembers'
import { addActiveMemberIds, filterMentionMembers, memberCandidates, memberDepartments, membersInDepartment, mentionRoleLabels, normalizeMentionIds, type MentionMember } from '../mentions'

const props = withDefaults(defineProps<{ modelValue: string[]; members: MentionMember[]; snapshots?: { id: string; name: string }[]; legacyName?: string; disabled?: boolean; inputId?: string; label?: string; currentUserId?: string; showLead?: boolean; departmentId?: string; allDepartments?: {id:string;name:string}[]; memberRoles?: string[]; hint?: string; single?: boolean; compact?: boolean }>(), {showLead:true,single:false,compact:false})
const emit = defineEmits<{ (event: 'update:modelValue', ids: string[]): void }>()
const query = ref(''), department = ref(''), opened = ref(false), activeIndex = ref(0), input = ref<HTMLInputElement | null>(null)
const trigger=ref<HTMLButtonElement|null>(null)
const root=ref<HTMLElement|null>(null),panel=ref<HTMLElement|null>(null),panelStyle=ref<Record<string,string>>({})
const workspace=useWorkspaceStore(),recentIDs=ref<string[]>([])
const scope=computed(()=>recentMemberScope(layoutScope.value,workspace.session))
const directoryScope=ref(scope.value)
const contextUsable=()=>!workspace.identityConflict&&!workspace.operationDisabled&&!!scope.value&&scope.value===directoryScope.value&&!root.value?.closest('[inert]')&&!root.value?.closest('fieldset[disabled]')
function storage(){try{return typeof localStorage==='undefined'?undefined:localStorage}catch{return undefined}}
function refreshRecent(){recentIDs.value=readRecentMembers(storage(),scope.value)}
const listID = 'member-multi-' + useId()
const ids = computed(() => normalizeMentionIds(props.modelValue))
const eligibleMembers = computed(() => memberCandidates(props.members, props.memberRoles, props.departmentId))
const options = computed(() => filterMentionMembers(membersInDepartment(eligibleMembers.value, department.value), query.value, t))
const recentOptions=computed(()=>(contextUsable()?recentIDs.value:[]).map(id=>options.value.find(member=>member.id===id)).filter((member):member is MentionMember=>!!member))
// 报表传入完整组织目录，空部门也应可见；实际可选人员仍受成员范围与职能约束。
const departments = computed(() => memberDepartments(eligibleMembers.value,props.allDepartments))
const departmentMembers = computed(() => department.value ? membersInDepartment(eligibleMembers.value, department.value) : [])
const currentMember = computed(() => membersInDepartment(eligibleMembers.value, department.value).find(member => props.currentUserId ? member.id === props.currentUserId : member.isCurrent))
const candidateRoles = computed(() => normalizeMentionIds(props.memberRoles).map(role => t(mentionRoleLabels[role] || role)).join(' / '))
const fieldLabel = computed(() => props.label || t('处理人'))
const selected = computed(() => ids.value.map(id => ({ id, name: props.members.find(member => member.id === id)?.name || props.snapshots?.find(member => member.id === id)?.name || id, historical: !eligibleMembers.value.some(member => member.id === id), email: props.members.find(member => member.id === id)?.email })))
watch(() => options.value.length, length => { activeIndex.value = Math.max(0, Math.min(activeIndex.value, length - 1)) })
watch(departments, values => { if (department.value && !values.some(value => value.id === department.value)) department.value = '' })
function remember(added:string[]){if(contextUsable())recentIDs.value=rememberMembers(storage(),scope.value,added,eligibleMembers.value)}
function selectMe() { if (!props.disabled && contextUsable() && currentMember.value) {const id=currentMember.value.id;remember([id]);emit('update:modelValue',props.single?[id]:addActiveMemberIds(ids.value,[currentMember.value]));if(props.single)close(true)} }
function addDepartment() { if (!props.single&&!props.disabled&&contextUsable()&&department.value){const added=departmentMembers.value.filter(member=>!ids.value.includes(member.id));remember(added.map(member=>member.id));emit('update:modelValue',addActiveMemberIds(ids.value,departmentMembers.value))} }
function toggle(id: string) {
  if (props.disabled||!contextUsable()) return
  if (!ids.value.includes(id) && !eligibleMembers.value.some(member => member.id === id)) return
  if(props.single&&ids.value.length===1&&ids.value[0]===id){close(true);return}
  const adding=!ids.value.includes(id)
  if(adding)remember([id])
  emit('update:modelValue', props.single?[id]:adding?[...ids.value,id]:ids.value.filter(value=>value!==id))
  if(props.single){query.value='';close(true);return}
  query.value = ''; activeIndex.value = 0
  void nextTick(() => input.value?.focus())
}
function remove(id: string) { if (!props.disabled&&contextUsable()) emit('update:modelValue', ids.value.filter(value => value !== id)) }
function search() { if(!props.disabled&&contextUsable()){opened.value = true; activeIndex.value = 0} }
function role(member: MentionMember) { const key = member.projectRole || member.role || ''; return t(mentionRoleLabels[key] || key || '项目成员') }
function keydown(event: KeyboardEvent) {
  if(props.disabled||!contextUsable())return
  if (event.isComposing || event.keyCode === 229) return
  if (event.key === 'Escape' && opened.value) { event.preventDefault(); event.stopImmediatePropagation(); close(true); return }
  if (['ArrowDown', 'ArrowUp'].includes(event.key)) {
    event.preventDefault()
    if (!opened.value) { opened.value = true; activeIndex.value = event.key === 'ArrowDown' ? 0 : Math.max(0, options.value.length - 1) }
    else if (options.value.length) activeIndex.value = (activeIndex.value + (event.key === 'ArrowDown' ? 1 : -1) + options.value.length) % options.value.length
    return
  }
  if(opened.value&&['Home','End','PageUp','PageDown'].includes(event.key)){event.preventDefault();activeIndex.value=event.key==='Home'?0:event.key==='End'?Math.max(0,options.value.length-1):Math.max(0,Math.min(options.value.length-1,activeIndex.value+(event.key==='PageUp'?-8:8)));return}
  if (event.key === 'Enter') {
    // 这是表单内部的选择器，Enter 只选择成员，不能隐式提交整个表单。
    event.preventDefault()
    if (opened.value && options.value[activeIndex.value]) toggle(options.value[activeIndex.value]!.id)
    else opened.value = true
  }
}
// 筛选栏仅显示一个紧凑入口；搜索与部门筛选按需展开，表单用法保持不变。
function openCompact(){if(props.disabled||!contextUsable())return;if(opened.value){close(true);return}search();void nextTick(()=>input.value?.focus())}
function close(focus=false){opened.value=false;if(focus&&!props.disabled&&contextUsable())void nextTick(()=>{suppressFocus=true;(props.compact?trigger.value:input.value)?.focus();suppressFocus=false})}
let suppressFocus=false
function focusInput(){if(!suppressFocus)search()}
function clearRecent(){if(!contextUsable())return;clearRecentMembers(storage(),scope.value);recentIDs.value=[]}
function contains(target:EventTarget|null){return target instanceof Node&&(!!root.value?.contains(target)||!!panel.value?.contains(target))}
function outside(event:Event){if(opened.value&&!contains(event.target))close()}
function focusOut(event:FocusEvent){if(event.relatedTarget&&!contains(event.relatedTarget))close()}
function positionPanel(){
 const anchor=props.compact?trigger.value:input.value
 if(!opened.value||!anchor||!contextUsable()||props.disabled){if(opened.value)close();return}
 const rect=anchor.getBoundingClientRect(),vp=window.visualViewport,viewport={width:vp?.width||window.innerWidth,height:vp?.height||window.innerHeight,left:vp?.offsetLeft||0,top:vp?.offsetTop||0}
 if(!anchor.getClientRects().length||rect.bottom<viewport.top||rect.top>viewport.top+viewport.height){close();return}
 const box=memberPanelGeometry(rect,viewport)
 panelStyle.value={position:'fixed',left:box.left+'px',width:box.width+'px',maxHeight:box.maxHeight+'px',top:(box.up?box.bottom:box.top)+'px',transform:box.up?'translateY(-100%)':'none'}
}
function scrollActive(){void nextTick(()=>panel.value?.querySelector<HTMLElement>('[data-member-index="'+activeIndex.value+'"]')?.scrollIntoView({block:'nearest'}))}
let observer:MutationObserver|null=null
function stopPanel(){
 if(typeof window==='undefined'||typeof document==='undefined')return
 window.removeEventListener('resize',positionPanel);window.removeEventListener('scroll',positionPanel,true)
 window.visualViewport?.removeEventListener('resize',positionPanel);window.visualViewport?.removeEventListener('scroll',positionPanel)
 document.removeEventListener('pointerdown',outside,true);document.removeEventListener('focusin',outside,true)
 observer?.disconnect();observer=null
}
function startPanel(){
 stopPanel();if(typeof window==='undefined'||typeof document==='undefined'||!input.value)return;positionPanel();if(!opened.value)return
 window.addEventListener('resize',positionPanel);window.addEventListener('scroll',positionPanel,true)
 window.visualViewport?.addEventListener('resize',positionPanel);window.visualViewport?.addEventListener('scroll',positionPanel)
 document.addEventListener('pointerdown',outside,true);document.addEventListener('focusin',outside,true)
 if(typeof MutationObserver!=='undefined'){observer=new MutationObserver(positionPanel);for(let element=root.value;element;element=element.parentElement)observer.observe(element,{attributes:true,attributeFilter:['inert','disabled','hidden','style','class']})}
 scrollActive()
}
watch(opened,value=>{if(value){refreshRecent();void nextTick(()=>{if(opened.value)startPanel()})}else stopPanel()})
watch(activeIndex,scrollActive)
watch(scope,(next,previous)=>{if(next!==previous)directoryScope.value=''},{flush:'sync'})
watch(()=>props.members,(next,previous)=>{if(next!==previous&&scope.value){directoryScope.value=scope.value;refreshRecent()}},{flush:'sync'})
watch(()=>[scope.value,workspace.identityConflict,workspace.operationDisabled,props.disabled],()=>{close();query.value='';department.value='';refreshRecent()},{flush:'sync'})
onMounted(refreshRecent)
onBeforeUnmount(stopPanel)
</script>

<template>
  <div ref="root" class="member-multi" :class="{ disabled, 'member-multi-compact': compact }" @focusout="focusOut">
    <div v-if="compact" class="member-filter-control">
      <button ref="trigger" type="button" class="member-filter-trigger" :disabled="disabled" :aria-label="fieldLabel" aria-haspopup="dialog" :aria-expanded="opened" :aria-controls="opened ? listID + '-panel' : undefined" :title="selected.map(member=>member.name).join('、') || hint || fieldLabel" @click="openCompact" @keydown.down.prevent="openCompact">
        <span>{{ selected.length ? selected.map(member=>member.name).join('、') : hint || fieldLabel }}</span><span aria-hidden="true">⌄</span>
      </button>
      <button v-if="ids.length" type="button" class="member-filter-clear" :disabled="disabled" :aria-label="t('清除筛选')" @click="emit('update:modelValue', [])">×</button>
    </div>
    <template v-if="!compact">
    <div v-if="legacyName && !ids.length" class="member-chips"><span class="member-chip">{{ legacyName }}<small>{{ t('历史成员') }}</small><button type="button" :disabled="disabled" :aria-label="t('移除成员 {name}', {name:legacyName})" @click="emit('update:modelValue', [])">×</button></span></div>
    <div v-if="selected.length" class="member-chips" :aria-label="t('已选{label}',{label:fieldLabel})">
      <span v-for="(member, index) in selected" :key="member.id" class="member-chip" :title="member.email || member.id"><b v-if="!single && index === 0 && showLead !== false">{{ t('主') }}</b>{{ member.name }}<small v-if="member.historical">{{ t('历史成员') }}</small><button type="button" :disabled="disabled" :aria-label="t('移除成员 {name}', {name:member.name})" @click="remove(member.id)">×</button></span>
    </div>
    <div class="member-shortcuts"><button v-if="currentMember" type="button" :disabled="disabled || ids.includes(currentMember.id)" @click="selectMe">{{ t('选择我') }}</button><select v-if="departments.length" v-model="department" :disabled="disabled" :aria-label="t('按部门选择成员')" @change="opened=true;activeIndex=0"><option value="">{{ t('全部部门') }}</option><option v-for="item in departments" :key="item.id" :value="item.id">{{item.name}}</option></select><button v-if="department && !single" type="button" :disabled="disabled || departmentMembers.every(member=>ids.includes(member.id))" @click="addDepartment">{{t('添加部门 {count} 人',{count:departmentMembers.length})}}</button></div>
    <div class="member-search"><input :id="inputId" ref="input" v-model="query" :disabled="disabled" :aria-label="fieldLabel" :placeholder="t('搜索姓名、邮箱或部门')" role="combobox" aria-autocomplete="list" :aria-expanded="opened && !disabled" :aria-controls="listID" :aria-activedescendant="opened && options[activeIndex] ? listID + '-' + activeIndex : undefined" @focus="focusInput" @click="focusInput" @input="search" @keydown="keydown"><span>{{ ids.length }}</span></div>
    </template>
    <Teleport :to="root?.closest('dialog') || 'body'"><div v-if="opened && !disabled" ref="panel" :id="listID + '-panel'" :role="compact ? 'dialog' : undefined" :aria-label="compact ? fieldLabel : undefined" class="member-options member-options-floating" :style="panelStyle" @focusout="focusOut" @keydown.esc.stop.prevent="close(true)">
      <div v-if="compact" class="member-filter-panel">
    <div class="member-shortcuts"><button v-if="currentMember" type="button" :disabled="disabled || ids.includes(currentMember.id)" @click="selectMe">{{ t('选择我') }}</button><select v-if="departments.length" v-model="department" :disabled="disabled" :aria-label="t('按部门选择成员')" @change="opened=true;activeIndex=0"><option value="">{{ t('全部部门') }}</option><option v-for="item in departments" :key="item.id" :value="item.id">{{item.name}}</option></select><button v-if="department && !single" type="button" :disabled="disabled || departmentMembers.every(member=>ids.includes(member.id))" @click="addDepartment">{{t('添加部门 {count} 人',{count:departmentMembers.length})}}</button></div>
    <div class="member-search"><input :id="inputId" ref="input" v-model="query" :disabled="disabled" :aria-label="fieldLabel" :placeholder="t('搜索姓名、邮箱或部门')" role="combobox" aria-autocomplete="list" :aria-expanded="opened && !disabled" :aria-controls="listID" :aria-activedescendant="opened && options[activeIndex] ? listID + '-' + activeIndex : undefined" @focus="focusInput" @click="focusInput" @input="search" @keydown="keydown"><span>{{ ids.length }}</span></div>
        <div v-if="selected.length" class="member-chips"><span v-for="member in selected" :key="member.id" class="member-chip">{{ member.name }}<button type="button" :aria-label="t('移除成员 {name}', {name:member.name})" @click="remove(member.id)">×</button></span></div>
      </div>
      <div :id="listID" class="member-options-list" role="listbox" :aria-multiselectable="!single" :aria-label="fieldLabel">
      <button v-for="(member, index) in options" :id="listID + '-' + index" :data-member-index="index" :key="member.id" type="button" role="option" :aria-selected="ids.includes(member.id)" :class="{ active:activeIndex === index, selected:ids.includes(member.id) }" @mousedown.prevent @mouseenter="activeIndex = index" @click="toggle(member.id)"><span class="member-check">{{ ids.includes(member.id) ? '✓' : '+' }}</span><span><b>{{ member.name }}</b><small>{{ member.email || role(member) }}</small><small v-if="member.department">{{ member.department }}</small></span><em>{{ role(member) }}</em></button>
      <p v-if="!options.length">{{ t('没有匹配的可用成员') }}</p>
    </div><section class="member-recents" :aria-label="t('最近选择')"><header><b>{{ t('最近选择') }}</b><button v-if="recentOptions.length" type="button" @click="clearRecent">{{ t('清除最近选择') }}</button><button type="button" :aria-label="t('关闭人员选择')" @click="close(true)">×</button></header><div v-if="recentOptions.length" class="member-recent-chips"><button v-for="member in recentOptions" :key="member.id" type="button" :aria-pressed="ids.includes(member.id)" @mousedown.prevent @click="toggle(member.id)">{{ member.name }}</button></div><p v-else>{{ t('尚无最近选择') }}</p><small>{{ t('仅显示当前可用候选成员') }}</small></section></div></Teleport>
    <p v-if="!compact && candidateRoles" class="member-hint">{{ t('候选角色：{roles}', {roles:candidateRoles}) }} · {{ t('与所选部门取交集，历史绑定保留。') }}</p>
    <p v-if="!compact" class="member-hint">{{ hint || t(single ? '请选择一位成员；历史绑定保留。' : showLead === false ? '可选多位成员；仅可新增当前项目与限定部门中的可用成员，历史绑定保留。' : '可选多人，首位为主负责人；历史成员不会被静默移除。') }}</p>
  </div>
</template>

<style scoped>
.member-filter-control{display:flex;align-items:center;min-width:0;height:32px;border:1px solid var(--input,#d9e0ea);border-radius:6px;background:var(--surface,#fff)}
.member-filter-control:focus-within{border-color:var(--primary,#3370eb);outline:2px solid var(--primary,#3370eb);outline-offset:2px}
.member-filter-trigger{display:flex;align-items:center;justify-content:space-between;gap:8px;min-width:0;flex:1;height:100%;padding:0 10px;border:0;background:transparent;color:var(--ink,#26334a);font:inherit;font-size:12px;text-align:left;box-shadow:none}
.member-filter-trigger>span:first-child{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.member-filter-trigger:focus{outline:none}
.member-filter-clear{flex:none;border:0;background:transparent;color:var(--muted,#758298);padding:0 8px;height:100%;font-size:16px}
.member-filter-panel{flex:none;padding:6px;border-bottom:1px solid var(--line,#d9e0ea)}
.member-filter-panel .member-shortcuts button{width:auto;flex:none}
.member-filter-panel .member-chips{margin:8px 0 0}
@media(max-width:820px){.member-filter-control{height:44px}.member-filter-trigger{font-size:14px}}
.member-shortcuts{display:flex;align-items:center;gap:5px;flex-wrap:wrap;margin-bottom:6px}.member-shortcuts:empty{display:none}.member-shortcuts button,.member-shortcuts select{width:auto!important;max-width:100%;min-height:25px;margin:0!important;border:1px solid #e1e4ec!important;border-radius:5px;padding:3px 6px!important;background:#fbfbfe;font-size:10px!important;color:#625de0}.member-shortcuts select{min-width:0;flex:1;color:#667085}.member-shortcuts button:disabled{opacity:.45}
.member-multi{position:relative;min-width:0}.member-chips{display:flex;gap:5px;flex-wrap:wrap;margin:0 0 7px}.member-chip{display:inline-flex;align-items:center;gap:5px;max-width:100%;padding:4px 6px;border:1px solid #e0dfff;border-radius:5px;background:#f3f2ff;color:#514cc7;font-size:11px;overflow-wrap:anywhere}.member-chip>b{padding:1px 3px;font-size:9px;font-weight:500;border-radius:3px;background:#e5e3ff}.member-chip small{font-size:9px;color:#8a93a3}.member-chip button{border:0;background:none;color:inherit;padding:0 2px;font-size:15px;line-height:1}.member-search{display:flex;align-items:center;gap:5px;border:1px solid #d0d5dd;border-radius:8px;background:white;padding-right:8px}.member-search input{min-width:0;width:100%;border:0!important;outline:0;padding:8px!important;background:transparent!important;font:inherit;font-size:12px!important;box-shadow:none!important}.member-search:focus-within{border-color:#8a85ed;box-shadow:none}.member-search>span{font-size:10px;color:#98a2b3}.member-options{position:absolute;left:0;right:0;z-index:70;margin-top:5px;border:1px solid #e4e7ec;border-radius:8px;background:#fff;padding:4px;max-height:225px;overflow:auto;box-shadow:none}.member-options button{display:flex;align-items:center;gap:7px;width:100%;padding:9px 7px;border:0;border-radius:5px;background:white;text-align:left;color:#344054}.member-options button.active{background:#f1f0ff}.member-options button>span:nth-child(2){flex:1;min-width:0}.member-options b,.member-options small{display:block;font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.member-options small{font-size:9px;margin-top:3px;color:#98a2b3}.member-options em{font-size:9px;font-style:normal;color:#98a2b3}.member-check{width:16px;text-align:center;color:#7773c7}.member-options button.selected .member-check{color:#514cc7}.member-options p,.member-hint{font-size:10px;line-height:1.7;color:#98a2b3;margin:7px 0 0}.member-options p{padding:12px;margin:0;text-align:center}.member-multi.disabled .member-search{background:#f9fafb}.member-chip button:disabled{cursor:not-allowed;opacity:.4}
</style>
<style scoped>
.member-options-floating{z-index:1000;margin:0;right:auto;box-sizing:border-box;display:flex;flex-direction:column;overflow:hidden;background:var(--surface-raised,#fff);border-color:var(--line,#d9e0ea);color:var(--ink,#26334a)}.member-options-list{overflow:auto;min-height:0;overscroll-behavior:contain;flex:1}.member-options-floating button{background:transparent;color:var(--ink,#26334a)}.member-options-floating button.active,.member-options-floating button:hover{background:var(--surface-subtle,#f4f7fb)}.member-options-floating small,.member-options-floating em,.member-options-floating p{color:var(--muted,#758298)}.member-options-floating button:focus-visible{outline:2px solid var(--primary,#3370eb);outline-offset:-2px}.member-recents{flex:none;border-top:1px solid var(--line,#d9e0ea);padding:10px 6px 6px}.member-recents header{display:flex;align-items:center;gap:7px}.member-recents header>b{font-size:11px;margin-right:auto}.member-recents header>button{width:auto;font-size:10px;padding:3px 4px;color:var(--primary,#3370eb)}.member-recents>small{font-size:9px}.member-recent-chips{display:flex;flex-wrap:wrap;gap:5px;max-height:92px;overflow:auto;padding:7px 0}.member-recent-chips>button{width:auto;font-size:11px;padding:5px 7px;border:1px solid var(--line,#d9e0ea)}.member-recent-chips>button[aria-pressed=true]{border-color:var(--primary,#3370eb);background:var(--primary-soft,#eef4ff)}.member-search{background:var(--surface,#fff);border-color:var(--line,#d9e0ea);color:var(--ink,#26334a)}.member-search input{color:inherit}.member-chip{background:var(--primary-soft,#eef4ff);color:var(--primary,#3370eb);border-color:var(--line,#d9e0ea)}.member-chip>b{background:var(--surface-subtle,#f4f7fb)}.member-shortcuts button,.member-shortcuts select{background:var(--surface-subtle,#f4f7fb);color:var(--primary,#3370eb);border-color:var(--line,#d9e0ea)!important}.member-multi.disabled .member-search{background:var(--surface-subtle,#f4f7fb)}
</style>
<style scoped>
@media(max-width:820px){
 .member-search input{font-size:16px!important;min-height:44px}
 .member-shortcuts button,.member-shortcuts select{min-height:44px;font-size:13px!important}
 .member-shortcuts select{font-size:16px!important}
 .member-chip button{min-width:28px;min-height:28px;flex:none}
 .member-options button{min-height:44px}.member-options em{max-width:30%;overflow-wrap:anywhere}
}
</style>
