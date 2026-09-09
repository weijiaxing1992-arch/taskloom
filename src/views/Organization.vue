<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { t } from '../i18n'
import { organizationSections, permits, type OrganizationContext } from '../organization'
import { useSettingsScope } from '../components/settingsScope'
import { Button } from '../components/ui/button'
import OrganizationMembers from '../components/OrganizationMembers.vue'
import OrganizationDirectory from '../components/OrganizationDirectory.vue'
import ServerMonitor from '../components/ServerMonitor.vue'
import DeliveryCenter from '../components/DeliveryCenter.vue'
import WechatSettings from '../components/WechatSettings.vue'
import SidebarCollapseButton from '../components/SidebarCollapseButton.vue'
import { useLayoutBoolean } from '../layoutScope'
import OrganizationInvitations from '../components/OrganizationInvitations.vue'
import '../organization.css'
const route=useRoute(),router=useRouter(),scope=useSettingsScope(),context=ref<OrganizationContext|null>(null),error=ref(''),loading=ref(true)
const organizationSidebarExpanded=useLayoutBoolean('organization.sidebar',true)
const section=computed(()=>organizationSections.some(item=>item.key===route.params.section)?String(route.params.section):'overview')
const visibleSections=computed(()=>organizationSections.filter(item=>!['groups','server-monitor','delivery','wechat-login'].includes(item.key)||context.value?.isTenantAdmin).filter(item=>item.key!=='invitations'||permits(context.value,'invitations.manage')).filter(item=>item.key!=='applications'||permits(context.value,'applications.review')))
async function load(){loading.value=true;error.value='';try{context.value=await scope.request<OrganizationContext>('/organization/admin')}catch(cause){error.value=cause instanceof Error?cause.message:'无法加载企业管理'}finally{loading.value=false}}
function navigate(key:string){void router.push(key==='archived-projects'?'/projects?status=archived':'/organization/'+key)}
const cards=computed(()=>context.value?[
 {key:'members',label:'企业成员',value:context.value.counts.members,note:'查看和配置企业成员'},
 {key:'departments',label:'部门 / 团队',value:context.value.counts.departments,note:'维护组织架构与部门成员'},
 ...(context.value.isTenantAdmin?[{key:'groups',label:'用户组权限',value:context.value.counts.groups,note:'按能力授权，不改变项目角色'}]:[]),
 ...(context.value.isTenantAdmin?[{key:'archived-projects',label:'已归档项目',value:context.value.projects.filter(project=>project.status==='archived').length,note:'查看、恢复或删除已归档项目'}]:[]),
 ...(permits(context.value,'applications.review')?[{key:'applications',label:'待审批申请',value:context.value.counts.pendingApplications,note:'管理员审核后才激活账号'}]:[]),
]:[])
onMounted(load)
</script>
<template>
  <div class="organization-page">
    <aside class="org-nav" :class="{'is-collapsed':!organizationSidebarExpanded}"><SidebarCollapseButton :expanded="organizationSidebarExpanded" :label="t('企业管理侧边栏')" @toggle="organizationSidebarExpanded=!organizationSidebarExpanded"/><div v-show="organizationSidebarExpanded" class="org-nav-content"><div class="org-brand"><span>D</span><h2>{{context?.organization.name||t('企业管理')}}</h2><small>{{t('组织与权限中心')}}</small></div><nav :aria-label="t('企业管理')"><RouterLink v-for="item in visibleSections" :key="item.key" :to="'/organization/'+item.key" :class="{active:section===item.key}" @dblclick="navigate(item.key)"><i aria-hidden="true">{{item.icon}}</i>{{t(item.name)}}<small v-if="item.key==='applications'&&context?.counts.pendingApplications">{{context.counts.pendingApplications}}</small></RouterLink></nav><p>{{t('企业权限决定管理能力，项目角色决定业务操作范围。')}}</p></div></aside>
    <main class="org-main"><header class="org-page-head"><div><small>{{t('企业管理')}} / {{t(organizationSections.find(item=>item.key===section)?.name||'企业概览')}}</small><h1>{{t(organizationSections.find(item=>item.key===section)?.name||'企业概览')}}</h1></div><div class="org-header-actions"><RouterLink v-if="context?.isTenantAdmin&&section!=='wechat-login'&&!scope.locked.value" class="btn org-wechat-entry" to="/organization/wechat-login">{{t('微信登录配置')}}</RouterLink><Button variant="outline" :disabled="loading||scope.locked.value" @click="load">{{t('刷新概览')}}</Button></div></header>
      <p v-if="scope.locked.value" role="alert" class="org-error">{{t('项目或账号已变化，请刷新页面后继续')}}</p><p v-if="error" role="alert" class="org-error">{{t(error)}}</p><p v-if="loading&&!context" role="status">{{t('正在加载…')}}</p>
      <template v-if="context&&!scope.locked.value"><section v-if="section==='overview'" class="org-overview"><div class="org-hero"><span>{{t('团队协作，从清晰的组织开始')}}</span><h2>{{context.organization.name}}</h2><p>{{t('统一维护成员、部门和用户组；所有自助加入申请均需管理员审核。')}}</p><Button v-if="permits(context,'invitations.manage')" @click="navigate('invitations')">{{t('创建邀请链接')}} ↗</Button></div><div class="org-summary"><button v-for="card in cards" :key="card.key" type="button" @click="navigate(card.key)" @dblclick="navigate(card.key)"><span>{{t(card.label)}} <i>↗</i></span><b>{{card.value}}</b><small>{{t(card.note)}}</small></button></div><section class="org-explainer"><h3>{{t('加入组织的审批流程')}}</h3><ol><li>{{t('管理员生成有时效的邀请链接')}}</li><li>{{t('成员仅填写姓名、部门和申请角色')}}</li><li>{{t('管理员确认登录信息与项目角色')}}</li><li>{{t('通过并激活后才能登录和访问项目')}}</li></ol><p>{{t('不通过的申请不会创建账号；链接可随时撤销。')}}</p></section></section>
      <OrganizationMembers v-else-if="section==='members'" :context="context" @changed="load" />
      <OrganizationDirectory v-else-if="section==='departments'||section==='groups'" :key="section" :context="context" :section="section" @changed="load" />
      <ServerMonitor v-else-if="section==='server-monitor'&&context.isTenantAdmin" />
      <WechatSettings v-else-if="section==='wechat-login'&&context.isTenantAdmin" />
      <section v-else-if="section==='wechat-login'" class="org-error" role="alert">{{t('仅企业管理员可配置微信登录')}}</section>
      <DeliveryCenter v-else-if="section==='delivery'&&context.isTenantAdmin" />
      <section v-else-if="section==='delivery'" class="org-error" role="alert">{{t('仅企业管理员可以查看交付资料')}}</section>
      <section v-else-if="section==='server-monitor'" class="org-error" role="alert">{{t('仅企业管理员可以查看服务器监控')}}</section>
      <OrganizationInvitations v-else :key="section" :context="context" :section="section" @changed="load" />
      </template>
    </main>
  </div>
