<script setup lang="ts">
import AIButton from '../AIButton.vue'
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { useSettingsScope } from '../settingsScope'
const { request:api }=useSettingsScope()
import { t } from '../../i18n'
import { useLayoutBoolean } from '../../layoutScope'
import SidebarCollapseButton from '../SidebarCollapseButton.vue'
import type { TestSettings } from '../../testingWorkspace'
const props=defineProps<{settings:TestSettings;canManage:boolean}>()
const emit=defineEmits<{refresh:[]}>()
const section=ref('template'), draft=ref<TestSettings>(clone(props.settings)), baseline=ref(''), saving=ref(false), error=ref(''), notice=ref('')
const settingsSidebarExpanded=useLayoutBoolean('testing.settings.sidebar',true)
const sections=[['template','用例模板'],['results','测试结果选项'],['standard','AI 规范审查'],['logic','AI 逻辑审查']]
function clone(value:TestSettings):TestSettings{return JSON.parse(JSON.stringify(value))}
function reset(){draft.value=clone(props.settings);baseline.value=JSON.stringify(draft.value);error.value=''}
const dirty=computed(()=>JSON.stringify(draft.value)!==baseline.value)
// 后台刷新只更新未编辑的配置；已有本地修改必须由用户保存或明确放弃。
watch(()=>props.settings,()=>{if(!dirty.value||!baseline.value)reset()},{immediate:true})
function allowed(){return !saving.value&&(!dirty.value||window.confirm(t('配置尚未保存，确定放弃修改吗？')))}
onBeforeRouteLeave(allowed);onBeforeRouteUpdate((to,from)=>to.query.tab===from.query.tab||allowed())
function unload(event:BeforeUnloadEvent){if(dirty.value){event.preventDefault();event.returnValue=''}}
window.addEventListener('beforeunload',unload);onBeforeUnmount(()=>window.removeEventListener('beforeunload',unload))
async function save(){
 if(!props.canManage||saving.value)return;saving.value=true;error.value='';notice.value=''
 try{draft.value=await api<TestSettings>('/testing/settings',{method:'PATCH',body:JSON.stringify(draft.value)});baseline.value=JSON.stringify(draft.value);notice.value='测试配置已保存';emit('refresh')}
 catch(cause){error.value=cause instanceof Error?cause.message:'保存失败，请重试'}finally{saving.value=false}
}
function toggleEnabled(index:number,value:boolean){const field=draft.value.fields[index];if(!field)return;field.enabled=value;if(!value)field.required=false}
</script>

