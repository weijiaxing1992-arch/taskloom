<script setup lang="ts">
import { computed,onBeforeUnmount,ref,watch } from 'vue'
import { useRoute,useRouter } from 'vue-router'
import { api } from '../api'
import { t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import type { TestWorkspace } from '../testingWorkspace'
import TestCaseLibrary from '../components/testing/TestCaseLibrary.vue'
import TestDesigns from '../components/testing/TestDesigns.vue'
import TestingSettings from '../components/testing/TestingSettings.vue'
import TestingOperations from '../components/testing/TestingOperations.vue'
import '../testing-workspace.css'

const route=useRoute(),router=useRouter(),session=useWorkspaceStore()
const tabs=[{key:'designs',label:'测试设计'},{key:'cases',label:'测试用例'},{key:'plans',label:'测试计划'},{key:'executions',label:'测试执行'},{key:'report',label:'质量报告'},{key:'settings',label:'基础设置'}]
const tab=computed(()=>tabs.some(x=>x.key===route.query.tab)?String(route.query.tab):route.query.plan?'plans':route.query.execution?'executions':'cases')
const workspace=ref<TestWorkspace|null>(null),members=ref<any[]>([]),plans=ref<any[]>([]),executions=ref<any[]>([]),sprints=ref<any[]>([])
const loading=ref(false),error=ref('');let version=0
async function load(){
 // 并行读取相互独立的数据；局部失败保留已成功内容，并显式提示重试。
 // 每批响应只允许当前版本提交，切换项目/账号后旧批次不回填新页面。
 const sequence=++version;loading.value=true;error.value=''
 const results=await Promise.allSettled([
  api<TestWorkspace>('/testing/workspace'),
  api<{items:any[]}>('/members'),
  api<{items:any[]}>('/test-plans'),
  api<{items:any[]}>('/test-executions'),
  api<{items:any[]}>('/sprints'),
 ])
 if(sequence!==version)return
 const [work,people,planData,executionData,sprintData]=results
 if(work.status==='fulfilled')workspace.value=work.value
 if(people.status==='fulfilled')members.value=people.value.items
 if(planData.status==='fulfilled')plans.value=planData.value.items
 if(executionData.status==='fulfilled')executions.value=executionData.value.items
 if(sprintData.status==='fulfilled')sprints.value=sprintData.value.items
 const failed=results.find(x=>x.status==='rejected')
 if(failed?.status==='rejected')error.value=failed.reason instanceof Error?failed.reason.message:'测试数据加载失败，请重试'
 loading.value=false
}
function changeTab(key:string){void router.replace({query:{tab:key}})}
watch(()=>`${session.currentProject?.id||''}:${session.currentUser?.id||''}`,()=>{workspace.value=null;members.value=[];plans.value=[];executions.value=[];void load()},{immediate:true})
onBeforeUnmount(()=>{version++})
</script>

<template>
 <div class="testing-workspace">
  <header class="testing-workspace-head compact-page-heading"><div><h1>{{t('测试协作')}}</h1><details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{t('从需求到测试设计、用例评审与执行，持续沉淀可复用的质量资产。')}}</p></details></div><button class="btn" :disabled="loading" @click="load">{{loading?t('刷新中…'):t('刷新数据')}}</button></header>
  <nav class="test-workspace-tabs" :aria-label="t('测试模块导航')"><button v-for="item in tabs" :key="item.key" :class="{active:tab===item.key,configuration:item.key==='settings'}" :aria-current="tab===item.key?'page':undefined" @click="changeTab(item.key)">{{t(item.label)}}</button></nav>
  <p v-if="error" class="test-notice error" role="alert">{{t(error)}} <button class="test-text-button" :disabled="loading" @click="load">{{t('重试')}}</button></p>
  <div v-if="loading&&!workspace" class="test-loading" role="status">{{t('正在载入测试工作台…')}}</div>
  <template v-if="workspace">
   <TestCaseLibrary v-if="tab==='cases'" :key="session.currentProject?.id" :workspace="workspace" :members="members" :executions="executions" @refresh="load"/>
   <TestDesigns v-else-if="tab==='designs'" :workspace="workspace" :members="members" @refresh="load"/>
   <TestingSettings v-else-if="tab==='settings'" :settings="workspace.settings" :can-manage="workspace.canManage" @refresh="load"/>
   <TestingOperations v-else :tab="tab" :workspace="workspace" :members="members" :plans="plans" :executions="executions" :sprints="sprints" @refresh="load"/>
  </template>
 </div>
</template>