</template>

<style scoped>
.org-header-actions{display:flex;align-items:center;flex-wrap:wrap;gap:8px}.org-wechat-entry{white-space:nowrap;text-decoration:none}
.org-nav{display:flex;flex-direction:column;transition:flex-basis .16s ease,padding .16s ease}.org-nav>:deep(.sidebar-collapse-toggle){align-self:flex-end;margin:-10px -3px 12px}.org-nav-content{display:flex;min-height:0;flex:1;flex-direction:column}.org-nav.is-collapsed{flex-basis:52px;padding:12px}.org-nav.is-collapsed>:deep(.sidebar-collapse-toggle){align-self:center;margin:0}
@media(max-width:820px){
 .organization-page{display:flex;flex-direction:column;height:auto;min-height:100%;min-width:0}
 .org-nav{width:100%;flex:none;min-width:0;padding:12px;border-right:0;border-bottom:1px solid var(--line);overflow:visible}
 .org-nav.is-collapsed{width:100%;min-width:0;padding:9px}.org-nav.is-collapsed>:deep(.sidebar-collapse-toggle){margin-left:auto}
 .org-brand{display:flex;align-items:center;gap:10px;flex-wrap:wrap;padding:0 5px 12px}.org-brand>span{width:30px;height:30px;border-radius:9px;font-size:18px;flex:none}.org-brand h2{margin:0;font-size:15px;overflow-wrap:anywhere}.org-brand small{font-size:10px}
 .org-nav nav{display:flex;max-width:100%;overflow:auto;gap:6px}.org-nav nav a{flex:none;white-space:nowrap;padding:10px 12px;min-height:40px}.org-nav>p{display:none}
 .org-main{width:100%;min-width:0;overflow:visible;padding:16px}.org-page-head{gap:12px;flex-wrap:wrap}.org-page-head h1{font-size:23px}
 .org-hero{padding:22px 18px}.org-hero h2{font-size:23px;overflow-wrap:anywhere}.org-summary{grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}.org-summary button{padding:14px}.org-summary span{font-size:11px;gap:6px}.org-summary b{font-size:26px}.org-explainer{padding:17px}
 .org-main :deep(.org-directory){grid-template-columns:minmax(0,1fr);min-height:0}.org-main :deep(.org-directory-list){border-right:0;border-bottom:1px solid var(--line);padding:14px}.org-main :deep(.org-directory-list nav){display:flex;overflow:auto;max-width:100%}.org-main :deep(.org-directory-list nav button){flex:none;max-width:220px;min-width:130px}.org-main :deep(.org-directory-list nav button span){overflow-wrap:anywhere}
 .org-main :deep(.org-directory-detail){padding:15px;min-width:0}.org-main :deep(.org-directory-detail>header){flex-wrap:wrap}.org-main :deep(.org-permission-grid){grid-template-columns:minmax(0,1fr)}
 .org-main :deep(.org-filters>input){min-width:0;width:100%;max-width:100%;flex-basis:100%}.org-main :deep(.org-table-wrap){max-width:100%;overflow:auto}
 .org-main :deep(.org-modal-shade){padding:10px}.org-main :deep(.org-modal){max-height:calc(100dvh - 20px);max-width:100%}.org-main :deep(.org-modal-body){padding:16px}.org-main :deep(.org-modal>footer){flex-wrap:wrap;padding:14px}.org-main :deep(.org-form-two){grid-template-columns:minmax(0,1fr)}.org-main :deep(.org-project-role){grid-template-columns:minmax(0,1fr);gap:9px}.org-main :deep(.org-project-role>button){justify-self:end;min-width:40px;min-height:40px}
}
</style>