<template>
 <div class="test-settings-layout">
  <aside class="test-settings-nav" :class="{'is-collapsed':!settingsSidebarExpanded}"><SidebarCollapseButton :expanded="settingsSidebarExpanded" :label="t('测试设置侧边栏')" @toggle="settingsSidebarExpanded=!settingsSidebarExpanded"/><div v-show="settingsSidebarExpanded" class="test-settings-nav-content"><p class="test-eyebrow">{{t('基础设置')}}</p><component :is="key==='standard'||key==='logic'?AIButton:'button'" v-for="[key,label] in sections" :key="key" :class="{active:section===key}" :aria-pressed="section===key" @click="section=key">{{t(label!)}}</component><router-link to="/settings/ai">{{t('AI 模型配置')}} ↗</router-link></div></aside>
  <form class="test-settings-main" @submit.prevent="save">
   <header class="test-section-head"><div><h2>{{t(sections.find(x=>x[0]===section)?.[1]||'基础设置')}}</h2><p>{{t('配置对当前项目生效；历史记录保留，不覆盖已保存的业务内容。')}}</p></div><span v-if="!canManage" class="test-pill neutral">{{t('只读')}}</span></header>
   <p v-if="error" class="test-notice error" role="alert">{{t(error)}}</p><p v-if="notice" class="test-notice success" role="status">{{t(notice)}}</p>
   <fieldset :disabled="!canManage||saving" class="test-fieldset">
    <template v-if="section==='template'">
     <div class="test-info">{{t('标题、步骤及预期结果为固定核心字段。以下选项用于设置补充字段的启用、必填、默认值和列表展示。')}}</div>
     <div class="test-table-wrap"><table class="test-table test-template-table"><thead><tr><th>{{t('字段')}}</th><th>{{t('启用')}}</th><th>{{t('必填')}}</th><th>{{t('列表展示')}}</th><th>{{t('默认值')}}</th></tr></thead><tbody>
      <tr v-for="(field,index) in draft.fields" :key="field.key"><td><b>{{t(field.name)}}</b><small>{{t(field.description)}}</small></td><td><input type="checkbox" :aria-label="t('启用 {name}',{name:t(field.name)})" :checked="field.enabled" @change="toggleEnabled(index,($event.target as HTMLInputElement).checked)"></td><td><input type="checkbox" :aria-label="t('必填 {name}',{name:t(field.name)})" v-model="field.required" :disabled="!field.enabled"></td><td><input type="checkbox" :aria-label="t('列表展示 {name}',{name:t(field.name)})" v-model="field.listVisible" :disabled="!field.enabled"></td><td>
       <input v-if="field.key==='estimatedMinutes'" type="number" min="0" max="10080" step="1" :aria-label="t('默认值 {name}',{name:t(field.name)})" :value="Number(field.defaultValue||0)" @input="field.defaultValue=Number(($event.target as HTMLInputElement).value||0)">
       <span v-else-if="field.key==='requirementId'" class="test-muted">{{t('创建时选择关联需求')}}</span>
       <textarea v-else :aria-label="t('默认值 {name}',{name:t(field.name)})" :value="String(field.defaultValue||'')" maxlength="20000" rows="2" @input="field.defaultValue=($event.target as HTMLTextAreaElement).value" />
      </td></tr>
     </tbody></table></div>
    </template>
    <template v-else-if="section==='results'">
     <div class="test-result-setting"><span class="test-pill green">{{t('通过')}}</span><p>{{t('所有步骤的实际结果符合预期。')}}</p><span>{{t('固定启用')}}</span></div>
     <div class="test-result-setting"><span class="test-pill red">{{t('失败')}}</span><p>{{t('至少一个步骤不符合预期，可从执行记录生成缺陷。')}}</p><span>{{t('固定启用')}}</span></div>
     <div class="test-result-setting"><span class="test-pill orange">{{t('阻塞')}}</span><p>{{t('环境、依赖或数据问题导致无法完成测试。')}}</p><label class="test-check"><input type="checkbox" v-model="draft.blockedEnabled">{{t('允许标记阻塞')}}</label></div>
     <p class="test-muted">{{t('关闭阻塞选项不会修改已有阻塞记录，也不会将其计为通过。')}}</p>
    </template>
    <template v-else-if="section==='standard'">
     <p class="test-info">{{t('AI 按以下规范检查用例表达、完整性与可执行性。审查只给出建议，不自动修改用例或通过人工评审。')}}</p>
     <div v-for="(_,index) in draft.aiReviewRules" :key="index" class="test-rule"><span>{{index+1}}</span><textarea v-model="draft.aiReviewRules[index]" :aria-label="t('规范规则 {number}',{number:index+1})" maxlength="2000" rows="2" required /><button class="test-icon-btn" type="button" :aria-label="t('删除规则')" @click="draft.aiReviewRules.splice(index,1)">×</button></div>
     <button type="button" class="btn" :disabled="draft.aiReviewRules.length>=20" @click="draft.aiReviewRules.push('')">＋ {{t('添加规范规则')}}</button>
    </template>
    <template v-else>
     <p class="test-info">{{t('结合业务背景检查重复步骤、边界、异常分支与测试逻辑；缺失的信息会作为待确认项展示。')}}</p>
     <label class="test-field">{{t('业务背景')}}<textarea v-model="draft.businessContext" maxlength="10000" rows="5" :placeholder="t('补充业务流程、术语、角色与关键约束')" /></label>
     <div v-for="(_,index) in draft.aiLogicRules" :key="index" class="test-rule"><span>{{index+1}}</span><textarea v-model="draft.aiLogicRules[index]" :aria-label="t('逻辑规则 {number}',{number:index+1})" maxlength="2000" rows="2" required /><button class="test-icon-btn" type="button" :aria-label="t('删除规则')" @click="draft.aiLogicRules.splice(index,1)">×</button></div>
     <button type="button" class="btn" :disabled="draft.aiLogicRules.length>=20" @click="draft.aiLogicRules.push('')">＋ {{t('添加逻辑规则')}}</button>
    </template>
   </fieldset>
   <footer v-if="canManage" class="test-settings-footer"><span class="test-muted">{{dirty?t('有未保存的修改'):t('配置已同步')}}</span><button class="btn" type="button" :disabled="!dirty||saving" @click="reset">{{t('取消修改')}}</button><button class="btn primary" :disabled="!dirty||saving">{{saving?t('保存中…'):t('应用配置')}}</button></footer>
  </form>
 </div>
</template>
